# 0006. Checkout Sessions, 3DS and the rest of the scenario catalog

Date: 2026-10-03. Status: accepted.

## Context

0.3 covers a backend that confirms PaymentIntents itself. Two common integrations are
missing, and both are where real code breaks:

- **Stripe Checkout.** The shop creates a Checkout Session, sends the customer to
  Stripe's page and fulfils the order on `checkout.session.completed`. The usual bug is
  fulfilling on the redirect to `success_url` instead: a customer who closes the tab
  after paying never gets the order, and a duplicate webhook ships it twice.
- **3DS.** A card payment stops in `requires_action` until the customer authenticates.
  The usual bugs: treating `requires_action` as a failure, not waiting for the webhook
  after authentication, keeping stock reserved for a payment nobody will finish.

The scenario catalog also still lists four scenarios as planned.

Checked on 2026-10-03 against `github.com/stripe/openapi` at
`6f855712dfc6a235a407136e630bf36a01c069a3`, the test cards on docs.stripe.com/testing,
and stripe-go v87.0.0 (error codes).

## Decision

### 3DS in both profiles

The domain gets a status `requires_action` between `pending` and the outcome:

```
pending -> requires_action -> (authenticated) -> authorized | captured | failed
                           -> (authentication failed) -> failed
                           -> canceled
```

A scenario `three_d_secure` with `outcome` = `succeeded` (default) or `declined` puts the
payment there after the processing delay. Nothing else happens until the customer acts;
a payment nobody authenticates stays in `requires_action`, as on Stripe. There is no
separate scenario for an abandoned authentication: not acting is the scenario, and for
Checkout the session expiry ends it (below).

The customer acts in one of two ways, both ending in the same engine call:

- a page `/_sandbox/ui/3ds/{id}` with "Complete" and "Fail" buttons (a plain HTML form,
  for browser tests); afterwards it redirects to the `return_url` the app gave, if any;
- `POST /_sandbox/payments/{id}/authenticate` with `{"result": "success"}` or
  `{"result": "failure"}`, for tests without a browser.

After success the payment goes on to `outcome`. After failure it fails with reason
`authentication_failed`.

Links to the pages are absolute, built from `PSP_PUBLIC_URL` (default
`http://localhost:<port of PSP_ADDR>`): the browser of an e2e test reaches the sandbox
at a different address than the app does.

Native profile: create answers `201` with `status: "requires_action"` and `action_url`;
the event is `payment.action_required`; then the usual events follow.

Stripe profile:

| Test card | Result |
|---|---|
| `pm_card_threeDSecure2Required`, `pm_card_authenticationRequired` | `three_d_secure` |
| `pm_card_threeDSecureRequiredChargeDeclined` | `three_d_secure; outcome=declined`: `402 card_declined` after authentication |

`confirm` answers `200` with `status: "requires_action"` and
`next_action: {"type": "redirect_to_url", "redirect_to_url": {"url": ..., "return_url": ...}}`.
Stripe gives `use_stripe_sdk` instead when no `return_url` is passed; Stripe.js cannot be
pointed at the sandbox, so the sandbox always gives a redirect, with `return_url: null`
if there was none. The event is `payment_intent.requires_action`. A failed
authentication leaves the PaymentIntent in `requires_payment_method` with
`last_payment_error.code = payment_intent_authentication_failure` (a code stripe-go
lists) and sends `payment_intent.payment_failed`. While the PaymentIntent waits, and
after a failed authentication, it has no `latest_charge`.

When the 3DS page sends the customer back, the sandbox adds what Stripe adds to
`return_url`: `payment_intent`, `payment_intent_client_secret` and `redirect_status`
(`succeeded` or `failed`).

### Checkout Sessions, stripe profile only

Scope is `mode=payment` with cards:

| Endpoint | Parameters read |
|---|---|
| `POST /v1/checkout/sessions` | `mode` (only `payment`), `line_items[][price_data][currency, unit_amount, product_data[name]]`, `line_items[][quantity]`, `success_url`, `cancel_url`, `client_reference_id`, `customer_email`, `metadata`, `payment_intent_data[metadata]`, `expires_at` (30 minutes to 24 hours, default 24 hours) |
| `GET /v1/checkout/sessions/{id}` | `expand[]` (`payment_intent`, `line_items`) |
| `GET /v1/checkout/sessions` | `limit`, `starting_after`, `payment_intent` |
| `POST /v1/checkout/sessions/{id}/expire` | |
| `GET /v1/checkout/sessions/{id}/line_items` | `limit` |

`line_items[][price]` (a Price id) gets `400` that says to use `price_data`: Prices and
Products are not in the profile. `payment_intent_data[capture_method]=manual` gets
`400` too: what Stripe reports for a session whose payment is only authorized was not
checked, so it is left out rather than guessed.

The session is `open` and `unpaid` with `url` pointing at `/_sandbox/ui/checkout/{id}`.
The customer pays on that page (pick a test card, pay or cancel) or a test calls
`POST /_sandbox/checkout/{id}/pay` with `{"payment_method": "pm_card_visa"}`. The first
attempt creates the PaymentIntent (`payment_intent` is `null` before), with
`payment_intent_data[metadata]` as its metadata; later attempts confirm the same one
again, as a declined customer would with another card.

The scenario is picked as at confirm: header on the control call,
`payment_intent_data[metadata][sandbox_scenario]`, then the session's
`metadata[sandbox_scenario]`, then the test card, then the rules file with `reference`
taken from `payment_intent_data[metadata][reference]` or else `client_reference_id`.

When the PaymentIntent succeeds, the session becomes `complete` and `paid`, gets
`customer_details` and loses `url`, and `checkout.session.completed` is sent after the
PaymentIntent's own events. A decline keeps the session `open`. With 3DS the session
completes after authentication.

At `expires_at` on the sandbox clock, or on `/expire`, an open session becomes `expired`
and `checkout.session.expired` is sent. A PaymentIntent of that session that has not
succeeded is canceled with `cancellation_reason: expired`, the value the spec lists as
set by Stripe, and sends `payment_intent.canceled`.

The PaymentIntent of a session cannot be confirmed or canceled through
`/v1/payment_intents/{id}/...`: `400`, as the spec says for Stripe.

Session events are delivered with the delivery plan of the session's PaymentIntent
scenario, so `duplicate_callback` also duplicates `checkout.session.completed` and
`out_of_order` sends it before the PaymentIntent events. A session that never got a
PaymentIntent uses the default scenario.

Sessions live in the domain as a generic hosted payment page with line items and an
expiry, because they need the clock, the store, the dispatcher and the reset by
reference prefix like payments. The native profile does not expose them.

### The rest of the catalog

| Scenario | Parameters | Behavior |
|---|---|---|
| `invalid_signature` | `mode` = `wrong_secret` \| `stale_timestamp` \| `missing` | Callbacks of the payment carry a bad signature: signed with another secret, a timestamp 10 minutes old, or no signature header. Both signing schemes. |
| `ack_ignored` | `times` = 2 (1 to 10) | The sandbox treats the app's `2xx` as a failure and retries `times` more times on the retry schedule. |
| `amount_mismatch` | `delta` = -1 (not 0) | The payment is captured for `amount + delta`. The callback carries the captured amount; Stripe: `amount_received` differs from `amount`. |
| `status_regression` | `delay` = 1s | After the payment succeeds, a `payment.failed` (Stripe: `payment_intent.payment_failed`) is sent with a failed snapshot, while the payment itself stays captured. A `GET` tells the truth. |

Parameters are the ones `docs/scenarios.md` and issues #12 to #14 already published, so
nobody who read them is surprised. `status_regression` gets a `delay`.

`invalid_signature` and `ack_ignored` are about delivery only; `amount_mismatch` and
`status_regression` change what the app sees about money.

### CI recipes, not a GitHub Action

`docs/ci.md` shows the sandbox as a `services:` entry in GitHub Actions and GitLab CI
and how to wait for its healthcheck. A separate GitHub Action would add a release and
upkeep for what `services:` already does in four lines.

### Not in this decision

Subscriptions and invoices, Customers, SetupIntents, Prices and Products, payouts,
Checkout in `setup` or `subscription` mode, embedded Checkout, Adyen.

### Not verified against a live Stripe account

- Whether a failed authentication also produces a failed charge. The sandbox sends no
  `charge.failed` for it.
- The `type` of `last_payment_error` after a failed authentication. The sandbox uses
  `invalid_request_error`, as with other `payment_intent_*` codes.
- The exact order of `checkout.session.completed` relative to the PaymentIntent events.
  Stripe does not promise an order, so handlers must not depend on it either way.

## Consequences

- The domain gets `requires_action` and a customer action, used by both profiles, and a
  session object used only by the stripe profile.
- The web UI gets two pages that change state (3DS, checkout). They are forms without
  JavaScript like the rest of the UI, and they work only for objects in a state that
  waits for the customer.
- `PSP_PUBLIC_URL` is a new setting; the default works when tests run on the host.
- Every scenario in the README table is implemented; issues #12 to #14 close with the
  commits that add them.
