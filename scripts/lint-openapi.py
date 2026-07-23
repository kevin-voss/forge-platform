#!/usr/bin/env python3
"""Lint OpenAPI 3 documents under contracts/openapi/.

Validates YAML parse + OpenAPI 3 shape, and asserts forge-models covers every
implemented path for epic 14.06.
"""

from __future__ import annotations

import sys
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parents[1]
OPENAPI_DIR = ROOT / "contracts" / "openapi"

REQUIRED_MODELS_PATHS = {
    "/health/live",
    "/health/ready",
    "/",
    "/v1/models",
    "/v1/models/{model}",
    "/v1/models/{model}/health",
    "/v1/models/{model}/embed",
    "/v1/models/{model}/generate",
    "/v1/models/{model}/classify",
    "/v1/models/{model}/summarize",
    "/v1/jobs",
    "/v1/jobs/{job_id}",
    "/v1/usage",
    "/metrics",
}


def lint_doc(path: Path) -> list[str]:
    errors: list[str] = []
    try:
        doc = yaml.safe_load(path.read_text(encoding="utf-8"))
    except Exception as exc:  # noqa: BLE001
        return [f"{path}: YAML parse error: {exc}"]

    if not isinstance(doc, dict):
        return [f"{path}: document must be a mapping"]

    openapi = doc.get("openapi")
    if not isinstance(openapi, str) or not openapi.startswith("3."):
        errors.append(f"{path}: openapi must be 3.x (got {openapi!r})")

    info = doc.get("info")
    if not isinstance(info, dict) or not info.get("title") or not info.get("version"):
        errors.append(f"{path}: info.title and info.version are required")

    paths = doc.get("paths")
    if not isinstance(paths, dict) or not paths:
        errors.append(f"{path}: paths must be a non-empty mapping")
        return errors

    for path_key, item in paths.items():
        if not isinstance(item, dict) or not item:
            errors.append(f"{path}: path {path_key} has no operations")
            continue
        for method, op in item.items():
            if method.startswith("x-") or method == "parameters":
                continue
            if method not in {"get", "post", "put", "patch", "delete", "head", "options", "trace"}:
                continue
            if not isinstance(op, dict):
                errors.append(f"{path}: {method.upper()} {path_key} invalid")
                continue
            # Path-item operation may be an inline Operation Object or a $ref.
            if "$ref" in op:
                continue
            if "responses" not in op:
                errors.append(f"{path}: {method.upper()} {path_key} missing responses")

    if path.name == "forge-models.openapi.yaml":
        missing = sorted(REQUIRED_MODELS_PATHS - set(paths))
        if missing:
            errors.append(f"{path}: missing required paths: {', '.join(missing)}")
        schemas = (doc.get("components") or {}).get("schemas") or {}
        for name in (
            "UsageResponse",
            "UsageModelStats",
            "EmbedResponse",
            "GenerateResponse",
            "CreateJobResponse",
        ):
            if name not in schemas:
                errors.append(f"{path}: missing schema {name}")

    return errors


def main() -> int:
    if not OPENAPI_DIR.is_dir():
        print(f"missing {OPENAPI_DIR}", file=sys.stderr)
        return 1
    files = sorted(OPENAPI_DIR.glob("*.openapi.yaml"))
    if not files:
        print(f"no OpenAPI files in {OPENAPI_DIR}", file=sys.stderr)
        return 1
    all_errors: list[str] = []
    for path in files:
        all_errors.extend(lint_doc(path))
    if all_errors:
        for err in all_errors:
            print(err, file=sys.stderr)
        return 1
    print(f"OpenAPI lint OK ({len(files)} files)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
