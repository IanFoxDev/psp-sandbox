<?php

declare(strict_types=1);

namespace PspSandbox;

use Http\Discovery\Exception as DiscoveryException;
use Http\Discovery\Psr17FactoryDiscovery;
use Http\Discovery\Psr18ClientDiscovery;
use Psr\Http\Client\ClientExceptionInterface;
use Psr\Http\Client\ClientInterface;
use Psr\Http\Message\RequestFactoryInterface;
use Psr\Http\Message\ResponseInterface;
use Psr\Http\Message\StreamFactoryInterface;
use PspSandbox\Exception\ApiError;
use PspSandbox\Exception\MissingHttpClient;
use PspSandbox\Exception\Timeout;
use PspSandbox\Exception\UnexpectedResponse;
use PspSandbox\Internal\Fields;

/**
 * Client for the provider API (/v1) and the control API (/_sandbox) of psp-sandbox.
 *
 * Any PSR-18 client works. Without one, php-http/discovery finds the one installed
 * in the project: Guzzle, or Symfony HttpClient together with nyholm/psr7.
 */
final class Client
{
    private readonly string $baseUrl;
    private readonly ClientInterface $http;
    private readonly RequestFactoryInterface $requests;
    private readonly StreamFactoryInterface $streams;

    /**
     * @param string      $baseUrl sandbox address, e.g. "http://psp-sandbox:8090"
     * @param string|null $apiKey  sent as a bearer token to /v1, needed only with PSP_API_KEY
     *
     * @throws MissingHttpClient
     */
    public function __construct(
        string $baseUrl = 'http://localhost:8090',
        ?ClientInterface $http = null,
        ?RequestFactoryInterface $requests = null,
        ?StreamFactoryInterface $streams = null,
        private readonly ?string $apiKey = null,
        /** Pause between polls of the wait helpers, in microseconds. */
        private readonly int $pollInterval = 50_000,
    ) {
        $this->baseUrl = rtrim($baseUrl, '/');
        try {
            $this->http = $http ?? Psr18ClientDiscovery::find();
            $this->requests = $requests ?? Psr17FactoryDiscovery::findRequestFactory();
            $this->streams = $streams ?? Psr17FactoryDiscovery::findStreamFactory();
        } catch (DiscoveryException $e) {
            throw MissingHttpClient::because($e);
        }
    }

    // Provider API: what the application under test calls. Useful in tests that
    // create payments directly instead of going through the application.

    /**
     * @param Scenario|string|null  $scenario       a catalog scenario, or a raw X-Sandbox-Scenario value
     * @param array<string, scalar> $scenarioParams parameters for a Scenario case
     * @param array<string, string> $metadata
     *
     * @throws ApiError
     * @throws ClientExceptionInterface
     */
    public function createPayment(
        int $amount,
        string $currency,
        ?string $reference = null,
        Scenario|string|null $scenario = null,
        array $scenarioParams = [],
        ?string $idempotencyKey = null,
        bool $manualCapture = false,
        ?string $callbackUrl = null,
        array $metadata = [],
        ?string $returnUrl = null,
    ): Payment {
        $body = ['amount' => $amount, 'currency' => $currency];
        if ($reference !== null) {
            $body['reference'] = $reference;
        }
        if ($manualCapture) {
            $body['capture'] = 'manual';
        }
        if ($callbackUrl !== null) {
            $body['callback_url'] = $callbackUrl;
        }
        if ($metadata !== []) {
            $body['metadata'] = $metadata;
        }
        if ($returnUrl !== null) {
            $body['return_url'] = $returnUrl;
        }

        $headers = [];
        if ($scenario instanceof Scenario) {
            $headers[Scenario::HEADER] = $scenario->header($scenarioParams);
        } elseif ($scenario !== null) {
            if ($scenarioParams !== []) {
                throw new \InvalidArgumentException('Pass parameters inside the scenario string, or use a Scenario case.');
            }
            $headers[Scenario::HEADER] = $scenario;
        }
        if ($idempotencyKey !== null) {
            $headers['Idempotency-Key'] = $idempotencyKey;
        }

        $response = $this->send('POST', '/v1/payments', $body, $headers);

        return Payment::fromFields($this->decode($response, 'payment'), self::replayed($response));
    }

    /**
     * @throws ApiError
     * @throws ClientExceptionInterface
     */
    public function payment(string $id): Payment
    {
        return Payment::fromFields($this->call('GET', '/v1/payments/' . rawurlencode($id), 'payment'));
    }

    /**
     * Payments with this reference, oldest first.
     *
     * @return list<Payment>
     *
     * @throws ApiError
     * @throws ClientExceptionInterface
     */
    public function payments(string $reference): array
    {
        $list = $this->call('GET', '/v1/payments?reference=' . rawurlencode($reference), 'payment list');

        return array_map(Payment::fromFields(...), $list->list('data'));
    }

    /**
     * Captures an authorized payment; null captures the full amount.
     *
     * @throws ApiError
     * @throws ClientExceptionInterface
     */
    public function capture(string $id, ?int $amount = null): Payment
    {
        $body = $amount === null ? null : ['amount' => $amount];

        return Payment::fromFields($this->call('POST', '/v1/payments/' . rawurlencode($id) . '/capture', 'payment', $body));
    }

    /**
     * @throws ApiError
     * @throws ClientExceptionInterface
     */
    public function cancel(string $id): Payment
    {
        return Payment::fromFields($this->call('POST', '/v1/payments/' . rawurlencode($id) . '/cancel', 'payment'));
    }

    /**
     * @throws ApiError
     * @throws ClientExceptionInterface
     */
    public function refund(string $paymentId, int $amount, ?string $reference = null, ?string $idempotencyKey = null): Refund
    {
        $body = ['amount' => $amount];
        if ($reference !== null) {
            $body['reference'] = $reference;
        }
        $headers = $idempotencyKey === null ? [] : ['Idempotency-Key' => $idempotencyKey];
        $response = $this->send('POST', '/v1/payments/' . rawurlencode($paymentId) . '/refunds', $body, $headers);

        return Refund::fromFields($this->decode($response, 'refund'), self::replayed($response));
    }

    // Control API: what tests call to inspect and drive the sandbox.

    /**
     * Every callback delivery of the payment, in the order they were queued.
     *
     * @return list<Delivery>
     *
     * @throws ApiError
     * @throws ClientExceptionInterface
     */
    public function deliveries(string $paymentId): array
    {
        $list = $this->call('GET', '/_sandbox/payments/' . rawurlencode($paymentId) . '/deliveries', 'delivery list');

        return array_map(Delivery::fromFields(...), $list->list('data'));
    }

    /**
     * Events of the payment and its refunds, in the order they happened.
     *
     * @return list<Event>
     *
     * @throws ApiError
     * @throws ClientExceptionInterface
     */
    public function events(string $paymentId): array
    {
        $list = $this->call('GET', '/_sandbox/payments/' . rawurlencode($paymentId) . '/events', 'event list');

        return array_map(Event::fromFields(...), $list->list('data'));
    }

    /**
     * Makes the provider send an event now, for example "chargeback.opened".
     * The payment status changes as the event says; the usual status rules apply.
     *
     * @param string|null $reason  decline reason for "payment.failed"
     * @param string|null $outcome "lost" or "won" for "chargeback.closed"
     *
     * @throws ApiError
     * @throws ClientExceptionInterface
     */
    public function forceEvent(string $paymentId, string $type, ?string $reason = null, ?string $outcome = null): ForcedEvent
    {
        $body = ['type' => $type];
        if ($reason !== null) {
            $body['reason'] = $reason;
        }
        if ($outcome !== null) {
            $body['outcome'] = $outcome;
        }
        $f = $this->call('POST', '/_sandbox/payments/' . rawurlencode($paymentId) . '/events', 'forced event', $body);

        return new ForcedEvent(Payment::fromFields($f->object('payment')), Event::fromFields($f->object('event')));
    }

    /**
     * Answers the 3DS challenge of a payment in RequiresAction, as the customer
     * would on its actionUrl page. The scenario decides what follows.
     *
     * @throws ApiError
     * @throws ClientExceptionInterface
     */
    public function authenticate(string $paymentId, bool $success = true): Payment
    {
        return Payment::fromFields($this->call('POST', '/_sandbox/payments/' . rawurlencode($paymentId) . '/authenticate',
            'payment', ['result' => $success ? 'success' : 'failure']));
    }

    /**
     * Pays a Checkout Session of the stripe profile with a test card, as the
     * customer would on its page, and returns the id of the session's
     * PaymentIntent, for waitForDeliveries().
     *
     * @throws ApiError
     * @throws ClientExceptionInterface
     */
    public function payCheckout(string $sessionId, string $paymentMethod = 'pm_card_visa'): string
    {
        $f = $this->call('POST', '/_sandbox/checkout/' . rawurlencode($sessionId) . '/pay', 'checkout session',
            ['payment_method' => $paymentMethod]);

        return $f->object('payment_intent')->string('id');
    }

    /**
     * Sends the event of a delivery again, as a new delivery with the same event id.
     *
     * @throws ApiError
     * @throws ClientExceptionInterface
     */
    public function replay(string $deliveryId): Delivery
    {
        return Delivery::fromFields($this->call('POST', '/_sandbox/deliveries/' . rawurlencode($deliveryId) . '/replay', 'delivery'));
    }

    /**
     * @throws ApiError
     * @throws ClientExceptionInterface
     */
    public function clock(): ClockState
    {
        return ClockState::fromFields($this->call('GET', '/_sandbox/clock', 'clock'));
    }

    /**
     * Moves a manual clock (PSP_CLOCK=manual). Status changes that fall due are
     * applied before it returns; callbacks are still sent in the background.
     *
     * @throws ApiError 409 clock_not_manual without a manual clock
     * @throws ClientExceptionInterface
     */
    public function advanceClock(int|float $seconds): ClockState
    {
        return ClockState::fromFields($this->call('POST', '/_sandbox/clock/advance', 'clock', ['seconds' => $seconds]));
    }

    /**
     * Drops all payments, events, deliveries, pending status changes and idempotency keys.
     *
     * With $referencePrefix only the payments whose reference starts with it are dropped,
     * so tests that share one sandbox can run in parallel, each with its own prefix.
     *
     * @throws ApiError
     * @throws ClientExceptionInterface
     */
    public function reset(?string $referencePrefix = null): void
    {
        $this->send('POST', '/_sandbox/reset', $referencePrefix === null ? null : ['reference_prefix' => $referencePrefix]);
    }

    // Wait helpers: callbacks and status changes are asynchronous.

    /**
     * Waits until the payment has at least $count deliveries and none of them is
     * pending, then returns all of them. A delivery is final when it succeeded,
     * used all retries, or was dropped by the scenario.
     *
     * @return list<Delivery>
     *
     * @throws Timeout
     * @throws ApiError
     * @throws ClientExceptionInterface
     */
    public function waitForDeliveries(string $paymentId, int $count = 1, float $timeoutSeconds = 5.0): array
    {
        $deadline = microtime(true) + $timeoutSeconds;
        while (true) {
            $deliveries = $this->deliveries($paymentId);
            $pending = count(array_filter($deliveries, static fn (Delivery $d): bool => !$d->status->isFinal()));
            if (count($deliveries) >= $count && $pending === 0) {
                return $deliveries;
            }
            if (microtime(true) >= $deadline) {
                throw new Timeout(sprintf(
                    'Payment %s has %d deliveries (%d pending) after %.1fs, waited for %d finished.',
                    $paymentId,
                    count($deliveries),
                    $pending,
                    $timeoutSeconds,
                    $count,
                ));
            }
            usleep($this->pollInterval);
        }
    }

    /**
     * Waits until the payment has the given status.
     *
     * @throws Timeout
     * @throws ApiError
     * @throws ClientExceptionInterface
     */
    public function waitForStatus(string $paymentId, PaymentStatus $status, float $timeoutSeconds = 5.0): Payment
    {
        $deadline = microtime(true) + $timeoutSeconds;
        while (true) {
            $payment = $this->payment($paymentId);
            if ($payment->status === $status) {
                return $payment;
            }
            if (microtime(true) >= $deadline) {
                throw new Timeout(sprintf(
                    'Payment %s is %s after %.1fs, waited for %s.',
                    $paymentId,
                    $payment->status->value,
                    $timeoutSeconds,
                    $status->value,
                ));
            }
            usleep($this->pollInterval);
        }
    }

    /**
     * @param array<string, mixed>|null $body
     *
     * @throws ApiError
     * @throws ClientExceptionInterface
     */
    private function call(string $method, string $path, string $what, ?array $body = null): Fields
    {
        return $this->decode($this->send($method, $path, $body), $what);
    }

    /**
     * @param array<string, mixed>|null $body
     * @param array<string, string>     $headers
     *
     * @throws ApiError
     * @throws ClientExceptionInterface
     */
    private function send(string $method, string $path, ?array $body = null, array $headers = []): ResponseInterface
    {
        $request = $this->requests->createRequest($method, $this->baseUrl . $path)
            ->withHeader('Accept', 'application/json');
        if ($body !== null) {
            $request = $request
                ->withHeader('Content-Type', 'application/json')
                ->withBody($this->streams->createStream(json_encode($body, JSON_THROW_ON_ERROR)));
        }
        if ($this->apiKey !== null && str_starts_with($path, '/v1/')) {
            $request = $request->withHeader('Authorization', 'Bearer ' . $this->apiKey);
        }
        foreach ($headers as $name => $value) {
            $request = $request->withHeader($name, $value);
        }

        $response = $this->http->sendRequest($request);
        $status = $response->getStatusCode();
        if ($status >= 200 && $status < 300) {
            return $response;
        }

        $raw = (string) $response->getBody();
        try {
            $error = Fields::decode($raw, 'error')->object('error');
            throw new ApiError($status, $error->string('code'), $error->string('message'));
        } catch (UnexpectedResponse) {
            throw new ApiError($status, 'unknown', sprintf('%s %s: %s', $method, $path, substr($raw, 0, 200)));
        }
    }

    private function decode(ResponseInterface $response, string $what): Fields
    {
        return Fields::decode((string) $response->getBody(), $what);
    }

    private static function replayed(ResponseInterface $response): bool
    {
        return strtolower($response->getHeaderLine('Idempotent-Replayed')) === 'true';
    }
}
