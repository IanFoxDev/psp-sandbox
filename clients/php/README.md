# psp-sandbox-php

PHP client for [psp-sandbox](https://github.com/ianfoxdev/psp-sandbox), a fake payment
provider that fails on demand.

> This repository is a read-only mirror of `clients/php` in the main repository.
> Please open issues and pull requests there.

```bash
composer require --dev ianfoxdev/psp-sandbox-php
```

## Drive the sandbox from tests

The client talks to the provider API (`/v1`) and the control API (`/_sandbox`). It
needs a PSR-18 HTTP client; if you pass none, [php-http/discovery](https://docs.php-http.org/en/latest/discovery.html)
finds the one your project already has (Guzzle, Symfony HttpClient and others).

```php
use PspSandbox\Client;
use PspSandbox\PaymentStatus;
use PspSandbox\Scenario;

$sandbox = new Client('http://psp-sandbox:8090');

$payment = $sandbox->createPayment(1000, 'EUR',
    reference: 'order-42',
    scenario: Scenario::DuplicateCallback,
    scenarioParams: ['times' => 3, 'parallel' => true],
);

// Wait until all three copies of payment.captured were delivered (or gave up).
$deliveries = $sandbox->waitForDeliveries($payment->id, count: 3);

// Your handler got the same event three times. It must credit the order once.
```

Most tests do not create payments themselves: the application does, through its
checkout code. Pick the scenario with a rules file on the sandbox side (by amount or
reference, see the main repository), then look up the payment:

```php
$payment = $sandbox->payments('order-42')[0];
$sandbox->waitForStatus($payment->id, PaymentStatus::Captured);
```

Other calls:

| Method | What it does |
|---|---|
| `payment($id)`, `payments($reference)` | Read payments. |
| `capture($id, $amount)`, `cancel($id)`, `refund($id, $amount)` | Provider operations. |
| `deliveries($paymentId)` | Every callback delivery with its attempts: headers, response, latency, error. |
| `events($paymentId)` | Events in the order they happened. |
| `forceEvent($paymentId, 'chargeback.opened')` | Make the provider send an event now. |
| `replay($deliveryId)` | Send a delivered event again, same event id. |
| `advanceClock($seconds)`, `clock()` | Move the clock of a sandbox started with `PSP_CLOCK=manual`. |
| `reset()` | Drop all state between tests. |

Errors from the sandbox throw `PspSandbox\Exception\ApiError` with the HTTP status and the
error code (`invalid_state`, `not_found`, ...). Network errors from the HTTP client are
passed through, because some scenarios close the connection on purpose.

## PHPUnit

```php
use PHPUnit\Framework\TestCase;
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

        // assert on your database here
    }
}
```

## Verify callbacks

The sandbox signs callbacks with [Standard Webhooks](https://www.standardwebhooks.com/).
The verifier has no dependencies:

```php
use PspSandbox\Webhook\Verifier;
use PspSandbox\Webhook\InvalidSignature;

$verifier = new Verifier('whsec_dGVzdC1zZWNyZXQ=');

try {
    $verifier->verify($request->getContent(), $request->headers->all());
} catch (InvalidSignature $e) {
    return new Response('', 400);
}
```

## Pick a scenario

```php
use PspSandbox\Scenario;

$header = Scenario::DuplicateCallback->header(['times' => 3, 'parallel' => true]);
// "duplicate_callback; times=3; parallel=true"
```

## License

MIT
