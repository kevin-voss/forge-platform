"""Async job submit / status / cancel API."""

from __future__ import annotations

import logging
from typing import Any, Literal

from fastapi import APIRouter, Request
from fastapi.responses import JSONResponse
from pydantic import BaseModel, Field

from app.jobs.store import JobStore
from app.jobs.worker import JobWorker

logger = logging.getLogger("forge-models")

router = APIRouter(tags=["jobs"])

JobTask = Literal["generate", "classify", "summarize", "embed"]


class CreateJobRequest(BaseModel):
    model: str = Field(..., min_length=1, description="Registry model id")
    task: JobTask = Field(..., description="Inference task to run asynchronously")
    input: Any = Field(..., description="Task-specific input payload")
    # Test/dev cooperative delay before work (milliseconds). Not advertised in OpenAPI.
    delay_ms: int | None = Field(default=None, ge=0, le=600_000)


def _store(request: Request) -> JobStore:
    return request.app.state.job_store


def _worker(request: Request) -> JobWorker:
    return request.app.state.job_worker


def _error(status: int, *, code: str, error: str) -> JSONResponse:
    return JSONResponse(status_code=status, content={"error": error, "code": code})


def _require_project(request: Request) -> str | JSONResponse:
    project = (request.headers.get("X-Forge-Project") or "").strip()
    if not project:
        return _error(
            400,
            code="project_required",
            error="X-Forge-Project header is required",
        )
    return project


@router.post("/v1/jobs")
async def create_job(body: CreateJobRequest, request: Request) -> JSONResponse:
    project = _require_project(request)
    if isinstance(project, JSONResponse):
        return project

    if not isinstance(body.model, str) or not body.model.strip():
        return _error(422, code="invalid_params", error="model must be a non-empty string")

    store = _store(request)
    job = store.create(
        project_id=project,
        model=body.model.strip(),
        task=body.task,
        input_payload=body.input,
        delay_ms=body.delay_ms or 0,
    )
    _worker(request).notify()
    logger.info(
        "job submitted",
        extra={"job_id": job.id, "project_id": project, "task": body.task, "model": body.model},
    )
    return JSONResponse(
        status_code=202,
        content={"job_id": job.id, "status": "queued"},
    )


@router.get("/v1/jobs/{job_id}")
async def get_job(job_id: str, request: Request) -> JSONResponse:
    project = _require_project(request)
    if isinstance(project, JSONResponse):
        return project

    job = _store(request).get(job_id, project_id=project)
    if job is None:
        return _error(404, code="job_not_found", error=f"job not found: {job_id}")
    return JSONResponse(status_code=200, content=job.as_public_dict())


@router.delete("/v1/jobs/{job_id}")
async def cancel_job(job_id: str, request: Request) -> JSONResponse:
    project = _require_project(request)
    if isinstance(project, JSONResponse):
        return project

    outcome = _store(request).request_cancel(job_id, project_id=project)
    if outcome is None:
        return _error(404, code="job_not_found", error=f"job not found: {job_id}")
    if outcome == "terminal":
        return _error(
            409,
            code="job_terminal",
            error=f"job {job_id} is already in a terminal state",
        )
    return JSONResponse(status_code=200, content={"status": "cancelled"})
