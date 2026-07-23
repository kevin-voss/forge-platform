"""In-process run lifecycle metrics (Prometheus labels later)."""

from __future__ import annotations

from collections import defaultdict
from threading import Lock


class RunMetrics:
    """Track agent_runs_total{status}, agent_run_steps, run duration histogram buckets."""

    def __init__(self) -> None:
        self._lock = Lock()
        self._runs: dict[str, int] = defaultdict(int)
        self._steps: int = 0
        self._durations_ms: list[float] = []

    def record_terminal(self, status: str, *, steps: int, duration_ms: float) -> None:
        with self._lock:
            self._runs[status] += 1
            self._steps += max(0, steps)
            self._durations_ms.append(duration_ms)

    def snapshot(self) -> dict[str, object]:
        with self._lock:
            durations = list(self._durations_ms)
            return {
                "agent_runs_total": [
                    {"status": status, "value": value}
                    for status, value in sorted(self._runs.items())
                ],
                "agent_run_steps": self._steps,
                "agent_run_duration_ms": {
                    "count": len(durations),
                    "sum": sum(durations) if durations else 0.0,
                    "max": max(durations) if durations else 0.0,
                },
            }

    def reset(self) -> None:
        with self._lock:
            self._runs.clear()
            self._steps = 0
            self._durations_ms.clear()


default_run_metrics = RunMetrics()
