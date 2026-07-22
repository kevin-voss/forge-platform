# 0001. Compose-based local foundation

## Status

Accepted

## Context

Step 00 needs a single-command local substrate for databases, messaging, registry, and observability before platform services exist.

## Decision

Use Docker Compose for local foundational infrastructure, orchestrated by a root Makefile.

## Consequences

* Fast onboarding with `make setup && make dev`
* Infrastructure versions are pinned in `compose.yaml`
* Later services can run in Compose or hybrid host mode
