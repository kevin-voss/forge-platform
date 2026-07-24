"""Forge Agents HTTP client for AskDocs grounded answerer (epic 53.04)."""

from __future__ import annotations

import json
import os
import time
import urllib.error
import urllib.request
from dataclasses import dataclass
from typing import Any


@dataclass
class AgentsConfig:
    base_url: str
    project_id: str
    agent_name: str
    poll_attempts: int
    poll_sleep_seconds: float


def load_agents_config(environ: dict[str, str] | None = None) -> AgentsConfig:
    env = environ if environ is not None else os.environ
    base = (env.get("FORGE_AGENTS_URL") or "").strip() or "http://host.docker.internal:4301"
    project = (
        env.get("FORGE_AGENTS_PROJECT")
        or env.get("FORGE_MEMORY_PROJECT")
        or env.get("FORGE_STORAGE_PROJECT")
        or env.get("FORGE_PROJECT")
        or ""
    ).strip() or "askdocs"
    agent = (env.get("ASKDOCS_AGENT_NAME") or "").strip() or "askdocs-answerer"
    attempts_raw = (env.get("ASKDOCS_AGENT_POLL_ATTEMPTS") or "").strip()
    sleep_raw = (env.get("ASKDOCS_AGENT_POLL_SLEEP_SECONDS") or "").strip()
    return AgentsConfig(
        base_url=base.rstrip("/"),
        project_id=project,
        agent_name=agent,
        poll_attempts=int(attempts_raw) if attempts_raw else 90,
        poll_sleep_seconds=float(sleep_raw) if sleep_raw else 0.25,
    )


class AgentsError(RuntimeError):
    """Agents client / run failure."""


class AgentsClient:
    def __init__(self, cfg: AgentsConfig | None = None) -> None:
        self.cfg = cfg or load_agents_config()

    def _request(
        self,
        method: str,
        url: str,
        data: bytes | None = None,
        timeout: float = 30.0,
    ) -> tuple[int, bytes]:
        headers = {
            "Accept": "application/json",
            "X-Forge-Project": self.cfg.project_id,
        }
        if data is not None:
            headers["Content-Type"] = "application/json"
        req = urllib.request.Request(url, data=data, headers=headers, method=method)
        try:
            with urllib.request.urlopen(req, timeout=timeout) as resp:
                return int(resp.status), resp.read()
        except urllib.error.HTTPError as exc:
            body = exc.read() if exc.fp is not None else b""
            return int(exc.code), body

    def ping(self) -> None:
        code, body = self._request("GET", f"{self.cfg.base_url}/health/ready")
        if code != 200:
            raise AgentsError(f"agents not ready HTTP {code}: {body[:200]!r}")

    def list_agents(self) -> list[str]:
        code, body = self._request("GET", f"{self.cfg.base_url}/v1/agents")
        if code != 200:
            raise AgentsError(f"list agents HTTP {code}: {body[:200]!r}")
        payload = json.loads(body.decode() or "{}")
        return [str(a.get("name") or "") for a in (payload.get("agents") or [])]

    def ensure_agent(self) -> None:
        names = self.list_agents()
        if self.cfg.agent_name not in names:
            raise AgentsError(
                f"agent {self.cfg.agent_name!r} not registered (have {names})"
            )

    def start_run(
        self,
        run_input: str,
        *,
        context: dict[str, Any] | None = None,
    ) -> str:
        payload = {
            "input": run_input,
            "context": dict(context or {}),
        }
        data = json.dumps(payload).encode()
        url = f"{self.cfg.base_url}/v1/agents/{self.cfg.agent_name}/runs"
        code, body = self._request("POST", url, data=data)
        if code not in (200, 202):
            raise AgentsError(f"start run HTTP {code}: {body[:300]!r}")
        parsed = json.loads(body.decode() or "{}")
        run_id = str(parsed.get("run_id") or "").strip()
        if not run_id:
            raise AgentsError(f"start run missing run_id: {parsed}")
        return run_id

    def get_run(self, run_id: str) -> dict[str, Any]:
        code, body = self._request("GET", f"{self.cfg.base_url}/v1/runs/{run_id}")
        if code != 200:
            raise AgentsError(f"get run HTTP {code}: {body[:300]!r}")
        return json.loads(body.decode() or "{}")

    def wait_run(
        self,
        run_id: str,
        *,
        want: set[str] | None = None,
    ) -> dict[str, Any]:
        terminal = want or {"succeeded", "failed", "cancelled", "stopped"}
        last: dict[str, Any] = {}
        for _ in range(max(1, self.cfg.poll_attempts)):
            last = self.get_run(run_id)
            status = str(last.get("status") or "")
            if status in terminal:
                return last
            time.sleep(self.cfg.poll_sleep_seconds)
        raise AgentsError(
            f"run {run_id} did not reach {sorted(terminal)}; last={last}"
        )

    def run_plan(
        self,
        run_input: str,
        plan: list[dict[str, Any]],
        *,
        extra_context: dict[str, Any] | None = None,
    ) -> dict[str, Any]:
        """Deterministic dry-run with scripted FakeModelClient plan."""
        context: dict[str, Any] = {
            "dry_run": True,
            "plan": plan,
            "collection": (extra_context or {}).get("collection") or "askdocs-chunks",
            "query": run_input,
            "top_k": int((extra_context or {}).get("top_k") or 5),
        }
        if extra_context:
            for key, value in extra_context.items():
                if key not in context:
                    context[key] = value
        run_id = self.start_run(run_input, context=context)
        result = self.wait_run(run_id, want={"succeeded", "failed", "stopped", "cancelled"})
        status = str(result.get("status") or "")
        if status != "succeeded":
            raise AgentsError(f"agent run status={status}: {result}")
        return result
