# 0006. Idempotent controllers and operation ids

## Status

Accepted (target architecture; applies from epic 20 onward, enforced by epic 35)

## Context

Once the control plane runs multiple replicas and controllers act on external systems —
cloud APIs, DNS providers, ACME, databases, registries — every action can be attempted
twice: a retry after a timeout, a leader change mid-operation, a crash between the API
call and its response. Duplicated actions here are expensive and sometimes destructive
(two VMs, two failovers, two certificate orders).

## Decision

Every mutating operation carries an **operation id** (`op_…`), written to durable storage
*before* the external call:

```text
Wrong:     create VM
Required:  create VM for infrastructure-operation op_01HZX…
```

Re-executing the same operation id must return the same resource or the same terminal
result. Controllers are written so that repeating any action is safe, and so that they can
recompute their work from persisted resource state after a restart.

## Consequences

* Provider adapters must tag created resources so an interrupted operation can be
  recovered by lookup rather than by guesswork
* Orphan reconciliation becomes possible: anything tagged with the installation id but not
  referenced by a resource can be identified and removed
* Leader election and controller leases (epic 35) are safe to add later, because
  correctness never depended on there being exactly one actor
* Every controller needs an operations table and a documented retry/backoff policy
