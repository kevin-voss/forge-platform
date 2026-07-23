"""Agent run APIs: start, get, list, cancel."""

from __future__ import annotations

from typing import Any

from fastapi import APIRouter, Request
from fastapi.responses import JSONResponse
from pydantic import BaseModel, Field

from app.run.engine import RunEngine, StartRunRequest
from app.run.store import TERMINAL_STATUSES

router = APIRouter(tags=["runs"])

PROJECT_HEADER = "X-Forge-Project"


class StartRunBody(BaseModel):
    input: str = Field(default="", description="User/task input for the agent run")
    context: dict[str, Any] = Field(default_factory=dict)


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


@router.post("/v1/agents/{name}/runs")
async def start_run(name: str, body: StartRunBody, request: Request) -> JSONResponse:
    project = _project_id(request)
    if isinstance(project, JSONResponse):
        return project

    engine = _engine(request)
    try:
        run = await engine.start(
            StartRunRequest(
                agent_name=name,
                project_id=project,
                run_input=body.input,
                context=dict(body.context or {}),
            )
        )
    except KeyError:
        return JSONResponse(
            status_code=404,
            content={"error": f"agent not found: {name}", "code": "agent_not_found"},
        )
    except RuntimeError as exc:
        if str(exc) == "max_concurrent_runs":
            return JSONResponse(
                status_code=429,
                content={
                    "error": "too many concurrent agent runs",
                    "code": "max_concurrent_runs",
                },
            )
        raise

    return JSONResponse(
        status_code=202,
        content={"run_id": run.id, "status": "running"},
    )


@router.get("/v1/runs/{run_id}")
async def get_run(run_id: str, request: Request) -> JSONResponse:
    project = _project_id(request)
    if isinstance(project, JSONResponse):
        return project

    engine = _engine(request)
    run = engine.store.get_run(run_id, project_id=project)
    if run is None:
        return JSONResponse(
            status_code=404,
            content={"error": f"run not found: {run_id}", "code": "run_not_found"},
        )
    payload = run.to_api_dict(include_steps=True)
    if run.status == "awaiting_approval" and engine.approvals is not None:
        pending = engine.approvals.get_pending_for_run(run_id)
        if pending is not None:
            payload["pending_approval"] = pending.to_api_dict()
    return JSONResponse(status_code=200, content=payload)


@router.get("/v1/runs")
async def list_runs(request: Request) -> JSONResponse:
    project = _project_id(request)
    if isinstance(project, JSONResponse):
        return project

    runs = _engine(request).store.list_runs(project_id=project)
    return JSONResponse(
        status_code=200,
        content={"runs": [r.to_api_dict(include_steps=False) for r in runs]},
    )


@router.post("/v1/runs/{run_id}/cancel")
async def cancel_run(run_id: str, request: Request) -> JSONResponse:
    project = _project_id(request)
    if isinstance(project, JSONResponse):
        return project

    store = _engine(request).store
    run = store.get_run(run_id, project_id=project)
    if run is None:
        return JSONResponse(
            status_code=404,
            content={"error": f"run not found: {run_id}", "code": "run_not_found"},
        )
    if run.status in TERMINAL_STATUSES:
        return JSONResponse(
            status_code=409,
            content={
                "error": f"run already terminal: {run.status}",
                "code": "run_terminal",
                "status": run.status,
            },
        )

    outcome = store.request_cancel(run_id)
    if outcome == "already_terminal":
        refreshed = store.get_run(run_id, project_id=project)
        status = refreshed.status if refreshed else run.status
        return JSONResponse(
            status_code=409,
            content={
                "error": f"run already terminal: {status}",
                "code": "run_terminal",
                "status": status,
            },
        )

    # Persist cancelled promptly so clients see terminal status; the engine
    # loop also checks the cancel flag and will no-op finish if already terminal.
    store.finish_run(run_id, status="cancelled", error="cancelled")
    return JSONResponse(status_code=200, content={"status": "cancelled"})
