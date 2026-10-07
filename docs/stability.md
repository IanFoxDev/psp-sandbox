# Stability

psp-sandbox follows [Semantic Versioning](https://semver.org/). From 1.0 on, a test
suite that passes on `1.x` keeps passing on every later `1.y` with the same
configuration. This page says what that promise covers and what it does not.

## What does not break within a major version

**Provider API, native profile.** Endpoints under `/v1`, request fields, response
fields and their types, payment statuses and the transitions between them, error codes
and HTTP statuses, `Idempotency-Key` behavior. [api.md](api.md) and
[openapi.yaml](openapi.yaml) are the reference.

**Control API.** Endpoints under `/_sandbox/*` that are listed in
[api.md](api.md#control-api): scenario catalog, deliveries, events, forced events, replay,
authenticate, Checkout `pay`, clock, reset (with and without `reference_prefix`). The web
UI is the exception, see below.

**Scenarios.** Scenario names, their parameters and accepted values, the
`X-Sandbox-Scenario` header syntax, `metadata[sandbox_scenario]` in the stripe profile,
and the rules file format ([scenarios.md](scenarios.md)). A scenario keeps producing the
same events in the same order with the same delivery pattern. A rules file that starts
the sandbox today starts it on every later `1.y`.

**Configuration.** Every `PSP_*` variable in [api.md](api.md#configuration), its
accepted values and its default. Defaults that set test timing (`PSP_PROCESSING_DELAY`,
`PSP_RETRY_SCHEDULE`) are part of it too.

**Callbacks.** Event types and body shape in both profiles, the Standard Webhooks
headers and signature in the native profile, `Stripe-Signature` in the stripe profile,
the delivery rules in [callbacks.md](callbacks.md#delivery-and-retries) (what counts as
success, timeout, ordering per payment).

**Stripe-compatible profile.** Every endpoint and parameter in the table in
[stripe.md](stripe.md#what-is-supported) stays accepted, and the test cards listed there
keep their outcomes.

**PHP client** (`ianfoxdev/psp-sandbox-php`). Public methods of `Client`, the
`InteractsWithSandbox` trait, `Webhook\Verifier`, the enums, the public properties of the
returned objects, and the exception classes with the `SandboxException` interface.

**Docker image.** `ghcr.io/ianfoxdev/psp-sandbox:1` always points to the newest `1.x.y`,
`:1.2` to the newest `1.2.x`. Port `8090`, the healthcheck and the `healthcheck`
subcommand stay.

## What may change in a minor version

These are additions. Code written against the current version keeps working, but a
test that asserts on exact output may need an update.

- New response fields, new endpoints, new scenarios, new parameters with a default that
  keeps the old behavior, new `PSP_*` variables.
- New event types, sent only for something new that a test opts into (a new scenario,
  endpoint or parameter). A test that uses only old features gets no new events.
- New cases in PHP enums (`Scenario`, `PaymentStatus`, `DeliveryStatus`). A `match`
  without a `default` arm may need one.
- New optional arguments at the end of PHP client methods.
- Dropping a PHP version that no longer gets security fixes from php.net. Composer will
  not install the new release on that PHP, so nothing breaks at runtime.

## What is not covered

- **Differences between the stripe profile and Stripe.** The profile follows Stripe. When
  the sandbox behaves differently from Stripe and that is fixed to match Stripe, the fix
  ships in a minor or patch release and is listed in the CHANGELOG. The known differences
  are in [stripe.md](stripe.md#differences-from-stripe).
- Behavior that contradicts the documentation. That is a bug, and the fix may change
  what a test sees.
- The web UI under `/_sandbox/` and the 3DS and Checkout pages: their HTML and layout. The
  URLs a payment links to (`action_url`, `next_action`, Checkout `url`) and what a click on
  each button does are covered.
- Log lines and their format, including `PSP_LOG_FORMAT=json` field names.
- Text of error messages, in the API and in PHP exceptions. Error codes are covered.
- Exact ids. The prefixes (`pay_`, `pi_`, `evt_` and so on) stay; the characters after
  them, including the sequence a given `PSP_SEED` produces, may change between versions.
- Classes, methods and constructors marked `@internal` in the PHP client, and everything
  under `PspSandbox\Internal`.
- The Go code. The module is a binary; everything under `internal/` can change at any
  time.
- `compat/` and `examples/`. They are tests and samples for this repository.
- Retry jitter, and timing beyond what is documented.

## Deprecation

Nothing is removed in a minor version. When a variable, endpoint, parameter, scenario or
PHP method has to go:

1. A minor release marks it deprecated: a **Deprecated** entry in the CHANGELOG with the
   replacement, a note in the docs, and a warning in the sandbox log at startup or on first
   use (or `@deprecated` in the PHP client, which PHPStan and IDEs show).
2. It keeps working through the rest of the major version.
3. The next major release removes it. The CHANGELOG lists every removal under
   **BREAKING**.

## Supported versions

The latest minor release of the current major gets bug fixes and security fixes. After a
new major comes out, the last minor of the previous one gets security fixes for 6 months.
Versions before 1.0 get no fixes once 1.0 is out.

See [SECURITY.md](../SECURITY.md) for how to report a security problem.
