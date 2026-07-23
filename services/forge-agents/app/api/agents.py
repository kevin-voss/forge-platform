"""Agent registry read APIs: list and get by name."""

from __future__ import annotations

from fastapi import APIRouter, Request
from fastapi.responses import JSONResponse

from app.agents.loader import AgentRegistry

router = APIRouter(tags=["agents"])


def _registry(request: Request) -> AgentRegistry:
    return request.app.state.registry


@router.get("/v1/agents")
async def list_agents(request: Request) -> dict:
    registry = _registry(request)
    return {"agents": [agent.to_api_dict() for agent in registry.list()]}


@router.get("/v1/agents/{name}")
async def get_agent(name: str, request: Request) -> JSONResponse:
    agent = _registry(request).get(name)
    if agent is None:
        return JSONResponse(
            status_code=404,
            content={
                "error": f"agent not found: {name}",
                "code": "agent_not_found",
            },
        )
    return JSONResponse(status_code=200, content=agent.to_api_dict())
