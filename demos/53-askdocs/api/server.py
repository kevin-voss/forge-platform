#!/usr/bin/env python3
"""AskDocs API — chat echo + document upload/ingest kickoff (epic 53.02)."""

from __future__ import annotations

import cgi
import json
import os
import re
import time
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any
from urllib.parse import parse_qs, urlparse

from events import EventsClient
from storage import StorageClient
from store import (
    EmptyDocumentError,
    EmptyTextError,
    MessageStore,
    StoreError,
    open_store_with_retry,
)

PORT = int(os.environ.get("PORT", "8080"))
SERVICE_NAME = os.environ.get("FORGE_SERVICE_NAME", "askdocs-api")
DEFAULT_SESSION = os.environ.get("ASKDOCS_DEFAULT_SESSION", "default")
STARTED_AT = time.time()

_STORE: MessageStore | None = None
_STORAGE: StorageClient | None = None
_EVENTS: EventsClient | None = None

_DOC_RE = re.compile(r"^/documents/([A-Za-z0-9_-]+)$")
_CHUNKS_RE = re.compile(r"^/documents/([A-Za-z0-9_-]+)/chunks$")


def get_store() -> MessageStore:
    global _STORE
    if _STORE is None:
        raise StoreError("store not initialized")
    return _STORE


def get_storage() -> StorageClient:
    global _STORAGE
    if _STORAGE is None:
        raise StoreError("storage not initialized")
    return _STORAGE


def get_events() -> EventsClient:
    global _EVENTS
    if _EVENTS is None:
        raise StoreError("events not initialized")
    return _EVENTS


def sanitize_filename(name: str) -> str:
    name = (name or "").strip().replace("\\", "_").replace("/", "_").replace("..", "_")
    if not name:
        return "document.txt"
    return name[:180]


def filename_stem(filename: str) -> str:
    name = sanitize_filename(filename)
    if "." in name:
        return name.rsplit(".", 1)[0]
    return name


def document_object_key(document_id: str, filename: str) -> str:
    return f"documents/{document_id}/{sanitize_filename(filename)}"


class Handler(BaseHTTPRequestHandler):
    server_version = "askdocs-api/0.2"

    def log_message(self, fmt: str, *args: Any) -> None:  # noqa: A003
        return

    def do_OPTIONS(self) -> None:  # noqa: N802
        self.send_response(204)
        self._cors()
        self.end_headers()

    def do_GET(self) -> None:  # noqa: N802
        parsed = urlparse(self.path)
        path = parsed.path
        if path == "/health/live":
            self._write_json(200, {"status": "ok"})
            return
        if path == "/health/ready":
            errors: list[str] = []
            try:
                get_store().ping()
            except Exception as exc:  # noqa: BLE001
                errors.append(f"database: {type(exc).__name__}: {exc}")
            try:
                get_storage().ping()
            except Exception as exc:  # noqa: BLE001
                errors.append(f"storage: {type(exc).__name__}: {exc}")
            try:
                get_events().ping()
            except Exception as exc:  # noqa: BLE001
                errors.append(f"events: {type(exc).__name__}: {exc}")
            if errors:
                self._write_json(503, {"status": "not_ready", "error": "; ".join(errors)})
            else:
                self._write_json(200, {"status": "ok"})
            return
        if path == "/messages":
            qs = parse_qs(parsed.query)
            session_id = (qs.get("sessionId") or qs.get("session_id") or [DEFAULT_SESSION])[0]
            try:
                messages = [m.to_json() for m in get_store().list_messages(session_id)]
                self._write_json(200, {"sessionId": session_id, "messages": messages})
            except Exception as exc:  # noqa: BLE001
                self._write_json(500, {"error": f"list failed: {exc}"})
            return
        if path == "/documents":
            try:
                docs = [d.to_json() for d in get_store().list_documents()]
                self._write_json(200, {"documents": docs})
            except Exception as exc:  # noqa: BLE001
                self._write_json(500, {"error": f"list documents failed: {exc}"})
            return
        m = _CHUNKS_RE.match(path)
        if m:
            doc_id = m.group(1)
            try:
                if get_store().get_document(doc_id) is None:
                    self._write_json(404, {"error": "not_found"})
                    return
                chunks = [c.to_json() for c in get_store().list_chunks(doc_id)]
                self._write_json(200, {"documentId": doc_id, "chunks": chunks})
            except Exception as exc:  # noqa: BLE001
                self._write_json(500, {"error": f"list chunks failed: {exc}"})
            return
        m = _DOC_RE.match(path)
        if m:
            doc_id = m.group(1)
            try:
                doc = get_store().get_document(doc_id)
                if doc is None:
                    self._write_json(404, {"error": "not_found"})
                    return
                self._write_json(200, doc.to_json())
            except Exception as exc:  # noqa: BLE001
                self._write_json(500, {"error": f"get document failed: {exc}"})
            return
        if path == "/" or path.startswith("/?"):
            self._write_json(
                200,
                {
                    "service": SERVICE_NAME,
                    "language": "python",
                    "status": "running",
                    "uptime_seconds": time.time() - STARTED_AT,
                    "chat": "POST /chat (echo stub until 53.04)",
                    "documents": "GET/POST /documents",
                },
            )
            return
        self._write_json(404, {"error": "not_found"})

    def do_POST(self) -> None:  # noqa: N802
        parsed = urlparse(self.path)
        path = parsed.path
        if path == "/chat":
            body = self._read_json()
            if body is None:
                self._write_json(400, {"error": "invalid json"})
                return
            text = str(body.get("text") or body.get("message") or "")
            session_id = str(
                body.get("sessionId") or body.get("session_id") or DEFAULT_SESSION
            )
            try:
                result = get_store().echo_chat(session_id, text)
                self._write_json(201, result)
            except EmptyTextError:
                self._write_json(400, {"error": "text is required"})
            except Exception as exc:  # noqa: BLE001
                self._write_json(500, {"error": f"chat failed: {exc}"})
            return
        if path == "/documents":
            try:
                title, text, filename, content_type = self._read_upload()
                doc = self._upload_document(title, text, filename, content_type)
                self._write_json(201, doc.to_json())
            except EmptyDocumentError:
                self._write_json(400, {"error": "text is required"})
            except ValueError as exc:
                self._write_json(400, {"error": str(exc)})
            except Exception as exc:  # noqa: BLE001
                self._write_json(500, {"error": f"upload failed: {exc}"})
            return
        self._write_json(404, {"error": "not_found"})

    def _upload_document(self, title: str, text: str, filename: str, content_type: str):
        text = (text or "").strip()
        if not text:
            raise EmptyDocumentError("text is required")
        filename = sanitize_filename(filename or "document.txt")
        title = (title or "").strip() or filename_stem(filename) or "Untitled"
        content_type = (content_type or "text/plain").split(";")[0].strip() or "text/plain"
        document_id = uuid.uuid4().hex
        object_key = document_object_key(document_id, filename)
        storage = get_storage()
        storage.ensure_bucket()
        storage.put_object(object_key, text.encode("utf-8"), content_type=content_type)
        doc = get_store().create_document(
            title=title,
            object_key=object_key,
            status="ingesting",
            document_id=document_id,
        )
        get_events().publish_document_uploaded(
            document_id=doc.id,
            object_key=doc.object_key,
            content_type=content_type,
            title=doc.title,
        )
        return doc

    def _read_upload(self) -> tuple[str, str, str, str]:
        ctype = (self.headers.get("Content-Type") or "").lower()
        if "multipart/form-data" in ctype:
            environ = {
                "REQUEST_METHOD": "POST",
                "CONTENT_TYPE": self.headers.get("Content-Type", ""),
                "CONTENT_LENGTH": self.headers.get("Content-Length") or "0",
            }
            form = cgi.FieldStorage(fp=self.rfile, headers=self.headers, environ=environ)
            title = _form_str(form, "title")
            text = _form_str(form, "text")
            filename = _form_str(form, "filename")
            content_type = "text/plain"
            if "file" in form:
                file_item = form["file"]
                if getattr(file_item, "file", None) is not None:
                    raw = file_item.file.read()
                    if isinstance(raw, bytes):
                        text = raw.decode("utf-8", errors="replace")
                    else:
                        text = str(raw)
                    if getattr(file_item, "filename", None):
                        filename = filename or str(file_item.filename)
                    if getattr(file_item, "type", None):
                        content_type = str(file_item.type) or content_type
            if not (text or "").strip():
                raise EmptyDocumentError("text is required")
            filename = filename or "document.txt"
            return title, text, filename, content_type

        body = self._read_json()
        if body is None:
            raise ValueError("invalid json")
        title = str(body.get("title") or "")
        text = str(body.get("text") or body.get("content") or "")
        filename = str(body.get("filename") or body.get("fileName") or "document.txt")
        content_type = str(body.get("contentType") or body.get("content_type") or "text/plain")
        if not text.strip():
            raise EmptyDocumentError("text is required")
        return title, text, filename, content_type

    def _read_json(self) -> dict[str, Any] | None:
        length = int(self.headers.get("Content-Length") or "0")
        raw = self.rfile.read(length) if length > 0 else b"{}"
        try:
            data = json.loads(raw.decode() or "{}")
        except json.JSONDecodeError:
            return None
        return data if isinstance(data, dict) else None

    def _cors(self) -> None:
        self.send_header("Access-Control-Allow-Origin", "*")
        self.send_header("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
        self.send_header("Access-Control-Allow-Headers", "Content-Type, Authorization")

    def _write_json(self, status: int, payload: dict[str, Any] | list[Any]) -> None:
        body = (json.dumps(payload) + "\n").encode()
        self.send_response(status)
        self._cors()
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


def _form_str(form: cgi.FieldStorage, key: str) -> str:
    if key not in form:
        return ""
    item = form[key]
    if getattr(item, "file", None) is not None and item.filename:
        raw = item.file.read()
        if isinstance(raw, bytes):
            return raw.decode("utf-8", errors="replace")
        return str(raw)
    val = item.value if hasattr(item, "value") else item
    return "" if val is None else str(val)


def main() -> None:
    global _STORE, _STORAGE, _EVENTS
    store = open_store_with_retry()
    store.migrate()
    storage = StorageClient()
    events = EventsClient()
    try:
        storage.ensure_bucket()
    except Exception as exc:  # noqa: BLE001
        print(f"askdocs-api warn: ensure_bucket: {exc}", flush=True)
    _STORE = store
    _STORAGE = storage
    _EVENTS = events
    print(f"askdocs-api migrations applied from {store.migrations_dir}", flush=True)
    print(
        f"askdocs-api listening on :{PORT} storage={storage.cfg.base_url} "
        f"bucket={storage.cfg.bucket} events={events.cfg.base_url}",
        flush=True,
    )
    ThreadingHTTPServer(("0.0.0.0", PORT), Handler).serve_forever()


if __name__ == "__main__":
    main()
