"""SQLite persistence for destructive-tool approval requests."""

from __future__ import annotations

import json
import sqlite3
import threading
import uuid
from dataclasses import dataclass
from datetime import datetime, timedelta, timezone
from pathlib import Path
from typing import Any

PENDING = "pending"
APPROVED = "approved"
DENIED = "denied"
EXPIRED = "expired"

APPROVAL_STATUSES = frozenset({PENDING, APPROVED, DENIED, EXPIRED})
TERMINAL_APPROVAL_STATUSES = frozenset({APPROVED, DENIED, EXPIRED})


def _utc_now() -> datetime:
    return datetime.now(timezone.utc)


def _fmt(dt: datetime) -> str:
    return dt.astimezone(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def _parse(ts: str) -> datetime:
    return datetime.strptime(ts, "%Y-%m-%dT%H:%M:%SZ").replace(tzinfo=timezone.utc)


@dataclass
class ApprovalRecord:
    """Persisted approval request + decision."""

    id: str
    run_id: str
    project_id: str
    tool: str
    args: dict[str, Any]
    status: str
    created_at: str
    expires_at: str
    decided_by: str | None = None
    reason: str | None = None
    decided_at: str | None = None

    def to_api_dict(self) -> dict[str, Any]:
        payload: dict[str, Any] = {
            "id": self.id,
            "run_id": self.run_id,
            "tool": self.tool,
            "args": self.args,
            "status": self.status,
            "created_at": self.created_at,
            "expires_at": self.expires_at,
        }
        if self.decided_by is not None:
            payload["decided_by"] = self.decided_by
        if self.reason is not None:
            payload["reason"] = self.reason
        if self.decided_at is not None:
            payload["decided_at"] = self.decided_at
        return payload


class ApprovalStore:
    """Thread-safe SQLite store for approval requests.

    Shares the agents DB path with RunStore; migrations are applied by RunStore.
    """

    def __init__(self, db_path: str | Path, *, conn: sqlite3.Connection | None = None) -> None:
        self._path = Path(db_path)
        self._lock = threading.RLock()
        self._owns_conn = conn is None
        if conn is not None:
            self._conn = conn
        else:
            if str(self._path) != ":memory:":
                self._path.parent.mkdir(parents=True, exist_ok=True)
            self._conn = sqlite3.connect(str(self._path), check_same_thread=False)
            self._conn.row_factory = sqlite3.Row
            self._conn.execute("PRAGMA foreign_keys = ON")

    def close(self) -> None:
        if self._owns_conn:
            with self._lock:
                self._conn.close()

    def create(
        self,
        *,
        run_id: str,
        project_id: str,
        tool: str,
        args: dict[str, Any],
        ttl_seconds: int,
    ) -> ApprovalRecord:
        approval_id = str(uuid.uuid4())
        now = _utc_now()
        created = _fmt(now)
        expires = _fmt(now + timedelta(seconds=max(1, ttl_seconds)))
        args_json = json.dumps(args)
        with self._lock:
            self._conn.execute(
                """
                INSERT INTO approvals
                  (id, run_id, project_id, tool, args, status, created_at, expires_at)
                VALUES (?, ?, ?, ?, ?, ?, ?, ?)
                """,
                (
                    approval_id,
                    run_id,
                    project_id,
                    tool,
                    args_json,
                    PENDING,
                    created,
                    expires,
                ),
            )
            self._conn.commit()
        return ApprovalRecord(
            id=approval_id,
            run_id=run_id,
            project_id=project_id,
            tool=tool,
            args=args,
            status=PENDING,
            created_at=created,
            expires_at=expires,
        )

    def get(
        self,
        approval_id: str,
        *,
        project_id: str | None = None,
    ) -> ApprovalRecord | None:
        with self._lock:
            if project_id is None:
                row = self._conn.execute(
                    "SELECT * FROM approvals WHERE id = ?",
                    (approval_id,),
                ).fetchone()
            else:
                row = self._conn.execute(
                    "SELECT * FROM approvals WHERE id = ? AND project_id = ?",
                    (approval_id, project_id),
                ).fetchone()
            if row is None:
                return None
            return self._row_to_record(row)

    def get_pending_for_run(self, run_id: str) -> ApprovalRecord | None:
        with self._lock:
            row = self._conn.execute(
                """
                SELECT * FROM approvals
                WHERE run_id = ? AND status = ?
                ORDER BY created_at DESC
                LIMIT 1
                """,
                (run_id, PENDING),
            ).fetchone()
            if row is None:
                return None
            return self._row_to_record(row)

    def list(
        self,
        *,
        project_id: str,
        status: str | None = None,
        limit: int = 100,
    ) -> list[ApprovalRecord]:
        capped = max(1, min(limit, 500))
        with self._lock:
            if status is None:
                rows = self._conn.execute(
                    """
                    SELECT * FROM approvals
                    WHERE project_id = ?
                    ORDER BY created_at DESC
                    LIMIT ?
                    """,
                    (project_id, capped),
                ).fetchall()
            else:
                rows = self._conn.execute(
                    """
                    SELECT * FROM approvals
                    WHERE project_id = ? AND status = ?
                    ORDER BY created_at DESC
                    LIMIT ?
                    """,
                    (project_id, status, capped),
                ).fetchall()
            return [self._row_to_record(row) for row in rows]

    def decide(
        self,
        approval_id: str,
        *,
        status: str,
        decided_by: str,
        reason: str | None = None,
        project_id: str | None = None,
    ) -> str:
        """Apply a terminal decision.

        Returns:
          'ok' | 'not_found' | 'already_terminal' | 'invalid_status'
        """
        if status not in {APPROVED, DENIED, EXPIRED}:
            return "invalid_status"
        decided_at = _fmt(_utc_now())
        with self._lock:
            if project_id is None:
                row = self._conn.execute(
                    "SELECT status FROM approvals WHERE id = ?",
                    (approval_id,),
                ).fetchone()
            else:
                row = self._conn.execute(
                    "SELECT status FROM approvals WHERE id = ? AND project_id = ?",
                    (approval_id, project_id),
                ).fetchone()
            if row is None:
                return "not_found"
            current = str(row["status"])
            if current in TERMINAL_APPROVAL_STATUSES:
                return "already_terminal"
            self._conn.execute(
                """
                UPDATE approvals
                SET status = ?, decided_by = ?, reason = ?, decided_at = ?
                WHERE id = ?
                """,
                (status, decided_by, reason, decided_at, approval_id),
            )
            self._conn.commit()
            return "ok"

    def expire_stale(self, *, now: datetime | None = None) -> list[ApprovalRecord]:
        """Mark pending approvals past expires_at as expired. Returns expired rows."""
        stamp = _fmt(now or _utc_now())
        expired: list[ApprovalRecord] = []
        with self._lock:
            rows = self._conn.execute(
                """
                SELECT * FROM approvals
                WHERE status = ? AND expires_at <= ?
                """,
                (PENDING, stamp),
            ).fetchall()
            for row in rows:
                self._conn.execute(
                    """
                    UPDATE approvals
                    SET status = ?, decided_by = ?, reason = ?, decided_at = ?
                    WHERE id = ? AND status = ?
                    """,
                    (
                        EXPIRED,
                        "system",
                        "approval_ttl_exceeded",
                        stamp,
                        row["id"],
                        PENDING,
                    ),
                )
                record = self._row_to_record(row)
                record.status = EXPIRED
                record.decided_by = "system"
                record.reason = "approval_ttl_exceeded"
                record.decided_at = stamp
                expired.append(record)
            if expired:
                self._conn.commit()
        return expired

    @staticmethod
    def _row_to_record(row: sqlite3.Row) -> ApprovalRecord:
        args_raw = row["args"]
        try:
            args = json.loads(args_raw) if args_raw else {}
        except (TypeError, json.JSONDecodeError):
            args = {}
        if not isinstance(args, dict):
            args = {}
        return ApprovalRecord(
            id=row["id"],
            run_id=row["run_id"],
            project_id=row["project_id"],
            tool=row["tool"],
            args=args,
            status=row["status"],
            decided_by=row["decided_by"],
            reason=row["reason"],
            created_at=row["created_at"],
            expires_at=row["expires_at"],
            decided_at=row["decided_at"],
        )
