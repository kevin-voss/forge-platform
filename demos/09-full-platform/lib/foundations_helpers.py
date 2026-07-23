"""Helpers for capstone foundations (Identity/Secrets/Observe/Storage/DB) — 19.03."""

from __future__ import annotations

import json
import re
from typing import Any, Iterable, Mapping


SENSITIVE_PATTERNS = (
    re.compile(r"postgresql://[^\s\"']+", re.I),
    re.compile(r"forge_pat_[A-Za-z0-9_\-]+"),
)


def mask_secrets_in_text(text: str, extra: Iterable[str] = ()) -> str:
    """Replace known secret substrings / URL shapes with ***."""
    out = text
    for secret in extra:
        if secret and secret in out:
            out = out.replace(secret, "***")
    for pat in SENSITIVE_PATTERNS:
        out = pat.sub("***", out)
    return out


def assert_no_plaintext(text: str, secrets: Iterable[str]) -> None:
    for secret in secrets:
        if secret and secret in text:
            raise AssertionError(f"plaintext secret leaked: {secret[:8]}...")


def tempo_service_names(payload: str | Mapping[str, Any] | list[Any]) -> set[str]:
    """Extract service.name / forge.service values from a Tempo trace JSON body."""
    data = json.loads(payload) if isinstance(payload, str) else payload
    names: set[str] = set()

    def attrs_to_map(attrs: Any) -> dict[str, str]:
        m: dict[str, str] = {}
        if not isinstance(attrs, list):
            return m
        for a in attrs:
            if not isinstance(a, Mapping):
                continue
            key = a.get("key")
            val = a.get("value") or {}
            if not isinstance(key, str) or not isinstance(val, Mapping):
                continue
            if "stringValue" in val:
                m[key] = str(val["stringValue"])
            elif "string_value" in val:
                m[key] = str(val["string_value"])
        return m

    batches = []
    if isinstance(data, Mapping):
        batches = data.get("batches") or data.get("resourceSpans") or []
        if not batches and isinstance(data.get("data"), list):
            for tr in data["data"]:
                if not isinstance(tr, Mapping):
                    continue
                for proc in (tr.get("processes") or {}).values():
                    if not isinstance(proc, Mapping):
                        continue
                    if proc.get("serviceName"):
                        names.add(str(proc["serviceName"]))
                    for tag in proc.get("tags") or []:
                        if isinstance(tag, Mapping) and tag.get("key") in (
                            "service.name",
                            "forge.service",
                        ):
                            names.add(str(tag.get("value")))
            return names

    for batch in batches:
        if not isinstance(batch, Mapping):
            continue
        res = batch.get("resource") or {}
        res_attrs = attrs_to_map(res.get("attributes") if isinstance(res, Mapping) else None)
        svc = res_attrs.get("service.name") or res_attrs.get("forge.service")
        for ss in batch.get("scopeSpans") or batch.get("instrumentationLibrarySpans") or []:
            if not isinstance(ss, Mapping):
                continue
            for span in ss.get("spans") or []:
                if not isinstance(span, Mapping):
                    continue
                sattrs = attrs_to_map(span.get("attributes"))
                name = sattrs.get("forge.service") or svc
                if name:
                    names.add(name)
    return names


def db_status_ok(payload: str | Mapping[str, Any]) -> bool:
    data = json.loads(payload) if isinstance(payload, str) else payload
    if not isinstance(data, Mapping):
        return False
    blob = json.dumps(data)
    if "postgresql://" in blob.lower():
        return False
    return bool(data.get("DATABASE_URL_present")) and data.get("backend") == "postgres"


def secret_status_ok(payload: str | Mapping[str, Any], forbidden: Iterable[str] = ()) -> bool:
    data = json.loads(payload) if isinstance(payload, str) else payload
    if not isinstance(data, Mapping):
        return False
    blob = json.dumps(data)
    for secret in forbidden:
        if secret and secret in blob:
            return False
    return bool(data.get("APP_SHARED_SECRET_present")) and bool(data.get("PRODUCT_MODE_present"))
