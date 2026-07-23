"""Unit tests for JSON Schema argument validation."""

from __future__ import annotations

from app.tools.base import schema_is_valid, validate_against_schema
from app.tools.fake import EchoPingTool


def test_valid_schema_document() -> None:
    assert schema_is_valid(EchoPingTool.input_schema)
    assert schema_is_valid(EchoPingTool.output_schema)


def test_invalid_schema_document() -> None:
    assert not schema_is_valid({"type": "not-a-real-type"})


def test_validate_args_accepts_valid() -> None:
    errors = validate_against_schema({"message": "hi"}, EchoPingTool.input_schema)
    assert errors == []


def test_validate_args_rejects_missing_required() -> None:
    errors = validate_against_schema({}, EchoPingTool.input_schema)
    assert errors
    assert any("message" in e or "required" in e.lower() for e in errors)


def test_validate_args_rejects_wrong_type() -> None:
    errors = validate_against_schema({"message": 123}, EchoPingTool.input_schema)
    assert errors


def test_validate_args_rejects_extra_properties() -> None:
    errors = validate_against_schema(
        {"message": "hi", "extra": True},
        EchoPingTool.input_schema,
    )
    assert errors
