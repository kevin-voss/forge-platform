# OrderPipe event schemas (`order.*`)

Epic **54.04** — services advance order status by publishing and consuming Forge Events
subjects under the `order` stream family.

| Subject | Producer | Consumer(s) | Status after |
|---|---|---|---|
| `order.placed` | order-api (on `POST /orders`) | order-api (`orderpipe-validate`) | → `validated` |
| `order.validated` | order-api | order-api (`orderpipe-charge`) | → `charged` |
| `order.charged` | order-api | fulfillment (`orderpipe-fulfill`) | (fulfillment records + emits) |
| `order.fulfilled` | fulfillment | order-api (`orderpipe-mark-fulfilled`), notify (`orderpipe-notify`) | → `fulfilled` / notify queues |
| `order.notified` | notify | order-api (`orderpipe-mark-notified`) | → `notified` |

JSON Schemas live in `contracts/events/order.*.schema.json` (loaded by forge-events).
Each stage also appends a row to `saga_events` (`outcome=ok` on the happy path).

Retry/compensation orchestration is **out of scope** here (see `54.05`).
