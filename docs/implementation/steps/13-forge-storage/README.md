# Steps for epic 13-forge-storage

Epic: [`../../epics/13-forge-storage.md`](../../epics/13-forge-storage.md) · Status: **Planning**

Project-scoped object storage (Rust, `services/forge-storage`, host port `4107`, demo `demos/13-object-storage`).

| Step | Title | Status | Depends on |
|---|---|---|---|
| [13.01](13.01-skeleton-local-fs-backend.md) | Skeleton + local FS backend | Not started | 00, 01 |
| [13.02](13.02-buckets-metadata-project-isolation.md) | Buckets + metadata + project isolation | Not started | 13.01, 09 |
| [13.03](13.03-streamed-upload-download.md) | Streamed upload/download | Not started | 13.02 |
| [13.04](13.04-sha256-range-requests.md) | SHA-256 + range requests | Not started | 13.03 |
| [13.05](13.05-signed-tokens-expiry.md) | Signed tokens + expiry | Not started | 13.04 |
| [13.06](13.06-quotas-delete-durability.md) | Quotas + delete + restart durability | Not started | 13.05 |
| [13.07](13.07-demo-and-gate.md) | Demo `13-object-storage` + gate | Not started | 13.06 |

Next to implement: **13.01**.
