"""In-process approval metrics (Prometheus labels later)."""

from __future__ import annotations

from collections import defaultdict
from threading import Lock


class ApprovalMetrics:
    """Track agent_approvals_total{status} and time-to-decision histogram."""

    def __init__(self) -> None:
        self._lock = Lock()
        self._total: dict[str, int] = defaultdict(int)
        self._decision_ms: list[float] = []

    def record_created(self) -> None:
        with self._lock:
            self._total["pending"] += 1

    def record_decision(self, status: str, *, decision_ms: float) -> None:
        with self._lock:
            self._total[status] += 1
            self._decision_ms.append(decision_ms)

    def snapshot(self) -> dict[str, object]:
        with self._lock:
            durations = list(self._decision_ms)
            return {
                "agent_approvals_total": [
                    {"status": status, "value": value}
                    for status, value in sorted(self._total.items())
                ],
                "agent_approval_decision_ms": {
                    "count": len(durations),
                    "sum": sum(durations) if durations else 0.0,
                    "max": max(durations) if durations else 0.0,
                },
            }

    def reset(self) -> None:
        with self._lock:
            self._total.clear()
            self._decision_ms.clear()


default_approval_metrics = ApprovalMetrics()
