"""Model decision client: HTTP to forge-models + deterministic fake for dry-run."""

from __future__ import annotations

import asyncio
import json
import logging
import re
from dataclasses import dataclass
from typing import Any, Protocol

import httpx

from app.agents.models import AgentDefinition

logger = logging.getLogger("forge-agents")

_JSON_BLOCK_RE = re.compile(r"\{.*\}", re.DOTALL)


@dataclass(frozen=True)
class ModelDecision:
    """One model turn: either call a tool or produce a final answer."""

    kind: str  # tool_call | final
    tool: str | None = None
    args: dict[str, Any] | None = None
    text: str | None = None
    raw: str | None = None

    def to_decision_json(self) -> str:
        payload: dict[str, Any] = {"kind": self.kind}
        if self.tool is not None:
            payload["tool"] = self.tool
        if self.args is not None:
            payload["args"] = self.args
        if self.text is not None:
            payload["text"] = self.text
        return json.dumps(payload, separators=(",", ":"))


class ModelClient(Protocol):
    """Decide the next agent action from run history."""

    async def decide(
        self,
        *,
        agent: AgentDefinition,
        run_input: str,
        history: list[dict[str, Any]],
        context: dict[str, Any],
    ) -> ModelDecision: ...

    async def aclose(self) -> None: ...


class FakeModelClient:
    """Deterministic scripted planner for dry-run / CI (no forge-models required)."""

    def __init__(self, *, decide_delay_seconds: float = 0.0) -> None:
        self._decide_delay_seconds = decide_delay_seconds

    async def decide(
        self,
        *,
        agent: AgentDefinition,
        run_input: str,
        history: list[dict[str, Any]],
        context: dict[str, Any],
    ) -> ModelDecision:
        delay = float(context.get("decide_delay_seconds", self._decide_delay_seconds) or 0.0)
        if delay > 0:
            await asyncio.sleep(delay)

        # Explicit scripted plan for multi-step CI demos (index by prior model steps).
        planned = _decision_from_plan(context, history)
        if planned is not None:
            return planned

        force_loop = bool(context.get("force_loop"))
        tool_obs = [h for h in history if h.get("type") == "tool"]

        if force_loop and agent.tools:
            tool = _pick_tool(agent, context)
            return ModelDecision(
                kind="tool_call",
                tool=tool,
                args=_args_for_tool(tool, run_input, context),
                raw="fake:force_loop",
            )

        if not tool_obs and agent.tools:
            tool = _pick_tool(agent, context)
            return ModelDecision(
                kind="tool_call",
                tool=tool,
                args=_args_for_tool(tool, run_input, context),
                raw="fake:first_tool",
            )

        # Prefer last tool observation as the final answer body.
        result_text = run_input
        if tool_obs:
            last = tool_obs[-1].get("observation")
            if isinstance(last, dict) and "echo" in last:
                result_text = str(last["echo"])
            elif last is not None:
                result_text = json.dumps(last) if isinstance(last, dict) else str(last)

        return ModelDecision(
            kind="final",
            text=result_text,
            raw="fake:final",
        )

    async def aclose(self) -> None:
        return None


class HttpModelClient:
    """Call forge-models generate and parse a structured decision JSON when present."""

    def __init__(
        self,
        base_url: str,
        *,
        timeout_seconds: float = 30.0,
        client: httpx.AsyncClient | None = None,
    ) -> None:
        self._base = base_url.rstrip("/")
        self._owns_client = client is None
        self._client = client or httpx.AsyncClient(
            base_url=self._base,
            timeout=timeout_seconds,
        )

    async def decide(
        self,
        *,
        agent: AgentDefinition,
        run_input: str,
        history: list[dict[str, Any]],
        context: dict[str, Any],
    ) -> ModelDecision:
        prompt = _build_decision_prompt(agent, run_input, history, context)
        url = f"/v1/models/{agent.model}/generate"
        try:
            resp = await self._client.post(
                url,
                json={"prompt": prompt, "max_tokens": 256, "temperature": 0},
            )
        except httpx.HTTPError as exc:
            raise RuntimeError(f"models request failed: {exc}") from exc

        if resp.status_code >= 400:
            raise RuntimeError(
                f"models generate failed: HTTP {resp.status_code}: {resp.text[:300]}"
            )

        body = resp.json()
        text = str(body.get("text") or "")
        parsed = _parse_decision_text(text)
        if parsed is not None:
            return parsed
        # Fake/local generate echoes the prompt — treat as final with truncated body.
        return ModelDecision(kind="final", text=text.strip() or run_input, raw=text)

    async def aclose(self) -> None:
        if self._owns_client:
            await self._client.aclose()


def _decision_from_plan(
    context: dict[str, Any],
    history: list[dict[str, Any]],
) -> ModelDecision | None:
    """Return the next decision from context['plan'] when present.

    Each plan entry is a dict with kind tool_call|final (same shape as ModelDecision).
    Index advances by the number of prior model steps already recorded in history.
    """
    plan = context.get("plan")
    if not isinstance(plan, list) or not plan:
        return None
    idx = len([h for h in history if h.get("type") == "model"])
    if idx >= len(plan):
        return None
    item = plan[idx]
    if not isinstance(item, dict):
        return None
    kind = str(item.get("kind") or item.get("type") or "").strip()
    if kind == "tool_call":
        tool = item.get("tool")
        if not isinstance(tool, str) or not tool.strip():
            return None
        args = item.get("args") if isinstance(item.get("args"), dict) else {}
        return ModelDecision(
            kind="tool_call",
            tool=tool.strip(),
            args=dict(args),
            raw=f"fake:plan:{idx}",
        )
    if kind == "final":
        return ModelDecision(
            kind="final",
            text=str(item.get("text") or ""),
            raw=f"fake:plan:{idx}",
        )
    return None


def _pick_tool(agent: AgentDefinition, context: dict[str, Any]) -> str:
    preferred = context.get("tool")
    if isinstance(preferred, str) and preferred in agent.tools:
        return preferred
    if "echo.ping" in agent.tools:
        return "echo.ping"
    return agent.tools[0]


def _args_for_tool(tool: str, run_input: str, context: dict[str, Any]) -> dict[str, Any]:
    override = context.get("tool_args")
    if isinstance(override, dict):
        return dict(override)
    if tool == "echo.ping":
        return {"message": run_input or "ping"}
    if tool == "deployment.read":
        dep = context.get("deployment_id") or "dep-fixture"
        return {"deployment_id": str(dep)}
    if tool == "logs.search":
        args: dict[str, Any] = {"limit": 20}
        if context.get("deployment_id"):
            args["deployment"] = str(context["deployment_id"])
        elif context.get("project_id"):
            args["project"] = str(context["project_id"])
        else:
            args["deployment"] = "dep-fixture"
        if context.get("q"):
            args["q"] = str(context["q"])
        return args
    if tool == "metrics.query":
        return {"query": str(context.get("query") or "up")}
    if tool == "runtime.restart":
        dep = context.get("deployment_id") or "dep-fixture"
        return {"deployment_id": str(dep)}
    if tool == "storage.get":
        return {
            "bucket": str(context.get("bucket") or "agent-notes"),
            "key": str(context.get("key") or "diag.txt"),
        }
    if tool == "storage.put":
        return {
            "bucket": str(context.get("bucket") or "agent-notes"),
            "key": str(context.get("key") or "diag.txt"),
            "content": run_input or "note",
        }
    if tool == "models.generate":
        return {
            "model": str(context.get("model") or "local-general"),
            "prompt": run_input or "diagnose",
        }
    if tool == "models.embed":
        return {
            "model": str(context.get("model") or "local-embed-small"),
            "input": run_input or "hello",
        }
    if tool == "events.publish":
        return {
            "subject": str(context.get("subject") or "application.diagnosed"),
            "data": {"message": run_input or "ok"},
            "source": "forge-agents",
        }
    if tool == "fail.raise":
        return {"reason": run_input or "fail"}
    return {}


def _build_decision_prompt(
    agent: AgentDefinition,
    run_input: str,
    history: list[dict[str, Any]],
    context: dict[str, Any],
) -> str:
    tools = ", ".join(agent.tools) if agent.tools else "(none)"
    hist = json.dumps(history, separators=(",", ":"))
    ctx = json.dumps(context, separators=(",", ":"))
    return (
        "Forge agent decision. Reply with JSON only: "
        '{"kind":"tool_call","tool":"<name>","args":{...}} or '
        '{"kind":"final","text":"..."}.\n'
        f"agent={agent.name}\n"
        f"tools=[{tools}]\n"
        f"input={run_input}\n"
        f"context={ctx}\n"
        f"history={hist}\n"
    )


def _parse_decision_text(text: str) -> ModelDecision | None:
    stripped = text.strip()
    if not stripped:
        return None
    candidates = [stripped]
    match = _JSON_BLOCK_RE.search(stripped)
    if match:
        candidates.append(match.group(0))
    for cand in candidates:
        try:
            data = json.loads(cand)
        except json.JSONDecodeError:
            continue
        if not isinstance(data, dict):
            continue
        kind = str(data.get("kind") or data.get("type") or "").strip()
        if kind == "tool_call":
            tool = data.get("tool")
            args = data.get("args") if isinstance(data.get("args"), dict) else {}
            if isinstance(tool, str) and tool.strip():
                return ModelDecision(
                    kind="tool_call",
                    tool=tool.strip(),
                    args=args,
                    raw=stripped,
                )
        if kind == "final":
            return ModelDecision(
                kind="final",
                text=str(data.get("text") or ""),
                raw=stripped,
            )
    return None
