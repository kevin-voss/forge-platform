"""In-memory job registry with validated state transitions."""

from __future__ import annotations

import asyncio
import logging
import time
import uuid
from dataclasses import dataclass, field
from enum import StrEnum
from typing import Any

logger = logging.getLogger("forge-models")


class JobStatus(StrEnum):
    QUEUED = "queued"
    RUNNING = "running"
    SUCCEEDED = "succeeded"
    FAILED = "failed"
    CANCELLED = "cancelled"


TERMINAL_STATUSES = frozenset({JobStatus.SUCCEEDED, JobStatus.FAILED, JobStatus.CANCELLED})

_ALLOWED_TRANSITIONS: dict[JobStatus, frozenset[JobStatus]] = {
    JobStatus.QUEUED: frozenset({JobStatus.RUNNING, JobStatus.CANCELLED}),
    JobStatus.RUNNING: frozenset({JobStatus.SUCCEEDED, JobStatus.FAILED, JobStatus.CANCELLED}),
    JobStatus.SUCCEEDED: frozenset(),
    JobStatus.FAILED: frozenset(),
    JobStatus.CANCELLED: frozenset(),
}


class InvalidTransitionError(ValueError):
    """Raised when a job status transition is not allowed."""


@dataclass
class Job:
    id: str
    project_id: str
    model: str
    task: str
    input: Any
    status: JobStatus = JobStatus.QUEUED
    result: Any | None = None
    error: dict[str, str] | None = None
    created_at: float = field(default_factory=time.time)
    updated_at: float = field(default_factory=time.time)
    cancel_event: asyncio.Event = field(default_factory=asyncio.Event)
    # Optional cooperative delay (ms) before/during work — used by tests.
    delay_ms: int = 0

    def is_terminal(self) -> bool:
        return self.status in TERMINAL_STATUSES

    def as_public_dict(self) -> dict[str, Any]:
        payload: dict[str, Any] = {"status": self.status.value}
        if self.result is not None:
            payload["result"] = self.result
        if self.error is not None:
            payload["error"] = self.error
        return payload


@dataclass
class JobMetrics:
    """In-process job/stream gauges until 14.06 wires Prometheus."""

    models_stream_active: int = 0
    models_jobs_total: dict[str, int] = field(default_factory=dict)
    models_job_duration_seconds_sum: float = 0.0
    models_job_duration_count: int = 0

    def bump_stream_active(self, delta: int) -> None:
        self.models_stream_active = max(0, self.models_stream_active + delta)

    def record_job(self, status: JobStatus, duration_seconds: float | None = None) -> None:
        key = status.value
        self.models_jobs_total[key] = self.models_jobs_total.get(key, 0) + 1
        if duration_seconds is not None and status in TERMINAL_STATUSES:
            self.models_job_duration_seconds_sum += max(0.0, duration_seconds)
            self.models_job_duration_count += 1


class JobStore:
    """Thread-safe-enough in-memory job map (single asyncio event loop)."""

    def __init__(self, *, ttl_seconds: int = 3600) -> None:
        self._jobs: dict[str, Job] = {}
        self._ttl_seconds = ttl_seconds
        self.metrics = JobMetrics()
        self._lock = asyncio.Lock()

    @property
    def ttl_seconds(self) -> int:
        return self._ttl_seconds

    def create(
        self,
        *,
        project_id: str,
        model: str,
        task: str,
        input_payload: Any,
        delay_ms: int = 0,
    ) -> Job:
        job = Job(
            id=str(uuid.uuid4()),
            project_id=project_id,
            model=model,
            task=task,
            input=input_payload,
            delay_ms=max(0, delay_ms),
        )
        self._jobs[job.id] = job
        self.metrics.record_job(JobStatus.QUEUED)
        logger.info(
            "job created",
            extra={
                "job_id": job.id,
                "project_id": project_id,
                "model": model,
                "task": task,
                "status": job.status.value,
            },
        )
        return job

    def get(self, job_id: str, *, project_id: str) -> Job | None:
        job = self._jobs.get(job_id)
        if job is None or job.project_id != project_id:
            return None
        return job

    def get_raw(self, job_id: str) -> Job | None:
        return self._jobs.get(job_id)

    def transition(
        self,
        job_id: str,
        new_status: JobStatus,
        *,
        result: Any | None = None,
        error: dict[str, str] | None = None,
    ) -> Job:
        job = self._jobs.get(job_id)
        if job is None:
            raise KeyError(job_id)
        allowed = _ALLOWED_TRANSITIONS[job.status]
        if new_status not in allowed:
            raise InvalidTransitionError(
                f"cannot transition job {job_id} from {job.status.value} to {new_status.value}"
            )
        previous = job.status
        job.status = new_status
        job.updated_at = time.time()
        if result is not None:
            job.result = result
        if error is not None:
            job.error = error
        if new_status == JobStatus.CANCELLED:
            job.cancel_event.set()
        duration = job.updated_at - job.created_at
        if new_status in TERMINAL_STATUSES:
            self.metrics.record_job(new_status, duration)
        logger.info(
            "job transition",
            extra={
                "job_id": job.id,
                "project_id": job.project_id,
                "from": previous.value,
                "status": new_status.value,
                "duration_ms": round(duration * 1000.0, 3),
            },
        )
        return job

    def request_cancel(self, job_id: str, *, project_id: str) -> Job | None | str:
        """
        Cancel a non-terminal job.

        Returns the job on success, None if missing/wrong project, or
        ``"terminal"`` when the job is already finished.
        """
        job = self.get(job_id, project_id=project_id)
        if job is None:
            return None
        if job.is_terminal():
            return "terminal"
        job.cancel_event.set()
        return self.transition(job_id, JobStatus.CANCELLED)

    def gc_expired(self, *, now: float | None = None) -> int:
        """Drop terminal jobs older than TTL. Returns number removed."""
        cutoff = (now if now is not None else time.time()) - self._ttl_seconds
        remove: list[str] = []
        for job_id, job in self._jobs.items():
            if job.is_terminal() and job.updated_at < cutoff:
                remove.append(job_id)
        for job_id in remove:
            del self._jobs[job_id]
        if remove:
            logger.info("job gc", extra={"removed": len(remove)})
        return len(remove)

    def queued_ids(self) -> list[str]:
        return [j.id for j in self._jobs.values() if j.status == JobStatus.QUEUED]
