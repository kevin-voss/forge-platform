# Epic 34: DNS and certificates

## Status

Planning

## Milestone

**M2 — Production platform.** Closing the public-access story — real domains, real TLS — is a named M2 requirement; everything before this epic is reachable only through internal service names or `localhost`.

## Goal

Stand up certificate management and DNS as twin capabilities that close the public-access story: an internal CA issuing node and workload certificates with a CSR flow and automatic rotation, public ACME certificates including wildcards with expiry monitoring, an internal authoritative DNS server shared with service discovery, public domain mapping with ownership verification, pluggable DNS provider adapters, and weighted/failover/region-aware DNS records. When this epic is done, an operator attaches a public domain to a service, ownership is verified, a DNS provider writes an ACME validation record, a certificate is issued, Gateway loads it, and the public route becomes `Ready` — with automatic rotation keeping the certificate valid indefinitely. Proven by `demos/34-domains-and-tls`.

## Why this epic exists

Every service so far is reachable over plain HTTP through Gateway's internal routing or a `.localhost` demo domain. A production platform needs real public domains with valid TLS, and it needs an internal PKI so platform services and workloads can establish mTLS identities — both DNS and certificates are foundational enough to warrant one epic covering both, since a public certificate cannot be issued without a DNS validation record, and a domain is not useful without a certificate.

## Relationship to shipped epics

New capability, additive to **epic 05 — Forge Gateway**. Gateway's dynamic route-update mechanism (`05.01`–`05.07`) is extended with a new precondition: a public route's `Ready` condition now also requires a loaded, valid certificate from this epic's `Certificate` resource. This is purely additive — Gateway's existing internal host/path routing for services without a public domain is completely unaffected, since only routes that opt into a public domain go through the certificate-readiness gate. No existing Gateway endpoint changes shape.

## Primary code areas

* `services/forge-dns/` — new Go service (API port `4121`; DNS protocol itself on `5053/udp` locally): authoritative DNS, provider adapters, record types
* `services/forge-certificates/` — new Go service (port `4122`): internal CA, CSR flow, ACME client, rotation
* `services/forge-gateway/` — certificate loading + public-route readiness gating (extends epic 05)
* `demos/34-domains-and-tls/`
* `contracts/openapi/forge-dns.openapi.yaml`, `contracts/openapi/forge-certificates.openapi.yaml`

## Suggested language

Go for both services — the DNS and ACME client ecosystems (`miekg/dns`, `golang.org/x/crypto/acme`, `lego`-style clients) are Go-native, and Go matches Gateway's language for the tightest integration point (certificate loading, route readiness).

## Spec references

* `docs/architecture/standalone-cloud.md` § DNS and certificates
* `specs.md` → Step 05: Forge Gateway (dynamic route updates, route readiness)
* [`epics/05-forge-gateway.md`](05-forge-gateway.md) → `05.01`–`05.07`

## Dependencies

* [`05-forge-gateway`](05-forge-gateway.md) — certificate loading + public-route readiness gate
* `21-forge-discovery` — shared internal authoritative DNS and service-discovery zones (future M1 epic)
* `09-forge-identity` — node/workload certificate issuance ties to service identities
* `20-declarative-resource-api` — `Certificate`/`Domain` resource conventions

## Out of scope for this epic

* Domain registration or registrar account management — domain purchase stays entirely external; Forge only configures records after delegation
* Exhaustive DNS registrar coverage — a small adapter set (Route53, Azure DNS, Hetzner DNS, self-hosted authoritative) plus a documented adapter interface, not every registrar on day one
* A private end-user-facing PKI beyond the internal CA — public-facing certificates go through ACME only, never a self-signed cert presented to end users

## Portability contract

A product manifest declares only `domains: [api.example.com]` — never a Route53 hosted-zone id, an Azure DNS zone name, or a Hetzner DNS API token. DNS provider adapters are pluggable and optional. Locally, a self-hosted authoritative DNS server (`5053/udp`) resolves everything — including ACME validation records via a local ACME server (e.g., Pebble) — so the entire flow, including certificate issuance, runs on local Docker with zero external DNS or ACME dependency. The identical manifest reaches a real registrar-delegated zone and public ACME (Let's Encrypt) unchanged on bare metal, Hetzner, AWS, and Azure — only the configured DNS provider adapter changes, never the manifest.

## Success demo

```bash
make demo DEMO=34
```

```text
forge domain attach api.example.com --service invoice-api
  → ownership verified (delegation check against the configured DNS provider adapter)
  → DNS adapter writes an ACME HTTP-01/DNS-01 validation record
  → forge-certificates requests a certificate → validation passes → certificate issued and stored
  → Gateway loads the certificate → public route api.example.com becomes Ready
  → certificate nears expiry → automatic rotation issues and loads a replacement with no downtime
```

## Planned steps

| Step | Title | Purpose |
|---|---|---|
| 34.01 | Internal CA + node/workload certificates + CSR flow | mTLS identity issuance for platform services and workloads |
| 34.02 | Certificate automatic rotation + expiry monitoring | Renew before expiry with no manual intervention |
| 34.03 | Public ACME certificates (HTTP-01/DNS-01) + wildcard certificates | Let's Encrypt-class issuance for public domains |
| 34.04 | Internal authoritative DNS + service-discovery zones | Shared zone with epic 21; resolves internal service names |
| 34.05 | Public domain mapping + ownership verification | `forge domain attach`; delegation/ownership check |
| 34.06 | DNS provider adapters | Route53, Azure DNS, Hetzner DNS, self-hosted authoritative |
| 34.07 | Weighted + failover + region-aware DNS records | Traffic-steering record types for later multi-region use |
| 34.08 | Demo `34-domains-and-tls` + gate | Attach domain → validate → issue cert → Gateway Ready → rotate |

> Steps are catalogued but not yet materialized. Materialize them with
> [`PLAN_STEPS.md`](../PLAN_STEPS.md) when milestone M1 is complete; step files and `N`
> values are assigned at that point.

## Assumptions

* `forge-dns` and `forge-certificates` are two services sharing one epic because they are functionally coupled (ACME validation needs DNS, DNS ownership verification benefits from the same delegation check certificate issuance performs) but are independently deployable and independently portable.
* The internal CA is self-contained (no external dependency) and is the root of trust for node/workload mTLS identities used across the platform; public ACME is a separate, additive certificate class for public-facing domains only.
* DNS provider adapters implement a common interface (`createRecord`, `deleteRecord`, `verifyDelegation`) so adding a new registrar does not touch `forge-dns`'s core logic.
* The local self-hosted authoritative DNS server and a local ACME server (e.g., Pebble) are wired into the Compose foundation for this epic so `demos/34-domains-and-tls` never depends on real internet DNS or Let's Encrypt's production rate limits.
* Certificate rotation is triggered by expiry-monitoring, not a fixed calendar schedule, so a certificate near a documented threshold (e.g., 30 days) is renewed regardless of when it was issued.

## Open questions

* Does ownership verification for a public domain require proof of DNS control, or is attaching a domain sufficient if the configured adapter has zone-write access? **Assumption:** zone-write access through the configured adapter is treated as sufficient proof of control for this epic; a separate manual verification flow (e.g., TXT record challenge for domains outside the adapter's managed zones) is a documented future enhancement.
* How does the local self-hosted DNS server interact with the demo's `.localhost` conventions used by earlier epics? **Assumption:** the two coexist — `.localhost` continues to resolve via the OS/browser's built-in loopback handling for internal demo routes, while this epic's authoritative DNS server handles the `api.example.com`-style public-domain flow entirely within the demo's isolated network, never touching real DNS.
* Should certificate issuance block Gateway route creation, or can a route exist in a `Progressing` state while a certificate is pending? **Assumption:** the route resource is created immediately but stays `Progressing` (not `Ready`) until the certificate is loaded, matching the platform-wide rule that `Ready` only means actually serving.
* Is DNS failover record health-checked by `forge-dns` itself, or does it consume health signals from Observe? **Assumption:** `forge-dns` consumes health signals from Observe (epic 12) rather than implementing a second independent health-check system, keeping one source of truth for service health.

## Next step to implement

**34.01 — Internal CA + node/workload certificates + CSR flow** (not yet materialized as a step file; run `PLAN_STEPS.md` once M1 lands to generate `34.01-internal-ca-and-csr-flow.md` and assign its `N`).
