"""Postgres repository for AskDocs messages, documents, and chunks."""

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


class EmptyDocumentError(StoreError):
    """Raised when document text is empty."""


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


@dataclass
class Document:
    id: str
    title: str
    object_key: str
    status: str
    created_at: datetime

    def to_json(self) -> dict[str, Any]:
        return {
            "id": self.id,
            "title": self.title,
            "objectKey": self.object_key,
            "status": self.status,
            "createdAt": self.created_at.astimezone(timezone.utc)
            .isoformat()
            .replace("+00:00", "Z"),
        }


@dataclass
class Chunk:
    id: str
    document_id: str
    ordinal: int
    text: str
    memory_id: str | None
    created_at: datetime

    def to_json(self) -> dict[str, Any]:
        return {
            "id": self.id,
            "documentId": self.document_id,
            "ordinal": self.ordinal,
            "text": self.text,
            "memoryId": self.memory_id,
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

    def create_document(
        self,
        title: str,
        object_key: str,
        status: str = "ingesting",
        document_id: str | None = None,
    ) -> Document:
        title = (title or "").strip() or "Untitled"
        object_key = (object_key or "").strip()
        if not object_key:
            raise StoreError("object_key is required")
        if status not in ("ingesting", "ready"):
            raise StoreError(f"invalid status: {status}")
        doc = Document(
            id=(document_id or "").strip() or uuid.uuid4().hex,
            title=title,
            object_key=object_key,
            status=status,
            created_at=datetime.now(timezone.utc),
        )
        self.connect()
        assert self._conn is not None
        self._conn.execute(
            """
            INSERT INTO documents (id, title, object_key, status, created_at)
            VALUES (%s, %s, %s, %s, %s)
            """,
            (doc.id, doc.title, doc.object_key, doc.status, doc.created_at),
        )
        self._conn.commit()
        return doc

    def get_document(self, document_id: str) -> Document | None:
        self.connect()
        assert self._conn is not None
        row = self._conn.execute(
            """
            SELECT id, title, object_key, status, created_at
            FROM documents
            WHERE id = %s
            """,
            (document_id,),
        ).fetchone()
        return self._row_to_document(row) if row else None

    def list_documents(self) -> list[Document]:
        self.connect()
        assert self._conn is not None
        rows = self._conn.execute(
            """
            SELECT id, title, object_key, status, created_at
            FROM documents
            ORDER BY created_at DESC, id ASC
            """
        ).fetchall()
        return [self._row_to_document(r) for r in rows]

    def replace_chunks(self, document_id: str, texts: list[str]) -> list[Chunk]:
        """Replace all chunks for a document. Leaves status=ingesting (ready is 53.03)."""
        document_id = (document_id or "").strip()
        if not document_id:
            raise StoreError("document_id is required")
        self.connect()
        assert self._conn is not None
        doc = self.get_document(document_id)
        if doc is None:
            raise StoreError(f"document not found: {document_id}")
        self._conn.execute("DELETE FROM chunks WHERE document_id = %s", (document_id,))
        now = datetime.now(timezone.utc)
        chunks: list[Chunk] = []
        for ordinal, text in enumerate(texts):
            chunk = Chunk(
                id=uuid.uuid4().hex,
                document_id=document_id,
                ordinal=ordinal,
                text=text,
                memory_id=None,
                created_at=now,
            )
            self._conn.execute(
                """
                INSERT INTO chunks (id, document_id, ordinal, text, memory_id, created_at)
                VALUES (%s, %s, %s, %s, %s, %s)
                """,
                (
                    chunk.id,
                    chunk.document_id,
                    chunk.ordinal,
                    chunk.text,
                    chunk.memory_id,
                    chunk.created_at,
                ),
            )
            chunks.append(chunk)
        # Keep status=ingesting until embeddings land in 53.03.
        self._conn.execute(
            "UPDATE documents SET status = 'ingesting' WHERE id = %s",
            (document_id,),
        )
        self._conn.commit()
        return chunks

    def list_chunks(self, document_id: str) -> list[Chunk]:
        self.connect()
        assert self._conn is not None
        rows = self._conn.execute(
            """
            SELECT id, document_id, ordinal, text, memory_id, created_at
            FROM chunks
            WHERE document_id = %s
            ORDER BY ordinal ASC, id ASC
            """,
            (document_id,),
        ).fetchall()
        return [self._row_to_chunk(r) for r in rows]

    def list_ready_chunks(self) -> list[Chunk]:
        """Chunks belonging to documents with status=ready."""
        self.connect()
        assert self._conn is not None
        rows = self._conn.execute(
            """
            SELECT c.id, c.document_id, c.ordinal, c.text, c.memory_id, c.created_at
            FROM chunks c
            INNER JOIN documents d ON d.id = c.document_id
            WHERE d.status = 'ready'
            ORDER BY c.document_id ASC, c.ordinal ASC, c.id ASC
            """
        ).fetchall()
        return [self._row_to_chunk(r) for r in rows]

    def get_chunks_by_ids(self, chunk_ids: list[str]) -> list[Chunk]:
        ids = [str(i).strip() for i in chunk_ids if str(i).strip()]
        if not ids:
            return []
        self.connect()
        assert self._conn is not None
        rows = self._conn.execute(
            """
            SELECT id, document_id, ordinal, text, memory_id, created_at
            FROM chunks
            WHERE id = ANY(%s)
            """,
            (ids,),
        ).fetchall()
        by_id = {str(r["id"]): self._row_to_chunk(r) for r in rows}
        return [by_id[i] for i in ids if i in by_id]

    def set_chunk_memory_ids(self, document_id: str, mapping: dict[str, str]) -> None:
        """Set memory_id for chunks of a document (chunk_id → memory_id)."""
        document_id = (document_id or "").strip()
        if not document_id:
            raise StoreError("document_id is required")
        if not mapping:
            return
        self.connect()
        assert self._conn is not None
        for chunk_id, memory_id in mapping.items():
            cid = str(chunk_id).strip()
            mid = str(memory_id).strip()
            if not cid or not mid:
                continue
            self._conn.execute(
                """
                UPDATE chunks
                SET memory_id = %s
                WHERE id = %s AND document_id = %s
                """,
                (mid, cid, document_id),
            )
        self._conn.commit()

    def mark_document_ready(self, document_id: str) -> Document:
        document_id = (document_id or "").strip()
        if not document_id:
            raise StoreError("document_id is required")
        self.connect()
        assert self._conn is not None
        self._conn.execute(
            "UPDATE documents SET status = 'ready' WHERE id = %s",
            (document_id,),
        )
        self._conn.commit()
        doc = self.get_document(document_id)
        if doc is None:
            raise StoreError(f"document not found: {document_id}")
        return doc

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

    @staticmethod
    def _row_to_document(row: dict[str, Any]) -> Document:
        created = row["created_at"]
        if created.tzinfo is None:
            created = created.replace(tzinfo=timezone.utc)
        return Document(
            id=str(row["id"]),
            title=str(row["title"]),
            object_key=str(row["object_key"]),
            status=str(row["status"]),
            created_at=created,
        )

    @staticmethod
    def _row_to_chunk(row: dict[str, Any]) -> Chunk:
        created = row["created_at"]
        if created.tzinfo is None:
            created = created.replace(tzinfo=timezone.utc)
        memory_id = row.get("memory_id")
        return Chunk(
            id=str(row["id"]),
            document_id=str(row["document_id"]),
            ordinal=int(row["ordinal"]),
            text=str(row["text"]),
            memory_id=str(memory_id) if memory_id is not None else None,
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
