"""Tool contract and JSON Schema argument validation."""

from __future__ import annotations

from abc import ABC, abstractmethod
from typing import TYPE_CHECKING, Any

from jsonschema import Draft202012Validator
from jsonschema.exceptions import SchemaError, ValidationError

if TYPE_CHECKING:
    from app.permissions import CallScope


class ToolResult:
    """Successful tool execution payload."""

    def __init__(self, output: dict[str, Any]) -> None:
        self.output = output


class Tool(ABC):
    """Base contract for all registered tools."""

    name: str
    input_schema: dict[str, Any]
    output_schema: dict[str, Any]
    destructive: bool
    required_permissions: list[str]

    @abstractmethod
    async def execute(
        self,
        args: dict[str, Any],
        *,
        scope: CallScope | None = None,
    ) -> ToolResult:
        """Run the tool with validated arguments (and optional call scope)."""


def validate_against_schema(instance: object, schema: dict[str, Any]) -> list[str]:
    """Validate `instance` against a JSON Schema. Returns error messages (empty if ok)."""
    try:
        validator = Draft202012Validator(schema)
    except SchemaError as exc:
        return [f"invalid schema: {exc.message}"]
    errors = sorted(validator.iter_errors(instance), key=lambda e: list(e.path))
    messages: list[str] = []
    for err in errors:
        path = ".".join(str(p) for p in err.path)
        messages.append(f"{path}: {err.message}" if path else err.message)
    return messages


def schema_is_valid(schema: dict[str, Any]) -> bool:
    """Return True when `schema` is a valid Draft 2020-12 JSON Schema."""
    try:
        Draft202012Validator.check_schema(schema)
    except SchemaError:
        return False
    except ValidationError:
        return False
    return True
