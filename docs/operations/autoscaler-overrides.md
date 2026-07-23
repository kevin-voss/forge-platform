# Autoscaler overrides and schedules (runbook)

Operations guide for pausing, forcing, or time-boxing Forge Autoscaler decisions
(`forge-autoscaler`, epic 24 / step 24.05).

## Precedence (highest wins)

1. **Manual override** — fixed replica count until TTL expires
2. **Metric recommendation** (or metric-outage fallback)
3. **Active schedules** — raise/lower effective `minReplicas` / `maxReplicas`
4. **Deployment freeze / active rollout** — block scale-down only; scale-up still allowed

## Force a replica count (manual override)

```bash
# Read current resourceVersion
curl -sS "$AUTOSCALER/v1/projects/$PROJECT/environments/$ENV/scalingpolicies/$NAME" \
  | jq '.metadata.resourceVersion'

# Force 8 replicas for one hour
curl -sS -X PUT "$AUTOSCALER/v1/projects/$PROJECT/environments/$ENV/scalingpolicies/$NAME/override" \
  -H 'content-type: application/json' \
  -d '{
    "metadata": { "resourceVersion": "'"$RV"'" },
    "replicas": 8,
    "reason": "incident response",
    "ttlSeconds": 3600,
    "createdBy": "oncall"
  }'
```

- Emits `autoscaling.override.created`
- Status shows `manualOverride` and `forge_autoscaler_manual_override_active{policy}=1`
- When TTL elapses the evaluator clears the override and emits `autoscaling.override.expired`

Clear early:

```bash
curl -sS -X DELETE "$AUTOSCALER/v1/projects/$PROJECT/environments/$ENV/scalingpolicies/$NAME/override" \
  -H 'content-type: application/json' \
  -d '{ "metadata": { "resourceVersion": "'"$RV"'" }, "reason": "mitigated" }'
```

## Pause scale-down (deployment freeze)

Set on the policy spec:

```yaml
spec:
  deploymentFreeze:
    enabled: true
    until: "2026-07-23T18:00:00Z"   # optional absolute end
```

Scale-down is also blocked while the target Application/Worker reports
`status.phase=Progressing` (or deploying/rolling). Scale-up remains allowed.

## Scheduled min/max

```yaml
spec:
  schedules:
    - name: business-hours
      cron: "* 7-19 * * MON-FRI"
      timeZone: America/New_York
      minReplicas: 10
    - name: overnight
      cron: "* 20-23,0-6 * * *"
      timeZone: America/New_York
      minReplicas: 2
      maxReplicas: 8
```

- Invalid cron/timezone is rejected at admission
- Overlapping active schedules take the highest min and lowest max that still satisfy
  `min <= max`; otherwise `ScheduleConflict=True` and the base policy bounds apply
- Optional `endTime` (RFC3339) permanently ends a schedule after that instant

## Metric outage fallback

```yaml
spec:
  metricOutageFallback:
    mode: hold          # default: keep last safe desired
    # mode: floor       # drop to effective minReplicas
    # mode: fixed
    # fixedReplicas: 4
```

Status surfaces `metricOutageMode` and `ScalingActive=Unknown` with reason
`MetricFetchFailed` whenever the fallback is applied.
