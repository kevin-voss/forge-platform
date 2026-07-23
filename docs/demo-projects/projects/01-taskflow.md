# Demo 1 — TaskFlow

**Epic:** [`51-demo-taskflow`](../../implementation/epics/51-demo-taskflow.md) · **Focus:**
authentication, managed database, secrets, and the baseline deploy path (build → apply → route).

A small **team task manager**: users sign up, log in, create projects, add tasks, and mark them
done. Roles gate what you can see and do. This is the reference product for the platform's
"boring but essential" path — the one every SaaS needs — so it proves Identity + Postgres +
Secrets + Gateway + Build work together for a real login-protected app.

---

## 1. Why this product

The platform promise is "bring a container that listens on `$PORT`, get identity, a database,
secrets, and a public route for free." TaskFlow is the smallest believable product that consumes
all of that. If TaskFlow works, the platform's core developer experience works.

## 2. Services exercised

| Service | How TaskFlow uses it | Proven by |
|---|---|---|
| forge-build | `forge build --source .` turns the Go API + SPA into images. | Deploy step; image appears in registry. |
| forge-control | `forge apply -f` creates Application + Route + Database resources. | Resources reach `Ready`. |
| forge-identity | Signup/login issue a **PAT**; API validates Bearer via Identity introspect; roles `admin`/`member`. Deploy itself needs a **developer** PAT (a **viewer** PAT gets 403). | E2E login + role gating; deploy RBAC. |
| forge-secrets | DB connection string + JWT signing key injected as env from Secrets — never in the manifest. | Manifest has secret *refs* only; app boots. |
| managed PostgreSQL | `dependencies.database { type: postgres }` → managed `Database`; migrations + task data. | Tasks persist across restarts. |
| forge-gateway | `app.taskflow.localhost` → SPA, `api.taskflow.localhost` → API. | Host preflight + browser. |
| forge-runtime / forge-observe | Containers run; API emits OTEL traces/logs. | Readiness; a trace exists for `POST /tasks`. |

## 3. Architecture

```text
Browser ──▶ Gateway :4000
   app.taskflow.localhost  ──▶ taskflow-web  (static SPA + nginx, minimal)
   api.taskflow.localhost  ──▶ taskflow-api  (Go)
                                   │  Bearer PAT ──▶ forge-identity (introspect)
                                   │  env from   ──▶ forge-secrets (DB url, JWT key)
                                   └─ SQL ───────▶ managed Postgres (Database: taskflow-db)
```

Two services only. The API owns auth-session issuance on top of Identity PATs (it exchanges a
successful login for a signed app JWT stored client-side; the *deploy-time* RBAC uses Identity
PATs directly). Keeping app-auth thin keeps the Identity integration honest.

## 4. Manifests (illustrative — authored in `51.01`)

```yaml
# demos/51-taskflow/forge.yaml (build manifest + resource docs)
service: { name: taskflow-api, port: 8080 }
build:   { dockerfile: Dockerfile, context: . }
---
apiVersion: forge.dev/v1
kind: Application
metadata: { name: taskflow-api, project: taskflow, environment: local }
spec:
  image: registry.forge.internal/taskflow/taskflow-api:latest
  env:
    - name: DATABASE_URL
      valueFrom: { secret: taskflow/db-url }        # forge-secrets ref
    - name: JWT_SIGNING_KEY
      valueFrom: { secret: taskflow/jwt-key }
    - name: IDENTITY_URL
      value: http://forge-identity.svc.forge:8080
  dependencies:
    database: { type: postgres, plan: standard, name: taskflow-db }
  routes:
    - { host: api.taskflow.localhost, path: /, healthPath: /health/ready }
---
apiVersion: forge.dev/v1
kind: Application
metadata: { name: taskflow-web, project: taskflow, environment: local }
spec:
  image: registry.forge.internal/taskflow/taskflow-web:latest
  routes:
    - { host: app.taskflow.localhost, path: / }
```

## 5. Data model (Postgres)

```text
users(id, email unique, password_hash, role[admin|member], created_at)
projects(id, name, owner_id → users.id, created_at)
tasks(id, project_id → projects.id, title, done bool, created_at, updated_at)
```

Migrations run on API boot (idempotent). Seed (`seed.sh`): one `admin` user, one `member` user,
one shared project with two open tasks.

## 6. E2E scenario (browser — `tests/e2e/projects/01-taskflow/spec.ts`)

Headed so you literally watch login and task creation.

1. Open `app.taskflow.localhost` → landing/login page renders.
2. **Sign up** a fresh, unique member user → redirected to the board.
3. **Log out**, **log in** again with the same credentials → board shows the seeded project.
4. **Create a task** "Buy milk" → row appears; reload → still there (**Postgres persistence**).
5. **Toggle done** → row shows completed; check API `PATCH /tasks/:id` returned 200.
6. **Role gating:** log in as `member` → "Delete project" control is absent/disabled; log in as
   `admin` → control present. (App-role check backed by Identity.)
7. **Deploy RBAC (CLI, not browser):** `forge deployment create` with a **viewer** PAT → **403**;
   with a **developer** PAT → success. Asserted in the deploy/seed phase.

### Platform assertions (→ findings if they fail)
* Identity introspect returns the expected roles/claims for the issued PAT.
* Secrets-injected `DATABASE_URL`/`JWT_SIGNING_KEY` are present in the container env and absent
  from the rendered manifest/logs (no plaintext leak).
* Managed `Database` reached `Ready` and survives an API container restart with data intact.
* At least one OTEL trace exists for `POST /tasks` in Observe.

## 7. Likely findings hotspots

Auth edges (token expiry, introspect latency, role claim shape), secret rotation/refresh
semantics, managed-DB connection-string format, Gateway Host-with-port matching (§5 of
[e2e-harness.md](../e2e-harness.md)).

## 8. Acceptance criteria

* `make demo DEMO=51` and the `01-taskflow` E2E pass headed and headless.
* Signup→login→create→persist→complete works through the browser.
* Role gating and deploy-time RBAC (viewer 403 / developer 200) both hold.
* No plaintext secrets in manifests or logs; data survives restart.
* Zero blocker findings attributed to TaskFlow (any real platform bug recorded, not patched).

## 9. Steps → see epic

`51.01` scaffold+deploy · `51.02` managed Postgres+schema · `51.03` Identity auth+roles ·
`51.04` Secrets injection · `51.05` E2E browser spec · `51.06` demo + gate. Details:
[epic 51](../../implementation/epics/51-demo-taskflow.md).
