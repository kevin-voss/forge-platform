# Workflow resource — `order-saga`

Portable intent for Forge Workflows (epic 54.05). Machine definition:
[`../definitions/order-saga.yaml`](../definitions/order-saga.yaml).

## Steps

| Step | Action | Retries | Compensate |
|---|---|---|---|
| validate | `orderpipe.validate` (order-api) | — | — |
| charge | `orderpipe.charge` (order-api; `PSP_API_KEY` from Secrets) | 3 fixed | `orderpipe.refund` |
| fulfill | `orderpipe.fulfill` (fulfillment via Discovery) | 2 fixed | `orderpipe.cancel_fulfillment` |
| notify | `orderpipe.notify` (notify via Discovery) | 5 fixed | — |

Trigger: `order.placed`. Injectable failure: place-order `declineCharge: true`
(or email containing `+declined@`) forces charge to fail every attempt.

## Runtime model (54.05)

forge-workflows **lists** this definition when the demo mounts
`definitions/` into `FORGE_WORKFLOWS_DEFS_DIR`. Execution of `orderpipe.*`
actions is **not** supported by the engine yet (finding **F-008**).

OrderPipe therefore runs an in-process saga driver (`api/saga.go`) that:

1. Performs validate → charge with durable `saga_events` (`ok` / `retry` / `compensated`).
2. On charge success, emits `order.charged` so fulfillment/notify continue via events (54.04).
3. On terminal charge failure, refunds → status `refunded` (no half-fulfilled state).

HTTP step handlers (`POST /saga/{validate,charge,refund}`) expose the same
handlers the Workflow engine would call once an HTTP/service action exists.
