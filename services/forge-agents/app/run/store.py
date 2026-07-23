"""SQLite persistence for agent runs and audit steps."""

from __future__ import annotations

import json
import sqlite3
import threading
import uuid
from dataclasses import dataclass, field
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Sequence

_MIGRATIONS_DIR = Path(__file__).resolve().parents[2] / "migrations"


def _utc_now() -> str:
    return datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


TERMINAL_STATUSES = frozenset({"succeeded", "failed", "cancelled", "stopped"})
NON_TERMINAL_STATUSES = frozenset({"running", "awaiting_approval"})


@dataclass
class RunStep:
    """One persisted audit step within a run."""

    id: str
    run_id: str
    idx: int
    type: str
    tool: str | None = None
    args: dict[str, Any] | None = None
    observation: dict[str, Any] | str | None = None
    decision: str | None = None
    ts: str = ""

    def to_api_dict(self) -> dict[str, Any]:
        payload: dict[str, Any] = {"type": self.type, "ts": self.ts}
        if self.tool is not None:
            payload["tool"] = self.tool
        if self.args is not None:
            payload["args"] = self.args
        if self.observation is not None:
            payload["observation"] = self.observation
        if self.decision is not None:
            payload["decision"] = self.decision
        return payload


@dataclass
class RunRecord:
    """Persisted run row plus ordered steps."""

    id: str
    project_id: str
    agent: str
    status: str
    result: str | None = None
    error: str | None = None
    step_count: int = 0
    started_at: str = ""
    ended_at: str | None = None
    steps: list[RunStep] = field(default_factory=list)

    def to_api_dict(self, *, include_steps: bool = True) -> dict[str, Any]:
        payload: dict[str, Any] = {
            "run_id": self.id,
            "project_id": self.project_id,
            "agent": self.agent,
            "status": self.status,
            "step_count": self.step_count,
            "started_at": self.started_at,
        }
        if self.ended_at is not None:
            payload["ended_at"] = self.ended_at
        if self.result is not None:
            payload["result"] = self.result
        if self.error is not None:
            payload["error"] = self.error
        if include_steps:
            payload["steps"] = [s.to_api_dict() for s in self.steps]
        return payload


class RunStore:
    """Thread-safe SQLite store for runs + steps."""

    def __init__(self, db_path: str | Path) -> None:
        self._path = Path(db_path)
        self._lock = threading.RLock()
        self._cancel_flags: dict[str, threading.Event] = {}
        if str(self._path) != ":memory:":
            self._path.parent.mkdir(parents=True, exist_ok=True)
        # check_same_thread=False: FastAPI may touch DB from worker threads.
        self._conn = sqlite3.connect(str(self._path), check_same_thread=False)
        self._conn.row_factory = sqlite3.Row
        self._conn.execute("PRAGMA foreign_keys = ON")
        self._apply_migrations()

    def close(self) -> None:
        with self._lock:
            self._conn.close()

    def _apply_migrations(self) -> None:
        if not _MIGRATIONS_DIR.is_dir():
            raise FileNotFoundError(f"missing migrations dir: {_MIGRATIONS_DIR}")
        files = sorted(_MIGRATIONS_DIR.glob("*.sql"))
        if not files:
            raise FileNotFoundError(f"no migrations in {_MIGRATIONS_DIR}")
        with self._lock:
            self._conn.execute(
                """
                CREATE TABLE IF NOT EXISTS schema_migrations (
                  id TEXT PRIMARY KEY,
                  applied_at TEXT NOT NULL
                )
                """
            )
            applied = {
                str(row["id"])
                for row in self._conn.execute("SELECT id FROM schema_migrations").fetchall()
            }
            for path in files:
                mid = path.name
                if mid in applied:
                    continue
                self._conn.executescript(path.read_text(encoding="utf-8"))
                self._conn.execute(
                    "INSERT INTO schema_migrations (id, applied_at) VALUES (?, ?)",
                    (mid, _utc_now()),
                )
            self._conn.commit()

    @property
    def connection(self) -> sqlite3.Connection:
        """Shared SQLite connection (ApprovalStore may reuse it)."""
        return self._conn

    def create_run(
        self,
        *,
        project_id: str,
        agent: str,
        run_input: str = "",
        context: dict[str, Any] | None = None,
    ) -> RunRecord:
        run_id = str(uuid.uuid4())
        started = _utc_now()
        ctx = context if isinstance(context, dict) else {}
        with self._lock:
            self._conn.execute(
                """
                INSERT INTO runs (id, project_id, agent, status, step_count, started_at)
                VALUES (?, ?, ?, 'running', 0, ?)
                """,
                (run_id, project_id, agent, started),
            )
            self._conn.execute(
                """
                INSERT INTO run_resume (run_id, input, context)
                VALUES (?, ?, ?)
                """,
                (run_id, run_input, json.dumps(ctx)),
            )
            self._conn.commit()
            self._cancel_flags[run_id] = threading.Event()
        return RunRecord(
            id=run_id,
            project_id=project_id,
            agent=agent,
            status="running",
            started_at=started,
        )

    def get_resume(self, run_id: str) -> tuple[str, dict[str, Any]] | None:
        """Return (input, context) for restart-safe resume, or None."""
        with self._lock:
            row = self._conn.execute(
                "SELECT input, context FROM run_resume WHERE run_id = ?",
                (run_id,),
            ).fetchone()
            if row is None:
                return None
            try:
                ctx = json.loads(row["context"] or "{}")
            except (TypeError, json.JSONDecodeError):
                ctx = {}
            if not isinstance(ctx, dict):
                ctx = {}
            return str(row["input"] or ""), ctx

    def set_status(self, run_id: str, status: str) -> None:
        """Update non-terminal run status (e.g. awaiting_approval → running)."""
        with self._lock:
            self._conn.execute(
                "UPDATE runs SET status = ? WHERE id = ?",
                (status, run_id),
            )
            self._conn.commit()

    def list_by_status(self, status: str) -> list[RunRecord]:
        with self._lock:
            rows = self._conn.execute(
                """
                SELECT * FROM runs
                WHERE status = ?
                ORDER BY started_at ASC
                """,
                (status,),
            ).fetchall()
            return [self._row_to_run(row, self._load_steps(str(row["id"]))) for row in rows]

    def ensure_cancel_flag(self, run_id: str) -> threading.Event:
        """Re-create in-memory cancel flag after process restart."""
        with self._lock:
            event = self._cancel_flags.get(run_id)
            if event is None:
                event = threading.Event()
                self._cancel_flags[run_id] = event
            return event

    def cancel_event(self, run_id: str) -> threading.Event | None:
        with self._lock:
            return self._cancel_flags.get(run_id)

    def request_cancel(self, run_id: str) -> str | None:
        """Request cancel. Returns new status, 'already_terminal', or None if missing."""
        with self._lock:
            row = self._conn.execute(
                "SELECT status FROM runs WHERE id = ?",
                (run_id,),
            ).fetchone()
            if row is None:
                return None
            status = str(row["status"])
            if status in TERMINAL_STATUSES:
                return "already_terminal"
            event = self._cancel_flags.get(run_id)
            if event is not None:
                event.set()
            return "cancelling"

    def is_cancel_requested(self, run_id: str) -> bool:
        with self._lock:
            event = self._cancel_flags.get(run_id)
            return bool(event is not None and event.is_set())

    def append_step(
        self,
        run_id: str,
        *,
        type: str,
        tool: str | None = None,
        args: dict[str, Any] | None = None,
        observation: dict[str, Any] | str | None = None,
        decision: str | None = None,
        ts: str | None = None,
    ) -> RunStep:
        step_id = str(uuid.uuid4())
        stamp = ts or _utc_now()
        with self._lock:
            row = self._conn.execute(
                "SELECT step_count FROM runs WHERE id = ?",
                (run_id,),
            ).fetchone()
            if row is None:
                raise KeyError(f"unknown run: {run_id}")
            idx = int(row["step_count"])
            args_json = json.dumps(args) if args is not None else None
            obs_json: str | None
            if observation is None:
                obs_json = None
            elif isinstance(observation, str):
                obs_json = observation
            else:
                obs_json = json.dumps(observation)
            self._conn.execute(
                """
                INSERT INTO run_steps
                  (id, run_id, idx, type, tool, args, observation, decision, ts)
                VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
                """,
                (
                    step_id,
                    run_id,
                    idx,
                    type,
                    tool,
                    args_json,
                    obs_json,
                    decision,
                    stamp,
                ),
            )
            self._conn.execute(
                "UPDATE runs SET step_count = ? WHERE id = ?",
                (idx + 1, run_id),
            )
            self._conn.commit()
        return RunStep(
            id=step_id,
            run_id=run_id,
            idx=idx,
            type=type,
            tool=tool,
            args=args,
            observation=observation,
            decision=decision,
            ts=stamp,
        )

    def finish_run(
        self,
        run_id: str,
        *,
        status: str,
        result: str | None = None,
        error: str | None = None,
    ) -> None:
        ended = _utc_now()
        with self._lock:
            self._conn.execute(
                """
                UPDATE runs
                SET status = ?, result = ?, error = ?, ended_at = ?
                WHERE id = ?
                """,
                (status, result, error, ended, run_id),
            )
            self._conn.commit()
            self._cancel_flags.pop(run_id, None)

    def get_run(self, run_id: str, *, project_id: str | None = None) -> RunRecord | None:
        with self._lock:
            if project_id is None:
                row = self._conn.execute(
                    "SELECT * FROM runs WHERE id = ?",
                    (run_id,),
                ).fetchone()
            else:
                row = self._conn.execute(
                    "SELECT * FROM runs WHERE id = ? AND project_id = ?",
                    (run_id, project_id),
                ).fetchone()
            if row is None:
                return None
            steps = self._load_steps(run_id)
            return self._row_to_run(row, steps)

    def list_runs(self, *, project_id: str, limit: int = 100) -> list[RunRecord]:
        capped = max(1, min(limit, 500))
        with self._lock:
            rows = self._conn.execute(
                """
                SELECT * FROM runs
                WHERE project_id = ?
                ORDER BY started_at DESC
                LIMIT ?
                """,
                (project_id, capped),
            ).fetchall()
            return [self._row_to_run(row, []) for row in rows]

    def _load_steps(self, run_id: str) -> list[RunStep]:
        rows = self._conn.execute(
            """
            SELECT * FROM run_steps
            WHERE run_id = ?
            ORDER BY idx ASC
            """,
            (run_id,),
        ).fetchall()
        steps: list[RunStep] = []
        for row in rows:
            args = json.loads(row["args"]) if row["args"] else None
            obs_raw = row["observation"]
            observation: dict[str, Any] | str | None
            if obs_raw is None:
                observation = None
            else:
                try:
                    observation = json.loads(obs_raw)
                except (TypeError, json.JSONDecodeError):
                    observation = str(obs_raw)
            steps.append(
                RunStep(
                    id=row["id"],
                    run_id=row["run_id"],
                    idx=int(row["idx"]),
                    type=row["type"],
                    tool=row["tool"],
                    args=args,
                    observation=observation,
                    decision=row["decision"],
                    ts=row["ts"],
                )
            )
        return steps

    @staticmethod
    def _row_to_run(row: sqlite3.Row, steps: Sequence[RunStep]) -> RunRecord:
        return RunRecord(
            id=row["id"],
            project_id=row["project_id"],
            agent=row["agent"],
            status=row["status"],
            result=row["result"],
            error=row["error"],
            step_count=int(row["step_count"]),
            started_at=row["started_at"],
            ended_at=row["ended_at"],
            steps=list(steps),
        )
