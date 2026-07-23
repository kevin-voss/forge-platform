"""Bounded agent run loop: model → optional tool → observe → repeat."""

from __future__ import annotations

import asyncio
import logging
import time
from dataclasses import dataclass
from typing import Any

from app.agents.loader import AgentRegistry
from app.agents.models import AgentDefinition
from app.permissions import CallScope
from app.run.metrics import RunMetrics, default_run_metrics
from app.run.model_client import FakeModelClient, ModelClient
from app.run.store import TERMINAL_STATUSES, RunRecord, RunStore
from app.tools.invoker import ToolInvoker

logger = logging.getLogger("forge-agents")


@dataclass
class StartRunRequest:
    """Inputs for starting an agent run."""

    agent_name: str
    project_id: str
    run_input: str
    context: dict[str, Any]


class RunEngine:
    """Orchestrate concurrent bounded runs with audit persistence."""

    def __init__(
        self,
        *,
        store: RunStore,
        registry: AgentRegistry,
        invoker: ToolInvoker,
        model_client: ModelClient,
        fake_model_client: ModelClient | None = None,
        max_concurrent_runs: int = 4,
        metrics: RunMetrics | None = None,
    ) -> None:
        self._store = store
        self._registry = registry
        self._invoker = invoker
        self._model_client = model_client
        self._fake_model = fake_model_client or FakeModelClient()
        self._max_concurrent = max(1, max_concurrent_runs)
        self._metrics = metrics or default_run_metrics
        self._tasks: set[asyncio.Task[None]] = set()
        self._active = 0
        self._active_lock = asyncio.Lock()

    @property
    def store(self) -> RunStore:
        return self._store

    @property
    def active_runs(self) -> int:
        return self._active

    @property
    def max_concurrent_runs(self) -> int:
        return self._max_concurrent

    async def aclose(self) -> None:
        tasks = list(self._tasks)
        for task in tasks:
            task.cancel()
        if tasks:
            await asyncio.gather(*tasks, return_exceptions=True)
        self._tasks.clear()
        await self._model_client.aclose()
        await self._fake_model.aclose()

    async def start(self, request: StartRunRequest) -> RunRecord:
        agent = self._registry.get(request.agent_name)
        if agent is None:
            raise KeyError(f"agent not found: {request.agent_name}")

        async with self._active_lock:
            if self._active >= self._max_concurrent:
                raise RuntimeError("max_concurrent_runs")
            self._active += 1

        run = self._store.create_run(project_id=request.project_id, agent=agent.name)
        dry_run = bool(request.context.get("dry_run"))
        model: ModelClient = self._fake_model if dry_run else self._model_client

        task = asyncio.create_task(
            self._execute(run.id, agent, request, model),
            name=f"agent-run-{run.id}",
        )
        self._tasks.add(task)
        task.add_done_callback(self._tasks.discard)
        logger.info(
            "run started",
            extra={
                "run_id": run.id,
                "agent": agent.name,
                "project_id": request.project_id,
                "dry_run": dry_run,
                "max_steps": agent.limits.max_steps,
                "timeout_seconds": agent.limits.timeout_seconds,
            },
        )
        return run

    async def _execute(
        self,
        run_id: str,
        agent: AgentDefinition,
        request: StartRunRequest,
        model: ModelClient,
    ) -> None:
        started = time.monotonic()
        max_steps = agent.limits.max_steps
        timeout = float(agent.limits.timeout_seconds)
        history: list[dict[str, Any]] = []
        scope = CallScope.from_permissions(
            agent.permissions,
            project_id=request.project_id,
        )
        terminal_status = "failed"
        result: str | None = None
        error: str | None = None
        steps_done = 0

        try:
            async with asyncio.timeout(timeout):
                for _ in range(max_steps):
                    if self._store.is_cancel_requested(run_id):
                        terminal_status = "cancelled"
                        error = "cancelled"
                        break

                    decision = await model.decide(
                        agent=agent,
                        run_input=request.run_input,
                        history=list(history),
                        context=dict(request.context),
                    )
                    steps_done += 1
                    model_step = self._store.append_step(
                        run_id,
                        type="model",
                        decision=decision.to_decision_json(),
                    )
                    history.append(model_step.to_api_dict())
                    logger.info(
                        "run model step",
                        extra={
                            "run_id": run_id,
                            "agent": agent.name,
                            "project_id": request.project_id,
                            "step_kind": decision.kind,
                            "step_idx": model_step.idx,
                        },
                    )

                    if decision.kind == "final":
                        text = decision.text if decision.text is not None else ""
                        final_step = self._store.append_step(
                            run_id,
                            type="final",
                            observation=text,
                            decision=decision.to_decision_json(),
                        )
                        history.append(final_step.to_api_dict())
                        terminal_status = "succeeded"
                        result = text
                        error = None
                        break

                    if decision.kind != "tool_call" or not decision.tool:
                        terminal_status = "failed"
                        error = "invalid_model_decision"
                        break

                    invoke = await self._invoker.invoke(
                        agent=agent,
                        tool_name=decision.tool,
                        arguments=decision.args or {},
                        scope=scope,
                    )
                    observation: dict[str, Any]
                    if invoke.ok:
                        observation = {
                            "ok": True,
                            **(invoke.output or {}),
                        }
                    else:
                        observation = {
                            "ok": False,
                            "reason": invoke.reason,
                            "error": invoke.error,
                        }
                    tool_step = self._store.append_step(
                        run_id,
                        type="tool",
                        tool=decision.tool,
                        args=decision.args or {},
                        observation=observation,
                        decision=invoke.decision,
                    )
                    history.append(tool_step.to_api_dict())

                    if not invoke.ok:
                        terminal_status = "failed"
                        error = invoke.reason or "tool_denied"
                        break
                else:
                    # Exhausted max_steps without a final decision.
                    if terminal_status == "failed" and error is None:
                        terminal_status = "stopped"
                        error = "max_steps_exceeded"
        except TimeoutError:
            terminal_status = "failed"
            error = "timeout"
            logger.info(
                "run timeout",
                extra={
                    "run_id": run_id,
                    "agent": agent.name,
                    "project_id": request.project_id,
                },
            )
        except asyncio.CancelledError:
            terminal_status = "cancelled"
            error = "cancelled"
            raise
        except Exception as exc:  # noqa: BLE001 — persist and surface
            terminal_status = "failed"
            error = f"run_error: {exc}"
            logger.exception(
                "run failed",
                extra={
                    "run_id": run_id,
                    "agent": agent.name,
                    "project_id": request.project_id,
                },
            )
        finally:
            if error == "cancelled":
                terminal_status = "cancelled"

            current = self._store.get_run(run_id)
            if current is not None and current.status not in TERMINAL_STATUSES:
                self._store.finish_run(
                    run_id,
                    status=terminal_status,
                    result=result,
                    error=error,
                )
                finished_status = terminal_status
            else:
                finished_status = current.status if current is not None else terminal_status

            duration_ms = (time.monotonic() - started) * 1000.0
            self._metrics.record_terminal(
                finished_status,
                steps=steps_done,
                duration_ms=duration_ms,
            )
            async with self._active_lock:
                self._active = max(0, self._active - 1)
            logger.info(
                "run finished",
                extra={
                    "run_id": run_id,
                    "agent": agent.name,
                    "project_id": request.project_id,
                    "status": finished_status,
                    "error": error,
                    "steps": steps_done,
                },
            )
