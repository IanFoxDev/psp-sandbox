# API

The sandbox exposes two APIs on the same port (default `8090`):

- **Provider API** (`/v1/*`): what your application calls, shaped like a typical card
  acquirer.
- **Control API** (`/_sandbox/*`): what your tests call to inspect and drive the sandbox.

All bodies are JSON. Amounts are integers in minor units (`1000` EUR means 10.00 EUR).
Currency is an ISO 4217 code; the sandbox knows the minor-unit exponent of each.

## Authentication

`Authorization: Bearer <key>`. Any key is accepted unless `PSP_API_KEY` is set, in which
case only that key is. Missing or wrong key returns `401`.

## Provider API

### Create a payment

`POST /v1/payments`

Headers:

| Header | Required | Meaning |
|---|---|---|
| `Idempotency-Key` | no | Same key and same body return the stored response. Same key with a different body returns `409`. |
| `X-Sandbox-Scenario` | no | Scenario for this payment, `name; param=value; param=value`. Overrides rules. |

Body:

```json
{
  "amount": 1000,
  "currency": "EUR",
  "reference": "order-42",
  "capture": "auto",
  "callback_url": "http://app/api/psp/callback",
  "metadata": { "customer_id": "c_1" }
}
```

- `capture`: `auto` (default) or `manual`. Manual leaves the payment `authorized` until
  a capture call.
- `callback_url`: overrides `PSP_CALLBACK_URL` for this payment.
- `reference`: your id. Not required to be unique; the sandbox does not deduplicate on it.

Response `201`:

```json
{
  "id": "pay_01J9Z3K8Q2W5",
  "status": "pending",
  "amount": 1000,
  "captured_amount": 0,
  "refunded_amount": 0,
  "currency": "EUR",
  "reference": "order-42",
  "scenario": "happy_path",
  "created_at": "2026-09-25T10:00:00Z",
  "metadata": { "customer_id": "c_1" }
}
```

The payment moves to its next status asynchronously and a callback is sent, unless the
scenario says otherwise.

### Get a payment

`GET /v1/payments/{id}` returns the payment object. `404` if unknown.

`GET /v1/payments?reference=order-42` returns `{"data": [ ...payments ]}`.

### Capture

`POST /v1/payments/{id}/capture` with optional `{"amount": 700}` for partial capture.
Only from `authorized`. Returns the payment.

### Cancel

`POST /v1/payments/{id}/cancel`. Only from `pending` or `authorized`.

### Refund

`POST /v1/payments/{id}/refunds`

```json
{ "amount": 300, "reference": "refund-1" }
```

Returns a refund object `{ "id": "ref_...", "payment_id": "...", "status": "pending", "amount": 300 }`.
The refund result arrives as a `refund.succeeded` or `refund.failed` callback.
`Idempotency-Key` works the same way as for create.

### Payment statuses

```
pending -> authorized -> captured -> partially_refunded -> refunded
   |           |            |
   v           v            v
 failed     canceled     disputed -> chargeback_lost | chargeback_won
```

### Errors

```json
{ "error": { "code": "invalid_state", "message": "payment is not authorized" } }
```

| HTTP | code |
|---|---|
| 400 | `invalid_request` |
| 401 | `unauthorized` |
| 404 | `not_found` |
| 409 | `idempotency_conflict`, `invalid_state` |
| 422 | `amount_exceeds_captured` |
| 5xx | returned only by scenarios that ask for it |

## Control API

Not authenticated. Do not expose the sandbox outside a test network.

| Method and path | Purpose |
|---|---|
| `GET /_sandbox/scenarios` | Catalog: names, parameters, defaults. |
| `GET /_sandbox/payments/{id}/deliveries` | Every callback attempt: event, URL, status code, latency, body sent. |
| `POST /_sandbox/deliveries/{id}/replay` | Send a delivered event again. |
| `POST /_sandbox/payments/{id}/events` | Force an event, e.g. `{"type": "chargeback.opened"}`. |
| `POST /_sandbox/clock/advance` | `{"seconds": 3600}`. Moves the sandbox clock; due delayed events fire. Only with `PSP_CLOCK=manual`. |
| `POST /_sandbox/reset` | Drop all payments, deliveries and idempotency keys. |
| `GET /_sandbox/` | Web UI. |
| `GET /healthz` | Liveness, `200 ok`. |

## Configuration

| Variable | Default | Meaning |
|---|---|---|
| `PSP_ADDR` | `:8090` | Listen address. |
| `PSP_API_KEY` | empty | If set, required bearer key. |
| `PSP_CALLBACK_URL` | empty | Default callback URL. |
| `PSP_WEBHOOK_SECRET` | random at start | `whsec_` + base64 secret for signing. Printed to the log if random. |
| `PSP_SCENARIOS_FILE` | empty | Path to a rules file, see [scenarios.md](scenarios.md). |
| `PSP_DEFAULT_SCENARIO` | `happy_path` | Scenario when neither header nor rule matches. |
| `PSP_PROCESSING_DELAY` | `200ms` | Time between create and the first status change. |
| `PSP_RETRY_SCHEDULE` | `0s,5s,30s,2m,10m,1h` | Callback retry delays. |
| `PSP_CLOCK` | `real` | `manual` enables `/_sandbox/clock/advance`. |
| `PSP_SEED` | random | Seed for ids and jitter. Same seed, same ids. |
| `PSP_LOG_FORMAT` | `text` | `text` or `json`. |
