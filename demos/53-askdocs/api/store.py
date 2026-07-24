"""Postgres repository for AskDocs messages (and schema migrations)."""

from __future__ import annotations

import json
import os
import time
import uuid
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

import psycopg
from psycopg.rows import dict_row


class StoreError(Exception):
    """Base store error."""


class EmptyTextError(StoreError):
    """Raised when a message text is empty."""


@dataclass
class Message:
    id: str
    session_id: str
    role: str
    text: str
    citations: list[Any]
    created_at: datetime

    def to_json(self) -> dict[str, Any]:
        return {
            "id": self.id,
            "sessionId": self.session_id,
            "role": self.role,
            "text": self.text,
            "citations": list(self.citations),
            "createdAt": self.created_at.astimezone(timezone.utc)
            .isoformat()
            .replace("+00:00", "Z"),
        }


def database_url(environ: dict[str, str] | None = None) -> str:
    env = environ if environ is not None else os.environ
    return (env.get("DATABASE_URL") or "").strip()


def resolve_migrations_dir(explicit: str | None = None) -> Path:
    if explicit and explicit.strip():
        return Path(explicit.strip())
    env = (os.environ.get("MIGRATIONS_DIR") or "").strip()
    if env:
        return Path(env)
    for candidate in (Path("/migrations"), Path("../migrations"), Path("migrations")):
        if candidate.is_dir():
            return candidate
    return Path("../migrations")


def refuse_control_db(url: str) -> None:
    if "postgres:5432/forge" in url or ":5001/forge" in url:
        raise StoreError("refusing Control database URL")


class MessageStore:
    def __init__(self, url: str, migrations_dir: Path | None = None) -> None:
        url = url.strip()
        if not url:
            raise StoreError("DATABASE_URL is required")
        refuse_control_db(url)
        self.url = url
        self.migrations_dir = migrations_dir or resolve_migrations_dir()
        self._conn: psycopg.Connection | None = None

    def connect(self) -> None:
        if self._conn is not None and not self._conn.closed:
            return
        self._conn = psycopg.connect(self.url, row_factory=dict_row)
        self._conn.execute("SELECT 1")

    def close(self) -> None:
        if self._conn is not None and not self._conn.closed:
            self._conn.close()
        self._conn = None

    def ping(self) -> None:
        self.connect()
        assert self._conn is not None
        self._conn.execute("SELECT 1")

    def migrate(self) -> None:
        self.connect()
        assert self._conn is not None
        files = sorted(self.migrations_dir.glob("*.sql"))
        if not files:
            raise StoreError(f"no .sql migrations in {self.migrations_dir}")
        for path in files:
            sql = path.read_text(encoding="utf-8")
            self._conn.execute(sql)
        self._conn.commit()

    def list_messages(self, session_id: str) -> list[Message]:
        self.connect()
        assert self._conn is not None
        rows = self._conn.execute(
            """
            SELECT id, session_id, role, text, citations, created_at
            FROM messages
            WHERE session_id = %s
            ORDER BY created_at ASC, id ASC
            """,
            (session_id,),
        ).fetchall()
        return [self._row_to_message(r) for r in rows]

    def append_message(
        self,
        session_id: str,
        role: str,
        text: str,
        citations: list[Any] | None = None,
    ) -> Message:
        text = (text or "").strip()
        if not text:
            raise EmptyTextError("text is required")
        if role not in ("user", "assistant"):
            raise StoreError(f"invalid role: {role}")
        session_id = (session_id or "").strip() or "default"
        msg = Message(
            id=uuid.uuid4().hex,
            session_id=session_id,
            role=role,
            text=text,
            citations=list(citations or []),
            created_at=datetime.now(timezone.utc),
        )
        self.connect()
        assert self._conn is not None
        self._conn.execute(
            """
            INSERT INTO messages (id, session_id, role, text, citations, created_at)
            VALUES (%s, %s, %s, %s, %s::jsonb, %s)
            """,
            (
                msg.id,
                msg.session_id,
                msg.role,
                msg.text,
                json.dumps(msg.citations),
                msg.created_at,
            ),
        )
        self._conn.commit()
        return msg

    def echo_chat(self, session_id: str, text: str) -> dict[str, Any]:
        """Persist user message + echo assistant reply (stub until 53.04)."""
        user = self.append_message(session_id, "user", text)
        reply_text = f"Echo: {user.text}"
        assistant = self.append_message(session_id, "assistant", reply_text, citations=[])
        return {
            "sessionId": user.session_id,
            "user": user.to_json(),
            "assistant": assistant.to_json(),
        }

    @staticmethod
    def _row_to_message(row: dict[str, Any]) -> Message:
        citations = row.get("citations") or []
        if isinstance(citations, str):
            citations = json.loads(citations)
        created = row["created_at"]
        if created.tzinfo is None:
            created = created.replace(tzinfo=timezone.utc)
        return Message(
            id=str(row["id"]),
            session_id=str(row["session_id"]),
            role=str(row["role"]),
            text=str(row["text"]),
            citations=list(citations),
            created_at=created,
        )


def open_store_with_retry(
    url: str | None = None,
    migrations_dir: Path | None = None,
    budget_s: float = 60.0,
) -> MessageStore:
    deadline = time.time() + budget_s
    last: Exception | None = None
    while True:
        try:
            store = MessageStore(url or database_url(), migrations_dir)
            store.connect()
            return store
        except Exception as exc:  # noqa: BLE001 — retry until budget
            last = exc
            if time.time() >= deadline:
                raise StoreError(f"database unavailable: {last}") from last
            time.sleep(2)
