# 0003. Declarative resource model with spec/status separation

## Status

Accepted (target architecture; introduced by epic 20)

## Context

Forge Control shipped as a set of purpose-built endpoints (projects, applications,
services, deployments). Each new capability — databases, queues, buckets, nodes,
certificates — would otherwise add its own endpoint shape, status vocabulary, deletion
semantics, and polling story. That does not scale to the ~40 kinds the target platform
needs, and it gives the CLI and Console no general way to answer "is my change applied
yet?".

## Decision

Every managed capability is a resource with `apiVersion`, `kind`, `metadata`, `spec`, and
`status`:

* desired state in `spec`, observed state in `status`
* immutable identity in `metadata.id`, human name in `metadata.name`
* change tracking through `metadata.generation` / `status.observedGeneration`
* optimistic concurrency through `metadata.resourceVersion`
* uniform list/filter/watch, labels, annotations, owner references, finalizers, and events

Exactly one primary controller owns each kind and is the only writer of its `status`.

## Consequences

* One API style, one CLI verb set (`apply`, `get`, `describe`, `wait`, `delete`), one
  Console data model
* `observedGeneration < generation` is the universal "not caught up yet" signal
* Epic 20 must land the envelope *underneath* the shipped endpoints as a facade, so epics
  02–19 keep their contracts and tests
* Vocabulary resembles Kubernetes deliberately, because it is well understood — but Forge
  takes no Kubernetes dependency and implements its own primitives
