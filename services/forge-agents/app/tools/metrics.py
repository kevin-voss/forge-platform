"""In-process counters for tool-call decisions (Prometheus labels later)."""

from __future__ import annotations

from collections import defaultdict
from threading import Lock


class ToolMetrics:
    """Track agent_tool_calls_total{tool,decision} and agent_tool_denied_total{reason}."""

    def __init__(self) -> None:
        self._lock = Lock()
        self._calls: dict[tuple[str, str], int] = defaultdict(int)
        self._denied: dict[str, int] = defaultdict(int)

    def record_allow(self, tool: str) -> None:
        with self._lock:
            self._calls[(tool, "allow")] += 1

    def record_deny(self, tool: str, reason: str) -> None:
        with self._lock:
            self._calls[(tool, "deny")] += 1
            self._denied[reason] += 1

    def snapshot(self) -> dict[str, object]:
        with self._lock:
            return {
                "agent_tool_calls_total": [
                    {"tool": tool, "decision": decision, "value": value}
                    for (tool, decision), value in sorted(self._calls.items())
                ],
                "agent_tool_denied_total": [
                    {"reason": reason, "value": value}
                    for reason, value in sorted(self._denied.items())
                ],
            }

    def reset(self) -> None:
        with self._lock:
            self._calls.clear()
            self._denied.clear()


# Process-wide default used by ToolInvoker unless injected.
default_tool_metrics = ToolMetrics()
