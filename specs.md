# Forge Platform Specification

## 1. Vision

Forge Platform is a self-hosted developer platform for building, deploying, operating, observing, and connecting applications written in different languages.

Products remain independent from the platform.

Example products:

* Go web application
* Kotlin business API
* Rust data service
* Python machine-learning service
* Elixir realtime service
* React frontend
* background worker
* scheduled data pipeline

The platform provides reusable capabilities:

* application deployment
* container execution
* routing
* identity
* secrets
* service discovery
* events and jobs
* observability
* object storage
* AI model access
* AI agents
* workflows
* semantic memory
* CLI automation

The long-term developer experience should feel like:

```bash
forge project create invoice-ai

forge service create api \
  --image invoice-api:latest \
  --port 8080

forge database create postgres main-db

forge secret set OPENAI_API_KEY

forge model enable embeddings

forge agent deploy invoice-reviewer

forge deploy

forge status

forge logs --follow
```

---

# 2. Core design principles

## 2.1 Products are platform consumers

The platform must not contain product-specific business logic.

A product interacts with the platform through:

* HTTP APIs
* event messages
* environment variables
* mounted files
* service credentials
* CLI commands
* optional SDKs

## 2.2 Containers are the runtime boundary

Every deployable service must:

1. provide an OCI-compatible container image
2. listen on the provided `PORT`
3. expose a readiness endpoint
4. expose a liveness endpoint
5. log to stdout and stderr
6. receive configuration through environment variables
7. handle graceful shutdown
8. publish telemetry through OpenTelemetry where supported

Recommended application contract:

```text
GET /health/live
GET /health/ready
GET /metrics
```

## 2.3 Share contracts, not implementations

Services written in different languages should share:

* OpenAPI definitions
* Protocol Buffer definitions
* JSON Schema event contracts
* environment-variable conventions
* health-check conventions
* telemetry conventions

They should not share language-specific business libraries.

## 2.4 Build incrementally

Every implementation step must produce:

* one usable capability
* automated tests
* a demo scenario
* acceptance criteria
* updated documentation
* a Git commit

No step should require unfinished future services to work.

---

# 3. Monorepo structure

```text
forge-platform/
├── README.md
├── Makefile
├── compose.yaml
├── .env.example
├── .gitignore
├── docs/
│   ├── architecture/
│   ├── concepts/
│   ├── contracts/
│   ├── development/
│   ├── operations/
│   ├── testing/
│   ├── decisions/
│   └── implementation/
│       ├── README.md
│       ├── IMPLEMENT_STEP.md
│       ├── progress.md
│       └── steps/
├── contracts/
│   ├── openapi/
│   ├── protobuf/
│   ├── events/
│   └── examples/
├── services/
│   ├── forge-control/
│   ├── forge-runtime/
│   ├── forge-gateway/
│   ├── forge-build/
│   ├── forge-identity/
│   ├── forge-secrets/
│   ├── forge-events/
│   ├── forge-observe/
│   ├── forge-storage/
│   ├── forge-models/
│   ├── forge-agents/
│   ├── forge-workflows/
│   └── forge-memory/
├── tools/
│   ├── forge-cli/
│   ├── test-runner/
│   └── contract-validator/
├── packages/
│   ├── go-sdk/
│   ├── python-sdk/
│   ├── kotlin-sdk/
│   └── rust-sdk/
├── infrastructure/
│   ├── docker/
│   ├── postgres/
│   ├── nats/
│   ├── registry/
│   ├── otel/
│   └── grafana/
├── demos/
│   ├── 01-container-runtime/
│   ├── 02-routed-service/
│   ├── 03-multi-service/
│   ├── 04-event-driven/
│   ├── 05-observed-system/
│   ├── 06-ai-model/
│   ├── 07-ai-agent/
│   ├── 08-agent-workflow/
│   └── 09-full-platform/
└── scripts/
    ├── wait-for-service.sh
    ├── smoke-test.sh
    ├── reset-local.sh
    └── generate-contracts.sh
```

---

# 4. Language allocation

| Component       | Language               | Main learning goal                           |
| --------------- | ---------------------- | -------------------------------------------- |
| Forge CLI       | Go                     | CLI design, API clients, streaming           |
| Forge Control   | Kotlin + Ktor          | platform domain, APIs, coroutines            |
| Forge Runtime   | Rust                   | containers, processes, system programming    |
| Forge Gateway   | Go                     | networking, reverse proxying, routing        |
| Forge Build     | Go                     | pipelines, Git, Docker builds                |
| Forge Identity  | Kotlin + Ktor          | sessions, tokens, authorization              |
| Forge Secrets   | Rust                   | encryption, safe storage, access control     |
| Forge Events    | Go                     | asynchronous systems and delivery guarantees |
| Forge Observe   | Go plus standard tools | telemetry ingestion and correlation          |
| Forge Storage   | Rust                   | streaming I/O and content storage            |
| Forge Models    | Python                 | model serving and inference                  |
| Forge Agents    | Python                 | tool use, planning, execution                |
| Forge Workflows | Elixir                 | processes, supervision, state machines       |
| Forge Memory    | Rust                   | vector search, indexing, persistence         |

This is a recommendation, not an immutable restriction.

---

# 5. Repository-wide standards

## 5.1 Makefile interface

The root repository must provide:

```bash
make setup
make dev
make stop
make restart
make status
make logs
make build
make test
make test-unit
make test-integration
make test-e2e
make lint
make format
make clean
make reset
make demo DEMO=01
```

Every service must provide:

```bash
make dev
make build
make run
make test
make test-unit
make test-integration
make lint
make format
make docker-build
make docker-run
make clean
```

The root Makefile delegates to individual services.

Example:

```bash
make service-test SERVICE=forge-runtime
make service-run SERVICE=forge-gateway
```

## 5.2 Docker strategy

Use Docker Compose for local platform development.

Docker Compose should start:

* PostgreSQL
* NATS initially
* local OCI registry
* OpenTelemetry Collector
* Prometheus
* Grafana
* Tempo
* Loki
* platform services
* selected demo applications

Application development may use one of two modes:

### Container mode

Everything runs in Docker.

```bash
make dev
```

### Hybrid mode

Infrastructure runs in Docker, while one service runs directly for faster development.

```bash
make infra-up
cd services/forge-gateway
make dev
```

## 5.3 Ports

Use documented port ranges.

```text
3000-3099  dashboards and development UIs
4000-4099  platform public APIs
4100-4199  internal platform APIs
4200-4299  demo applications
4300-4399  AI and model services
5000-5099  infrastructure
```

## 5.4 Logging

All services must produce structured JSON logs.

Required fields:

```json
{
  "timestamp": "2026-07-22T14:30:00Z",
  "level": "info",
  "service": "forge-gateway",
  "version": "0.1.0",
  "message": "request completed",
  "request_id": "req-123",
  "trace_id": "trace-123"
}
```

## 5.5 Configuration

Required common environment variables:

```text
FORGE_ENV
FORGE_SERVICE_NAME
FORGE_SERVICE_VERSION
FORGE_HTTP_PORT
FORGE_LOG_LEVEL
FORGE_DATABASE_URL
FORGE_EVENTS_URL
FORGE_OTEL_ENDPOINT
```

Each service must include:

```text
.env.example
config.example.yaml
```

Secrets must never be committed.

---

# 6. Testing strategy

## 6.1 Unit tests

Test isolated business logic.

Examples:

* scheduler decisions
* route matching
* permission evaluation
* retry calculations
* workflow transitions
* vector similarity
* agent tool validation

## 6.2 Integration tests

Test the service against real dependencies.

Use containers for dependencies such as:

* PostgreSQL
* NATS
* OCI registry
* Docker daemon
* OpenTelemetry Collector

Mocks should not replace critical infrastructure behavior.

## 6.3 Contract tests

Validate:

* OpenAPI requests and responses
* event payloads against JSON Schema
* Protocol Buffer compatibility
* required headers
* error response formats

## 6.4 End-to-end tests

Start the platform through Docker Compose and execute realistic flows through the CLI or public API.

## 6.5 Demo tests

Every completed step must have a demo folder containing:

```text
README.md
Makefile
compose.override.yaml
fixtures/
scripts/
expected/
```

Every demo must support:

```bash
make run
make test
make clean
```

The demo is both:

* an executable acceptance test
* a documented usage example

---

# 7. Error format

All HTTP services should return a shared error structure.

```json
{
  "error": {
    "code": "SERVICE_NOT_FOUND",
    "message": "The requested service does not exist.",
    "request_id": "req-123",
    "details": {}
  }
}
```

Errors must have:

* stable machine-readable codes
* human-readable messages
* request IDs
* optional structured details
* correct HTTP status codes

---

# 8. Implementation roadmap

---

## Step 00: Repository foundation

### Goal

Create the monorepo structure, common conventions, local infrastructure, and documentation framework.

### Build

* root Makefile
* Docker Compose
* PostgreSQL
* NATS
* local OCI registry
* OpenTelemetry Collector
* Grafana stack
* shared scripts
* contracts folders
* service templates
* demo template
* CI skeleton
* documentation skeleton

### Demo

`demos/00-foundation`

The demo verifies that:

* Docker Compose starts
* PostgreSQL accepts connections
* NATS responds
* registry responds
* OpenTelemetry Collector is healthy
* Grafana is reachable

### Run

```bash
make setup
make dev
make status
make demo DEMO=00
```

### Tests

```bash
make test-infrastructure
```

### Acceptance criteria

* one command starts all foundational infrastructure
* one command stops it
* health checks are configured
* environment variables are documented
* no secrets are committed
* all directories follow the defined structure
* CI can execute root lint and test commands
* demo 00 passes

### Commit

```text
chore(step-00): initialize forge platform foundation
```

---

## Step 01: Runtime contract and demo applications

### Goal

Define the contract that any product must follow to run on Forge Platform.

### Build

Create minimal demo applications in:

* Go
* Kotlin
* Rust
* Python
* Elixir

Each must:

* run in a container
* listen on `PORT`
* expose liveness and readiness endpoints
* log structured output
* handle termination
* return language and version information

### Example response

```json
{
  "service": "demo-rust-api",
  "language": "rust",
  "status": "running"
}
```

### Demo

`demos/01-container-runtime`

Start all five applications directly through Docker Compose.

### Flow

```text
Docker Compose
    ├── Go demo
    ├── Kotlin demo
    ├── Rust demo
    ├── Python demo
    └── Elixir demo
```

### Tests

* every image builds
* every container starts
* health endpoints return success
* shutdown exits without forced termination
* required environment variables are respected
* logs contain required fields

### Acceptance criteria

* the platform runtime contract is documented
* all five languages implement the same contract
* a shared test runner validates all applications
* no application contains platform-specific SDK dependencies
* demo 01 passes

### Commit

```text
feat(step-01): define multi-language runtime contract
```

---

## Step 02: Forge Control

### Language

Kotlin with Ktor.

### Goal

Build the central platform API and source of truth.

### Responsibilities

* projects
* environments
* applications
* services
* desired deployments
* basic audit records

### Initial entities

```text
Project
Environment
Application
Service
Deployment
```

### Example API

```text
POST /v1/projects
POST /v1/projects/{projectId}/applications
POST /v1/applications/{applicationId}/services
POST /v1/services/{serviceId}/deployments
GET  /v1/deployments/{deploymentId}
```

### Integration

* PostgreSQL
* OpenTelemetry
* structured logging

No runtime execution yet.

### Demo

`demos/02-control-plane`

The test creates:

1. a project
2. a development environment
3. an application
4. a service
5. a desired deployment

It then reads the complete hierarchy back.

### Run

```bash
cd services/forge-control
make dev
```

Or:

```bash
make service-run SERVICE=forge-control
```

### Tests

* domain unit tests
* repository integration tests
* API integration tests
* database migration tests
* OpenAPI contract validation
* idempotency tests for selected creation operations

### Acceptance criteria

* state survives restart
* IDs are stable UUIDs
* database migrations run automatically or through a documented command
* invalid relationships are rejected
* API follows shared error format
* OpenAPI contract exists
* demo 02 passes

### Commit

```text
feat(step-02): add forge control plane API
```

---

## Step 03: Forge CLI

### Language

Go.

### Goal

Provide the primary developer interface.

### Commands

```bash
forge project create
forge project list
forge app create
forge service create
forge deployment create
forge deployment status
forge config set
forge config show
```

### Features

* profiles
* configurable API endpoint
* table output
* JSON output
* useful exit codes
* request timeout
* request IDs
* shell completion
* non-interactive mode

### Integration

Uses Forge Control through its public API.

### Demo

`demos/03-cli-control`

The demo creates the same control-plane hierarchy using only CLI commands.

### Tests

* command parsing
* configuration loading
* output formatting
* fake-server unit tests
* real control-plane integration tests
* failure exit-code tests

### Acceptance criteria

* all Step 02 operations are available through the CLI
* `--output json` produces stable machine-readable output
* errors include useful messages
* no direct database access exists
* demo 03 passes

### Commit

```text
feat(step-03): add forge platform CLI
```

---

## Step 04: Forge Runtime

### Language

Rust.

### Goal

Run arbitrary application containers on one local node.

### Responsibilities

* register runtime node
* receive desired workload instructions
* pull container image
* create container
* inject environment variables
* map ports
* start and stop container
* perform health checks
* capture status
* stream logs
* gracefully remove workload

Use Docker Engine as the execution backend.

Do not implement containers from scratch.

### Initial workload definition

```json
{
  "deployment_id": "deployment-123",
  "image": "localhost:5000/demo-go:latest",
  "port": 8080,
  "environment": {
    "FORGE_ENV": "development"
  }
}
```

### Integration

* Forge Control
* Docker Engine
* OpenTelemetry

### Demo

`demos/04-runtime`

Flow:

```text
Forge CLI
    ↓
Forge Control
    ↓
Forge Runtime
    ↓
Demo application container
```

### Test case

Deploy the Go demo application from Step 01.

Verify:

* container starts
* application becomes ready
* deployment status becomes active
* logs can be read
* deleting the deployment removes the container

### Tests

* Docker API adapter tests
* process and lifecycle state tests
* health-check tests
* integration test with real Docker
* crash-recovery test
* graceful-shutdown test

### Acceptance criteria

* one arbitrary OCI image can be deployed
* actual state is reported to Forge Control
* failed startup produces a failed deployment state
* duplicate commands do not produce duplicate containers
* container names and labels are deterministic
* demo 04 passes

### Commit

```text
feat(step-04): add single-node container runtime
```

---

## Step 05: Forge Gateway

### Language

Go.

### Goal

Expose deployed services through stable HTTP routes.

### Features

* host-based routing
* path-based routing
* reverse proxy
* round-robin balancing
* health-aware upstreams
* forwarded headers
* request IDs
* timeouts
* WebSocket support
* SSE support
* dynamic route updates

### Example route

```text
go-demo.localhost
    ↓
Forge Gateway
    ↓
127.0.0.1:random-runtime-port
```

### Integration

* reads active service endpoints from Forge Control
* forwards traffic to Forge Runtime workloads
* emits telemetry to Forge Observe

### Demo

`demos/05-routed-service`

Deploy three language demos:

* Go
* Rust
* Python

Expose them through:

```text
go.demo.localhost
rust.demo.localhost
python.demo.localhost
```

### Tests

* route-matching unit tests
* proxy integration tests
* unavailable-upstream tests
* timeout tests
* WebSocket test
* SSE streaming test
* dynamic route update test

### Acceptance criteria

* services are accessible without knowing runtime ports
* unhealthy workloads receive no traffic
* route changes do not require gateway restart
* request IDs propagate to upstream applications
* demo 05 passes

### Commit

```text
feat(step-05): add dynamic application gateway
```

---

## Step 06: Forge Build

### Language

Go.

### Goal

Build container images from Git repositories.

### Features

* clone repository
* checkout commit
* read `forge.yaml`
* build Dockerfile
* stream logs
* enforce timeout
* tag image
* push to local registry
* report build status
* clean working directory

### Example `forge.yaml`

```yaml
service:
  name: api
  port: 8080

build:
  dockerfile: Dockerfile
  context: .
```

### Flow

```text
Git repository
    ↓
Forge Build
    ↓
Docker build
    ↓
Local OCI registry
    ↓
Forge Runtime
```

### Integration

* Forge Control
* local registry
* Forge Runtime
* OpenTelemetry

### Demo

`demos/06-source-to-deployment`

The demo:

1. uses a local fixture Git repository
2. creates an application
3. submits a build
4. pushes the image
5. deploys the image
6. accesses it through Forge Gateway

### Tests

* Git checkout tests
* invalid repository tests
* failed Dockerfile tests
* timeout tests
* registry-push tests
* log-streaming tests
* complete source-to-route end-to-end test

### Acceptance criteria

* a Git commit can become a running routed service
* build logs are accessible
* failed builds do not create deployments
* image tags include commit and build IDs
* temporary files are removed
* demo 06 passes

### Commit

```text
feat(step-06): add source-to-container build service
```

---

## Step 07: Reconciliation and deployment controller

### Language

Kotlin within Forge Control initially.

It may later be extracted.

### Goal

Continuously reconcile desired and actual deployment state.

### Features

* desired replica count
* actual replica tracking
* rolling deployment
* readiness verification
* deployment timeout
* automatic rollback
* restart failed instances
* deployment history

### Flow

```text
Desired state: image v2, replicas 2
Actual state: image v1, replicas 2

Controller:
1. starts one v2 instance
2. waits for readiness
3. routes traffic to v2
4. stops one v1 instance
5. repeats
```

### Demo

`demos/07-rolling-deployment`

Deploy version 1 and then version 2.

Version 2 includes a visible response change.

A second test deploys an intentionally unhealthy version 3 and verifies rollback to version 2.

### Tests

* reconciliation unit tests
* state-machine tests
* deployment timeout tests
* rollback tests
* idempotency tests
* runtime-loss recovery test

### Acceptance criteria

* desired and actual state converge
* deployment survives controller restart
* healthy rolling deployment avoids total downtime
* unhealthy release rolls back
* deployment history records transitions
* demo 07 passes

### Commit

```text
feat(step-07): add deployment reconciliation and rollback
```

---

## Step 08: Multi-node scheduler

### Language

Go or Kotlin.

Recommended: Go as a separate service.

### Goal

Place workloads across multiple runtime nodes.

### Features

* node registration
* node heartbeat
* available-resource reporting
* workload requirements
* first-fit scheduling
* least-allocated scheduling
* anti-affinity
* rescheduling after node loss
* pending workload handling

### Integration

* Forge Control
* multiple Forge Runtime agents
* deployment reconciler

### Demo

`demos/08-multi-node`

Run multiple runtime agents with simulated resource capacities.

Deploy four replicas and verify distribution.

Then stop one runtime agent and verify rescheduling.

### Tests

* scheduling algorithm unit tests
* capacity tests
* anti-affinity tests
* offline-node tests
* rescheduling integration tests

### Acceptance criteria

* workloads are distributed deterministically
* overloaded nodes are rejected
* offline nodes stop receiving workloads
* lost workloads are recreated elsewhere
* demo 08 passes

### Commit

```text
feat(step-08): add multi-node workload scheduler
```

---

## Step 09: Forge Identity

### Language

Kotlin with Ktor.

### Goal

Add platform users, organizations, permissions, and machine identities.

### Features

* registration
* login
* sessions
* organizations
* projects and memberships
* roles
* API tokens
* service accounts
* token revocation
* audit events

### Initial roles

```text
organization-owner
project-admin
developer
viewer
service-account
```

### Integration

* Forge CLI authentication
* Forge Control authorization
* Forge Gateway optional authentication checks

### Demo

`demos/09-platform-identity`

Test:

1. create user
2. create organization
3. create project
4. create developer token
5. deploy an application
6. verify a viewer cannot deploy
7. revoke token
8. verify access fails

### Tests

* password hashing
* session lifecycle
* permission evaluation
* API token tests
* revocation tests
* cross-project isolation tests
* audit-log tests

### Acceptance criteria

* unauthenticated platform modifications are rejected
* project roles are enforced
* service accounts work without human sessions
* revoked tokens become unusable
* secrets are not logged
* demo 09 passes

### Commit

```text
feat(step-09): add platform identity and authorization
```

---

## Step 10: Forge Secrets and configuration

### Language

Rust.

### Goal

Store encrypted configuration and inject it into workloads.

### Features

* project-scoped secrets
* environment-scoped secrets
* encrypted storage
* key versioning
* secret metadata
* secret rotation
* access audit
* runtime delivery
* masking in logs
* configuration values separate from secrets

### CLI

```bash
forge secret set DATABASE_PASSWORD
forge secret list
forge secret rotate DATABASE_PASSWORD
forge config set FEATURE_X=true
```

### Integration

* Forge CLI
* Forge Control
* Forge Runtime
* Forge Identity

### Demo

`demos/10-secrets`

Deploy a demo application that returns:

* whether a secret exists
* never the actual secret

Rotate the secret and redeploy.

### Tests

* encryption and decryption tests
* wrong-key tests
* access-control tests
* audit tests
* masking tests
* runtime-injection test
* rotation test

### Acceptance criteria

* plaintext secrets are not stored
* plaintext secrets are not returned by list APIs
* unauthorized projects cannot access secrets
* runtime receives the correct secret
* logs mask configured secret values
* demo 10 passes

### Commit

```text
feat(step-10): add encrypted secrets and configuration
```

---

## Step 11: Forge Events

### Language

Go.

### Goal

Provide event delivery and background jobs.

Start with NATS as the transport and build a Forge abstraction around it.

A custom storage engine can be explored later.

### Features

* publish events
* durable subscriptions
* acknowledgement
* retries
* dead-letter handling
* scheduled delivery
* event schemas
* consumer identities
* idempotency keys

### Example events

```text
build.requested
build.completed
deployment.started
deployment.completed
deployment.failed
runtime.node.offline
application.crashed
agent.run.requested
agent.run.completed
```

### Integration

All platform services can publish and consume events.

### Demo

`demos/11-event-driven`

Deploy a producer and consumer written in different languages.

Example:

```text
Go producer
    ↓
Forge Events
    ↓
Elixir consumer
```

Test retry and dead-letter behavior.

### Tests

* schema validation
* publish and consume
* acknowledgement
* retry
* dead-letter
* duplicate-event handling
* consumer restart
* ordering within a stream

### Acceptance criteria

* events survive consumer restart
* malformed events are rejected
* retry limits are enforced
* dead-letter events are inspectable
* consumers can be written in different languages
* demo 11 passes

### Commit

```text
feat(step-11): add durable platform events
```

---

## Step 12: Forge Observe

### Language

Go for platform-specific telemetry services.

Use OpenTelemetry, Prometheus, Tempo, Loki, and Grafana.

### Goal

Provide unified logs, metrics, and traces.

### Features

* OpenTelemetry ingestion
* trace propagation
* structured log collection
* platform dashboards
* service dashboards
* deployment dashboards
* runtime-node dashboards
* basic alerts
* CLI log tail
* correlation by request and trace IDs

### Integration

Every completed service must be instrumented.

### Demo

`demos/12-observability`

Execute:

```text
CLI
→ Control
→ Build
→ Runtime
→ Gateway
→ Demo application
```

Verify one distributed trace covers the request path.

### Tests

* trace-header propagation
* required metric presence
* structured log validation
* correlation-ID validation
* telemetry collector outage behavior
* dashboard provisioning test

### Acceptance criteria

* every service emits health metrics
* one request can be followed across services
* logs can be filtered by project and deployment
* telemetry failure does not crash application services
* predefined dashboards load automatically
* demo 12 passes

### Commit

```text
feat(step-12): add unified platform observability
```

---

## Step 13: Forge Storage

### Language

Rust.

### Goal

Provide object storage for products and platform services.

### Features

* buckets
* streamed upload
* streamed download
* metadata
* SHA-256 integrity
* content deduplication
* quotas
* object deletion
* range requests
* signed access tokens
* local filesystem backend

### Uses

* build artifacts
* deployment artifacts
* user uploads
* database backups
* model files
* agent outputs
* workflow outputs

### Demo

`demos/13-object-storage`

Test:

1. create bucket
2. upload large object
3. download object
4. verify checksum
5. request byte range
6. reject expired signed token
7. delete object

### Tests

* streaming tests
* checksum tests
* interrupted-upload tests
* range-request tests
* quota tests
* authorization tests
* concurrent-upload tests

### Acceptance criteria

* large files are not loaded entirely into memory
* corrupted data is detected
* project isolation is enforced
* expired access tokens fail
* storage survives restart
* demo 13 passes

### Commit

```text
feat(step-13): add shared object storage
```

---

## Step 14: Forge Models

### Language

Python.

### Goal

Provide a unified model-serving layer.

### Initial capabilities

* text embeddings
* text generation
* classification
* summarization
* model registry
* synchronous inference
* asynchronous inference
* streaming responses
* batching
* model health
* usage metrics

### Model backends

Support adapters rather than hardcoding one vendor:

* local Hugging Face model
* ONNX model
* local OpenAI-compatible server
* external provider adapter

### API concept

```text
POST /v1/models/{model}/generate
POST /v1/models/{model}/embed
POST /v1/models/{model}/classify
GET  /v1/models
```

### Integration

* Forge Identity
* Forge Events
* Forge Storage
* Forge Observe
* deployed products

### Demo

`demos/14-model-serving`

A Go application sends text to Forge Models and receives:

* embedding
* classification
* generated summary

### Tests

* model-adapter tests
* request validation
* batching tests
* streaming tests
* timeout tests
* unavailable-model tests
* usage-metric tests

### Acceptance criteria

* at least one local model runs without an external API
* model adapters use one stable API
* long-running jobs can execute asynchronously
* streaming output works
* model usage is observable
* demo 14 passes

### Commit

```text
feat(step-14): add AI model serving platform
```

---

## Step 15: Forge Agents

### Language

Python.

### Goal

Provide a safe agent execution environment.

### Agent definition

```yaml
name: deployment-investigator
model: local-general
tools:
  - deployment.read
  - logs.search
  - metrics.query
  - runtime.restart
permissions:
  - project:read
  - deployment:read
limits:
  max_steps: 10
  timeout_seconds: 120
```

### Features

* agent registry
* model selection
* tool registry
* structured tool invocation
* execution limits
* run history
* human approval points
* cancellation
* token and cost tracking
* sandboxed outputs
* permission-aware tools

### Initial agents

* deployment investigator
* log summarizer
* documentation assistant
* release reviewer
* infrastructure health agent

### Integration

Agents can use controlled tools backed by:

* Forge Control
* Forge Runtime
* Forge Observe
* Forge Storage
* Forge Models
* Forge Events

### Demo

`demos/15-agent-runtime`

Create a deliberately failing deployment.

The deployment investigator agent must:

1. inspect deployment status
2. read recent logs
3. inspect readiness failure
4. produce a diagnosis
5. recommend an action
6. not restart anything without approval

### Tests

* tool-schema tests
* permission tests
* maximum-step tests
* cancellation tests
* approval-gate tests
* hallucinated-tool rejection
* deterministic fake-model tests
* local-model integration test

### Acceptance criteria

* agents can only call registered tools
* permissions are checked before every tool call
* maximum execution limits are enforced
* agent activity is auditable
* destructive actions require approval
* demo 15 passes

### Commit

```text
feat(step-15): add permission-aware AI agents
```

---

## Step 16: Forge Workflows

### Language

Elixir.

### Goal

Coordinate reliable multi-step platform and agent workflows.

### Features

* workflow definitions
* durable workflow state
* retries
* delays
* timeouts
* parallel steps
* conditional steps
* human approvals
* agent steps
* event-triggered workflows
* compensation actions
* workflow history

### Example workflow

```text
deployment.failed
    ↓
collect logs
    ↓
collect metrics
    ↓
run investigator agent
    ↓
request human approval
    ↓
rollback deployment
    ↓
send result event
```

### Integration

* Forge Events
* Forge Agents
* Forge Models
* Forge Control
* Forge Runtime
* Forge Observe

### Demo

`demos/16-agent-workflow`

Trigger an unhealthy deployment.

Verify:

1. workflow starts
2. diagnostics are collected
3. agent produces analysis
4. approval is requested
5. approval triggers rollback
6. final report is stored

### Tests

* workflow state-machine tests
* retry tests
* process-crash recovery
* timeout tests
* parallel-step tests
* approval tests
* compensation tests
* event-trigger tests

### Acceptance criteria

* workflows survive service restart
* completed steps are not repeated accidentally
* failed actions retry according to policy
* approval state is durable
* final state is auditable
* demo 16 passes

### Commit

```text
feat(step-16): add durable AI workflow orchestration
```

---

## Step 17: Forge Memory

### Language

Rust.

### Goal

Provide semantic storage and retrieval for agents and products.

### Features

* collections
* fixed-dimension vectors
* metadata
* cosine similarity
* brute-force search first
* persistent storage
* filtering
* namespaces
* access control
* document references
* HNSW later
* hybrid lexical and semantic retrieval later

### Uses

* agent memory
* product semantic search
* deployment incident history
* documentation retrieval
* source-code retrieval
* model context retrieval

### Integration

* Forge Models produces embeddings
* Forge Storage stores source artifacts
* Forge Agents queries memory
* Forge Identity enforces access
* Forge Observe tracks query performance

### Demo

`demos/17-agent-memory`

Store historical deployment incidents.

Create a new failure with similar symptoms.

The agent must retrieve related incidents and use them in its diagnosis.

### Tests

* similarity calculations
* persistence
* metadata filters
* namespace isolation
* access control
* deletion
* benchmark tests
* restart recovery

### Acceptance criteria

* nearest-neighbor results are correct for fixtures
* project data is isolated
* persisted vectors survive restart
* agents can cite retrieved memory records
* performance benchmark is documented
* demo 17 passes

### Commit

```text
feat(step-17): add semantic memory and retrieval
```

---

## Step 18: Managed PostgreSQL service

### Language

Go.

### Goal

Let products request a managed PostgreSQL database.

### Features

* create instance
* create database
* create credentials
* attach to application
* inject connection URL
* health monitoring
* backup
* restore
* credential rotation
* deletion protection
* resource limits

This is a management service around PostgreSQL.

It is not a PostgreSQL replacement.

### Demo

`demos/18-managed-database`

Deploy an application that requires PostgreSQL.

Flow:

```text
forge database create
forge database attach
forge deploy
application runs migrations
application writes data
database backup created
database restored
```

### Tests

* provisioning
* attachment
* credential injection
* backup and restore
* credential rotation
* deletion protection
* application restart persistence

### Acceptance criteria

* products receive no hardcoded database credentials
* databases are isolated
* backup restores known fixture data
* rotated credentials invalidate old credentials
* demo 18 passes

### Commit

```text
feat(step-18): add managed PostgreSQL provisioning
```

---

## Step 19: Full AI-native platform demo

### Goal

Prove that the complete ecosystem works together.

### Demo architecture

Build a small incident-management product containing:

* Go API
* Kotlin administration service
* Rust log-processing worker
* Python classification service
* Elixir notification worker

The product uses:

* Forge Control
* Forge CLI
* Forge Build
* Forge Runtime
* Forge Scheduler
* Forge Gateway
* Forge Identity
* Forge Secrets
* Forge Events
* Forge Observe
* Forge Storage
* Forge Models
* Forge Agents
* Forge Workflows
* Forge Memory
* managed PostgreSQL

### Main scenario

```text
1. Developer deploys the product
2. Build service creates images
3. Runtime starts multiple services
4. Gateway exposes the product
5. Product writes events
6. Observability records telemetry
7. A deliberately broken release is deployed
8. Readiness check fails
9. Failure event starts a workflow
10. Agent inspects logs, metrics, and deployment state
11. Memory returns a similar historical incident
12. Agent prepares a diagnosis
13. Human approves rollback
14. Workflow rolls back the deployment
15. Final report is stored
16. Product becomes healthy again
```

### Tests

* complete platform smoke test
* full deployment test
* identity and permission test
* secret-injection test
* event-processing test
* telemetry test
* model-serving test
* agent-tool test
* workflow recovery test
* rollback test
* multi-language interoperability test

### Acceptance criteria

* one root command starts the entire demo
* one command executes the acceptance suite
* all services communicate only through documented contracts
* a broken deployment is detected and recovered
* the agent diagnosis references real telemetry
* approval is required before rollback
* all actions are auditable
* demo 19 passes

### Commit

```text
feat(step-19): complete AI-native forge platform demo
```

---

# 9. Step dependencies

```text
00 Foundation
    ↓
01 Runtime Contract
    ↓
02 Forge Control
    ↓
03 Forge CLI
    ↓
04 Forge Runtime
    ↓
05 Forge Gateway
    ↓
06 Forge Build
    ↓
07 Deployment Reconciliation
    ↓
08 Scheduler
    ↓
09 Identity
    ↓
10 Secrets
    ↓
11 Events
    ↓
12 Observability
    ↓
13 Storage
    ↓
14 Models
    ↓
15 Agents
    ↓
16 Workflows
    ↓
17 Memory
    ↓
18 Managed PostgreSQL
    ↓
19 Full Platform Demo
```

Some components could be reordered later, but this sequence keeps every step testable.

---

# 10. Definition of done for every implementation step

A step is complete only when all applicable items are satisfied.

## Implementation

* required functionality exists
* implementation follows repository conventions
* public contracts are documented
* configuration is documented
* migrations are included
* no unrelated future step is implemented

## Testing

* unit tests pass
* integration tests pass
* contract tests pass
* step-specific demo test passes
* root regression suite passes
* failure cases are tested

## Operations

* health endpoints exist
* structured logging exists
* telemetry exists where applicable
* graceful shutdown works
* Docker image builds
* service starts through Docker Compose

## Documentation

* service README updated
* architecture documentation updated
* implementation progress updated
* demo README explains the flow
* important design decisions recorded as ADRs

## Git

* changes are reviewed through `git diff`
* generated or temporary files are not committed
* one commit is created
* working tree is clean after commit

---

# 11. Implementation-step document format

Each file in `docs/implementation/steps/` should use this structure:

```text
# Step XX: Title

## Status

## Goal

## Why this step exists

## Dependencies

## Scope

## Explicitly out of scope

## Architecture

## Components

## Contracts

## Data model

## Configuration

## Security considerations

## Observability

## Failure handling

## Implementation tasks

## Files to create

## Files to modify

## Unit tests

## Integration tests

## Contract tests

## Demo scenario

## Manual verification

## Acceptance criteria

## Definition of done

## Expected Git commit
```

Recommended filenames:

```text
00-repository-foundation.md
01-runtime-contract.md
02-forge-control.md
03-forge-cli.md
04-forge-runtime.md
05-forge-gateway.md
06-forge-build.md
07-deployment-reconciliation.md
08-multi-node-scheduler.md
09-forge-identity.md
10-forge-secrets.md
11-forge-events.md
12-forge-observe.md
13-forge-storage.md
14-forge-models.md
15-forge-agents.md
16-forge-workflows.md
17-forge-memory.md
18-managed-postgresql.md
19-full-platform-demo.md
```

---

# 12. Generic implementation prompt

Store the reusable prompt at:

```text
docs/implementation/IMPLEMENT_STEP.md
```

Only `STEP_NUMBER` should need to change.

The implementation agent must:

1. read the selected step
2. inspect existing architecture
3. implement only that step
4. run all required tests
5. fix failures
6. update documentation
7. update progress
8. review the diff
9. create one Git commit
10. leave a clean working tree

The agent must not:

* implement later steps
* silently weaken tests
* skip failing tests
* rewrite unrelated services
* commit secrets
* commit generated runtime data
* claim completion while acceptance criteria fail

---

# 13. Root progress file

Use:

```text
docs/implementation/progress.md
```

Example:

```markdown
# Implementation Progress

| Step | Title | Status | Commit | Notes |
|---:|---|---|---|---|
| 00 | Repository foundation | Complete | abc1234 | Infrastructure starts locally |
| 01 | Runtime contract | In progress |  | Rust demo pending |
| 02 | Forge Control | Not started |  |  |
```

Allowed statuses:

```text
Not started
In progress
Blocked
Complete
```

The implementation agent updates this file after every step.

---

# 14. Final developer experience

After the platform reaches maturity:

```bash
git clone <product-repository>
cd <product-repository>

forge login

forge project create sample-product

forge app create backend

forge database create postgres main

forge database attach main --app backend

forge secret set API_KEY

forge deploy

forge status

forge logs --follow
```

An AI agent can later assist with:

```bash
forge agent run deployment-investigator \
  --deployment deployment-123

forge workflow run safe-release \
  --service backend \
  --image backend:v2
```

The platform should feel like a small AI-native application cloud rather than a collection of disconnected projects.
