# Epic 06: Forge Build

## Status

In progress — 5/7 steps complete

## Goal

Deliver a Go build service that turns a Git repository into a deployable container image: clone/checkout a commit, read `forge.yaml`, run `docker build` with streamed logs and a timeout, tag the image with commit/build IDs, push it to the local OCI registry (`localhost:5000`), report build status, clean up the workspace, and record the resulting image reference on the Control service so it can be deployed and routed. When this epic is done, a Git commit can become a running, routed service end-to-end.

## Why this epic exists

Until now images must exist already (manually built demo images). Build closes the source-to-deployment loop that the platform promises: developers push code, the platform builds and deploys it. It sits atop the registry (00), Control (02), Runtime (04), and Gateway (05) to produce the `demos/06-source-to-deployment` capstone-style flow.

## Primary code areas

* `services/forge-build/` — Go service, Git + Docker build orchestration, workspace management, HTTP API (port `4103`)
* `contracts/openapi/forge-build.openapi.yaml` — build job API contract
* `demos/06-source-to-deployment/` — fixture repo → image → deploy → route

## Suggested language

Go (per `specs.md` §4). Standard library + `os/exec` (or the Docker client) for `docker build`/`push`, and `go-git` or the `git` CLI for clone/checkout. Docker Engine is the build backend.

## Spec references

* `specs.md` → Step 06: Forge Build
* `specs.md` → §4 Language matrix (Go for Build)
* Epic [`02-forge-control`](02-forge-control.md) → service/deployment records to attach image refs to
* Epic [`04-forge-runtime`](04-forge-runtime.md) → runs the built image; Epic [`05-forge-gateway`](05-forge-gateway.md) → routes to it

## Dependencies

* Epic `00` — local OCI registry at `localhost:5000`, Docker Engine, Compose, ports
* Epic [`02-forge-control`](02-forge-control.md) — record image ref on a service (`06.06`)
* Epic [`04-forge-runtime`](04-forge-runtime.md) — deploy the built image (demo)
* Epic [`05-forge-gateway`](05-forge-gateway.md) — route to the deployed service (demo final access)

## Out of scope for this epic

* Remote Git auth / private repos (fixture/local repos only in this epic)
* Multi-stage build caching optimization / buildkit tuning beyond defaults
* Non-Docker builders (buildpacks, nix) — Dockerfile only
* Automatic deployment orchestration (the demo wires build→deploy explicitly; the reconciler is epic 07)
* Signing / SBOM / vulnerability scanning (future hardening)

## Success demo

```bash
make demo DEMO=06
```

`demos/06-source-to-deployment` uses a local fixture Git repository, creates an application/service in Control, submits a build, streams logs, pushes the image to `localhost:5000`, records the image ref on the service, deploys it via Runtime, and accesses it through the Gateway.

```text
Git repo → Forge Build → docker build → localhost:5000 → Control (image ref) → Runtime → Gateway
```

## Planned steps

| Step | Title | Status | Notes |
|---|---|---|---|
| [06.01](../steps/06-forge-build/06.01-skeleton-docker-workspace.md) | Skeleton + Docker + workspace | Complete | Go service, port 4103, workspace volume |
| [06.02](../steps/06-forge-build/06.02-forge-yaml-schema-and-openapi.md) | `forge.yaml` schema + build OpenAPI | Complete | Schema, OpenAPI, manifest parser, DTOs |
| [06.03](../steps/06-forge-build/06.03-clone-checkout-docker-build-logs.md) | Clone/checkout + docker build + streamed logs | Complete | Clone, docker build, log stream, worker pool |
| [06.04](../steps/06-forge-build/06.04-tag-and-push-registry.md) | Tag + push local registry `:5000` | Complete | Tag/push + digest on build record |
| [06.05](../steps/06-forge-build/06.05-build-status-and-failure-paths.md) | Build status + failure paths | Complete | Durable status/phases, cancel, cleanup + restart recovery |
| [06.06](../steps/06-forge-build/06.06-control-integration-image-ref.md) | Control integration (image ref on service) | Not started | Depends on 06.04 + Control 02 |
| [06.07](../steps/06-forge-build/06.07-demo-source-to-deployment-and-gate.md) | Demo `06-source-to-deployment` + gate | Not started | Depends on all prior + Runtime 04 + Gateway 05 |

## Assumptions

* Build service source lives under `services/forge-build/`; demo under `demos/06-source-to-deployment/`.
* Build service listens on host port `4103` (internal range); in-container `PORT` default `8080`.
* Docker access is via the mounted Docker socket (dev-only privileged mount, like Runtime).
* A workspace volume holds transient clones; each build gets an isolated directory that is cleaned up after.
* Image tags encode commit SHA + build id, e.g. `localhost:5000/<service>:<shortsha>-<buildid>` plus a moving `:latest`.
* The fixture Git repo is local (a path or a file:// URL) to avoid network/auth in CI.
* Until Identity `09.06`, Build↔Control calls use `FORGE_AUTH_MODE=dev`.

## Open questions

* **Build execution model:** shell out to `docker build` via `os/exec`, or use the Docker client build API? (Assumption: Docker client/API for structured log streaming; `os/exec` acceptable fallback.)
* **Job model:** synchronous request that streams until done, or async job with a status endpoint? (Assumption: async — `POST` returns a build id; logs + status via separate endpoints — better for long builds.)
* **`forge.yaml` location:** repo root only, or configurable path? (Assumption: repo root `forge.yaml`; path override allowed in the build request.)
* **Registry auth:** local registry is anonymous in dev; private-registry push auth deferred.
* **Concurrency:** how many concurrent builds? (Assumption: bounded worker pool; single build sufficient for the demo.)

## Next step to implement

**[06.06](../steps/06-forge-build/06.06-control-integration-image-ref.md) — Control integration (image ref on service)**.
