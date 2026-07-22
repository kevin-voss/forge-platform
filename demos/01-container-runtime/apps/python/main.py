#!/usr/bin/env python3
"""demo-python-api — Forge runtime contract reference workload (Python)."""

from __future__ import annotations

import signal
import sys
import threading

from config import load_config
from jsonlog import Logger
from server import make_server


def main() -> int:
    try:
        cfg = load_config()
    except ValueError as exc:
        print(f"fatal: {exc}", file=sys.stderr)
        return 1

    log = Logger(cfg.service_name, cfg.log_level)
    httpd = make_server(cfg, log)

    shutting_down = threading.Event()

    def on_signal(signum: int, _frame: object) -> None:
        name = signal.Signals(signum).name
        log.info("shutdown signal received", signal=name)
        shutting_down.set()
        # shutdown() must not run on the signal handler thread
        threading.Thread(target=httpd.shutdown, daemon=True).start()

    signal.signal(signal.SIGTERM, on_signal)
    signal.signal(signal.SIGINT, on_signal)

    log.info(
        "listening",
        port=cfg.port,
        version=cfg.service_version,
        env=cfg.env,
    )
    try:
        httpd.serve_forever(poll_interval=0.25)
    finally:
        httpd.server_close()
        log.info("shutdown complete")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
