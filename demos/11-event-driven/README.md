# Demo 11: Event-driven (Go → Elixir)

End-to-end acceptance gate for epic 11 (Forge Events). The script brings up
NATS JetStream, Forge Events (port `4105`), a **Go producer**, and an **Elixir
consumer**, then proves polyglot delivery, schema rejection, retry→DLQ, and
idempotent processing:

```text
Go producer → N application.crashed → Elixir consumer acks all N
producer sends 1 malformed → 422 rejected (never delivered)
producer sends 1 poison → consumer naks → retried 3x → DLQ (inspectable)
restart Elixir consumer + re-publish duplicate (same Idempotency-Key)
  → processed exactly once
```

```text
Go producer ──HTTP──► Forge Events (JetStream) ──pull──► Elixir consumer
                              │
                              ├── schema validate (422)
                              ├── durable ack/nak + max_deliveries
                              └── DLQ inspect + processed_events
```

## What this demo checks

* A Go producer’s schema-valid `application.crashed` events are delivered to and
  processed by an Elixir durable consumer (polyglot).
* A malformed event is rejected at publish (`422`) and never delivered.
* A poison message (`reason=poison`) is nacked, retried up to
  `FORGE_DEFAULT_MAX_DELIVERIES`, and lands in an inspectable DLQ.
* Consumer restart plus a duplicate publish with the same `Idempotency-Key`
  results in exactly-once logical processing (`GET /v1/processed`).
* Producer and consumer satisfy the epic-01 runtime contract (`/`, `/health/*`).
* Stack tears down on exit (Compose stop).

**Auth:** this gate runs with `FORGE_EVENTS_AUTH_MODE=dev` (unauthenticated) for
simplicity. Enforced Identity-bound consumers are covered by step `11.06` tests.

## Run

From the repository root:

```bash
make demo DEMO=11
```

Expect a final `demo 11 PASSED` line and exit code `0`:

```text
[deliver] Go->Elixir delivered 5/5 OK
[schema] malformed rejected 422 OK
[dlq] poison in DLQ after 3 retries OK
[idempotency] restart + duplicate -> processed once OK
demo 11 PASSED
```

Optional phase flags (CI targeting):

```bash
./demos/11-event-driven/run.sh --phase=delivery
./demos/11-event-driven/run.sh --phase=dlq
./demos/11-event-driven/run.sh --phase=idempotency
```

## Configuration

| Variable | Default | Purpose |
|---|---|---|
| `FORGE_EVENTS_HOST_URL` | `http://127.0.0.1:4105` | Events API + readiness |
| `FORGE_PRODUCER_HOST_URL` | `http://127.0.0.1:4211` | Go producer |
| `FORGE_CONSUMER_HOST_URL` | `http://127.0.0.1:4212` | Elixir consumer |
| `FORGE_DEFAULT_ACK_WAIT_S` | `2` | Redelivery delay |
| `FORGE_DEFAULT_MAX_DELIVERIES` | `3` | Retry budget before DLQ |
| `FORGE_DEDUP_WINDOW_S` | `60` | Publish Idempotency-Key window |
| `FORGE_DEMO_EVENT_COUNT` | `5` | Valid events in delivery phase |
| `FORGE_DEMO_IDEMPOTENCY_KEY` | `demo-11-idempotency-key` | Fixed key for duplicate phase |
| `FORGE_EVENTS_AUTH_MODE` | `dev` | Demo gate auth mode |

## Notes

* The script resets the local `forge-nats-data` volume so leftover JetStream
  messages do not interfere with assertions.
* On failure, `run.sh` dumps Events/producer/consumer logs and the DLQ list.
