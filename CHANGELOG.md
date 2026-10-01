# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses
[Semantic Versioning](https://semver.org/). Before 1.0, minor versions may break the API;
such changes are marked **BREAKING**.

## [Unreleased]

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

[Unreleased]: https://github.com/IanFoxDev/psp-sandbox/compare/v0.1.1...HEAD
[0.1.1]: https://github.com/IanFoxDev/psp-sandbox/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/IanFoxDev/psp-sandbox/releases/tag/v0.1.0
