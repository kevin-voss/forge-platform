# Expected final incident report shape (19.05)

After human approval and Control rollback, the workflow `finalize` / `report.store`
step produces a report with at least:

| Field | Notes |
|---|---|
| `run_id` | Workflow run id |
| `deployment_id` | Failed deployment from `deployment.failed` event |
| `rolled_back` | `true` after approved rollback; `false`/absent on deny |
| `report_ref` | Stable ref (`inline://…` or `storage://wf-reports/…`) |
| `generated_at` | RFC3339 timestamp |
| `saga` | Compensator audit trail (includes `control.rollback_deployment`) |
| `trigger` | e.g. `report.store` / `approved_rollback` |

Run result also exposes:

```json
{
  "ok": true,
  "rolled_back": true,
  "report_ref": "storage://wf-reports/workflow-reports/<run_id>.json",
  "report": { "...": "same fields as above" }
}
```

Completion event (documented `deployment.completed`) is emitted after a successful
approved recovery so observers can see product health restored to the last healthy
image (v1).
