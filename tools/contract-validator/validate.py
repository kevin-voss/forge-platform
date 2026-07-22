#!/usr/bin/env python3
"""Shared Forge runtime contract validator (epic 01 / step 01.02).

Talks only over HTTP + OS signals / Docker. No product SDKs.
Exit 0 on pass; non-zero on failure with actionable stderr.
"""

from __future__ import annotations

import argparse
import json
import os
import signal
import subprocess
import sys
import time
import urllib.error
import urllib.request
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any
from urllib.parse import urljoin

REQUIRED_LOG_FIELDS = ("timestamp", "level", "service", "message")
ALLOWED_LOG_LEVELS = frozenset({"debug", "info", "warn", "error"})
REQUIRED_IDENTITY_FIELDS = ("service", "language", "status")
DEFAULT_SHUTDOWN_TIMEOUT = 10.0


@dataclass
class CheckResult:
    name: str
    ok: bool
    detail: str = ""


@dataclass
class Report:
    results: list[CheckResult] = field(default_factory=list)

    def add(self, name: str, ok: bool, detail: str = "") -> None:
        self.results.append(CheckResult(name, ok, detail))

    @property
    def passed(self) -> bool:
        return all(r.ok for r in self.results)

    def print(self) -> None:
        for r in self.results:
            status = "PASS" if r.ok else "FAIL"
            line = f"[{status}] {r.name}"
            if r.detail:
                line = f"{line}: {r.detail}"
            stream = sys.stdout if r.ok else sys.stderr
            print(line, file=stream)
        summary = "PASS" if self.passed else "FAIL"
        print(f"\nContract validation: {summary}", file=sys.stdout if self.passed else sys.stderr)


def parse_timeout(value: str) -> float:
    raw = value.strip().lower()
    if raw.endswith("s"):
        raw = raw[:-1]
    try:
        seconds = float(raw)
    except ValueError as exc:
        raise argparse.ArgumentTypeError(
            f"invalid timeout {value!r}; use seconds like 10 or 10s"
        ) from exc
    if seconds <= 0:
        raise argparse.ArgumentTypeError("timeout must be positive")
    return seconds


def http_get(url: str, timeout: float = 5.0) -> tuple[int | None, Any | None, str]:
    """Return (status, parsed_json_or_None, error_message)."""
    req = urllib.request.Request(url, method="GET", headers={"Accept": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            body = resp.read()
            status = resp.getcode()
    except urllib.error.HTTPError as exc:
        body = exc.read()
        status = exc.code
    except urllib.error.URLError as exc:
        reason = getattr(exc, "reason", exc)
        return None, None, f"service not listening ({reason})"
    except TimeoutError:
        return None, None, "request timed out"
    except OSError as exc:
        return None, None, f"service not listening ({exc})"

    if not body:
        return status, None, ""
    try:
        return status, json.loads(body.decode("utf-8")), ""
    except (UnicodeDecodeError, json.JSONDecodeError):
        return status, None, "response is not valid JSON"


def check_listening(base_url: str, report: Report) -> bool:
    status, _, err = http_get(urljoin(base_url.rstrip("/") + "/", "health/live"))
    if status is None:
        report.add("listen", False, err or "service not listening")
        return False
    report.add("listen", True, f"base URL reachable ({base_url})")
    return True


def check_health(base_url: str, path: str, label: str, report: Report) -> None:
    url = urljoin(base_url.rstrip("/") + "/", path.lstrip("/"))
    status, _, err = http_get(url)
    if status is None:
        report.add(label, False, err)
        return
    if status != 200:
        report.add(label, False, f"{path} → HTTP {status} (expected 200)")
        return
    report.add(label, True, f"{path} → 200")


def check_identity(
    base_url: str,
    expect_service: str | None,
    expect_language: str | None,
    report: Report,
) -> None:
    url = base_url.rstrip("/") + "/"
    status, payload, err = http_get(url)
    if status is None:
        report.add("identity", False, err)
        return
    if status != 200:
        report.add("identity", False, f"GET / → HTTP {status} (expected 200)")
        return
    if not isinstance(payload, dict):
        report.add("identity", False, "GET / response is not a JSON object")
        return

    missing = [f for f in REQUIRED_IDENTITY_FIELDS if f not in payload]
    if missing:
        report.add(
            "identity",
            False,
            f"missing required field(s): {', '.join(missing)}",
        )
        return

    for key in REQUIRED_IDENTITY_FIELDS:
        if not isinstance(payload[key], str) or not payload[key]:
            report.add("identity", False, f"field {key!r} must be a non-empty string")
            return

    if expect_service is not None and payload["service"] != expect_service:
        report.add(
            "identity",
            False,
            f"service={payload['service']!r} (expected {expect_service!r})",
        )
        return
    if expect_language is not None and payload["language"] != expect_language:
        report.add(
            "identity",
            False,
            f"language={payload['language']!r} (expected {expect_language!r})",
        )
        return

    report.add(
        "identity",
        True,
        f"service={payload['service']!r} language={payload['language']!r} status={payload['status']!r}",
    )


def validate_log_line(obj: Any, line_no: int) -> str | None:
    if not isinstance(obj, dict):
        return f"line {line_no}: not a JSON object"
    missing = [f for f in REQUIRED_LOG_FIELDS if f not in obj]
    if missing:
        return f"line {line_no}: missing required field(s): {', '.join(missing)}"
    for key in REQUIRED_LOG_FIELDS:
        if not isinstance(obj[key], str) or not obj[key]:
            return f"line {line_no}: field {key!r} must be a non-empty string"
    if obj["level"] not in ALLOWED_LOG_LEVELS:
        return (
            f"line {line_no}: level={obj['level']!r} "
            f"(expected one of {sorted(ALLOWED_LOG_LEVELS)})"
        )
    return None


def check_log_file(log_path: Path, report: Report) -> None:
    if not log_path.is_file():
        report.add("logs", False, f"log file not found: {log_path}")
        return
    text = log_path.read_text(encoding="utf-8")
    lines = [ln for ln in text.splitlines() if ln.strip()]
    if not lines:
        report.add("logs", False, f"log file is empty: {log_path}")
        return

    errors: list[str] = []
    for idx, line in enumerate(lines, start=1):
        try:
            obj = json.loads(line)
        except json.JSONDecodeError as exc:
            errors.append(f"line {idx}: invalid JSON ({exc.msg})")
            continue
        err = validate_log_line(obj, idx)
        if err:
            errors.append(err)

    if errors:
        report.add("logs", False, "; ".join(errors[:5]))
        return
    report.add("logs", True, f"validated {len(lines)} JSON line(s) against runtime-log schema")


def process_alive(pid: int) -> bool:
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return False
    except PermissionError:
        return True
    return True


def check_shutdown_pid(pid: int, timeout: float, report: Report) -> None:
    if not process_alive(pid):
        report.add("shutdown", False, f"pid {pid} is not running")
        return
    try:
        os.kill(pid, signal.SIGTERM)
    except OSError as exc:
        report.add("shutdown", False, f"failed to send SIGTERM to {pid}: {exc}")
        return

    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if not process_alive(pid):
            report.add("shutdown", True, f"pid {pid} exited within {timeout:g}s after SIGTERM")
            return
        time.sleep(0.05)

    report.add(
        "shutdown",
        False,
        f"pid {pid} did not exit within {timeout:g}s after SIGTERM (would be force-killed)",
    )


def check_shutdown_docker(container: str, timeout: float, report: Report) -> None:
    try:
        proc = subprocess.run(
            ["docker", "stop", "-t", str(int(timeout)), container],
            capture_output=True,
            text=True,
            check=False,
        )
    except FileNotFoundError:
        report.add("shutdown", False, "docker not found on PATH")
        return

    if proc.returncode != 0:
        detail = (proc.stderr or proc.stdout or "docker stop failed").strip()
        report.add("shutdown", False, detail)
        return
    report.add(
        "shutdown",
        True,
        f"docker stop -t {int(timeout)} {container!r} succeeded",
    )


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="contract-validator",
        description=(
            "Validate a running HTTP workload against the Forge runtime contract. "
            "Callers must provide BASE_URL and expected identity; required env vars "
            "for workloads are documented in docs/contracts/runtime-contract.md "
            "(PORT, FORGE_SERVICE_NAME, FORGE_SERVICE_VERSION, FORGE_LOG_LEVEL)."
        ),
    )
    parser.add_argument(
        "--base-url",
        required=True,
        help="Base URL of the running service (e.g. http://127.0.0.1:4201)",
    )
    parser.add_argument("--expect-service", help="Expected identity.service value")
    parser.add_argument("--expect-language", help="Expected identity.language value")
    parser.add_argument(
        "--log-file",
        type=Path,
        help="Optional path to captured JSONL stdout for schema checks",
    )
    parser.add_argument(
        "--shutdown-pid",
        type=int,
        help="Optional PID to SIGTERM; assert exit within --shutdown-timeout",
    )
    parser.add_argument(
        "--shutdown-container",
        help="Optional Docker container name/id; docker stop with grace timeout",
    )
    parser.add_argument(
        "--shutdown-timeout",
        type=parse_timeout,
        default=DEFAULT_SHUTDOWN_TIMEOUT,
        help="Grace window for shutdown checks (default: 10s)",
    )
    parser.add_argument(
        "--skip-http",
        action="store_true",
        help="Skip HTTP checks (useful for log-only / shutdown-only tests)",
    )
    return parser


def main(argv: list[str] | None = None) -> int:
    parser = build_parser()
    args = parser.parse_args(argv)

    if args.shutdown_pid is not None and args.shutdown_container:
        print("Provide only one of --shutdown-pid or --shutdown-container", file=sys.stderr)
        return 2

    report = Report()

    if not args.skip_http:
        if check_listening(args.base_url, report):
            check_health(args.base_url, "/health/live", "health.live", report)
            check_health(args.base_url, "/health/ready", "health.ready", report)
            check_identity(
                args.base_url,
                args.expect_service,
                args.expect_language,
                report,
            )
    else:
        report.add("http", True, "skipped (--skip-http)")

    if args.log_file is not None:
        check_log_file(args.log_file, report)

    if args.shutdown_pid is not None:
        check_shutdown_pid(args.shutdown_pid, args.shutdown_timeout, report)
    elif args.shutdown_container:
        check_shutdown_docker(args.shutdown_container, args.shutdown_timeout, report)

    report.print()
    return 0 if report.passed else 1


if __name__ == "__main__":
    sys.exit(main())
