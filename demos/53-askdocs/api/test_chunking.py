#!/usr/bin/env python3
"""Deterministic chunking tests for AskDocs (epic 53.02)."""

from __future__ import annotations

import unittest
from pathlib import Path

from chunking import chunk_text, normalize_text

PLANTED_FACT = "The office is closed on the first Monday of each month."
FIXTURE = Path(__file__).resolve().parents[1] / "fixtures" / "company-handbook.txt"


class TestChunking(unittest.TestCase):
    def test_normalize_collapses_blank_lines(self) -> None:
        raw = "A\r\n\r\n\r\nB  C\tD\n"
        self.assertEqual(normalize_text(raw), "A\n\nB C D")

    def test_fixture_contains_planted_fact(self) -> None:
        self.assertTrue(FIXTURE.is_file(), f"missing fixture {FIXTURE}")
        text = FIXTURE.read_text(encoding="utf-8")
        self.assertIn(PLANTED_FACT, text)

    def test_fixture_chunking_is_deterministic_and_keeps_fact(self) -> None:
        text = FIXTURE.read_text(encoding="utf-8")
        a = chunk_text(text, max_chars=400)
        b = chunk_text(text, max_chars=400)
        self.assertEqual(a, b)
        self.assertGreaterEqual(len(a), 1)
        self.assertTrue(all(len(c) <= 400 for c in a), a)
        joined = "\n".join(a)
        self.assertIn(PLANTED_FACT, joined)
        # Fact should live in a single chunk (paragraph-based).
        self.assertTrue(any(PLANTED_FACT in c for c in a), a)

    def test_empty_input(self) -> None:
        self.assertEqual(chunk_text(""), [])
        self.assertEqual(chunk_text("   \n\n  "), [])

    def test_long_paragraph_splits(self) -> None:
        words = ["word"] * 120
        para = " ".join(words)
        chunks = chunk_text(para, max_chars=40)
        self.assertGreater(len(chunks), 1)
        self.assertTrue(all(len(c) <= 40 for c in chunks))
        self.assertEqual(" ".join(chunks).split(), words)


if __name__ == "__main__":
    unittest.main()
