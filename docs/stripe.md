# Stripe-compatible profile

With `PSP_PROFILE=stripe` the sandbox answers the part of the Stripe API that a backend
uses to take card payments, and sends webhooks signed the way Stripe signs them. Code
that calls the official Stripe SDK runs against it after one change: the SDK's base URL.
The failure scenarios are the same as in the native profile.

The profile follows Stripe API version `2026-09-30.endive`. It is checked in CI against
Stripe's published OpenAPI spec and with stripe-php, stripe-go and stripe-node (see
[compat/](../compat/README.md)). psp-sandbox is not affiliated with Stripe.

Why a profile per container and what is left out: [ADR 0005](adr/0005-stripe-compatible-profile.md).

## Start

```yaml
services:
  stripe:
    image: ghcr.io/ianfoxdev/psp-sandbox:0.3
    ports: ["8090:8090"]
    environment:
      PSP_PROFILE: stripe
      PSP_CALLBACK_URL: http://app/stripe/webhook
      PSP_WEBHOOK_SECRET: whsec_test_secret
```

`PSP_WEBHOOK_SECRET` is any non-empty string here, as in the Stripe dashboard. Your
app verifies webhooks with the same value.

## Point the SDK at the sandbox

Any key starting with `sk_test_` or `rk_test_` works (or exactly `PSP_API_KEY` if set).
A live key or a publishable key gets `401`.

PHP (stripe-php):

```php
$stripe = new \Stripe\StripeClient(['api_key' => 'sk_test_123', 'api_base' => 'http://stripe:8090']);
// autoPagingIterator() and nextPage() use the global base URL, not the client's.
\Stripe\Stripe::$apiBase = 'http://stripe:8090';
```

Laravel Cashier passes `Cashier::$apiBaseUrl` to stripe-php, so set it in a service
provider for tests: `Cashier::$apiBaseUrl = 'http://stripe:8090';`. One-off payments
work; subscriptions and invoices are not in the profile.

Go (stripe-go):

```go
sc := stripe.NewClient("sk_test_123", stripe.WithBackends(stripe.NewBackendsWithConfig(&stripe.BackendConfig{
	URL: stripe.String("http://stripe:8090"),
})))
```

Node (stripe-node):

```js
const stripe = new Stripe('sk_test_123', { host: 'stripe', port: 8090, protocol: 'http' });
```

curl:

```bash
curl -u sk_test_123: localhost:8090/v1/payment_intents \
  -d amount=1000 -d currency=eur -d confirm=true -d payment_method=pm_card_visa
```

Other SDKs that take a base URL should work too; only these three are checked in CI.

## Picking a scenario

In order, the first one that applies wins:

1. `X-Sandbox-Scenario` header, for clients that can add one (curl, stripe-go through
   `Params.Headers`).
2. `metadata[sandbox_scenario]` with the same syntax: `duplicate_callback; times=3`.
   Every SDK can set metadata, so this is the usual way. The PHP
   client builds it: `Scenario::DuplicateCallback->metadata(['times' => 3])`.
3. The payment method, for Stripe's test cards:

   | Payment method | Result |
   |---|---|
   | `pm_card_visa`, any other `pm_card_*` | success |
   | `pm_card_visa_chargeDeclined` | `402`, `card_declined`, `generic_decline` |
   | `pm_card_visa_chargeDeclinedInsufficientFunds` | `402`, `card_declined`, `insufficient_funds` |
   | `pm_card_visa_chargeDeclinedLostCard` | `402`, `card_declined`, `lost_card` |
   | `pm_card_visa_chargeDeclinedStolenCard` | `402`, `card_declined`, `stolen_card` |
   | `pm_card_chargeDeclinedExpiredCard` | `402`, `expired_card` |
   | `pm_card_chargeDeclinedIncorrectCvc` | `402`, `incorrect_cvc` |
   | `pm_card_chargeDeclinedProcessingError` | `402`, `processing_error` |
   | `pm_card_createDispute` | success, then a dispute after 1 second on the sandbox clock |

   Any other `pm_` id gets `404 resource_missing`, as on Stripe.
4. The rules file. `reference` and `reference_prefix` match `metadata[reference]`.
5. `PSP_DEFAULT_SCENARIO`.

A bad scenario in metadata is rejected with `400` on the call that sets it, not later
at confirm.

The scenario is picked when the PaymentIntent is confirmed. A declined PaymentIntent can
be confirmed again with another payment method; that attempt picks its scenario anew
and gets its own charge.

## Confirming

Card payments in Stripe answer with the outcome, and so does the sandbox: `confirm`
(and `create` with `confirm=true`) returns `succeeded` or `requires_capture`, or `402`
with the `card_error` that carries the `payment_intent`. In this profile
`PSP_PROCESSING_DELAY` defaults to `0s` for that reason.

With `PSP_CLOCK=manual` and a non-zero processing delay, nothing settles until the test
moves the clock, so `confirm` answers right away with `processing` and refunds with
`pending`. Read the outcome with a `GET` after `POST /_sandbox/clock/advance`.

The browser part of a Stripe checkout (Stripe.js, Payment Element) has no counterpart.
A test confirms the PaymentIntent from the server with a test card, which is what the
browser would have done.

## Webhooks

Webhooks go to `PSP_CALLBACK_URL`, or to `metadata[sandbox_callback_url]` of the
PaymentIntent (handy when one sandbox serves several apps).

They carry `Stripe-Signature: t=<unix>,v1=<hex>`, verified by the SDKs' own
`constructEvent`. `t` is the real time even with a manual clock, because the SDKs
reject signatures older than 300 seconds.

One change of a payment may send several events:

| What happened | Events, in this order |
|---|---|
| PaymentIntent created | `payment_intent.created` |
| authorized (`capture_method=manual`) | `charge.succeeded`, `payment_intent.amount_capturable_updated` |
| captured | `charge.succeeded` (or `charge.captured` after manual capture), `payment_intent.succeeded` |
| declined | `charge.failed`, `payment_intent.payment_failed` |
| canceled | `payment_intent.canceled` |
| refunded | `refund.created`, `charge.refunded` |
| refund failed | `refund.updated`, `refund.failed` |
| dispute opened | `charge.dispute.created` |
| dispute closed | `charge.dispute.closed`, status `won` or `lost` |

The scenario's delivery applies to each of them: `duplicate_callback; times=3` sends
`charge.succeeded` three times and `payment_intent.succeeded` three times;
`out_of_order` reverses them. `callback_before_response` holds the confirm answer until
every event of the change has been tried, so `payment_intent.succeeded` is handled before
your code gets the PaymentIntent id. `payment_intent.created` is sent before the scenario
is picked, so scenarios do not change it.

The first event of a change has an id like `evt_1A2B3C4D5E6F`, the next ones the same id
with `_2`, `_3`. A webhook body is byte for byte what `GET /v1/events/{id}` returns.

The control API and the web UI work as in the native profile, with Stripe ids.

## What is supported

| Endpoint | Parameters the sandbox reads |
|---|---|
| `POST /v1/payment_intents` | `amount`, `currency`, `capture_method`, `confirm`, `payment_method`, `description`, `metadata`, `expand[]` |
| `GET /v1/payment_intents/{id}` | `expand[]` |
| `POST /v1/payment_intents/{id}` | `amount` and `payment_method` before a successful confirm, `description`, `metadata` |
| `POST /v1/payment_intents/{id}/confirm` | `payment_method`, `expand[]` |
| `POST /v1/payment_intents/{id}/capture` | `amount_to_capture`, `expand[]` |
| `POST /v1/payment_intents/{id}/cancel` | `cancellation_reason`, `expand[]` |
| `GET /v1/payment_intents` | `limit`, `starting_after`, `expand[]` |
| `GET /v1/charges/{id}` | |
| `POST /v1/refunds` | `payment_intent` or `charge`, `amount`, `reason`, `metadata` |
| `GET /v1/refunds/{id}` | |
| `GET /v1/events` | `limit`, `starting_after`, `type` (with `*`), `types[]` |
| `GET /v1/events/{id}` | |

`expand[]` knows `latest_charge`. Errors, `Idempotency-Key` (replay with
`Idempotent-Replayed: true`, `400 idempotency_error` on other parameters, `409` while the
first request runs) and `Request-Id` behave as on Stripe. Any other path gets `404`.

## Differences from Stripe

Things a test may run into, as far as they are known:

- Parameters the sandbox does not model (`automatic_payment_methods`, `receipt_email`,
  `customer` and so on) are accepted and ignored, with one warning per name in the log.
  Stripe would act on them or reject them.
- One API version. `Stripe-Version` is read and ignored.
- Cards only, no `requires_action` or 3DS, no Checkout Sessions, Customers,
  PaymentMethod objects, SetupIntents, subscriptions, invoices or Connect.
- No minimum amounts and no per-currency rules beyond a three-letter code.
- Charge ids come from the PaymentIntent: `ch_<same suffix>` for the first attempt,
  `ch_<suffix>_2` for the second.
- `request` in events is always `{"id": null, "idempotency_key": null}`.
- After a partial capture the charge has `amount_refunded: 0`. This was not checked
  against a real Stripe account; if Stripe shows something else, please open an issue.
- Disputes have reason `fraudulent`, take no evidence and are closed by the scenario
  (`outcome=lost|won`) or by hand through the control API.
- Refunds succeed, unless the payment was disputed before the refund settled.
- Lists read `limit` and `starting_after` only; `ending_before` and `created` filters are
  ignored.

## What the SDKs do

The SDK checks in [compat/](../compat/README.md) found three things worth knowing before
you rely on retries in production:

- **stripe-go does not retry 5xx answers**, only network errors and timeouts.
  `server_error_then_success` reaches your code as an error even with retries on.
- **stripe-php sends `Idempotency-Key` only when retries are on globally.**
  `max_network_retries` on `StripeClient` alone retries POSTs without a key, so a retry
  after a timeout creates a second payment. Call `\Stripe\Stripe::setMaxNetworkRetries()`
  too, or pass `idempotency_key` yourself. `timeout_then_success` shows the difference.
- **stripe-php pages through lists on the global base URL**, not the client's. Set
  `\Stripe\Stripe::$apiBase` as well, as above.

## Parallel tests

As in the native profile: give each test its own prefix in `metadata[reference]` and
reset only that with `POST /_sandbox/reset` and `{"reference_prefix": "..."}`. See
[Parallel tests](api.md#parallel-tests).
