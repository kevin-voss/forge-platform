# Epic 03: Forge CLI

## Status

In progress

## Goal

Deliver `forge`, a thin Go command-line client over the Forge Control API that gives developers the primary happy-path workflow: manage endpoint/profile configuration, create and list projects/apps/services, create and inspect deployments, and get machine-readable (`--output json`) or human (table) output with useful exit codes, timeouts, request-ID surfacing, shell completion, and a non-interactive mode. The CLI never touches the database directly — it speaks only HTTP to Control.

## Why this epic exists

`specs.md` §1/§14 frame the CLI as the primary developer interface. It turns the Control API (epic 02) into an ergonomic, scriptable tool and becomes the driver for later demos (deploy, status, logs). Building it as a thin, well-tested client establishes CLI conventions (config profiles, output modes, exit codes) reused by every later command group (secrets, database, model, agent).

## Primary code areas

* `tools/forge-cli/` — Go module, Cobra (or urfave/cli) command tree, Control API client
* `demos/03-cli-control/` — CLI-driven control-plane demo

## Suggested language

Go (per `specs.md` §4). Cobra for command structure + Viper (or equivalent) for config/profiles is a reasonable default; implementers may choose an alternative CLI framework within Go.

## Spec references

* `specs.md` → Step 03: Forge CLI
* `specs.md` → §1 developer happy path, §14 CLI command surface
* `specs.md` → §4 Language matrix (Go for CLI)
* Epic [`02-forge-control`](02-forge-control.md) → Control API, OpenAPI, error envelope, idempotency

## Dependencies

* Epic [`02-forge-control`](02-forge-control.md) — Control APIs `02.03`–`02.05` (projects/environments, applications/services, deployments), the shared error envelope and OpenAPI (`02.06`). Minimum `02.05` for deployment commands.
* Epic `00` — Make interface, tool template.

## Out of scope for this epic

* Direct database access (forbidden — CLI is API-only)
* `forge login` / real auth token acquisition (epic 09; a dev token/`FORGE_AUTH_MODE=dev` is used until then)
* `forge secret` / `forge config` platform secrets (epic 10; `config` here is CLI endpoint/profile config only)
* `forge database` / `forge model` / `forge agent` (later epics)
* `forge logs --follow` streaming from Runtime/Observe (epics 04/12)
* Source build (`forge deploy` from git) — epic 06

## Success demo

```bash
make demo DEMO=03
```

`demos/03-cli-control` runs Control, configures a CLI profile pointing at `http://127.0.0.1:4001`, then recreates the entire hierarchy from 02 using only `forge` commands — project, environment, app, service, deployment — and reads it back, asserting stable output in both table and `--output json` forms and correct exit codes on error.

## Planned steps

| Step | Title | Status | Notes |
|---|---|---|---|
| [03.01](../steps/03-forge-cli/03.01-cli-skeleton-and-config.md) | CLI skeleton, profiles, endpoint config, global flags | Complete | Cobra tree, secure config file, global flags, HTTP client factory |
| [03.02](../steps/03-forge-cli/03.02-project-app-service-commands.md) | `project` / `app` / `service` commands | Complete | Typed Control client, resource commands, basic table/JSON output |
| [03.03](../steps/03-forge-cli/03.03-deployment-commands.md) | `deployment create\|status` | Complete | Deployment create, status, list, and idempotent retries |
| [03.04](../steps/03-forge-cli/03.04-output-exit-codes-timeouts.md) | Table/JSON output, exit codes, timeouts, request IDs | Complete | Stable output, exit codes, timeouts, and request IDs |
| [03.05](../steps/03-forge-cli/03.05-completion-and-non-interactive.md) | Shell completion + non-interactive mode | Not started | Depends on 03.01+ |
| [03.06](../steps/03-forge-cli/03.06-demo-cli-control-and-gate.md) | Demo `03-cli-control` + gate | Not started | Depends on all prior |

## Assumptions

* CLI source lives under `tools/forge-cli/`; the built binary is named `forge`.
* Config file at `~/.config/forge/config.yaml` (XDG) with named profiles; `FORGE_ENDPOINT`/`FORGE_PROFILE` env override; `--endpoint`/`--profile` flags override env.
* Default Control endpoint for local dev is `http://127.0.0.1:4001`.
* Output modes: `table` (default) and `json` via `--output`.
* Until Identity (epic 09), the CLI sends no auth or a documented dev token; Control runs `FORGE_AUTH_MODE=dev`.
* Exit codes: `0` success, `1` generic error, `2` usage/validation error, `3+` reserved for specific API error classes (finalized in 03.04).

## Open questions

* **CLI framework:** Cobra vs urfave/cli — implementer's choice, or set a repo standard here?
* **Config precedence:** confirm flag > env > profile-file > built-in default ordering.
* **`forge config`:** in this epic it manages CLI-side endpoint/profile only; platform secret config is epic 10. Confirm no naming collision when `forge config set/show` for secrets arrives.
* **Request ID surfacing:** always print `requestId` on error, and on success only with `--verbose`? (Assumption: yes.)
* **JSON schema stability:** should `--output json` emit exactly the Control response bodies, or a CLI-wrapped envelope? (Assumption: pass through Control resource JSON for single-resource reads; wrap lists in a stable array.)

## Next step to implement

**[03.05](../steps/03-forge-cli/03.05-completion-and-non-interactive.md) — shell completion and non-interactive mode**.
