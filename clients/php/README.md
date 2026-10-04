# psp-sandbox-php

PHP client for [psp-sandbox](https://github.com/IanFoxDev/psp-sandbox), a fake payment
provider for tests and CI that sends duplicate, early, late or lost callbacks on demand.

> This repository is a read-only mirror of `clients/php` in the main repository.
> Please open issues and pull requests there.

The package has three parts:

- `Client`: create payments with a failure scenario, read what the sandbox sent to your
  app, replay callbacks, move the clock, reset state.
- `Testing\InteractsWithSandbox`: a PHPUnit trait with wait helpers that fail the test
  on timeout.
- `Webhook\Verifier`: checks the callback signature in your application. No
  dependencies, works with any Standard Webhooks sender.

Client 0.4 goes with server 0.4 (`ghcr.io/ianfoxdev/psp-sandbox:0.4`). Older clients work
with server 0.4 too, without the newer scenarios, 3DS (`authenticate()`) and
`payCheckout()`.

## Install

```bash
composer require --dev ianfoxdev/psp-sandbox-php
```

The client needs a PSR-18 HTTP client and PSR-17 factories. Laravel projects already
have Guzzle. Otherwise install one of:

```bash
composer require --dev guzzlehttp/guzzle
composer require --dev symfony/http-client nyholm/psr7
```

Without them, `new Client()` throws `MissingHttpClient`. You can also pass your own
client and factories to the constructor.

## Start the sandbox

In the `compose.yaml` of your project, next to the app:

```yaml
services:
  psp:
    image: ghcr.io/ianfoxdev/psp-sandbox:0.4
    ports: ["8090:8090"]
    environment:
      PSP_CALLBACK_URL: http://app/api/psp/callback
      PSP_WEBHOOK_SECRET: whsec_dGVzdC1zZWNyZXQ=
```

Or on its own, with callbacks going to an app on your machine:

```bash
docker run --rm -p 8090:8090 \
  -e PSP_CALLBACK_URL=http://host.docker.internal:8000/api/psp/callback \
  -e PSP_WEBHOOK_SECRET=whsec_dGVzdC1zZWNyZXQ= \
  ghcr.io/ianfoxdev/psp-sandbox:0.4
```

Set `PSP_WEBHOOK_SECRET` explicitly. Without it the sandbox makes up a random secret at
every start and your verifier rejects every callback. All settings:
[configuration](https://github.com/IanFoxDev/psp-sandbox/blob/master/docs/api.md#configuration).

## Drive the sandbox from a test

```php
use PspSandbox\Client;
use PspSandbox\Scenario;

$sandbox = new Client('http://psp:8090');

$payment = $sandbox->createPayment(1000, 'EUR',
    reference: 'order-42',
    scenario: Scenario::DuplicateCallback,
    scenarioParams: ['times' => 3, 'parallel' => true],
);

// Wait until all three copies of payment.captured were delivered (or gave up).
$deliveries = $sandbox->waitForDeliveries($payment->id, count: 3);

// Your handler got the same event three times. It must credit the order once.
```

`createPayment()` also takes `idempotencyKey`, `manualCapture` (stop at `authorized`),
`callbackUrl` (overrides `PSP_CALLBACK_URL` for this payment) and `metadata`. A
`Payment` has `status` (`PaymentStatus` enum), `capturedAmount`, `refundedAmount`,
`failureReason` and `replayed` (true when the sandbox answered from the idempotency
cache).

Most tests do not create payments themselves: the application does, through its
checkout code. Pick the scenario on the sandbox side with a
[rules file](https://github.com/IanFoxDev/psp-sandbox/blob/master/docs/scenarios.md#rules-file)
(by amount, currency or reference), then find the payment the app created:

```php
use PspSandbox\PaymentStatus;

$payments = $sandbox->payments('order-42'); // empty until the app has called the sandbox
$sandbox->waitForStatus($payments[0]->id, PaymentStatus::Captured);
```

### Scenarios

| Case | Parameters |
|---|---|
| `Scenario::HappyPath` | none |
| `Scenario::Declined` | `reason`: `insufficient_funds`, `do_not_honor`, `expired_card`, `fraud_suspected`, `generic_decline`, `lost_card`, `stolen_card`, `incorrect_cvc`, `processing_error` |
| `Scenario::DuplicateCallback` | `times` (2 to 20), `parallel`, `interval` |
| `Scenario::CallbackBeforeResponse` | `lead` |
| `Scenario::TimeoutThenSuccess` | `delay`, `mode`: `hold` or `reset` |
| `Scenario::LostCallback` | none |
| `Scenario::DelayedCallback` | `delay` |
| `Scenario::ChargebackAfter` | `delay`, `outcome`: `lost` or `won`, `close_after` |
| `Scenario::ServerErrorThenSuccess` | `failures` (1 to 10), `status`: `503`, `500`, `502` or `504` |
| `Scenario::OutOfOrder` | `window` |
| `Scenario::ThreeDSecure` | `outcome`: `succeeded` or `declined` |
| `Scenario::InvalidSignature` | `mode`: `wrong_secret`, `stale_timestamp` or `missing` |
| `Scenario::AckIgnored` | `times` (1 to 10) |
| `Scenario::AmountMismatch` | `delta` (not 0) |
| `Scenario::StatusRegression` | `delay` |

Durations are strings in Go syntax: `'500ms'`, `'35s'`. Defaults and exact behavior:
[docs/scenarios.md](https://github.com/IanFoxDev/psp-sandbox/blob/master/docs/scenarios.md).
New scenarios are added to the enum in the release that ships them on the server.

`TimeoutThenSuccess` holds the create response for 35 seconds by default. Guzzle has no
timeout unless you set one, so give your HTTP client a timeout shorter than `delay`, as
you would in production.

`Scenario::DuplicateCallback->header(['times' => 3])` returns the raw
`X-Sandbox-Scenario` value, for when the app under test sends the header itself.

With the sandbox in the stripe profile (`PSP_PROFILE=stripe`), stripe-php cannot add a
header to a request, so the scenario goes into metadata instead:
`Scenario::DuplicateCallback->metadata(['times' => 3])` returns
`['sandbox_scenario' => 'duplicate_callback; times=3']`. The control API methods of this
client (`waitForDeliveries()`, `events()`, `reset()` and the rest) work in both profiles,
with `pi_` ids in the stripe one.

### All methods

| Method | What it does |
|---|---|
| `createPayment($amount, $currency, ...)` | `POST /v1/payments`. |
| `payment($id)`, `payments($reference)` | Read payments. |
| `capture($id, $amount = null)`, `cancel($id)` | Capture (full amount by default) or cancel. |
| `refund($paymentId, $amount, $reference = null, $idempotencyKey = null)` | Returns a `Refund`. |
| `deliveries($paymentId)` | Every callback delivery with its attempts. |
| `events($paymentId)` | Events in the order they happened. |
| `forceEvent($paymentId, 'chargeback.opened')` | Make the provider send an event now. Takes `reason` for `payment.failed`, `outcome` for `chargeback.closed`. |
| `replay($deliveryId)` | Send a delivered event again, same event id. |
| `payCheckout($sessionId, $paymentMethod = 'pm_card_visa')` | Stripe profile: pay a Checkout Session as the customer would on its page; returns the PaymentIntent id. |
| `authenticate($paymentId, $success = true)` | Answer the 3DS challenge of a payment in `RequiresAction`, as the customer would on its `actionUrl` page. |
| `clock()`, `advanceClock($seconds)` | Read or move the clock of a sandbox started with `PSP_CLOCK=manual`. |
| `reset()` | Drop all payments, events and deliveries. `reset('test-42-')` drops only payments whose reference starts with the prefix. |
| `waitForDeliveries($paymentId, $count = 1, $timeoutSeconds = 5.0)` | Poll until `$count` deliveries have finished. |
| `waitForStatus($paymentId, $status, $timeoutSeconds = 5.0)` | Poll until the payment has the status. |

A `Delivery` has `eventType`, `copy`, `status` (`succeeded`, `failed`, `dropped`,
`pending`), the signed `body` and `attempts`. Each `Attempt` has `requestHeaders`,
`statusCode`, `responseBody`, `latencyMs` and `error`, so a test can assert on what your
app answered, not only on what it stored.

Constructor options: `new Client($baseUrl, $http, $requests, $streams, apiKey: ...,
pollInterval: ...)`. `apiKey` is needed only when the sandbox runs with `PSP_API_KEY`.
`pollInterval` is the pause between polls of the wait helpers, in microseconds.

### Errors

Every exception from this package implements `PspSandbox\Exception\SandboxException`:

- `ApiError`: the sandbox answered with an error. `$e->status` is the HTTP status,
  `$e->errorCode` the code (`invalid_request`, `not_found`, `invalid_state`, ...).
- `Timeout`: a wait helper gave up.
- `UnexpectedResponse`: the answer does not look like the sandbox API (wrong URL, a
  proxy page).
- `MissingHttpClient`: no HTTP client installed, see [Install](#install).
- `Webhook\InvalidSignature`: thrown by the verifier.

Network errors from the HTTP client are passed through as they are, because some
scenarios close the connection on purpose.

## PHPUnit

```php
use PHPUnit\Framework\TestCase;
use PspSandbox\PaymentStatus;
use PspSandbox\Scenario;
use PspSandbox\Testing\InteractsWithSandbox;

final class CallbackTest extends TestCase
{
    use InteractsWithSandbox; // reads PSP_SANDBOX_URL, default http://localhost:8090

    protected function setUp(): void
    {
        $this->resetSandbox();
    }

    public function testDuplicateCallbackCreditsOnce(): void
    {
        $payment = $this->sandbox()->createPayment(1000, 'EUR',
            scenario: Scenario::DuplicateCallback, scenarioParams: ['times' => 3]);

        $this->waitForDeliveries($payment->id, 3); // fails the test on timeout
        $this->waitForPaymentStatus($payment->id, PaymentStatus::Captured);

        // assert on your database here
    }
}
```

`PSP_SANDBOX_URL` is read from `$_SERVER`, `$_ENV` and the process environment, so it
can come from `phpunit.xml` or a Symfony `.env.test`. Override `sandbox()` to build the
client differently.

With paratest or several CI jobs on one sandbox, `resetSandbox()` would wipe the
payments of tests running next to this one. Give each test its own reference prefix
and reset only that:

```php
protected function setUp(): void
{
    $this->prefix = 'test-' . bin2hex(random_bytes(4)) . '-';
    $this->resetSandbox($this->prefix);
}

// and create payments with reference: $this->prefix . 'order-1'
```

Tests that move a manual clock need a sandbox of their own: the clock is shared.

## Verify callbacks

The sandbox signs callbacks with [Standard Webhooks](https://www.standardwebhooks.com/):
`webhook-id`, `webhook-timestamp` and `webhook-signature` headers. Verify the raw
request body, before any JSON decoding. A body that was decoded and encoded again
does not match the signature.

Laravel:

```php
use Illuminate\Http\Request;
use PspSandbox\Webhook\InvalidSignature;
use PspSandbox\Webhook\Verifier;

Route::post('/api/psp/callback', function (Request $request) {
    try {
        (new Verifier(config('services.psp.webhook_secret')))
            ->verify($request->getContent(), $request->headers->all());
    } catch (InvalidSignature) {
        return response('', 400);
    }

    // Deduplicate by $request->header('webhook-id') before touching the order.
    return response('', 204);
});
```

Symfony:

```php
use PspSandbox\Webhook\InvalidSignature;
use PspSandbox\Webhook\Verifier;
use Symfony\Component\HttpFoundation\Request;
use Symfony\Component\HttpFoundation\Response;
use Symfony\Component\Routing\Attribute\Route;

final class PspCallbackController
{
    public function __construct(private readonly Verifier $verifier)
    {
    }

    #[Route('/api/psp/callback', methods: ['POST'])]
    public function __invoke(Request $request): Response
    {
        try {
            $this->verifier->verify($request->getContent(), $request->headers->all());
        } catch (InvalidSignature) {
            return new Response('', 400);
        }

        return new Response('', 204);
    }
}
```

`new Verifier($secret, toleranceSeconds: 300)`: the secret is `PSP_WEBHOOK_SECRET` with or
without the `whsec_` prefix. Timestamps further than the tolerance from now are rejected.

The main repository has [Laravel and Symfony example apps](https://github.com/IanFoxDev/psp-sandbox/tree/master/examples)
with a naive and a safe callback handler, and tests that tell them apart.

## Links

- [API and configuration](https://github.com/IanFoxDev/psp-sandbox/blob/master/docs/api.md)
- [Callbacks and signing](https://github.com/IanFoxDev/psp-sandbox/blob/master/docs/callbacks.md)
- [Changelog](https://github.com/IanFoxDev/psp-sandbox/blob/master/CHANGELOG.md)

## License

MIT
