# 0002. Provider-neutral platform: primitives only

## Status

Accepted (target architecture; applies from epic 23 onward)

## Context

Forge must run identically on local Docker, bare metal, Hetzner, AWS EC2, and Azure VMs.
Every provider offers managed services (ECS, EKS, RDS, SQS, Lambda, App Service, Service
Bus, managed PostgreSQL) that are faster to adopt but produce a different platform per
provider: different failure modes, different limits, different operations, different bills.

## Decision

Infrastructure providers supply **primitives only**: virtual machines, CPU, memory, disks,
private networks, public IP addresses, DNS delegation, and GPU machines. Forge owns all
platform behaviour above that line — scheduling, autoscaling, deployment, networking,
databases, queues, storage, secrets, and observability.

Provider-managed services may later be added as **optional adapters** that an operator
opts into. No default Forge architecture, demo, or acceptance gate may depend on one.

## Consequences

* One platform to reason about, document, support, and test, regardless of target
* Product manifests stay portable — no provider names, machine types, region ids, or
  managed-service names ever appear in them
* Forge must implement and operate stateful capabilities itself (epics 29, 30, 31), which
  is significant work and the reason those epics carry explicit data-safety rules
* Local Docker is a first-class target, not a simulation, because it exercises the same
  code paths as every cloud target
