"""Helpers for capstone Models/Agents/Memory diagnosis wiring (19.04)."""

from __future__ import annotations

import json
import re
from pathlib import Path
from typing import Any, Mapping

_TOOL_RE = re.compile(r"^[a-z][a-z0-9_-]*(\.[a-z][a-z0-9_-]*)+$")
_PERMISSION_RE = re.compile(r"^[a-z][a-z0-9_-]*:[a-z][a-z0-9_-]*$")
_NAME_RE = re.compile(r"^[a-z][a-z0-9_-]*$")

REQUIRED_INVESTIGATOR_TOOLS = (
    "deployment.read",
    "logs.search",
    "metrics.query",
    "memory.search",
    "runtime.restart",
)

REQUIRED_INVESTIGATOR_PERMISSIONS = (
    "deployment:read",
    "logs:read",
    "metrics:read",
    "memory:read",
    "runtime:restart",
)


def load_json(path: str | Path) -> dict[str, Any]:
    data = json.loads(Path(path).read_text(encoding="utf-8"))
    if not isinstance(data, dict):
        raise ValueError(f"expected object in {path}")
    return data


def parse_simple_yaml_mapping(text: str) -> dict[str, Any]:
    """Minimal YAML subset parser for the capstone agent config (no PyYAML required)."""
    root: dict[str, Any] = {}
    current_list_key: str | None = None
    limits: dict[str, Any] | None = None

    for raw in text.splitlines():
        if not raw.strip() or raw.strip().startswith("#"):
            continue
        indent = len(raw) - len(raw.lstrip(" "))
        line = raw.strip()

        if indent == 0 and line.endswith(":") and not line.startswith("-"):
            key = line[:-1].strip()
            if key == "limits":
                limits = {}
                root["limits"] = limits
                current_list_key = None
            else:
                root[key] = []
                current_list_key = key
                limits = None
            continue

        if indent == 0 and ":" in line and not line.startswith("-"):
            key, _, value = line.partition(":")
            root[key.strip()] = value.strip()
            current_list_key = None
            limits = None
            continue

        if line.startswith("- ") and current_list_key is not None:
            root[current_list_key].append(line[2:].strip())
            continue

        if limits is not None and ":" in line:
            key, _, value = line.partition(":")
            raw_val = value.strip()
            limits[key.strip()] = int(raw_val) if raw_val.isdigit() else raw_val
            continue

        raise ValueError(f"unsupported yaml line: {raw!r}")

    return root


def validate_agent_config(payload: Mapping[str, Any]) -> dict[str, Any]:
    """Validate deployment-investigator shape (schema + required tools)."""
    name = str(payload.get("name") or "").strip()
    if not _NAME_RE.fullmatch(name):
        raise ValueError(f"invalid agent name: {name!r}")

    model = str(payload.get("model") or "").strip()
    if not model:
        raise ValueError("model is required")

    tools = payload.get("tools")
    if not isinstance(tools, list) or not tools:
        raise ValueError("tools must be a non-empty list")
    cleaned_tools: list[str] = []
    seen: set[str] = set()
    for item in tools:
        tool = str(item).strip()
        if not _TOOL_RE.fullmatch(tool):
            raise ValueError(f"malformed tool: {tool!r}")
        if tool in seen:
            raise ValueError(f"duplicate tool: {tool}")
        seen.add(tool)
        cleaned_tools.append(tool)
    for required in REQUIRED_INVESTIGATOR_TOOLS:
        if required not in seen:
            raise ValueError(f"missing required tool: {required}")

    permissions = payload.get("permissions")
    if not isinstance(permissions, list) or not permissions:
        raise ValueError("permissions must be a non-empty list")
    cleaned_perms: list[str] = []
    seen_perms: set[str] = set()
    for item in permissions:
        perm = str(item).strip()
        if not _PERMISSION_RE.fullmatch(perm):
            raise ValueError(f"malformed permission: {perm!r}")
        if perm in seen_perms:
            raise ValueError(f"duplicate permission: {perm}")
        seen_perms.add(perm)
        cleaned_perms.append(perm)
    for required in REQUIRED_INVESTIGATOR_PERMISSIONS:
        if required not in seen_perms:
            raise ValueError(f"missing required permission: {required}")

    limits = payload.get("limits")
    if not isinstance(limits, Mapping):
        raise ValueError("limits mapping required")
    max_steps = int(limits.get("max_steps") or 0)
    timeout_seconds = int(limits.get("timeout_seconds") or 0)
    if not (1 <= max_steps <= 100):
        raise ValueError(f"max_steps out of range: {max_steps}")
    if not (1 <= timeout_seconds <= 3600):
        raise ValueError(f"timeout_seconds out of range: {timeout_seconds}")

    return {
        "name": name,
        "model": model,
        "tools": cleaned_tools,
        "permissions": cleaned_perms,
        "limits": {"max_steps": max_steps, "timeout_seconds": timeout_seconds},
    }


def validate_historical_incidents(payload: Mapping[str, Any]) -> dict[str, Any]:
    """Validate Memory seed fixture shape and expected NN target."""
    collection = str(payload.get("collection") or "").strip()
    if not collection:
        raise ValueError("collection required")
    dim = int(payload.get("dim") or 0)
    if dim <= 0:
        raise ValueError("dim must be positive")
    model = str(payload.get("model") or "").strip()
    if not model:
        raise ValueError("model required")
    expected_id = str(payload.get("expected_id") or "").strip()
    if not expected_id:
        raise ValueError("expected_id required")
    new_failure_text = str(payload.get("new_failure_text") or "").strip()
    if not new_failure_text:
        raise ValueError("new_failure_text required")
    incidents = payload.get("incidents")
    if not isinstance(incidents, list) or not incidents:
        raise ValueError("incidents must be a non-empty list")
    ids = []
    for item in incidents:
        if not isinstance(item, Mapping):
            raise ValueError("incident entries must be objects")
        iid = str(item.get("id") or "").strip()
        text = str(item.get("text") or "").strip()
        if not iid or not text:
            raise ValueError("each incident needs id + text")
        ids.append(iid)
    if expected_id not in ids:
        raise ValueError(f"expected_id {expected_id!r} missing from incidents")
    return {
        "collection": collection,
        "dim": dim,
        "model": model,
        "expected_id": expected_id,
        "new_failure_text": new_failure_text,
        "count": len(incidents),
        "ids": ids,
    }


def collection_create_body(fixtures: Mapping[str, Any]) -> dict[str, Any]:
    fx = validate_historical_incidents(fixtures)
    return {
        "name": fx["collection"],
        "dim": fx["dim"],
        "distance": str(fixtures.get("distance") or "cosine"),
    }


def upsert_body(fixtures: Mapping[str, Any]) -> dict[str, Any]:
    fx = validate_historical_incidents(fixtures)
    items = []
    for item in fixtures["incidents"]:
        items.append(
            {
                "id": item["id"],
                "text": item["text"],
                "metadata": item.get("metadata") or {},
            }
        )
    return {"model": fx["model"], "items": items}


def nn_top_id(query_response: str | Mapping[str, Any]) -> str:
    data = json.loads(query_response) if isinstance(query_response, str) else query_response
    results = data.get("results") or []
    if not results:
        raise ValueError(f"empty NN results: {data!r}")
    top = results[0]
    iid = top.get("id") if isinstance(top, Mapping) else None
    if not isinstance(iid, str) or not iid:
        raise ValueError(f"missing top id: {results!r}")
    return iid


def diagnosis_cites_telemetry_and_memory(
    run_body: str | Mapping[str, Any],
    *,
    expected_memory_id: str,
    deployment_id: str,
    classification_label: str | None = None,
) -> str:
    """Assert investigator run cites Observe evidence + Memory record; no restart exec."""
    body = json.loads(run_body) if isinstance(run_body, str) else run_body
    status = body.get("status")
    if status not in {"awaiting_approval", "succeeded"}:
        raise AssertionError(f"unexpected run status: {status}")

    steps = body.get("steps") or []
    tool_steps = [s for s in steps if isinstance(s, Mapping) and s.get("type") == "tool"]
    by_tool = {s.get("tool"): s for s in tool_steps}

    for name in ("deployment.read", "logs.search", "metrics.query", "memory.search"):
        if name not in by_tool:
            raise AssertionError(f"missing tool step {name}; have={list(by_tool)}")
        obs = by_tool[name].get("observation") or {}
        if obs.get("ok") is not True:
            raise AssertionError(f"{name} not ok: {obs}")

    dep_obs = by_tool["deployment.read"]["observation"]
    if dep_obs.get("ready") is not False:
        raise AssertionError(f"expected ready=false: {dep_obs}")
    if str(dep_obs.get("deployment_id") or "") != deployment_id:
        raise AssertionError(f"deployment_id mismatch: {dep_obs}")

    logs_obs = by_tool["logs.search"]["observation"]
    entries = logs_obs.get("entries") or []
    joined = " ".join(
        str(e.get("message") or "") for e in entries if isinstance(e, Mapping)
    ).lower()
    if "readiness probe failed" not in joined:
        raise AssertionError(f"logs missing readiness evidence: {logs_obs}")

    metrics_obs = by_tool["metrics.query"]["observation"]
    samples = metrics_obs.get("samples") or []
    if not samples:
        raise AssertionError(f"metrics missing samples: {metrics_obs}")
    if float(samples[0].get("value", 1)) != 0.0:
        raise AssertionError(f"expected up=0: {samples}")

    mem_step = by_tool["memory.search"]
    mem_obs = mem_step.get("observation") or {}
    results = mem_obs.get("results") or []
    if not results or results[0].get("id") != expected_memory_id:
        raise AssertionError(f"memory.search top id mismatch: {results}")

    # runtime.restart must remain approval-gated (not executed as a tool step).
    if "runtime.restart" in by_tool:
        raise AssertionError("runtime.restart executed before approval")
    pending = body.get("pending_approval") or {}
    if status == "awaiting_approval":
        if pending.get("tool") != "runtime.restart":
            raise AssertionError(f"expected pending runtime.restart: {pending}")
        if str(pending.get("status") or "pending").lower() != "pending":
            raise AssertionError(f"approval not pending: {pending}")

    blob = json.dumps(body)
    if expected_memory_id not in blob:
        raise AssertionError("diagnosis did not cite memory incident id")

    if classification_label:
        mem_args = mem_step.get("args") or {}
        query = str(mem_args.get("query") or "")
        if classification_label not in query and classification_label not in blob:
            raise AssertionError(
                f"classification label {classification_label!r} not available to agent"
            )

    trace_id = str(dep_obs.get("trace_id") or "")
    if trace_id:
        log_traces = [
            str(e.get("trace_id") or "")
            for e in entries
            if isinstance(e, Mapping) and e.get("trace_id")
        ]
        if trace_id not in blob and trace_id not in log_traces:
            raise AssertionError(f"telemetry trace_id {trace_id} not referenced")

    diagnosis = (
        f"Diagnosis: deployment {deployment_id} ready=false; "
        f"readiness probe failed; metrics up=0; "
        f"similar memory incident {expected_memory_id}; "
        "recommend runtime.restart (approval-gated)."
    )
    return diagnosis


def build_investigator_plan(
    *,
    deployment_id: str,
    collection: str,
    query: str,
    expected_memory_id: str,
    classification: Mapping[str, Any] | None = None,
) -> list[dict[str, Any]]:
    """Dry-run plan: telemetry tools → memory.search → approval-gated restart."""
    cls = dict(classification or {})
    label = str(cls.get("label") or "infra.readiness_failure")
    memory_query = query if label in query else f"{query} classification={label}"
    return [
        {
            "kind": "tool_call",
            "tool": "deployment.read",
            "args": {"deployment_id": deployment_id},
        },
        {
            "kind": "tool_call",
            "tool": "logs.search",
            "args": {"deployment": deployment_id, "limit": 20},
        },
        {
            "kind": "tool_call",
            "tool": "metrics.query",
            "args": {"query": f'up{{deployment="{deployment_id}"}}'},
        },
        {
            "kind": "tool_call",
            "tool": "memory.search",
            "args": {
                "collection": collection,
                "query": memory_query,
                "top_k": 3,
            },
        },
        {
            "kind": "tool_call",
            "tool": "runtime.restart",
            "args": {"deployment_id": deployment_id},
        },
    ]
