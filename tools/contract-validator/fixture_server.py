#!/usr/bin/env python3
"""Tiny fixture HTTP server for contract-validator tests.

Modes (FORGE_FIXTURE_MODE or --mode):
  compliant         All contract endpoints succeed; SIGTERM exits cleanly.
  no_ready          /health/ready returns 503.
  missing_language  GET / omits language.
  ignore_sigterm    Ignores SIGTERM (for shutdown timeout tests).
"""

from __future__ import annotations

import argparse
import json
import os
import signal
import sys
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


MODES = ("compliant", "no_ready", "missing_language", "ignore_sigterm")


class FixtureHandler(BaseHTTPRequestHandler):
    server_version = "ForgeFixture/0.1"

    def log_message(self, fmt: str, *args: object) -> None:
        # Quiet by default; tests capture structured lines separately.
        if os.environ.get("FORGE_FIXTURE_VERBOSE") == "1":
            super().log_message(fmt, *args)

    def _json(self, status: int, payload: dict) -> None:
        body = json.dumps(payload).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self) -> None:  # noqa: N802
        mode = getattr(self.server, "fixture_mode", "compliant")
        path = self.path.split("?", 1)[0]

        if path == "/health/live":
            self._json(200, {"status": "ok"})
            return

        if path == "/health/ready":
            if mode == "no_ready":
                self._json(503, {"status": "not_ready"})
            else:
                self._json(200, {"status": "ok"})
            return

        if path == "/":
            identity = {
                "service": getattr(self.server, "service_name", "fixture"),
                "language": getattr(self.server, "language", "go"),
                "status": "running",
            }
            if mode == "missing_language":
                identity.pop("language")
            self._json(200, identity)
            return

        self._json(404, {"error": "not_found", "path": path})


def _handle_sigterm(signum: int, frame: object) -> None:  # noqa: ARG001
    mode = os.environ.get("FORGE_FIXTURE_MODE", "compliant")
    if mode == "ignore_sigterm":
        # Stay alive past the validator grace window.
        return
    sys.exit(0)


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="Fixture server for contract-validator")
    parser.add_argument("--host", default="127.0.0.1")
    parser.add_argument("--port", type=int, default=8099)
    parser.add_argument("--mode", choices=MODES, default=None)
    parser.add_argument("--service", default="fixture")
    parser.add_argument("--language", default="go")
    args = parser.parse_args(argv)

    mode = args.mode or os.environ.get("FORGE_FIXTURE_MODE", "compliant")
    if mode not in MODES:
        print(f"Unknown mode: {mode}", file=sys.stderr)
        return 2

    os.environ["FORGE_FIXTURE_MODE"] = mode
    signal.signal(signal.SIGTERM, _handle_sigterm)
    # Also exit cleanly on SIGINT for interactive use.
    if mode != "ignore_sigterm":
        signal.signal(signal.SIGINT, _handle_sigterm)

    httpd = ThreadingHTTPServer((args.host, args.port), FixtureHandler)
    httpd.fixture_mode = mode  # type: ignore[attr-defined]
    httpd.service_name = args.service  # type: ignore[attr-defined]
    httpd.language = args.language  # type: ignore[attr-defined]

    # One structured startup line for log-schema fixtures.
    print(
        json.dumps(
            {
                "timestamp": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
                "level": "info",
                "service": args.service,
                "message": f"fixture listening on {args.host}:{args.port} mode={mode}",
            }
        ),
        flush=True,
    )

    try:
        httpd.serve_forever()
    except KeyboardInterrupt:
        if mode != "ignore_sigterm":
            return 0
    finally:
        httpd.server_close()
    return 0


if __name__ == "__main__":
    sys.exit(main())
