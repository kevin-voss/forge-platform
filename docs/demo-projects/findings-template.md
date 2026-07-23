# Platform finding — template

Copy one block per finding into [`PLATFORM_FINDINGS.md`](PLATFORM_FINDINGS.md). A finding is a
place where the **platform** (a Forge service or contract) behaved differently from what the specs
/ OpenAPI contracts promise, discovered while running a demo. **Do not fix the service as part of
the demo** — record it here.

> Only record a finding when the cause is the platform. If the demo's own Dockerfile, app code,
> manifest, or test is wrong, fix the demo instead (that is not a finding).

---

```markdown
### F-NNN — <short title>

| Field | Value |
|---|---|
| Status | Open |
| Severity | blocker \| major \| minor |
| Service | forge-<name> (e.g. forge-events) |
| Area / contract | e.g. contracts/openapi/forge-events.openapi.yaml `POST /v1/publish` |
| Found by demo | 0X-<name> (step 5X.YY) |
| First seen | <date> |
| Reproducible | always \| intermittent (N/M runs) |

**What we tested**
<the exact product action / API call the demo made, and why>

**Expected (per spec/contract)**
<what the platform should have done — cite specs.md section or the OpenAPI operation>

**Actual**
<what actually happened — status codes, bodies, missing data, wrong state>

**Evidence**
- HTTP: `<method> <url>` → `<status>` body: `<snippet>`
- Logs: `<service log lines>`
- Trace id: `<id>` (if telemetry involved)
- Artifact: `tests/e2e/artifacts/<trace|video|screenshot>`

**Reproduce**
```bash
make dev
make demo DEMO=5X        # or: cd tests/e2e && node harness/orchestrator.js PROJECTS=0X
# <extra steps that isolate the failure, ideally a single curl>
```

**Impact on demo**
<did the demo fail hard (blocker), or degrade but continue? which acceptance criterion is blocked?>

**Suspected component / notes** (optional)
<file/handler guess, related findings [[F-NNN]], workaround used to keep the demo running>
```

---

## Severity guide

| Severity | Meaning | Effect on the demo run |
|---|---|---|
| **blocker** | The demo cannot demonstrate its purpose; a core contract is broken. | Product marked **failed**; `make test-platform-e2e` exits non-zero. |
| **major** | A real platform bug, but the demo can route around it (workaround/degraded path). | Product marked **degraded (passed with findings)**; run continues. |
| **minor** | Spec/contract mismatch, papercut, or missing telemetry with no functional impact. | Recorded; run passes. |

## Field conventions

* **F-NNN** — sequential id, never reused (`F-001`, `F-002`, …).
* **Service** — exactly one owning service; if it spans two, pick the one that must change and note
  the other in "Suspected component".
* **Evidence** — always include at least one machine-verifiable artifact (status+body, log line, or
  trace id). A finding without evidence is a hunch, not a finding.
* **Reproduce** — must be runnable from a clean `make dev` without the surrounding session.
