# 0005. A Stripe-compatible profile

Date: 2026-10-02. Status: accepted.

## Context

The native API (`/v1/payments`, Standard Webhooks callbacks) works for code that talks to
its provider through an interface the team owns. Most code does not: it calls the
provider SDK directly, so the sandbox cannot replace the provider without an adapter
nobody wants to write for tests.

Stripe is the provider most of this code calls. Its official SDKs take a base URL
(`api_base` in stripe-php, `BackendConfig.URL` in stripe-go, `host`/`port`/`protocol`
in stripe-node, `Cashier::$apiBaseUrl` in Laravel Cashier). If the sandbox answers like
Stripe at that URL and signs webhooks like Stripe, a test changes one line of
configuration and runs the production handler unchanged.

`stripe-mock` already answers like Stripe, from the OpenAPI spec. It sends no webhooks,
keeps no state between calls and has no failure scenarios, which is the part this
sandbox exists for.

Facts below were checked on 2026-10-02 against stripe-php 22.0.0, stripe-go v87.0.0,
stripe-node 23.0.0 (all three pin API version `2026-09-30.endive`) and
`github.com/stripe/openapi` at `6f855712dfc6a235a407136e630bf36a01c069a3`.

## Decision

### Profile per container

`PSP_PROFILE=native|stripe`, default `native`. With `stripe`, the Stripe routes replace
the native `/v1` routes; `/_sandbox/*`, the web UI, the rules file and the clock stay as
they are. Paths collide (`/v1/refunds`) and the webhook format is per receiver, so a
project with two providers runs two containers.

A path prefix (`/stripe/v1`) was rejected: stripe-node takes host and port, not a base
path, and every SDK would have to be checked for how it joins a prefix.

### Scope

| In | Out (later or never) |
|---|---|
| PaymentIntents: create, retrieve, update (`metadata`, `amount`, `description`, `payment_method` before confirm), confirm, capture, cancel, list | Checkout Sessions, `requires_action` and 3DS (candidates for 0.4) |
| Refunds: create, retrieve | Customers, PaymentMethods as objects, SetupIntents |
| Charges: retrieve | Subscriptions, Invoices, Connect |
| Events: retrieve, list | API v2, Stripe.js |

Confirming on the client with Stripe.js has no equivalent: the test calls `confirm` with
the secret key and a `pm_card_*` id, which is what the browser would have done.

### Payment states

The domain gains one status, `unconfirmed`, before `pending`. The native API never
produces it, since a native create is also a confirm. `Engine.Confirm` moves a payment
from `unconfirmed` to `pending` and starts its scenario; the scenario is picked at
confirm time, because the payment method and metadata may arrive after create.

A failed payment may go back to `unconfirmed` through `Confirm` with another payment
method. Each attempt is a new charge (`ch_`); `latest_charge` points to the last one.
The native API does not call this.

| Domain status | PaymentIntent `status` | Notes |
|---|---|---|
| `unconfirmed` | `requires_payment_method`, or `requires_confirmation` once a payment method is set | |
| `pending` | `processing` | visible only while a scenario holds the answer |
| `authorized` | `requires_capture` | `capture_method=manual` |
| `captured`, `partially_refunded`, `refunded` | `succeeded` | charge carries `amount_refunded`, `refunded` |
| `disputed`, `chargeback_lost`, `chargeback_won` | `succeeded` | Stripe does not change the intent on a dispute; the charge gets `disputed: true` |
| `failed` | `requires_payment_method` | with `last_payment_error` |
| `canceled` | `canceled` | |

`capture_method=automatic_async` is treated as `automatic`.

### Synchronous answers

Stripe answers a card confirm with the outcome. So in this profile `confirm` (and create
with `confirm=true`) holds the answer until the first status step of the scenario is
applied: `succeeded` or `requires_capture`, or `402` with a `card_error` that carries the
`payment_intent`. Scenarios that shape the answer (`timeout_then_success`,
`callback_before_response`) apply on top of that.

### Requests

- Bodies are `application/x-www-form-urlencoded` with bracket keys. All three SDKs send
  lists indexed (`expand[0]=latest_charge`); `expand[]=` from hand-written curl is
  accepted too. A JSON body gets `400` with a message that v1 takes form encoding.
- `Authorization: Bearer sk_test_...` or basic auth with the key as user (`curl -u`).
  Any `sk_test_` key passes, or exactly `PSP_API_KEY` when set. `sk_live_`, `rk_live_`
  and `pk_` keys get `401` with a message saying so.
- Unknown parameters are accepted and ignored, with one WARN per parameter name in the
  log. Real code sends `automatic_payment_methods`, `receipt_email` and others the
  sandbox does not model; rejecting them would stop adoption. Missing or malformed known
  parameters get `400` with `parameter_missing` or `parameter_invalid_*` and `param`.
- `Stripe-Version` is read and ignored. Objects have the shape of the pinned version;
  events carry it in `api_version`.
- Every response has `Request-Id: req_...`.
- Currencies are accepted in any case and returned lowercase.

### Errors

`{"error": {"type", "code", "message", "param", "decline_code", "payment_intent",
"charge"}}`, fields present as Stripe would set them. Status codes follow what the SDKs
map to exceptions: `400` and `404` invalid request (`idempotency_error` type at `400`
becomes `IdempotencyException` in stripe-php), `401` authentication, `402` card error.

### Idempotency

- Same key, same parameters: the stored answer, with `Idempotent-Replayed: true`.
- Same key, different parameters: `400`, type `idempotency_error`.
- Same key while the first request is still running: `409`, type `idempotency_error`.
  All three SDKs retry `409`.
- Validation errors (`400`) and refused calls (`5xx` from `server_error_then_success`)
  are not stored under the key. A `402` decline is stored.

### Retries

The SDKs retry on connection errors, timeouts, `409` and `5xx`, and obey
`Stripe-Should-Retry` when it is present. Defaults differ: stripe-go and stripe-node retry
twice and send an `Idempotency-Key` on every POST; stripe-php does not retry
(`max_network_retries` defaults to 0) and sends no key unless retries are on.

`server_error_then_success` answers `500`/`503` with type `api_error` and
`Stripe-Should-Retry: true`, which is the truth: nothing was created and a retry is safe.
With stripe-php defaults the test sees the exception; with retries on, it sees one
payment. That difference is the point of the scenario.

`timeout_then_success` must hold the answer past the SDK timeout, which is 80 seconds by
default in all three SDKs. Tests set a short client timeout, as they do for the native
API.

### Picking a scenario

In order, first match wins:

1. `X-Sandbox-Scenario` header, where the SDK allows custom headers.
2. `metadata[sandbox_scenario]` with the header syntax, e.g. `duplicate_callback; times=3`.
   Invalid values get `400`, as with the header. stripe-php has no per-request custom
   headers; metadata is available everywhere.
3. Stripe test payment methods:

   | Payment method | Scenario |
   |---|---|
   | `pm_card_visa_chargeDeclined` | `declined`, `generic_decline` |
   | `pm_card_visa_chargeDeclinedInsufficientFunds` | `declined`, `insufficient_funds` |
   | `pm_card_visa_chargeDeclinedLostCard` | `declined`, `lost_card` |
   | `pm_card_visa_chargeDeclinedStolenCard` | `declined`, `stolen_card` |
   | `pm_card_chargeDeclinedExpiredCard` | `declined`, code `expired_card` |
   | `pm_card_chargeDeclinedIncorrectCvc` | `declined`, code `incorrect_cvc` |
   | `pm_card_chargeDeclinedProcessingError` | `declined`, code `processing_error` |
   | `pm_card_createDispute` | `chargeback_after` |
   | any other `pm_card_*` | no scenario from the payment method |

   Other `pm_` ids get `404 resource_missing`, as on Stripe.
4. The rules file. `reference` and `reference_prefix` match `metadata[reference]`, since
   a PaymentIntent has no reference field. Prefix reset uses the same key.
5. `PSP_DEFAULT_SCENARIO`.

`metadata[sandbox_callback_url]` sends this payment's webhooks elsewhere, like
`callback_url` in the native API.

### Webhooks

A domain event becomes zero or more Stripe events, in this order:

| Domain event | Stripe events |
|---|---|
| created (new, not sent by the native profile) | `payment_intent.created` |
| `payment.authorized` | `charge.succeeded`, `payment_intent.amount_capturable_updated` |
| `payment.captured` | `charge.succeeded` and `payment_intent.succeeded`; after manual capture `charge.captured` and `payment_intent.succeeded` |
| `payment.failed` | `charge.failed`, `payment_intent.payment_failed` |
| `payment.canceled` | `payment_intent.canceled` |
| `refund.succeeded` | `refund.created`, `charge.refunded` |
| `refund.failed` | `refund.updated` |
| `chargeback.opened` | `charge.dispute.created` |
| `chargeback.closed` | `charge.dispute.closed` with `status` `won` or `lost` |

The scenario's delivery plan for a domain event applies to each Stripe event made from
it: `duplicate_callback; times=3` on a capture delivers both events three times,
`out_of_order` reverses all of them.

Event body: `id` (`evt_`), `object: "event"`, `api_version`, `created`, `data.object`
(snapshot at the time of the change), `livemode: false`, `pending_webhooks`, `request`
(`id` and `idempotency_key` of the API call that caused it, or nulls), `type`.

Signature: `Stripe-Signature: t=<unix>,v1=<hex HMAC-SHA256(secret, "<t>.<body>")>`, with
the whole secret string as the key. `PSP_WEBHOOK_SECRET` can be any non-empty string in
this profile. `t` is wall-clock time, not sandbox time: all three SDKs reject signatures
older than 300 seconds, so a test that moved the manual clock a day ahead would
otherwise fail every webhook. The native signer already uses wall-clock time.

### Control API and UI

Unchanged. Ids are Stripe ids (`pi_`, `ch_`, `re_`, `evt_`, `dp_`). Deliveries show the
Stripe event that was sent. Forcing an event by hand takes domain event names.

### How compatibility is checked

1. Every response and every webhook body in the tests is validated against the
   component schemas of the pinned `stripe/openapi` spec. The spec is downloaded in CI
   at the pinned commit; the JSON Schema validator is a test-only dependency.
2. A CI job runs a script on each of stripe-php, stripe-go and stripe-node against the
   built image: create and confirm, decline as the SDK's card exception, manual
   capture, refund, webhook verification with the SDK's own `constructEvent`, and a
   retried `server_error_then_success`.
3. `docs/stripe.md` lists what differs from Stripe. The native `docs/openapi.yaml` does
   not describe the Stripe routes; the route test skips them.

### Naming

"Stripe-compatible profile". The docs say the project is not affiliated with Stripe. No
logos.

## Consequences

- Code that calls the Stripe SDK can be tested against failure scenarios without an
  adapter. The native profile is untouched, so 0.3 has no breaking changes.
- The domain gets `unconfirmed` and a retry after failure, used only by this profile.
- The sandbox follows one Stripe API version at a time. Moving to a newer one means
  updating the pinned spec and the SDK versions in CI, and fixing what they report.
- Anything outside the scope table fails with `404`, so a test that needs Checkout
  Sessions finds out on the first call, not by silently wrong data.
- Contributors adding a scenario check it in both profiles. `invalid_signature` (#12)
  depends on the profile's signature format.
