<?php

declare(strict_types=1);

namespace PspSandbox\Tests;

use Http\Discovery\ClassDiscovery;
use Nyholm\Psr7\Factory\Psr17Factory;
use PHPUnit\Framework\TestCase;
use PspSandbox\Client;
use PspSandbox\DeliveryStatus;
use PspSandbox\Exception\ApiError;
use PspSandbox\Exception\MissingHttpClient;
use PspSandbox\Exception\Timeout;
use PspSandbox\Exception\UnexpectedResponse;
use PspSandbox\PaymentStatus;
use PspSandbox\Scenario;

final class ClientTest extends TestCase
{
    private FakeHttpClient $http;

    protected function setUp(): void
    {
        $this->http = new FakeHttpClient();
    }

    private function client(?string $apiKey = null): Client
    {
        $factory = new Psr17Factory();

        return new Client('http://sandbox:8090/', $this->http, $factory, $factory, $apiKey, pollInterval: 1_000);
    }

    /**
     * @param array<string, mixed> $extra
     *
     * @return array<string, mixed>
     */
    private static function payment(string $status = 'pending', array $extra = []): array
    {
        return $extra + [
            'id' => 'pay_1',
            'status' => $status,
            'amount' => 1000,
            'captured_amount' => 0,
            'refunded_amount' => 0,
            'currency' => 'EUR',
            'reference' => 'order-1',
            'capture' => 'auto',
            'scenario' => 'happy_path',
            'created_at' => '2026-09-25T10:00:00Z',
            'updated_at' => '2026-09-25T10:00:00.2Z',
        ];
    }

    /**
     * @return array<string, mixed>
     */
    private static function delivery(string $status, int $copy = 1): array
    {
        return [
            'id' => 'dlv_' . $copy,
            'event_id' => 'evt_1',
            'event_type' => 'payment.captured',
            'payment_id' => 'pay_1',
            'url' => 'http://app/callback',
            'copy' => $copy,
            'status' => $status,
            'created_at' => '2026-09-25T10:00:00.2Z',
            'body' => ['id' => 'evt_1', 'type' => 'payment.captured'],
            'attempts' => [[
                'n' => 1,
                'at' => '2026-09-25T10:00:00.2Z',
                'request_headers' => ['Webhook-Id' => 'evt_1'],
                'status_code' => 200,
                'response_body' => 'ok',
                'latency_ms' => 4,
            ]],
        ];
    }

    public function testNamesWhatToInstallWhenNoHttpClientIsFound(): void
    {
        $strategies = iterator_to_array(ClassDiscovery::getStrategies());
        ClassDiscovery::setStrategies([]);
        ClassDiscovery::clearCache();

        try {
            $this->expectException(MissingHttpClient::class);
            $this->expectExceptionMessage('guzzlehttp/guzzle');
            new Client();
        } finally {
            ClassDiscovery::setStrategies($strategies);
        }
    }

    public function testCreatePayment(): void
    {
        $this->http->queue(201, self::payment(extra: ['metadata' => ['order' => '42']]));

        $p = $this->client('key-1')->createPayment(
            1000,
            'EUR',
            reference: 'order-1',
            scenario: Scenario::DuplicateCallback,
            scenarioParams: ['times' => 3, 'parallel' => true],
            idempotencyKey: 'idem-1',
            manualCapture: true,
            callbackUrl: 'http://app/callback',
            metadata: ['order' => '42'],
        );

        $req = $this->http->last();
        self::assertSame('POST', $req->getMethod());
        self::assertSame('http://sandbox:8090/v1/payments', (string) $req->getUri());
        self::assertSame('duplicate_callback; times=3; parallel=true', $req->getHeaderLine('X-Sandbox-Scenario'));
        self::assertSame('idem-1', $req->getHeaderLine('Idempotency-Key'));
        self::assertSame('Bearer key-1', $req->getHeaderLine('Authorization'));
        self::assertSame('application/json', $req->getHeaderLine('Content-Type'));
        self::assertSame([
            'amount' => 1000,
            'currency' => 'EUR',
            'reference' => 'order-1',
            'capture' => 'manual',
            'callback_url' => 'http://app/callback',
            'metadata' => ['order' => '42'],
        ], $this->http->lastBody());

        self::assertSame('pay_1', $p->id);
        self::assertSame(PaymentStatus::Pending, $p->status);
        self::assertSame(['order' => '42'], $p->metadata);
        self::assertSame('2026-09-25T10:00:00.200+00:00', $p->updatedAt->format('Y-m-d\TH:i:s.vP'));
        self::assertFalse($p->replayed);
        self::assertNull($p->failureReason);
    }

    public function testCreatePaymentMinimalAndReplayed(): void
    {
        $this->http->queue(201, self::payment(), ['Idempotent-Replayed' => 'true']);

        $p = $this->client()->createPayment(500, 'USD', scenario: 'declined; reason=expired_card');

        self::assertSame(['amount' => 500, 'currency' => 'USD'], $this->http->lastBody());
        self::assertSame('declined; reason=expired_card', $this->http->last()->getHeaderLine('X-Sandbox-Scenario'));
        self::assertFalse($this->http->last()->hasHeader('Authorization'));
        self::assertFalse($this->http->last()->hasHeader('Idempotency-Key'));
        self::assertTrue($p->replayed);
    }

    public function testRawScenarioWithParamsIsRejected(): void
    {
        $this->expectException(\InvalidArgumentException::class);
        $this->client()->createPayment(1, 'EUR', scenario: 'declined', scenarioParams: ['reason' => 'x']);
    }

    public function testPaymentsCaptureCancelRefund(): void
    {
        $this->http
            ->queue(200, ['data' => [self::payment(), self::payment('captured')]])
            ->queue(200, self::payment('captured', ['captured_amount' => 700]))
            ->queue(200, self::payment('canceled'))
            ->queue(201, [
                'id' => 'ref_1', 'payment_id' => 'pay_1', 'status' => 'pending', 'amount' => 300,
                'currency' => 'EUR', 'created_at' => '2026-09-25T10:00:00Z', 'updated_at' => '2026-09-25T10:00:00Z',
            ]);
        $c = $this->client();

        $list = $c->payments('order 1/2');
        self::assertSame('http://sandbox:8090/v1/payments?reference=order%201%2F2', (string) $this->http->last()->getUri());
        self::assertCount(2, $list);
        self::assertSame(PaymentStatus::Captured, $list[1]->status);

        self::assertSame(700, $c->capture('pay_1', 700)->capturedAmount);
        self::assertSame(['amount' => 700], $this->http->lastBody());

        self::assertSame(PaymentStatus::Canceled, $c->cancel('pay_1')->status);
        self::assertSame('', (string) $this->http->last()->getBody());

        $r = $c->refund('pay_1', 300, 'refund-1', 'idem-r');
        self::assertSame('http://sandbox:8090/v1/payments/pay_1/refunds', (string) $this->http->last()->getUri());
        self::assertSame(['amount' => 300, 'reference' => 'refund-1'], $this->http->lastBody());
        self::assertSame('idem-r', $this->http->last()->getHeaderLine('Idempotency-Key'));
        self::assertSame('ref_1', $r->id);
        self::assertNull($r->reference);
    }

    public function testDeliveriesAndEvents(): void
    {
        $this->http
            ->queue(200, ['data' => [self::delivery('succeeded'), self::delivery('failed', 2)]])
            ->queue(200, ['data' => [['id' => 'evt_1', 'type' => 'payment.captured', 'created_at' => '2026-09-25T10:00:00Z', 'data' => ['id' => 'pay_1']]]]);
        $c = $this->client('key-1');

        $deliveries = $c->deliveries('pay_1');
        self::assertSame('http://sandbox:8090/_sandbox/payments/pay_1/deliveries', (string) $this->http->last()->getUri());
        self::assertFalse($this->http->last()->hasHeader('Authorization'), 'control API gets no API key');
        self::assertSame(DeliveryStatus::Failed, $deliveries[1]->status);
        self::assertSame(2, $deliveries[1]->copy);
        self::assertNull($deliveries[0]->replayOf);
        self::assertSame(['id' => 'evt_1', 'type' => 'payment.captured'], $deliveries[0]->body);
        $a = $deliveries[0]->attempts[0];
        self::assertSame([1, 200, 'ok', 4, null], [$a->n, $a->statusCode, $a->responseBody, $a->latencyMs, $a->error]);
        self::assertSame(['Webhook-Id' => 'evt_1'], $a->requestHeaders);

        $events = $c->events('pay_1');
        self::assertSame('payment.captured', $events[0]->type);
        self::assertSame(['id' => 'pay_1'], $events[0]->data);
    }

    public function testForceReplayClockReset(): void
    {
        $this->http
            ->queue(201, ['payment' => self::payment('disputed'), 'event' => ['id' => 'evt_2', 'type' => 'chargeback.closed', 'created_at' => '2026-09-25T10:00:00Z', 'data' => []]])
            ->queue(202, self::delivery('pending') + ['replay_of' => 'dlv_0'])
            ->queue(200, ['now' => '2026-09-25T11:00:00Z', 'manual' => true])
            ->queue(200, ['now' => '2026-09-25T12:00:00Z', 'manual' => true])
            ->queue(204)
            ->queue(204);
        $c = $this->client();

        $forced = $c->forceEvent('pay_1', 'chargeback.closed', outcome: 'won');
        self::assertSame(['type' => 'chargeback.closed', 'outcome' => 'won'], $this->http->lastBody());
        self::assertSame(PaymentStatus::Disputed, $forced->payment->status);
        self::assertSame('evt_2', $forced->event->id);

        self::assertSame('dlv_0', $c->replay('dlv_1')->replayOf);
        self::assertSame('http://sandbox:8090/_sandbox/deliveries/dlv_1/replay', (string) $this->http->last()->getUri());

        self::assertTrue($c->clock()->manual);
        self::assertSame('12:00', $c->advanceClock(3600)->now->format('H:i'));
        self::assertSame(['seconds' => 3600], $this->http->lastBody());

        $c->reset();
        self::assertSame('http://sandbox:8090/_sandbox/reset', (string) $this->http->last()->getUri());
        self::assertSame('', (string) $this->http->last()->getBody());

        $c->reset('test-42-');
        self::assertSame(['reference_prefix' => 'test-42-'], $this->http->lastBody());
    }

    public function testApiError(): void
    {
        $this->http->queue(409, ['error' => ['code' => 'invalid_state', 'message' => 'payment is not authorized']]);

        try {
            $this->client()->capture('pay_1');
            self::fail('no exception');
        } catch (ApiError $e) {
            self::assertSame(409, $e->status);
            self::assertSame('invalid_state', $e->errorCode);
            self::assertSame('409 invalid_state: payment is not authorized', $e->getMessage());
        }
    }

    public function testErrorWithoutJson(): void
    {
        $this->http->queue(502, '<html>Bad Gateway</html>');

        try {
            $this->client()->payment('pay_1');
            self::fail('no exception');
        } catch (ApiError $e) {
            self::assertSame(502, $e->status);
            self::assertSame('unknown', $e->errorCode);
            self::assertStringContainsString('Bad Gateway', $e->getMessage());
        }
    }

    public function testUnexpectedBody(): void
    {
        $this->http->queue(200, ['id' => 'pay_1', 'status' => 'pending']);

        $this->expectException(UnexpectedResponse::class);
        $this->expectExceptionMessage('payment.amount must be an integer');
        $this->client()->payment('pay_1');
    }

    public function testUnknownStatus(): void
    {
        $this->http->queue(200, self::payment('teleported'));

        $this->expectException(UnexpectedResponse::class);
        $this->client()->payment('pay_1');
    }

    public function testWaitForDeliveries(): void
    {
        $this->http
            ->queue(200, ['data' => []])
            ->queue(200, ['data' => [self::delivery('pending')]])
            ->queue(200, ['data' => [self::delivery('succeeded'), self::delivery('pending', 2)]])
            ->queue(200, ['data' => [self::delivery('succeeded'), self::delivery('dropped', 2)]]);

        $deliveries = $this->client()->waitForDeliveries('pay_1', 2);

        self::assertCount(2, $deliveries);
        self::assertCount(4, $this->http->requests);
    }

    public function testWaitForDeliveriesTimeout(): void
    {
        for ($i = 0; $i < 1000; $i++) {
            $this->http->queue(200, ['data' => [self::delivery('pending')]]);
        }

        $this->expectException(Timeout::class);
        $this->expectExceptionMessage('Payment pay_1 has 1 deliveries (1 pending)');
        $this->client()->waitForDeliveries('pay_1', 1, 0.05);
    }

    public function testWaitForStatus(): void
    {
        $this->http->queue(200, self::payment())->queue(200, self::payment('captured'));

        self::assertSame(PaymentStatus::Captured, $this->client()->waitForStatus('pay_1', PaymentStatus::Captured)->status);
    }
}
