# Demo 13: Object storage

End-to-end acceptance gate for epic 13 (Forge Storage). A standalone Compose
stack brings up `forge-storage` on host port **4107** with a fresh named volume,
then `acceptance.sh` proves the full Step 13 contract:

```text
1. create bucket "artifacts"
2. upload 50 MiB streamed object (generated at runtime — not committed)
3. download streamed + byte-compare
4. verify SHA-256 via ETag / X-Content-SHA256
5. GET Range bytes=0-1023 → 206, exactly 1024 bytes
6. GET token ttl=1s → sleep → 401 token_expired
7. DELETE object → 204; GET → 404
8. re-upload + docker compose restart → object + bucket survive
```

```text
acceptance.sh (curl on host :4107)
        │  X-Forge-Project: demo-13
        ▼
forge-storage ──► named volume storage-data
   (streamed PUT/GET, SHA-256, Range, signed tokens, quotas, delete)
```

## What this demo checks

* Bucket create under project isolation (`FORGE_AUTH_MODE=dev` + `X-Forge-Project`).
* Large-object upload/download are streamed (curl `--data-binary @file` / file sink)
  and round-trip byte-identical.
* Content integrity: client SHA-256 matches `ETag` and `X-Content-SHA256`.
* Single byte-range returns `206` with exactly 1024 bytes.
* HMAC signed GET tokens expire (`token_expired`) when clock skew is 0.
* Hard delete removes the object (`204` then `404`).
* Restart durability: bucket + a re-uploaded object survive `compose restart`
  on the named volume.
* OpenAPI contract file parses before HTTP assertions.
* No large binaries are committed; the 50 MiB fixture is generated with
  `head -c … /dev/urandom` at runtime.

## Run

From the repository root:

```bash
make demo DEMO=13
```

Expect a final `demo 13 PASSED` line and exit code `0`. On failure the script
dumps a tail of `forge-storage` logs and tears down with `docker compose down -v`.

Optional bring-up only (leaves the stack running):

```bash
./demos/13-object-storage/run.sh --phase=up
```

## Configuration

| Variable | Default | Purpose |
|---|---|---|
| `FORGE_STORAGE_URL` | `http://127.0.0.1:4107` | Host API + readiness |
| `FORGE_STORAGE_PROJECT` | `demo-13` | `X-Forge-Project` value |
| `FORGE_STORAGE_SIGNING_KEY` | `demo-13-signing-key-not-a-secret` | Demo-only HMAC key |
| `FORGE_STORAGE_CLOCK_SKEW_SECONDS` | `0` | Makes `ttl=1s` expiry observable |
| `FORGE_STORAGE_DEFAULT_QUOTA_BYTES` | `1073741824` (1 GiB) | Quota headroom for 50 MiB upload |
| `FORGE_AUTH_MODE` | `dev` | Header-based project context |
| `FORGE_STORAGE_OBJECT_BYTES` | `52428800` (50 MiB) | Fixture size |

See [`.env.example`](.env.example) for the documented demo placeholders.

## Security notes

* The signing key is a **documented non-secret placeholder** for the local gate.
  Do not reuse it outside this demo.
* No Identity/enforce path in this gate (deferred to consumer epics); project
  isolation uses the documented `dev` header.
