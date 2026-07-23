"""Background worker that runs queued inference jobs with a concurrency cap."""

from __future__ import annotations

import asyncio
import logging
from typing import Any

from app.adapters.base import Capability
from app.adapters.local_embed import LocalEmbeddingAdapter
from app.adapters.local_gen import LocalGenerationAdapter, summarize_prompt
from app.config import Settings
from app.jobs.store import InvalidTransitionError, Job, JobStatus, JobStore
from app.registry import ModelRegistry

logger = logging.getLogger("forge-models")


class JobCancelled(Exception):
    """Cooperative cancellation requested."""


class JobWorker:
    """Polls the job store and executes work under a semaphore."""

    def __init__(
        self,
        store: JobStore,
        registry: ModelRegistry,
        settings: Settings,
    ) -> None:
        self._store = store
        self._registry = registry
        self._settings = settings
        self._sem = asyncio.Semaphore(settings.forge_models_max_concurrent_jobs)
        self._wake = asyncio.Event()
        self._stopped = asyncio.Event()
        self._task: asyncio.Task[None] | None = None
        self._gc_task: asyncio.Task[None] | None = None
        self._inflight: set[asyncio.Task[None]] = set()
        self._claimed: set[str] = set()

    def notify(self) -> None:
        self._wake.set()

    async def start(self) -> None:
        self._stopped.clear()
        self._task = asyncio.create_task(self._loop(), name="forge-models-job-worker")
        self._gc_task = asyncio.create_task(self._gc_loop(), name="forge-models-job-gc")
        logger.info(
            "job worker started",
            extra={"max_concurrent_jobs": self._settings.forge_models_max_concurrent_jobs},
        )

    async def stop(self) -> None:
        self._stopped.set()
        self._wake.set()
        for task in list(self._inflight):
            task.cancel()
        if self._task is not None:
            self._task.cancel()
            try:
                await self._task
            except asyncio.CancelledError:
                pass
            self._task = None
        if self._gc_task is not None:
            self._gc_task.cancel()
            try:
                await self._gc_task
            except asyncio.CancelledError:
                pass
            self._gc_task = None
        logger.info("job worker stopped")

    async def _gc_loop(self) -> None:
        while not self._stopped.is_set():
            try:
                await asyncio.wait_for(self._stopped.wait(), timeout=30.0)
                return
            except TimeoutError:
                self._store.gc_expired()

    async def _loop(self) -> None:
        while not self._stopped.is_set():
            started_any = False
            for job_id in self._store.queued_ids():
                if job_id in self._claimed:
                    continue
                job = self._store.get_raw(job_id)
                if job is None or job.status != JobStatus.QUEUED:
                    continue
                if job.cancel_event.is_set():
                    try:
                        self._store.transition(job_id, JobStatus.CANCELLED)
                    except InvalidTransitionError:
                        pass
                    continue
                self._claimed.add(job_id)
                started_any = True
                task = asyncio.create_task(self._run_guarded(job_id), name=f"job-{job_id}")
                self._inflight.add(task)

                def _done(t: asyncio.Task[None], *, jid: str = job_id) -> None:
                    self._inflight.discard(t)
                    self._claimed.discard(jid)

                task.add_done_callback(_done)
            if started_any:
                await asyncio.sleep(0)
            else:
                self._wake.clear()
                try:
                    await asyncio.wait_for(self._wake.wait(), timeout=0.25)
                except TimeoutError:
                    pass

    async def _run_guarded(self, job_id: str) -> None:
        try:
            async with self._sem:
                await self._execute(job_id)
        finally:
            self._claimed.discard(job_id)

    async def _execute(self, job_id: str) -> None:
        job = self._store.get_raw(job_id)
        if job is None or job.status != JobStatus.QUEUED:
            return
        if job.cancel_event.is_set():
            try:
                self._store.transition(job_id, JobStatus.CANCELLED)
            except InvalidTransitionError:
                pass
            return

        try:
            self._store.transition(job_id, JobStatus.RUNNING)
        except InvalidTransitionError:
            return

        timeout = float(self._settings.forge_models_job_timeout_seconds)
        try:
            async with asyncio.timeout(timeout):
                result = await self._run_task(job)
                if job.cancel_event.is_set():
                    raise JobCancelled()
                self._store.transition(job_id, JobStatus.SUCCEEDED, result=result)
        except JobCancelled:
            try:
                self._store.transition(job_id, JobStatus.CANCELLED)
            except InvalidTransitionError:
                pass
        except TimeoutError:
            try:
                self._store.transition(
                    job_id,
                    JobStatus.FAILED,
                    error={"code": "timeout", "message": "job exceeded timeout"},
                )
            except InvalidTransitionError:
                pass
        except asyncio.CancelledError:
            try:
                self._store.transition(
                    job_id,
                    JobStatus.FAILED,
                    error={"code": "worker_cancelled", "message": "worker shutting down"},
                )
            except InvalidTransitionError:
                pass
            raise
        except Exception as exc:  # noqa: BLE001 — mark job failed, keep worker alive
            logger.exception("job failed", extra={"job_id": job_id})
            try:
                self._store.transition(
                    job_id,
                    JobStatus.FAILED,
                    error={"code": "job_failed", "message": str(exc)},
                )
            except InvalidTransitionError:
                pass

    async def _run_task(self, job: Job) -> Any:
        if job.delay_ms > 0:
            await self._cancellable_sleep(job, job.delay_ms / 1000.0)

        if job.cancel_event.is_set():
            raise JobCancelled()

        adapter = self._registry.get(job.model)
        if adapter is None:
            raise ValueError(f"model not found: {job.model}")

        task = job.task
        if task == "generate":
            return await self._run_generate(job, adapter)
        if task == "summarize":
            return await self._run_summarize(job, adapter)
        if task == "classify":
            return await self._run_classify(job, adapter)
        if task == "embed":
            return await self._run_embed(job, adapter)
        raise ValueError(f"unsupported task: {task}")

    async def _cancellable_sleep(self, job: Job, seconds: float) -> None:
        remaining = seconds
        step = 0.05
        while remaining > 0:
            if job.cancel_event.is_set():
                raise JobCancelled()
            await asyncio.sleep(min(step, remaining))
            remaining -= step

    async def _run_generate(self, job: Job, adapter: object) -> dict[str, Any]:
        if not isinstance(adapter, LocalGenerationAdapter):
            raise ValueError(f"model '{job.model}' has no local generation adapter")
        if Capability.GENERATE not in adapter.capabilities:
            raise ValueError(f"model '{job.model}' does not support generate")
        prompt, max_tokens, temperature = self._parse_generate_input(job.input)
        result = await asyncio.to_thread(
            adapter.generate,
            prompt,
            max_tokens=max_tokens,
            temperature=temperature,
        )
        if job.cancel_event.is_set():
            raise JobCancelled()
        return {
            "text": result.text,
            "finish_reason": result.finish_reason,
            "usage": result.usage.as_dict(),
        }

    async def _run_summarize(self, job: Job, adapter: object) -> dict[str, Any]:
        if not isinstance(adapter, LocalGenerationAdapter):
            raise ValueError(f"model '{job.model}' has no local generation adapter")
        if Capability.SUMMARIZE not in adapter.capabilities:
            raise ValueError(f"model '{job.model}' does not support summarize")
        text, max_tokens, temperature = self._parse_summarize_input(job.input)
        prompt = summarize_prompt(text)
        result = await asyncio.to_thread(
            adapter.generate,
            prompt,
            max_tokens=max_tokens,
            temperature=temperature,
        )
        if job.cancel_event.is_set():
            raise JobCancelled()
        return {"summary": result.text, "usage": result.usage.as_dict()}

    async def _run_classify(self, job: Job, adapter: object) -> dict[str, Any]:
        if not isinstance(adapter, LocalGenerationAdapter):
            raise ValueError(f"model '{job.model}' has no local generation adapter")
        if Capability.CLASSIFY not in adapter.capabilities:
            raise ValueError(f"model '{job.model}' does not support classify")
        text, labels = self._parse_classify_input(job.input)
        scored = await asyncio.to_thread(adapter.classify, text, labels)
        if job.cancel_event.is_set():
            raise JobCancelled()
        return {"labels": [item.as_dict() for item in scored]}

    async def _run_embed(self, job: Job, adapter: object) -> dict[str, Any]:
        if not isinstance(adapter, LocalEmbeddingAdapter):
            raise ValueError(f"model '{job.model}' has no local embeddings adapter")
        if Capability.EMBED not in adapter.capabilities:
            raise ValueError(f"model '{job.model}' does not support embed")
        texts = self._parse_embed_input(job.input)
        embeddings = await asyncio.to_thread(adapter.embed, texts)
        if job.cancel_event.is_set():
            raise JobCancelled()
        return {
            "model": job.model,
            "embeddings": embeddings,
            "dim": adapter.embedding_dim,
            "usage": {"input_count": len(texts)},
        }

    def _parse_generate_input(self, raw: Any) -> tuple[str, int, float]:
        settings = self._settings
        default_max = min(128, settings.forge_models_gen_max_tokens)
        default_temp = settings.forge_models_gen_default_temp
        if isinstance(raw, str):
            if not raw:
                raise ValueError("input prompt must be a non-empty string")
            return raw, default_max, default_temp
        if not isinstance(raw, dict):
            raise ValueError("generate input must be a string or object")
        prompt = raw.get("prompt")
        if not isinstance(prompt, str) or not prompt:
            raise ValueError("input.prompt must be a non-empty string")
        max_tokens = raw.get("max_tokens", default_max)
        temperature = raw.get("temperature", default_temp)
        if not isinstance(max_tokens, int) or isinstance(max_tokens, bool) or max_tokens < 1:
            raise ValueError("max_tokens must be a positive integer")
        if max_tokens > settings.forge_models_gen_max_tokens:
            raise ValueError(
                f"max_tokens {max_tokens} exceeds cap {settings.forge_models_gen_max_tokens}"
            )
        if not isinstance(temperature, (int, float)) or isinstance(temperature, bool):
            raise ValueError("temperature must be a number")
        if temperature < 0.0 or temperature > 2.0:
            raise ValueError("temperature must be between 0 and 2 inclusive")
        return prompt, max_tokens, float(temperature)

    def _parse_summarize_input(self, raw: Any) -> tuple[str, int, float]:
        settings = self._settings
        default_max = min(128, settings.forge_models_gen_max_tokens)
        default_temp = settings.forge_models_gen_default_temp
        if isinstance(raw, str):
            if not raw:
                raise ValueError("input must be a non-empty string")
            return raw, default_max, default_temp
        if not isinstance(raw, dict):
            raise ValueError("summarize input must be a string or object")
        text = raw.get("input", raw.get("text"))
        if not isinstance(text, str) or not text:
            raise ValueError("input must be a non-empty string")
        max_tokens = raw.get("max_tokens", default_max)
        temperature = raw.get("temperature", default_temp)
        if not isinstance(max_tokens, int) or isinstance(max_tokens, bool) or max_tokens < 1:
            raise ValueError("max_tokens must be a positive integer")
        if max_tokens > settings.forge_models_gen_max_tokens:
            raise ValueError(
                f"max_tokens {max_tokens} exceeds cap {settings.forge_models_gen_max_tokens}"
            )
        if not isinstance(temperature, (int, float)) or isinstance(temperature, bool):
            raise ValueError("temperature must be a number")
        if temperature < 0.0 or temperature > 2.0:
            raise ValueError("temperature must be between 0 and 2 inclusive")
        return text, max_tokens, float(temperature)

    def _parse_classify_input(self, raw: Any) -> tuple[str, list[str]]:
        if not isinstance(raw, dict):
            raise ValueError("classify input must be an object with input and labels")
        text = raw.get("input")
        labels = raw.get("labels")
        if not isinstance(text, str) or not text:
            raise ValueError("input must be a non-empty string")
        if not isinstance(labels, list) or not labels:
            raise ValueError("labels must be a non-empty list")
        max_labels = self._settings.forge_models_classify_max_labels
        if len(labels) > max_labels:
            raise ValueError(f"labels size {len(labels)} exceeds max {max_labels}")
        for index, label in enumerate(labels):
            if not isinstance(label, str) or not label:
                raise ValueError(f"labels[{index}] must be a non-empty string")
        return text, list(labels)

    def _parse_embed_input(self, raw: Any) -> list[str]:
        settings = self._settings
        if isinstance(raw, str):
            texts = [raw]
        elif isinstance(raw, dict) and "input" in raw:
            value = raw["input"]
            texts = [value] if isinstance(value, str) else list(value)
        elif isinstance(raw, list):
            texts = list(raw)
        else:
            raise ValueError("embed input must be a string, list, or {input:...}")
        if not texts:
            raise ValueError("input must be a non-empty string or list")
        if len(texts) > settings.forge_models_embed_max_batch:
            raise ValueError(
                f"batch size {len(texts)} exceeds max {settings.forge_models_embed_max_batch}"
            )
        for index, text in enumerate(texts):
            if not isinstance(text, str) or not text:
                raise ValueError(f"input[{index}] must be a non-empty string")
            if len(text) > settings.forge_models_embed_max_chars:
                raise ValueError(
                    f"input[{index}] length {len(text)} exceeds max "
                    f"{settings.forge_models_embed_max_chars} characters"
                )
        return texts
