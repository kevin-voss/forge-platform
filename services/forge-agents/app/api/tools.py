"""Tool registry read APIs."""

from __future__ import annotations

from fastapi import APIRouter, Request

from app.tools.registry import ToolRegistry

router = APIRouter(tags=["tools"])


def _tools(request: Request) -> ToolRegistry:
    return request.app.state.tool_registry


@router.get("/v1/tools")
async def list_tools(request: Request) -> dict:
    """List registered tools with schemas, destructive flag, and permissions."""
    return {"tools": _tools(request).to_api_list()}
