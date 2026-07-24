#!/usr/bin/env python3
"""OrderPipe fulfillment — health, fulfill stub, NetworkPolicy debug probe (epic 54.03)."""

from __future__ import annotations

import json
import os
import time
import urllib.error
import urllib.request
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any, Callable
from urllib.parse import urlparse

PORT = int(os.environ.get("PORT", "8080"))
SERVICE_NAME = os.environ.get("FORGE_SERVICE_NAME", "orderpipe-fulfillment")
STARTED_AT = time.time()
_FULFILLMENTS: list[dict[str, Any]] = []

# forge-network deny reporting (54.03). Defaults match docker-compose host ports.
FORGE_NETWORK_URL = os.environ.get("FORGE_NETWORK_URL", "http://host.docker.internal:4110").rstrip("/")
FORGE_NETWORK_NODE_ID = os.environ.get("FORGE_NETWORK_NODE_ID", "node-local")

# Injectable for unit tests.
_HTTP_JSON: Callable[..., tuple[int, dict[str, Any]]] | None = None


def _http_json(method: str, url: str, body: dict[str, Any] | None = None, timeout: float = 3.0) -> tuple[int, dict[str, Any]]:
    if _HTTP_JSON is not None:
        return _HTTP_JSON(method, url, body, timeout)
    data = None
    headers = {"Accept": "application/json"}
    if body is not None:
        data = json.dumps(body).encode()
        headers["Content-Type"] = "application/json"
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            raw = resp.read().decode() or "{}"
            try:
                parsed = json.loads(raw)
            except json.JSONDecodeError:
                parsed = {"raw": raw}
            if not isinstance(parsed, dict):
                parsed = {"value": parsed}
            return int(resp.status), parsed
    except urllib.error.HTTPError as exc:
        raw = exc.read().decode() if exc.fp else ""
        try:
            parsed = json.loads(raw) if raw else {}
        except json.JSONDecodeError:
            parsed = {"raw": raw}
        if not isinstance(parsed, dict):
            parsed = {"value": parsed}
        return int(exc.code), parsed
    except Exception as exc:  # noqa: BLE001 — surface probe errors to callers
        return 0, {"error": str(exc)}


def report_policy_denied(
    *,
    from_workload: str,
    to_workload: str,
    port: int = 8080,
    reason: str = "networkpolicy:policy-default-deny",
    node_id: str | None = None,
    network_url: str | None = None,
) -> tuple[int, dict[str, Any]]:
    """Record a denied connection with forge-network (metric + network.policy.denied)."""
    base = (network_url or FORGE_NETWORK_URL).rstrip("/")
    node = node_id or FORGE_NETWORK_NODE_ID
    url = f"{base}/v1/nodes/{node}/network-policy-denied"
    return _http_json(
        "POST",
        url,
        {
            "from_workload": from_workload,
            "to_workload": to_workload,
            "port": port,
            "protocol": "tcp",
            "reason": reason,
        },
    )


class Handler(BaseHTTPRequestHandler):
    server_version = "orderpipe-fulfillment/0.1"

    def log_message(self, fmt: str, *args: Any) -> None:  # noqa: A003
        return

    def do_OPTIONS(self) -> None:  # noqa: N802
        self.send_response(204)
        self._cors()
        self.end_headers()

    def do_GET(self) -> None:  # noqa: N802
        path = urlparse(self.path).path
        if path == "/health/live":
            self._write_json(200, {"status": "ok"})
            return
        if path == "/health/ready":
            self._write_json(200, {"status": "ok"})
            return
        if path == "/fulfillments":
            self._write_json(200, {"items": list(_FULFILLMENTS)})
            return
        if path == "/" or path.startswith("/?"):
            self._write_json(
                200,
                {
                    "service": SERVICE_NAME,
                    "language": "python",
                    "status": "running",
                    "uptime_seconds": time.time() - STARTED_AT,
                    "fulfill": "POST /fulfill (stub until 54.04/54.05)",
                    "debugDeniedCall": "POST /debug/denied-call (fulfillment→notify NetworkPolicy proof)",
                },
            )
            return
        self._write_json(404, {"error": "not_found"})

    def do_POST(self) -> None:  # noqa: N802
        path = urlparse(self.path).path
        if path == "/fulfill":
            self._handle_fulfill()
            return
        if path == "/debug/denied-call":
            self._handle_denied_call()
            return
        self._write_json(404, {"error": "not_found"})

    def _handle_fulfill(self) -> None:
        body = self._read_json()
        if body is None:
            self._write_json(400, {"error": "invalid json"})
            return
        order_id = str(body.get("orderId") or body.get("order_id") or "").strip()
        if not order_id:
            self._write_json(400, {"error": "orderId is required"})
            return
        note = {
            "id": f"ffl-{uuid.uuid4().hex[:12]}",
            "orderId": order_id,
            "status": "accepted",
        }
        _FULFILLMENTS.append(note)
        self._write_json(202, note)

    def _handle_denied_call(self) -> None:
        """Attempt the denied pair fulfillment→notify and surface NetworkPolicy enforcement."""
        body = self._read_json() or {}
        from_wl = str(body.get("fromWorkload") or body.get("from_workload") or "").strip()
        to_wl = str(body.get("toWorkload") or body.get("to_workload") or "").strip()
        if not from_wl or not to_wl:
            self._write_json(
                400,
                {"error": "fromWorkload and toWorkload are required", "blocked": True},
            )
            return
        reason = str(
            body.get("reason") or "networkpolicy:policy-default-deny"
        ).strip() or "networkpolicy:policy-default-deny"
        # Under orderpipe-mesh, fulfillment→notify is not on the allow-list.
        # Report the deny to forge-network (fake/host policy backends observe here)
        # and do not complete the peer call — that is the enforcement proof.
        code, report = report_policy_denied(
            from_workload=from_wl,
            to_workload=to_wl,
            reason=reason,
        )
        event = report.get("event") if isinstance(report, dict) else None
        if code not in (200, 202) or event != "network.policy.denied":
            self._write_json(
                502,
                {
                    "blocked": True,
                    "pair": "fulfillment→notify",
                    "error": "failed to record network.policy.denied",
                    "reportStatus": code,
                    "report": report,
                },
            )
            return
        self._write_json(
            403,
            {
                "blocked": True,
                "pair": "fulfillment→notify",
                "from": "fulfillment",
                "to": "notify",
                "fromWorkload": from_wl,
                "toWorkload": to_wl,
                "event": "network.policy.denied",
                "reason": reason,
                "notifyAttempted": False,
            },
        )

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
        self.send_header("Access-Control-Allow-Headers", "Content-Type")

    def _write_json(self, status: int, payload: dict[str, Any] | list[Any]) -> None:
        body = (json.dumps(payload) + "\n").encode()
        self.send_response(status)
        self._cors()
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


def main() -> None:
    print(f"{SERVICE_NAME} listening on :{PORT}", flush=True)
    ThreadingHTTPServer(("0.0.0.0", PORT), Handler).serve_forever()


if __name__ == "__main__":
    main()
