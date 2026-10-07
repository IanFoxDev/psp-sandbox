# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses
[Semantic Versioning](https://semver.org/). Before 1.0, minor versions may break the API;
such changes are marked **BREAKING**.

## [Unreleased]

### Added

- `docs/stability.md`: what stays compatible from 1.0 on, what may change in a minor
  release, how deprecation works and which versions get fixes. `SECURITY.md` has the
  same support table.

## [0.4.0] - 2026-10-04

3D Secure in both profiles, Stripe Checkout in the stripe profile, and the rest of the
scenario catalog: every scenario in the README is now implemented. A test can pay,
pass or fail 3DS and abandon a checkout on the sandbox's own pages or through the
control API, and get the duplicate, late or out-of-order webhooks on top. Nothing that
worked in 0.3 changes.

### Added

- 3DS in the native profile: scenario `three_d_secure` (`outcome=succeeded|declined`)
  puts a payment in `requires_action` with an `action_url` and sends
  `payment.action_required`. The customer answers on that page (`/_sandbox/ui/3ds/{id}`,
  then back to the payment's `return_url`) or a test calls
  `POST /_sandbox/payments/{id}/authenticate`. A failed authentication fails the payment
  with `authentication_failed`.
- 3DS in the stripe profile: `pm_card_threeDSecure2Required`,
  `pm_card_authenticationRequired` and `pm_card_threeDSecureRequiredChargeDeclined` put the
  PaymentIntent in `requires_action` with `next_action.redirect_to_url` to the sandbox's
  3DS page; `return_url` on create and confirm; `payment_intent.requires_action`; a failed
  authentication gives `payment_intent_authentication_failure`. Coming back from the page
  adds `payment_intent`, `payment_intent_client_secret` and `redirect_status` to
  `return_url`, as Stripe does.
- Checkout Sessions in the stripe profile, `mode=payment`: create (line items with
  `price_data`), retrieve, list, expire, line items. The customer pays on the sandbox's
  hosted page `/_sandbox/ui/checkout/{id}` (test cards, 3DS, back to `success_url` with
  `{CHECKOUT_SESSION_ID}` filled in) or a test calls `POST /_sandbox/checkout/{id}/pay`.
  `checkout.session.completed` follows the PaymentIntent's events and gets the same
  delivery scenario; at `expires_at` on the sandbox clock or on `/expire` the session
  sends `checkout.session.expired` and cancels an unpaid PaymentIntent with
  `cancellation_reason: expired`.
- Scenarios `invalid_signature` (`wrong_secret`, `stale_timestamp` or `missing`),
  `ack_ignored` (the first `times` 2xx answers are treated as failures),
  `amount_mismatch` (captured for `amount + delta`) and `status_regression` (a failed
  callback after success while the payment stays captured), in both profiles. Every
  scenario in the README table is now implemented.
- `PSP_PUBLIC_URL`: where a browser reaches the sandbox, for links to its pages.
- PHP client: `payCheckout()` pays a Checkout Session as the customer would.
- Laravel example: Stripe Checkout, fulfilled on the success page (naive) or on
  `checkout.session.completed` (safe), with tests for a customer who closes the tab
  after paying and for a reloaded success page.
- PHP client: `authenticate()`, `Scenario::ThreeDSecure`, `PaymentStatus::RequiresAction`,
  `Payment::$actionUrl`, `returnUrl` on `createPayment()`.
- [docs/ci.md](docs/ci.md): the sandbox as a service in GitHub Actions and GitLab CI,
  with callbacks back to the job.

## [0.3.0] - 2026-10-03

A Stripe-compatible profile: code on the official Stripe SDK runs against the sandbox
with one change, its base URL, and gets every failure scenario. Checked against Stripe's
OpenAPI spec and with stripe-php, stripe-go and stripe-node. Nothing in the native
profile changes.

### Added

- Stripe-compatible profile, `PSP_PROFILE=stripe`: PaymentIntents (create, confirm,
  capture, cancel, update, list), charges, refunds and events on Stripe's paths and
  form encoding, Stripe errors and idempotency, webhooks with Stripe events and
  `Stripe-Signature`. Stripe's test cards pick declines and disputes;
  `metadata[sandbox_scenario]` picks any scenario. All scenarios work in it. See
  [docs/stripe.md](docs/stripe.md).
- Contract test of the stripe profile against Stripe's OpenAPI spec (`make contract`),
  and checks with stripe-php, stripe-go and stripe-node (`make compat`, `compat/`).
- Laravel example: the same shop paying through stripe-php, with a naive and a safe
  webhook handler.
- `declined` takes five more reasons: `generic_decline`, `lost_card`, `stolen_card`,
  `incorrect_cvc`, `processing_error`.
- PHP client: `Scenario::...->metadata($params)` for picking a scenario through metadata
  in the stripe profile.

## [0.2.0] - 2026-10-01

Three more failure scenarios, tests in parallel on one sandbox, and an OpenAPI
description for clients in other languages. Nothing from 0.1 changes.

### Added

- Scenario `chargeback_after`: the payment is captured, `chargeback.opened` comes
  `delay` later (24h by default) and `chargeback.closed` `close_after` after that, as
  lost or won. With `PSP_CLOCK=manual` a test gets there in two clock advances.
- Scenario `server_error_then_success`: the first create calls of a request answer
  `503` (or `500`, `502`, `504`) with code `server_error` and create nothing, then a
  retry with the same `Idempotency-Key` creates exactly one payment.
- Scenario `out_of_order`: events of a payment that come within `window` (2s by
  default) of the first one are held and delivered newest first, so a refund can
  arrive before the capture it refunds.
- `POST /_sandbox/reset` takes `{"reference_prefix": "..."}` and drops only the payments
  of one test, so tests that share a sandbox can run in parallel. PHP client:
  `reset($referencePrefix)` and `resetSandbox($referencePrefix)`. See "Parallel tests"
  in `docs/api.md`.
- `docs/openapi.yaml`: OpenAPI 3.1 for the provider and control APIs and the callbacks.
  A Go test keeps it in step with the routes and response fields, and CI lints it.

## [0.1.1] - 2026-09-28

### Added

- `psp-sandbox healthcheck` checks `/healthz` of the running server and exits 0 or 1.
  The image uses it as its Docker `HEALTHCHECK`, so compose can wait for
  `service_healthy`.
- Startup warnings when `PSP_CALLBACK_URL` or `PSP_WEBHOOK_SECRET` is not set.
- Unknown paths and wrong methods answer with the JSON error shape
  (`404 not_found`, `405 method_not_allowed`) instead of plain text.

### Changed

- The sandbox refuses to start when `PSP_CALLBACK_URL` is not an absolute http or
  https URL. It used to start and fail on every delivery.
- PHP client: `new Client()` without an HTTP client throws `MissingHttpClient`,
  which names the packages to install, instead of a php-http/discovery error.
- PHP client: `InvalidSignature` implements `SandboxException` like the other
  exceptions of the package.
- Invalid JSON bodies and rules files are reported by field and line, without Go
  type names: `amount must be an integer`, `line 2: unknown key amout`.
- Failed callback deliveries are logged as warnings.

### Fixed

- A rule with a lowercase `currency` is rejected at startup. It used to load and
  never match.
- `POST /_sandbox/clock/advance` accepts at most 10 years. Larger values overflowed
  and could move the clock back.
- PHP client: `Verifier::verify()` accepts `$request->headers->all()` from Symfony
  and Laravel without a PHPStan error in the calling code.
- PHP client: the package archive no longer ships tests and tool configs.
- One `POST /_sandbox/clock/advance` runs every callback retry that falls due within
  it. Before, each call released at most one retry per delivery.
- `POST /v1/payments/{id}/capture` with `"amount": 0` returns `400` instead of
  capturing the full amount. Leave `amount` out to capture everything.
- The server exits with code 1 when it cannot listen (busy port, bad `PSP_ADDR`).
  It used to log "listening" and exit with 0.
- PHP client: removed `Scenario` cases the server does not implement yet
  (`OutOfOrder`, `AckIgnored`, `InvalidSignature`, `ServerErrorThenSuccess`,
  `AmountMismatch`, `ChargebackAfter`). Every request with them failed with
  `400 invalid_request`. They come back with the release that adds them.

## [0.1.0] - 2026-09-27

First release.

### Added

- One static binary in a multi-arch image (`ghcr.io/ianfoxdev/psp-sandbox`, amd64 and
  arm64), configured with `PSP_*` environment variables, `/healthz` and `/version`.
- Provider API: create, get, list by reference, capture, cancel and refund payments,
  `Idempotency-Key` on create and refund, optional bearer key (`PSP_API_KEY`).
- Signed callbacks (Standard Webhooks) with retries on `PSP_RETRY_SCHEDULE`, one at a
  time per payment in event order, and a log of every attempt.
- Manual clock (`PSP_CLOCK=manual`) that drives status changes, delayed callbacks and
  retries.
- Control API under `/_sandbox`: scenario catalog, delivery log with every attempt,
  event history, replay of a delivery, forced payment and chargeback events, clock
  status and advance, reset.
- Rules file (`PSP_SCENARIOS_FILE`) that picks a scenario by amount, currency, reference
  prefix or metadata when the create request has no `X-Sandbox-Scenario` header. Checked
  at startup, so a typo stops the sandbox instead of falling back to `happy_path`.
- Web UI at `/_sandbox/`: payments with delivery counts, filter by reference, payment
  page with events and every delivery attempt, replay and reset buttons, auto-refresh.
  Screenshots in the README and `docs/api.md`.
- PHP client (`ianfoxdev/psp-sandbox-php`): Standard Webhooks signature verifier,
  scenario enum, PSR-18 `Client` for the provider and control APIs with typed results,
  `waitForDeliveries()` and `waitForStatus()`, `ApiError` with the error code, and the
  PHPUnit trait `InteractsWithSandbox`. Integration tests run against a real sandbox
  in CI.
- Laravel and Symfony example shops with a naive and a safe callback handler, and
  tests that fail on the naive one under `duplicate_callback` and
  `callback_before_response`. Run in CI.
- Scenarios `happy_path`, `declined`, `duplicate_callback`, `callback_before_response`,
  `timeout_then_success`, `lost_callback`, `delayed_callback`.

[Unreleased]: https://github.com/IanFoxDev/psp-sandbox/compare/v0.4.0...HEAD
[0.4.0]: https://github.com/IanFoxDev/psp-sandbox/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/IanFoxDev/psp-sandbox/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/IanFoxDev/psp-sandbox/compare/v0.1.1...v0.2.0
[0.1.1]: https://github.com/IanFoxDev/psp-sandbox/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/IanFoxDev/psp-sandbox/releases/tag/v0.1.0
