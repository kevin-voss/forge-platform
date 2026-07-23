"""In-process counters for tool-call decisions (Prometheus labels later)."""

from __future__ import annotations

from collections import defaultdict
from threading import Lock


class ToolMetrics:
    """Track tool decisions, denials, and backend latency/errors."""

    def __init__(self) -> None:
        self._lock = Lock()
        self._calls: dict[tuple[str, str], int] = defaultdict(int)
        self._denied: dict[str, int] = defaultdict(int)
        self._backend_errors: dict[tuple[str, str], int] = defaultdict(int)
        self._backend_latency: dict[str, list[float]] = defaultdict(list)

    def record_allow(self, tool: str) -> None:
        with self._lock:
            self._calls[(tool, "allow")] += 1

    def record_deny(self, tool: str, reason: str) -> None:
        with self._lock:
            self._calls[(tool, "deny")] += 1
            self._denied[reason] += 1

    def record_backend_latency(self, tool: str, seconds: float) -> None:
        with self._lock:
            self._backend_latency[tool].append(seconds)

    def record_backend_error(self, tool: str, error_code: str) -> None:
        with self._lock:
            self._backend_errors[(tool, error_code)] += 1

    def snapshot(self) -> dict[str, object]:
        with self._lock:
            latency = []
            for tool, samples in sorted(self._backend_latency.items()):
                if not samples:
                    continue
                latency.append(
                    {
                        "tool": tool,
                        "count": len(samples),
                        "sum_seconds": sum(samples),
                        "max_seconds": max(samples),
                    }
                )
            return {
                "agent_tool_calls_total": [
                    {"tool": tool, "decision": decision, "value": value}
                    for (tool, decision), value in sorted(self._calls.items())
                ],
                "agent_tool_denied_total": [
                    {"reason": reason, "value": value}
                    for reason, value in sorted(self._denied.items())
                ],
                "agent_tool_backend_errors_total": [
                    {"tool": tool, "error_code": code, "value": value}
                    for (tool, code), value in sorted(self._backend_errors.items())
                ],
                "agent_tool_backend_latency_seconds": latency,
            }

    def reset(self) -> None:
        with self._lock:
            self._calls.clear()
            self._denied.clear()
            self._backend_errors.clear()
            self._backend_latency.clear()


# Process-wide default used by ToolInvoker unless injected.
default_tool_metrics = ToolMetrics()
