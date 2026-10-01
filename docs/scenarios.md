# Scenarios

A scenario decides how the sandbox behaves for one payment: what the create call returns,
which events happen, and how callbacks are delivered.

## Choosing a scenario

Order of precedence:

1. `X-Sandbox-Scenario` header on the create request.
2. First matching rule in `PSP_SCENARIOS_FILE`.
3. `PSP_DEFAULT_SCENARIO` (default `happy_path`).

Header format: `name; key=value; key=value`. Durations use Go syntax (`500ms`, `35s`, `2m`).

The chosen scenario is stored on the payment and applies to its refunds and later events.

## Rules file

```yaml
rules:
  - when: { amount: 1313 }                 # exact amount in minor units
    scenario: declined
    params: { reason: insufficient_funds }
  - when: { reference_prefix: "dup-" }
    scenario: duplicate_callback
    params: { times: 3, parallel: true }
  - when: { currency: "USDT" }
    scenario: delayed_callback
    params: { delay: 30s }
```

`when` keys: `amount` (exact, minor units), `currency` (exact), `reference_prefix`,
`metadata` (map; each listed key must be present with that value, other keys are
ignored). All keys in one `when` must match. Rules are checked top to bottom, the first
match wins. The header always wins over the rules.

`params` take the same values as the header, written as YAML scalars: `times: 3`,
`parallel: true`, `delay: 30s`.

The file is read once at startup and checked strictly. The sandbox refuses to start if
the file is missing, has an unknown key, a rule without `when` or `scenario`, an unknown
scenario or parameter, or an invalid parameter value. The error names the rule by its
position (`rule 3: unknown scenario "happy"`), or the line for a YAML mistake
(`line 2: unknown key amout`). A catch-all rule is not allowed: use `PSP_DEFAULT_SCENARIO`.
To change the rules, restart the container.

## Catalog

The first column is the version a scenario first shipped in. **planned** and **later**
are not implemented yet: planned ones are next in line, later ones are ideas.

| | Scenario | Parameters | Behavior |
|---|---|---|---|
| v0.1 | `happy_path` | none | `pending -> captured` (or `authorized` with manual capture), one callback per event. |
| v0.1 | `declined` | `reason` = `insufficient_funds` \| `do_not_honor` \| `expired_card` \| `fraud_suspected` | `pending -> failed`, `payment.failed` with the reason in `failure_reason`. |
| v0.1 | `duplicate_callback` | `times` = 2 (up to 20), `parallel` = false, `interval` = 0s | Every event is delivered `times` times. With `parallel=true` all copies are sent at once to hit race conditions. |
| v0.1 | `callback_before_response` | `lead` = 50ms | The status changes and the callback is sent, then the create response is returned `lead` later. The response still says `pending`. |
| v0.1 | `timeout_then_success` | `delay` = 35s, `mode` = `hold` \| `reset` | `hold`: the create response is delayed by `delay`. `reset`: the connection is closed without a response. The payment is created and captured either way, callback included. A retry with the same `Idempotency-Key` returns the payment. |
| v0.1 | `lost_callback` | none | No callbacks for this payment. Status is only visible through `GET`. |
| v0.1 | `delayed_callback` | `delay` = 10s | Callbacks are held for `delay` after the status change. |
| planned | `out_of_order` | none | Events for the payment are buffered and delivered in reverse order (e.g. `refund.succeeded` before `payment.captured`). |
| planned | `ack_ignored` | `times` = 2 | The sandbox treats your `2xx` as a failure and retries `times` more times. |
| planned | `invalid_signature` | `mode` = `wrong_secret` \| `stale_timestamp` \| `missing` | The callback signature is wrong in the chosen way. Your handler must reject it. |
| planned | `server_error_then_success` | `failures` = 1, `status` = 503 | The first `failures` create calls with the same `Idempotency-Key` return `status` without creating anything. The next one succeeds. |
| planned | `amount_mismatch` | `delta` = -1 | Captured amount is `amount + delta`. The callback carries the captured amount. |
| v0.2 | `chargeback_after` | `delay` = 24h, `outcome` = `lost` \| `won`, `close_after` = 720h | The payment is captured, `chargeback.opened` follows `delay` later and `chargeback.closed` with the outcome `close_after` after that. Both delays count from creation on the sandbox clock: with manual capture, capture before `delay` runs out or no chargeback comes. Use with `PSP_CLOCK=manual`. |
| later | `partial_capture_only` | `max` | Capture is limited to `max` regardless of the requested amount. |
| later | `status_regression` | none | A `failed` callback arrives after `captured` for the same payment. |

The first value listed for a parameter is its default. Unknown scenario names, unknown
parameters and invalid values are rejected with `400 invalid_request`, so a typo in a
test does not silently fall back to `happy_path`.

Scenarios combine only through separate payments. One payment has one scenario.

## Adding a scenario

A scenario is a Go type in `internal/scenario` that implements the `Scenario` interface
and is registered in the catalog. See [architecture.md](architecture.md) and
[CONTRIBUTING.md](../CONTRIBUTING.md). Each new scenario needs a test that shows the
behavior from the point of view of an HTTP client and a callback receiver.
