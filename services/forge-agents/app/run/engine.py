"""Bounded agent run loop: model → optional tool → observe → repeat."""

from __future__ import annotations

import asyncio
import logging
import time
from dataclasses import dataclass
from datetime import datetime, timezone
from typing import Any

from app.agents.loader import AgentRegistry
from app.agents.models import AgentDefinition
from app.approvals.metrics import ApprovalMetrics, default_approval_metrics
from app.approvals.store import (
    APPROVED,
    DENIED,
    EXPIRED,
    PENDING,
    ApprovalRecord,
    ApprovalStore,
)
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
        approvals: ApprovalStore | None = None,
        approval_ttl_seconds: int = 3600,
        max_concurrent_runs: int = 4,
        metrics: RunMetrics | None = None,
        approval_metrics: ApprovalMetrics | None = None,
    ) -> None:
        self._store = store
        self._registry = registry
        self._invoker = invoker
        self._model_client = model_client
        self._fake_model = fake_model_client or FakeModelClient()
        self._approvals = approvals
        self._approval_ttl = max(1, approval_ttl_seconds)
        self._max_concurrent = max(1, max_concurrent_runs)
        self._metrics = metrics or default_run_metrics
        self._approval_metrics = approval_metrics or default_approval_metrics
        self._tasks: set[asyncio.Task[None]] = set()
        self._active = 0
        self._active_lock = asyncio.Lock()
        self._approval_events: dict[str, asyncio.Event] = {}
        self._events_lock = asyncio.Lock()

    @property
    def store(self) -> RunStore:
        return self._store

    @property
    def approvals(self) -> ApprovalStore | None:
        return self._approvals

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

        run = self._store.create_run(
            project_id=request.project_id,
            agent=agent.name,
            run_input=request.run_input,
            context=request.context,
        )
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

    async def recover_awaiting_runs(self) -> int:
        """Re-attach tasks for runs left in awaiting_approval across restarts."""
        awaiting = self._store.list_by_status("awaiting_approval")
        recovered = 0
        for run in awaiting:
            if self._approvals is None:
                continue
            pending = self._approvals.get_pending_for_run(run.id)
            if pending is None:
                # Orphaned awaiting state: treat as denied and resume if possible.
                logger.info(
                    "awaiting run has no pending approval; failing closed",
                    extra={"run_id": run.id, "project_id": run.project_id},
                )
                self._store.finish_run(
                    run.id,
                    status="failed",
                    error="approval_missing",
                )
                continue
            agent = self._registry.get(run.agent)
            resume = self._store.get_resume(run.id)
            if agent is None or resume is None:
                self._store.finish_run(
                    run.id,
                    status="failed",
                    error="resume_state_missing",
                )
                continue
            run_input, context = resume
            request = StartRunRequest(
                agent_name=agent.name,
                project_id=run.project_id,
                run_input=run_input,
                context=context,
            )
            dry_run = bool(context.get("dry_run"))
            model: ModelClient = self._fake_model if dry_run else self._model_client
            self._store.ensure_cancel_flag(run.id)

            async with self._active_lock:
                self._active += 1

            task = asyncio.create_task(
                self._execute(
                    run.id,
                    agent,
                    request,
                    model,
                    resume_from_approval=pending,
                ),
                name=f"agent-run-resume-{run.id}",
            )
            self._tasks.add(task)
            task.add_done_callback(self._tasks.discard)
            recovered += 1
            logger.info(
                "recovered awaiting_approval run",
                extra={
                    "run_id": run.id,
                    "approval_id": pending.id,
                    "tool": pending.tool,
                    "project_id": run.project_id,
                },
            )
        return recovered

    async def notify_approval_decision(self, approval_id: str) -> None:
        """Wake a paused run after approve/deny/expire."""
        async with self._events_lock:
            event = self._approval_events.get(approval_id)
        if event is not None:
            event.set()

    async def expire_stale_approvals(self) -> int:
        """Expire pending approvals past TTL and wake waiters."""
        if self._approvals is None:
            return 0
        expired = self._approvals.expire_stale()
        for record in expired:
            created = _parse_ts(record.created_at)
            decision_ms = (time.time() - created.timestamp()) * 1000.0
            self._approval_metrics.record_decision(EXPIRED, decision_ms=decision_ms)
            logger.info(
                "approval expired",
                extra={
                    "approval_id": record.id,
                    "run_id": record.run_id,
                    "tool": record.tool,
                    "project_id": record.project_id,
                    "actor": "system",
                },
            )
            await self.notify_approval_decision(record.id)
        return len(expired)

    async def _execute(
        self,
        run_id: str,
        agent: AgentDefinition,
        request: StartRunRequest,
        model: ModelClient,
        *,
        resume_from_approval: ApprovalRecord | None = None,
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
        paused_for_approval = False

        # Rebuild history when resuming after restart.
        existing = self._store.get_run(run_id)
        if existing is not None and existing.steps:
            history = [s.to_api_dict() for s in existing.steps]
            steps_done = len([s for s in existing.steps if s.type == "model"])

        try:
            async with asyncio.timeout(timeout):
                # Finish a pending approval gate before continuing the model loop.
                if resume_from_approval is not None:
                    outcome = await self._await_and_apply_approval(
                        run_id=run_id,
                        agent=agent,
                        request=request,
                        scope=scope,
                        approval=resume_from_approval,
                        history=history,
                    )
                    if outcome == "cancelled":
                        terminal_status = "cancelled"
                        error = "cancelled"
                        raise _RunStop()
                    if outcome == "failed":
                        terminal_status = "failed"
                        error = "approval_gate_failed"
                        raise _RunStop()

                for _ in range(max_steps - steps_done if resume_from_approval else max_steps):
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

                    tool_name = decision.tool
                    tool_args = decision.args or {}
                    tool = self._invoker.registry.get(tool_name)
                    is_destructive = bool(tool is not None and tool.destructive)

                    if is_destructive:
                        if self._approvals is None:
                            observation = {
                                "ok": False,
                                "reason": "approval_required",
                                "error": "destructive tool requires approval store",
                            }
                            tool_step = self._store.append_step(
                                run_id,
                                type="tool",
                                tool=tool_name,
                                args=tool_args,
                                observation=observation,
                                decision="deny",
                            )
                            history.append(tool_step.to_api_dict())
                            terminal_status = "failed"
                            error = "approval_required"
                            break

                        pre = await self._invoker.validate(
                            agent=agent,
                            tool_name=tool_name,
                            arguments=tool_args,
                            scope=scope,
                        )
                        if not pre.ok:
                            observation = {
                                "ok": False,
                                "reason": pre.reason,
                                "error": pre.error,
                            }
                            tool_step = self._store.append_step(
                                run_id,
                                type="tool",
                                tool=tool_name,
                                args=tool_args,
                                observation=observation,
                                decision=pre.decision,
                            )
                            history.append(tool_step.to_api_dict())
                            terminal_status = "failed"
                            error = pre.reason or "tool_denied"
                            break

                        approval = self._approvals.create(
                            run_id=run_id,
                            project_id=request.project_id,
                            tool=tool_name,
                            args=tool_args,
                            ttl_seconds=self._approval_ttl,
                        )
                        self._approval_metrics.record_created()
                        self._store.set_status(run_id, "awaiting_approval")
                        paused_for_approval = True
                        logger.info(
                            "approval created",
                            extra={
                                "approval_id": approval.id,
                                "run_id": run_id,
                                "tool": tool_name,
                                "project_id": request.project_id,
                                "expires_at": approval.expires_at,
                            },
                        )

                        outcome = await self._await_and_apply_approval(
                            run_id=run_id,
                            agent=agent,
                            request=request,
                            scope=scope,
                            approval=approval,
                            history=history,
                        )
                        paused_for_approval = False
                        if outcome == "cancelled":
                            terminal_status = "cancelled"
                            error = "cancelled"
                            break
                        if outcome == "failed":
                            terminal_status = "failed"
                            error = "approval_gate_failed"
                            break
                        # approved / denied / expired → tool step recorded; continue loop
                        continue

                    invoke = await self._invoker.invoke(
                        agent=agent,
                        tool_name=tool_name,
                        arguments=tool_args,
                        scope=scope,
                    )
                    observation = _observation_from_invoke(invoke)
                    tool_step = self._store.append_step(
                        run_id,
                        type="tool",
                        tool=tool_name,
                        args=tool_args,
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
        except _RunStop:
            pass
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

            # If still paused awaiting approval (shutdown), leave status as-is.
            current = self._store.get_run(run_id)
            if (
                paused_for_approval
                and current is not None
                and current.status == "awaiting_approval"
            ):
                async with self._active_lock:
                    self._active = max(0, self._active - 1)
                logger.info(
                    "run paused awaiting_approval",
                    extra={
                        "run_id": run_id,
                        "agent": agent.name,
                        "project_id": request.project_id,
                    },
                )
                return

            if current is not None and current.status not in TERMINAL_STATUSES:
                # Expire any still-pending approval when the run ends.
                if self._approvals is not None:
                    pending = self._approvals.get_pending_for_run(run_id)
                    if pending is not None:
                        self._approvals.decide(
                            pending.id,
                            status=EXPIRED,
                            decided_by="system",
                            reason=error or terminal_status,
                        )
                        await self.notify_approval_decision(pending.id)

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

    async def _await_and_apply_approval(
        self,
        *,
        run_id: str,
        agent: AgentDefinition,
        request: StartRunRequest,
        scope: CallScope,
        approval: ApprovalRecord,
        history: list[dict[str, Any]],
    ) -> str:
        """Wait for a human/system decision, then execute or skip the tool.

        Returns: 'ok' | 'cancelled' | 'failed'
        """
        if self._approvals is None:
            return "failed"

        event = await self._ensure_approval_event(approval.id)
        # Already decided (e.g. expired while recovering)?
        current = self._approvals.get(approval.id)
        if current is not None and current.status != PENDING:
            event.set()

        while True:
            if self._store.is_cancel_requested(run_id):
                return "cancelled"

            refreshed = self._approvals.get(approval.id)
            if refreshed is None:
                return "failed"
            if refreshed.status != PENDING:
                break

            # Auto-expire if wall clock passed TTL while waiting.
            if _is_expired(refreshed.expires_at):
                self._approvals.decide(
                    approval.id,
                    status=EXPIRED,
                    decided_by="system",
                    reason="approval_ttl_exceeded",
                )
                created = _parse_ts(refreshed.created_at)
                self._approval_metrics.record_decision(
                    EXPIRED,
                    decision_ms=(time.time() - created.timestamp()) * 1000.0,
                )
                logger.info(
                    "approval expired",
                    extra={
                        "approval_id": approval.id,
                        "run_id": run_id,
                        "tool": refreshed.tool,
                        "project_id": request.project_id,
                        "actor": "system",
                    },
                )
                break

            try:
                await asyncio.wait_for(event.wait(), timeout=0.25)
            except TimeoutError:
                continue
            event.clear()

        decided = self._approvals.get(approval.id)
        if decided is None:
            return "failed"

        tool_name = decided.tool
        tool_args = decided.args

        if decided.status == APPROVED:
            # Safety: never execute without a persisted approved record.
            self._store.set_status(run_id, "running")
            invoke = await self._invoker.invoke(
                agent=agent,
                tool_name=tool_name,
                arguments=tool_args,
                scope=scope,
            )
            observation = _observation_from_invoke(invoke)
            observation["approval_id"] = decided.id
            observation["approval_status"] = APPROVED
            tool_step = self._store.append_step(
                run_id,
                type="tool",
                tool=tool_name,
                args=tool_args,
                observation=observation,
                decision=invoke.decision,
            )
            history.append(tool_step.to_api_dict())
            logger.info(
                "approval approved; tool executed",
                extra={
                    "approval_id": decided.id,
                    "run_id": run_id,
                    "tool": tool_name,
                    "project_id": request.project_id,
                    "actor": decided.decided_by,
                    "ok": invoke.ok,
                },
            )
            if not invoke.ok:
                return "failed"
            return "ok"

        # deny / expired → skip tool execution, record, continue
        reason = (
            "approval_denied"
            if decided.status == DENIED
            else "approval_expired"
            if decided.status == EXPIRED
            else f"approval_{decided.status}"
        )
        observation = {
            "ok": False,
            "reason": reason,
            "approval_id": decided.id,
            "approval_status": decided.status,
            "error": decided.reason or reason,
        }
        tool_step = self._store.append_step(
            run_id,
            type="tool",
            tool=tool_name,
            args=tool_args,
            observation=observation,
            decision="deny",
        )
        history.append(tool_step.to_api_dict())
        self._store.set_status(run_id, "running")
        logger.info(
            "approval denied; tool skipped",
            extra={
                "approval_id": decided.id,
                "run_id": run_id,
                "tool": tool_name,
                "project_id": request.project_id,
                "actor": decided.decided_by,
                "status": decided.status,
            },
        )
        return "ok"

    async def _ensure_approval_event(self, approval_id: str) -> asyncio.Event:
        async with self._events_lock:
            event = self._approval_events.get(approval_id)
            if event is None:
                event = asyncio.Event()
                self._approval_events[approval_id] = event
            return event


class _RunStop(Exception):
    """Internal control-flow for resume failures inside the timeout block."""


def _observation_from_invoke(invoke: Any) -> dict[str, Any]:
    if invoke.ok:
        return {"ok": True, **(invoke.output or {})}
    return {
        "ok": False,
        "reason": invoke.reason,
        "error": invoke.error,
    }


def _parse_ts(ts: str) -> datetime:
    return datetime.strptime(ts, "%Y-%m-%dT%H:%M:%SZ").replace(tzinfo=timezone.utc)


def _is_expired(expires_at: str) -> bool:
    try:
        return _parse_ts(expires_at) <= datetime.now(timezone.utc)
    except ValueError:
        return False
