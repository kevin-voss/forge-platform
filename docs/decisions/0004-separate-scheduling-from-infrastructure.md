# 0004. Separate workload scheduling from infrastructure provisioning

## Status

Accepted (target architecture; introduced by epics 23–25)

## Context

The shipped scheduler (epic 08) places workloads on registered Runtime nodes. Autoscaling
introduces a second, very different question: where do nodes come from? Merging the two
concerns produces a component that both packs containers and calls cloud APIs — hard to
test, hard to reason about, and dangerous when it fails (a scheduling bug becomes a
billing incident).

## Decision

Four components, each answering exactly one question:

| Component | Question |
|---|---|
| Scheduler | Where should this workload run? |
| Workload autoscaler | How many workload replicas should exist? |
| Node autoscaler | How many runtime nodes should exist? |
| Infrastructure | How are nodes created and deleted? |

The scheduler never creates a machine. The autoscaler never starts a container — it only
changes numbers on resources. Infrastructure never decides how many nodes are needed.

## Consequences

* Every crossing between components is a resource change, so it is observable, auditable,
  and replayable
* A bad metric can only produce a wrong replica count, never an orphaned fleet
* Node autoscaling requires the scheduler to publish a precise unschedulable reason, which
  epic 25 makes a first-class part of placement output
* Forge Autoscaler and Forge Infrastructure become separate deployable services
