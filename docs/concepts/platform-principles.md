# Platform principles

1. Products are platform consumers
2. Containers are the runtime boundary
3. Share contracts, not language-specific implementations
4. Build incrementally with demos and tests at every step

Source of truth: repository root `specs.md`.

## Principles for the standalone-cloud phase (epics 20–43)

5. **Providers supply primitives; Forge supplies the platform** — machines, disks,
   networks, IPs, and GPUs come from a provider; no provider-managed service is ever
   required ([ADR 0002](../decisions/0002-provider-neutral-platform.md))
6. **Everything is a declarative resource** — desired state in `spec`, observed state in
   `status`, one owning controller per kind ([resource model](resource-model.md))
7. **One question per component** — the scheduler places, the autoscaler counts, the
   infrastructure service creates machines, the runtime starts containers
   ([ADR 0004](../decisions/0004-separate-scheduling-from-infrastructure.md))
8. **Portable by construction** — a product manifest never names a provider, machine type,
   region, or address ([application manifest](application-manifest.md))
9. **Additive evolution** — new capability never rewrites shipped capability
   ([ADR 0007](../decisions/0007-additive-evolution-after-epic-19.md))

Source of truth for this phase:
[`architecture/standalone-cloud.md`](../architecture/standalone-cloud.md).
