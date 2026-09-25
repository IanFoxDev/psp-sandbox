# psp-sandbox

A fake payment provider for tests and CI that fails the way real providers do.

Real PSP sandboxes are built to show the happy path. Money gets lost somewhere else:
the same callback arrives three times, the callback arrives before the HTTP response to
"create payment", the request times out on your side while the payment succeeds on theirs,
a refund comes back before the capture. None of this can be triggered on demand in a
vendor sandbox, so this code usually ships untested.

psp-sandbox is a single Docker container that speaks a simple PSP-style API, sends signed
callbacks to your app, and lets each test pick a failure scenario by name.

> Status: early development. The API described in [docs/api.md](docs/api.md) is the
> target for v0.1 and may change before the first release.

## Quick start

```yaml
# compose.yaml in your project
services:
  psp:
    image: ghcr.io/ghuser/psp-sandbox:0.1
    ports: ["8090:8090"]
    environment:
      PSP_CALLBACK_URL: http://app/api/psp/callback
      PSP_WEBHOOK_SECRET: whsec_dGVzdC1zZWNyZXQ=
```

Create a payment and ask for a duplicate callback:

```bash
curl -s localhost:8090/v1/payments \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: order-42' \
  -H 'X-Sandbox-Scenario: duplicate_callback; times=3' \
  -d '{"amount": 1000, "currency": "EUR", "reference": "order-42"}'
```

Your app now receives the same `payment.captured` event three times, each one signed.
If your handler credits the order three times, the test catches it before production does.

## Scenarios

| Scenario | What happens |
|---|---|
| `happy_path` | Payment is captured, one callback. Default. |
| `declined` | Payment fails with a decline reason. |
| `duplicate_callback` | The same event is delivered N times, sequentially or in parallel. |
| `callback_before_response` | The callback is sent before the HTTP response to create. |
| `timeout_then_success` | The create request hangs past your client timeout, the payment still succeeds. |
| `lost_callback` | No callback is ever sent. Only polling tells you the result. |
| `delayed_callback` | The callback arrives after a configurable delay. |
| `out_of_order` | Events for one payment arrive in reverse order. |
| `ack_ignored` | Your app returns 200, the sandbox retries anyway. |
| `invalid_signature` | The callback carries a wrong signature. |
| `server_error_then_success` | The first N create requests return 5xx, the next one succeeds. |
| `amount_mismatch` | The captured amount differs from the requested one. |
| `chargeback_after` | A chargeback is opened some time after capture. |

Parameters and exact behavior: [docs/scenarios.md](docs/scenarios.md).

Scenarios can also be picked without headers, by rules on amount or reference, so tests
that go through your real checkout code do not need to know about the sandbox:

```yaml
# scenarios.yaml
rules:
  - when: { amount: 1313 }
    scenario: declined
  - when: { reference_prefix: "dup-" }
    scenario: duplicate_callback
    params: { times: 3 }
```

## PHP client

```bash
composer require --dev ghuser/psp-sandbox-php
```

```php
use PspSandbox\Scenario;
use PspSandbox\Webhook\Verifier;

// verify callbacks in your app the same way you would for a real provider
(new Verifier($secret))->verify($body, $headers);

// pick a scenario from a test
$client->withScenario(Scenario::DuplicateCallback, ['times' => 3])->createPayment(1000, 'EUR', 'order-42');
```

The PHP package lives in [clients/php](clients/php) and is published as a separate
read-only repository.

## Callbacks

Callbacks follow the [Standard Webhooks](https://www.standardwebhooks.com/) signing
scheme (`webhook-id`, `webhook-timestamp`, `webhook-signature` headers), so any Standard
Webhooks library can verify them. Failed deliveries are retried with backoff. Every
delivery attempt is visible in the web UI and through the control API.
Details: [docs/callbacks.md](docs/callbacks.md).

## Control API

Tests talk to `/_sandbox/*` to inspect and drive the sandbox: list deliveries for a
payment, replay a callback, open a chargeback by hand, move the clock forward, reset
state between tests. See [docs/api.md](docs/api.md#control-api).

## Documentation

- [API](docs/api.md)
- [Scenarios](docs/scenarios.md)
- [Callbacks and signing](docs/callbacks.md)
- [Architecture](docs/architecture.md)
- [Decision records](docs/adr)

## Contributing

New failure scenarios are the most useful contribution. If your team lost money or
time to a provider behavior that is not in the list, open an issue with the
"Scenario request" template. See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

MIT
