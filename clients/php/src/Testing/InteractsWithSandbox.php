<?php

declare(strict_types=1);

namespace PspSandbox\Testing;

use PHPUnit\Framework\TestCase;
use PspSandbox\Client;
use PspSandbox\Delivery;
use PspSandbox\Exception\Timeout;
use PspSandbox\Payment;
use PspSandbox\PaymentStatus;

/**
 * PHPUnit helpers. The sandbox address comes from the PSP_SANDBOX_URL environment
 * variable (default http://localhost:8090); override sandbox() to build the client
 * differently.
 *
 * @phpstan-require-extends TestCase
 */
trait InteractsWithSandbox
{
    private ?Client $sandboxClient = null;

    protected function sandbox(): Client
    {
        if ($this->sandboxClient === null) {
            $url = getenv('PSP_SANDBOX_URL');
            $this->sandboxClient = new Client(is_string($url) && $url !== '' ? $url : 'http://localhost:8090');
        }

        return $this->sandboxClient;
    }

    protected function resetSandbox(): void
    {
        $this->sandbox()->reset();
    }

    /**
     * Waits until the payment has at least $count finished deliveries, or fails the test.
     *
     * @return list<Delivery>
     */
    protected function waitForDeliveries(string $paymentId, int $count = 1, float $timeoutSeconds = 5.0): array
    {
        try {
            return $this->sandbox()->waitForDeliveries($paymentId, $count, $timeoutSeconds);
        } catch (Timeout $e) {
            self::fail($e->getMessage());
        }
    }

    /**
     * Waits until the payment has the status, or fails the test.
     */
    protected function waitForPaymentStatus(string $paymentId, PaymentStatus $status, float $timeoutSeconds = 5.0): Payment
    {
        try {
            return $this->sandbox()->waitForStatus($paymentId, $status, $timeoutSeconds);
        } catch (Timeout $e) {
            self::fail($e->getMessage());
        }
    }
}
