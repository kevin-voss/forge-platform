"""Approval APIs: list/get/approve/deny destructive tool requests."""

from __future__ import annotations

import logging
import time
from datetime import datetime, timezone

from fastapi import APIRouter, Request
from fastapi.responses import JSONResponse
from pydantic import BaseModel, Field

from app.approvals.store import APPROVED, DENIED, TERMINAL_APPROVAL_STATUSES
from app.run.engine import RunEngine

logger = logging.getLogger("forge-agents")
router = APIRouter(tags=["approvals"])

PROJECT_HEADER = "X-Forge-Project"
ACTOR_HEADER = "X-Forge-Actor"


class DenyBody(BaseModel):
    reason: str = Field(default="", description="Human reason for denial")


def _engine(request: Request) -> RunEngine:
    return request.app.state.run_engine


def _project_id(request: Request) -> str | JSONResponse:
    project = (request.headers.get(PROJECT_HEADER) or "").strip()
    if not project:
        return JSONResponse(
            status_code=400,
            content={
                "error": f"{PROJECT_HEADER} header is required",
                "code": "project_required",
            },
        )
    return project


def _actor(request: Request) -> str:
    actor = (request.headers.get(ACTOR_HEADER) or "").strip()
    return actor if actor else "anonymous"


def _decision_ms(created_at: str) -> float:
    try:
        created = datetime.strptime(created_at, "%Y-%m-%dT%H:%M:%SZ").replace(tzinfo=timezone.utc)
        return max(0.0, (time.time() - created.timestamp()) * 1000.0)
    except ValueError:
        return 0.0


@router.get("/v1/approvals")
async def list_approvals(request: Request) -> JSONResponse:
    project = _project_id(request)
    if isinstance(project, JSONResponse):
        return project

    store = _engine(request).approvals
    if store is None:
        return JSONResponse(
            status_code=503,
            content={"error": "approvals unavailable", "code": "approvals_unavailable"},
        )
    status = request.query_params.get("status")
    approvals = store.list(project_id=project, status=status)
    return JSONResponse(
        status_code=200,
        content={"approvals": [a.to_api_dict() for a in approvals]},
    )


@router.get("/v1/approvals/{approval_id}")
async def get_approval(approval_id: str, request: Request) -> JSONResponse:
    project = _project_id(request)
    if isinstance(project, JSONResponse):
        return project

    store = _engine(request).approvals
    if store is None:
        return JSONResponse(
            status_code=503,
            content={"error": "approvals unavailable", "code": "approvals_unavailable"},
        )
    approval = store.get(approval_id, project_id=project)
    if approval is None:
        return JSONResponse(
            status_code=404,
            content={
                "error": f"approval not found: {approval_id}",
                "code": "approval_not_found",
            },
        )
    return JSONResponse(status_code=200, content=approval.to_api_dict())


@router.post("/v1/approvals/{approval_id}/approve")
async def approve(approval_id: str, request: Request) -> JSONResponse:
    return await _decide(approval_id, request, status=APPROVED, reason=None)


@router.post("/v1/approvals/{approval_id}/deny")
async def deny(approval_id: str, body: DenyBody, request: Request) -> JSONResponse:
    reason = (body.reason or "").strip() or "denied"
    return await _decide(approval_id, request, status=DENIED, reason=reason)


async def _decide(
    approval_id: str,
    request: Request,
    *,
    status: str,
    reason: str | None,
) -> JSONResponse:
    project = _project_id(request)
    if isinstance(project, JSONResponse):
        return project

    engine = _engine(request)
    store = engine.approvals
    if store is None:
        return JSONResponse(
            status_code=503,
            content={"error": "approvals unavailable", "code": "approvals_unavailable"},
        )

    existing = store.get(approval_id, project_id=project)
    if existing is None:
        return JSONResponse(
            status_code=404,
            content={
                "error": f"approval not found: {approval_id}",
                "code": "approval_not_found",
            },
        )
    if existing.status in TERMINAL_APPROVAL_STATUSES:
        return JSONResponse(
            status_code=409,
            content={
                "error": f"approval already terminal: {existing.status}",
                "code": "approval_terminal",
                "status": existing.status,
            },
        )

    actor = _actor(request)
    outcome = store.decide(
        approval_id,
        status=status,
        decided_by=actor,
        reason=reason,
        project_id=project,
    )
    if outcome == "not_found":
        return JSONResponse(
            status_code=404,
            content={
                "error": f"approval not found: {approval_id}",
                "code": "approval_not_found",
            },
        )
    if outcome == "already_terminal":
        refreshed = store.get(approval_id, project_id=project)
        current = refreshed.status if refreshed else existing.status
        return JSONResponse(
            status_code=409,
            content={
                "error": f"approval already terminal: {current}",
                "code": "approval_terminal",
                "status": current,
            },
        )

    metrics = getattr(request.app.state, "approval_metrics", None)
    if metrics is not None:
        metrics.record_decision(status, decision_ms=_decision_ms(existing.created_at))

    logger.info(
        "approval approved" if status == APPROVED else "approval denied",
        extra={
            "approval_id": approval_id,
            "run_id": existing.run_id,
            "tool": existing.tool,
            "project_id": project,
            "actor": actor,
            "status": status,
        },
    )

    await engine.notify_approval_decision(approval_id)
    return JSONResponse(status_code=200, content={"status": status})
