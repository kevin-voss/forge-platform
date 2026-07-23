"""Normalized tool execution errors (backend / timeout)."""

from __future__ import annotations


class ToolError(Exception):
    """Raised by platform tools when a backend call fails in a known way."""

    def __init__(self, error_code: str, message: str) -> None:
        self.error_code = error_code
        self.message = message
        super().__init__(message)

    def to_normalized(self, tool: str) -> dict[str, str]:
        return {
            "tool": tool,
            "error_code": self.error_code,
            "message": self.message,
        }


ERROR_BACKEND_UNAVAILABLE = "backend_unavailable"
ERROR_TOOL_TIMEOUT = "tool_timeout"
ERROR_TOOL_ERROR = "tool_error"
