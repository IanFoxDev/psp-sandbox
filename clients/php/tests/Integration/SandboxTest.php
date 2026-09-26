<?php

declare(strict_types=1);

namespace PspSandbox\Tests\Integration;

use PHPUnit\Framework\Attributes\Group;
use PHPUnit\Framework\TestCase;
use PspSandbox\DeliveryStatus;
use PspSandbox\Exception\ApiError;
use PspSandbox\PaymentStatus;
use PspSandbox\Scenario;
use PspSandbox\Testing\InteractsWithSandbox;

/**
 * Runs against a real sandbox when PSP_SANDBOX_URL is set, for example:
 *
 *     PSP_CALLBACK_URL=http://127.0.0.1:1/ PSP_RETRY_SCHEDULE=0s go run ./cmd/psp-sandbox &
 *     PSP_SANDBOX_URL=http://localhost:8090 vendor/bin/phpunit
 *
 * The callback URL points nowhere on purpose: every delivery fails after one attempt,
 * which is enough to check what the sandbox recorded.
 */
#[Group('integration')]
final class SandboxTest extends TestCase
{
    use InteractsWithSandbox;

    protected function setUp(): void
    {
        if (getenv('PSP_SANDBOX_URL') === false) {
            self::markTestSkipped('Set PSP_SANDBOX_URL to run against a sandbox.');
        }
        $this->resetSandbox();
    }

    public function testHappyPath(): void
    {
        $p = $this->sandbox()->createPayment(1000, 'EUR', reference: 'order-1', metadata: ['cart' => '7']);
        self::assertSame(PaymentStatus::Pending, $p->status);
        self::assertSame('happy_path', $p->scenario);

        $captured = $this->waitForPaymentStatus($p->id, PaymentStatus::Captured);
        self::assertSame(1000, $captured->capturedAmount);
        self::assertSame(['cart' => '7'], $captured->metadata);

        $deliveries = $this->waitForDeliveries($p->id);
        self::assertSame('payment.captured', $deliveries[0]->eventType);
        self::assertSame(DeliveryStatus::Failed, $deliveries[0]->status);
        self::assertNotNull($deliveries[0]->attempts[0]->error);
        self::assertArrayHasKey('Webhook-Signature', $deliveries[0]->attempts[0]->requestHeaders);

        $events = $this->sandbox()->events($p->id);
        self::assertSame($deliveries[0]->eventId, $events[0]->id);
        self::assertCount(1, $this->sandbox()->payments('order-1'));
    }

    public function testDuplicateCallbackAndReplay(): void
    {
        $p = $this->sandbox()->createPayment(1000, 'EUR', scenario: Scenario::DuplicateCallback, scenarioParams: ['times' => 3]);
        $deliveries = $this->waitForDeliveries($p->id, 3);
        self::assertSame([1, 2, 3], array_map(static fn ($d) => $d->copy, $deliveries));

        $replay = $this->sandbox()->replay($deliveries[0]->id);
        self::assertSame($deliveries[0]->id, $replay->replayOf);
        self::assertCount(4, $this->waitForDeliveries($p->id, 4));
    }

    public function testIdempotencyManualCaptureRefund(): void
    {
        $c = $this->sandbox();
        $first = $c->createPayment(1000, 'EUR', idempotencyKey: 'k-1', manualCapture: true);
        $again = $c->createPayment(1000, 'EUR', idempotencyKey: 'k-1', manualCapture: true);
        self::assertSame($first->id, $again->id);
        self::assertTrue($again->replayed);

        $this->waitForPaymentStatus($first->id, PaymentStatus::Authorized);
        self::assertSame(PaymentStatus::Captured, $c->capture($first->id, 600)->status);
        $refund = $c->refund($first->id, 200);
        self::assertSame('pending', $refund->status);
        $this->waitForPaymentStatus($first->id, PaymentStatus::PartiallyRefunded);
    }

    public function testForcedEventAndErrors(): void
    {
        $c = $this->sandbox();
        $p = $c->createPayment(1000, 'EUR');
        $this->waitForPaymentStatus($p->id, PaymentStatus::Captured);

        $forced = $c->forceEvent($p->id, 'chargeback.opened');
        self::assertSame(PaymentStatus::Disputed, $forced->payment->status);
        self::assertSame('chargeback.opened', $forced->event->type);

        try {
            $c->cancel($p->id);
            self::fail('cancel of a disputed payment worked');
        } catch (ApiError $e) {
            self::assertSame('invalid_state', $e->errorCode);
        }

        try {
            $c->createPayment(1, 'EUR', scenario: 'no_such_scenario');
            self::fail('unknown scenario accepted');
        } catch (ApiError $e) {
            self::assertSame(400, $e->status);
        }
    }

    public function testClock(): void
    {
        $clock = $this->sandbox()->clock();
        if (!$clock->manual) {
            try {
                $this->sandbox()->advanceClock(60);
                self::fail('advance worked on a real clock');
            } catch (ApiError $e) {
                self::assertSame('clock_not_manual', $e->errorCode);
            }

            return;
        }
        $after = $this->sandbox()->advanceClock(3600);
        self::assertGreaterThanOrEqual(3600, $after->now->getTimestamp() - $clock->now->getTimestamp());
    }
}
