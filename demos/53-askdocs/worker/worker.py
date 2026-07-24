#!/usr/bin/env python3
"""AskDocs ingest worker — consume document.uploaded, chunk, persist (epic 53.02)."""

from __future__ import annotations

import json
import os
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any

from chunking import chunk_text
from events import DeliveredMessage, EventsClient
from storage import StorageClient
from store import MessageStore, open_store_with_retry

PORT = int(os.environ.get("PORT", "8080"))
SERVICE_NAME = os.environ.get("FORGE_SERVICE_NAME", "askdocs-worker")

_processed = 0
_processed_lock = threading.Lock()
_ready = False


def _inc_processed() -> int:
    global _processed
    with _processed_lock:
        _processed += 1
        return _processed


def _processed_count() -> int:
    with _processed_lock:
        return _processed


def wait_ping(fn, label: str, budget_s: float = 60.0) -> None:
    deadline = time.time() + budget_s
    last: Exception | None = None
    while True:
        try:
            fn()
            return
        except Exception as exc:  # noqa: BLE001
            last = exc
            if time.time() >= deadline:
                raise RuntimeError(f"{label} unavailable: {last}") from last
            print(f"waiting for {label}: {exc}", flush=True)
            time.sleep(2)


def process_message(store: MessageStore, storage: StorageClient, msg: DeliveredMessage) -> None:
    data = msg.data or {}
    document_id = str(data.get("document_id") or "").strip()
    object_key = str(data.get("object_key") or "").strip()
    if not document_id:
        raise ValueError("missing document_id")
    doc = store.get_document(document_id)
    if doc is None:
        raise RuntimeError(f"document not found: {document_id}")
    if not object_key:
        object_key = doc.object_key
    raw = storage.get_object(object_key)
    text = raw.decode("utf-8", errors="replace")
    chunks = chunk_text(text, max_chars=400)
    store.replace_chunks(document_id, chunks)
    print(
        f"ingested document_id={document_id} chunks={len(chunks)} event_id={msg.event_id} "
        f"delivery={msg.delivery_count}",
        flush=True,
    )


def handle_message(
    events: EventsClient,
    store: MessageStore,
    storage: StorageClient,
    msg: DeliveredMessage,
) -> bool:
    try:
        process_message(store, storage, msg)
    except Exception as exc:  # noqa: BLE001
        print(f"process failed event_id={msg.event_id}: {exc}", flush=True)
        try:
            events.nak(msg.ack_token, delay_s=2)
        except Exception as nak_exc:  # noqa: BLE001
            print(f"nak failed: {nak_exc}", flush=True)
        return False
    try:
        events.mark_processed(msg.event_id)
        events.ack(msg.ack_token)
    except Exception as exc:  # noqa: BLE001
        print(f"ack/processed failed event_id={msg.event_id}: {exc}", flush=True)
        try:
            events.nak(msg.ack_token, delay_s=2)
        except Exception:
            pass
        return False
    _inc_processed()
    return True


def run_consume_loop(events: EventsClient, store: MessageStore, storage: StorageClient) -> None:
    poll = max(events.cfg.poll_ms, 100) / 1000.0
    while True:
        try:
            msgs = events.consume()
        except Exception as exc:  # noqa: BLE001
            print(f"consume: {exc}", flush=True)
            time.sleep(poll)
            continue
        for msg in msgs:
            handle_message(events, store, storage, msg)
        if not msgs:
            time.sleep(poll)


class Handler(BaseHTTPRequestHandler):
    server_version = "askdocs-worker/0.1"
    store: MessageStore
    storage: StorageClient
    events: EventsClient

    def log_message(self, fmt: str, *args: Any) -> None:  # noqa: A003
        return

    def do_GET(self) -> None:  # noqa: N802
        if self.path == "/health/live":
            self._write(200, {"status": "ok"})
            return
        if self.path == "/health/ready":
            errors: list[str] = []
            try:
                self.store.ping()
            except Exception as exc:  # noqa: BLE001
                errors.append(f"database: {exc}")
            try:
                self.storage.ping()
            except Exception as exc:  # noqa: BLE001
                errors.append(f"storage: {exc}")
            try:
                self.events.ping()
            except Exception as exc:  # noqa: BLE001
                errors.append(f"events: {exc}")
            if not _ready:
                errors.append("starting")
            if errors:
                self._write(503, {"status": "not_ready", "error": "; ".join(errors)})
            else:
                self._write(
                    200,
                    {
                        "status": "ok",
                        "processed_count": _processed_count(),
                        "consumer": self.events.cfg.consumer,
                        "subject": self.events.cfg.subject,
                    },
                )
            return
        self._write(404, {"error": "not_found"})

    def _write(self, status: int, payload: dict[str, Any]) -> None:
        body = (json.dumps(payload) + "\n").encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


def main() -> None:
    global _ready
    store = open_store_with_retry()
    storage = StorageClient()
    events = EventsClient()
    wait_ping(storage.ping, "forge-storage")
    wait_ping(events.ping, "forge-events")
    wait_ping(events.ensure_consumer, "events-consumer")
    _ready = True

    Handler.store = store
    Handler.storage = storage
    Handler.events = events

    threading.Thread(
        target=run_consume_loop,
        args=(events, store, storage),
        name="consume-loop",
        daemon=True,
    ).start()

    print(
        f"{SERVICE_NAME} listening on :{PORT} consumer={events.cfg.consumer} "
        f"subject={events.cfg.subject} bucket={storage.cfg.bucket}",
        flush=True,
    )
    ThreadingHTTPServer(("0.0.0.0", PORT), Handler).serve_forever()


if __name__ == "__main__":
    main()
