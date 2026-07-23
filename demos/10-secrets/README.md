# Demo 10: Secrets

End-to-end acceptance gate for epic 10 (Forge Secrets). The script brings up
PostgreSQL, the local registry, Identity, Secrets (with a per-run master key),
Control in **enforce** mode (with a service-account token for resolve), and
Runtime; then proves the secret lifecycle against the real secured stack:

```text
set DATABASE_PASSWORD + FEATURE_X → bind → deploy
/secret-status → present:true, value_length:3
rotate DATABASE_PASSWORD → redeploy
/secret-status → present:true, value_length:9
secret list → metadata only (no values)
service logs → no plaintext (masked)
```

```text
forge secret/config ──Bearer──► Forge Secrets (encrypted store)
                                      ▲ resolve (service-account)
Forge Control (enforce) ──────────────┘
        │ inject env (names + fingerprint only in logs)
        ▼
Forge Runtime → demo app (/secret-status reports presence + length only)
```

## What this demo checks

* `forge secret set` / `forge config set` store a secret and non-secret config.
* Service bindings declare which names the workload consumes.
* At deploy, Control resolves the env bundle and Runtime injects it; the app
  reports `DATABASE_PASSWORD_present` + `value_length` and **never** the value.
* Rotating the secret and redeploying makes the new value length take effect.
* `forge secret list` returns metadata only (no `value` field / no plaintext).
* Secrets, Control, and Runtime logs contain no plaintext secret values.
* The demo app satisfies the epic-01 runtime contract (`/`, `/health/*`).
* Stack tears down on exit (Compose stop + CLI logout + managed containers).

**This demo never sets `FORGE_AUTH_MODE=dev` for Identity/Control/Secrets.**
The master key and service-account token are generated per run (demo-only).

## Run

From the repository root:

```bash
make demo DEMO=10
```

Expect a final `demo 10 PASSED` line and exit code `0`:

```text
[set] DATABASE_PASSWORD + FEATURE_X set
[deploy] secret present: true (len 3)
[rotate+redeploy] secret present: true (len 9) OK
[list] no plaintext values OK
[logs] no plaintext secret OK
demo 10 PASSED
```

Optional phase split (CI targeting):

```bash
./demos/10-secrets/run.sh --phase=set
./demos/10-secrets/run.sh --phase=rotate
```

## Configuration

| Variable | Default | Purpose |
|---|---|---|
| `FORGE_CONTROL_URL` | `http://127.0.0.1:4001` | Control API + readiness |
| `FORGE_IDENTITY_HOST_URL` | `http://127.0.0.1:4002` | Host Identity API + `forge login` |
| `FORGE_RUNTIME_HOST_URL` | `http://127.0.0.1:4102` | Runtime readiness + hostPort lookup |
| `FORGE_SECRETS_HOST_URL` | `http://127.0.0.1:4104` | Secrets readiness + bindings |
| `FORGE_SECRETS_MASTER_KEY` | generated per run | Demo-only AES master key (base64 32 bytes) |
| `FORGE_SECRETS_SERVICE_ACCOUNT` | issued by `run.sh` | Control → Secrets resolve bearer |
| `FORGE_AUTH_MODE` | `enforce` (forced) | Must remain enforce |
| `DEMO_IMAGE` | `localhost:5000/demo-secrets:10` | Contract-compliant deploy target |

## Security notes

* Master key is demo-only; do not reuse outside this acceptance gate.
* The workload endpoint deliberately never returns secret values.
* Failure dumps Secrets/Control/Runtime logs and the project audit trail
  (still without values).
