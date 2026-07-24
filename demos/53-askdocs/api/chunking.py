"""Deterministic text chunking for AskDocs ingest (epic 53.02)."""

from __future__ import annotations

import re

DEFAULT_MAX_CHARS = 400

_WS_RE = re.compile(r"[ \t]+")
_BLANK_RE = re.compile(r"\n{3,}")


def normalize_text(text: str) -> str:
    """Normalize newlines/whitespace for stable chunk boundaries."""
    if text is None:
        return ""
    s = str(text).replace("\r\n", "\n").replace("\r", "\n")
    lines = [_WS_RE.sub(" ", line).rstrip() for line in s.split("\n")]
    s = "\n".join(lines)
    s = _BLANK_RE.sub("\n\n", s)
    return s.strip()


def _split_long(paragraph: str, max_chars: int) -> list[str]:
    """Split an oversized paragraph on word boundaries when possible."""
    para = paragraph.strip()
    if not para:
        return []
    if len(para) <= max_chars:
        return [para]
    out: list[str] = []
    rest = para
    while rest:
        if len(rest) <= max_chars:
            out.append(rest)
            break
        window = rest[:max_chars]
        # Prefer breaking on whitespace near the end of the window.
        cut = window.rfind(" ")
        if cut < max_chars // 2:
            cut = max_chars
        piece = rest[:cut].rstrip()
        if not piece:
            piece = rest[:max_chars]
            cut = len(piece)
        out.append(piece)
        rest = rest[cut:].lstrip()
    return out


def chunk_text(text: str, max_chars: int = DEFAULT_MAX_CHARS) -> list[str]:
    """Split normalized text into paragraphs, then cap each at max_chars.

    Empty / whitespace-only input yields an empty list. Output order is stable
    for a given input (deterministic).
    """
    if max_chars < 1:
        raise ValueError("max_chars must be >= 1")
    normalized = normalize_text(text)
    if not normalized:
        return []
    paragraphs = [p.strip() for p in re.split(r"\n\s*\n", normalized) if p.strip()]
    chunks: list[str] = []
    for para in paragraphs:
        chunks.extend(_split_long(para, max_chars))
    return chunks
