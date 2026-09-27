<?php

namespace App\Tests;

use Doctrine\DBAL\Connection;
use PspSandbox\Testing\InteractsWithSandbox;
use Symfony\Bundle\FrameworkBundle\KernelBrowser;
use Symfony\Bundle\FrameworkBundle\Test\WebTestCase;

/**
 * Runs the checkout against psp-sandbox. The sandbox picks the scenario from the
 * order reference (see ../scenarios.yaml) and calls back the running app, so the
 * handler under test is the real HTTP endpoint, not a mocked one.
 *
 * With PSP_CALLBACK_HANDLER=naive every test here fails.
 */
final class PspCallbackTest extends WebTestCase
{
    use InteractsWithSandbox;

    private KernelBrowser $browser;
    private Connection $db;

    protected function setUp(): void
    {
        $this->browser = self::createClient();
        $db = self::getContainer()->get(Connection::class);
        self::assertInstanceOf(Connection::class, $db);
        $this->db = $db;
    }

    /**
     * @return array<string, mixed>
     */
    private function checkout(string $prefix, int $amount = 1000): array
    {
        $this->browser->jsonRequest('POST', '/orders', [
            'reference' => $prefix.bin2hex(random_bytes(5)),
            'amount' => $amount,
            'currency' => 'EUR',
        ]);
        self::assertResponseStatusCodeSame(201);

        return json_decode((string) $this->browser->getResponse()->getContent(), true, flags: JSON_THROW_ON_ERROR);
    }

    /**
     * @return array<string, mixed>
     */
    private function order(int $id): array
    {
        $order = $this->db->fetchAssociative('SELECT * FROM orders WHERE id = ?', [$id]);
        self::assertIsArray($order);

        return $order;
    }

    public function testDuplicateCallbacksCreditTheOrderOnce(): void
    {
        // duplicate_callback; times=5; parallel=true: five copies at the same moment.
        $order = $this->checkout('dup-');

        $deliveries = $this->waitForDeliveries($order['psp_payment_id'], 5);
        self::assertCount(5, $deliveries);
        self::assertCount(1, array_unique(array_map(fn ($d) => $d->eventId, $deliveries)), 'copies of one event');

        $order = $this->order($order['id']);
        self::assertSame('paid', $order['status']);
        self::assertSame(1000, (int) $order['credited_amount'], 'the same event was applied more than once');
    }

    public function testCallbackThatArrivesBeforeTheCreateResponseIsNotLost(): void
    {
        // callback_before_response: the callback is delivered, then the create call returns.
        $order = $this->checkout('early-');

        $this->waitForDeliveries($order['psp_payment_id'], 1);

        $order = $this->order($order['id']);
        self::assertSame('paid', $order['status'], 'the early callback was dropped');
        self::assertSame(1000, (int) $order['credited_amount']);
    }

    public function testUnsignedCallbackIsRejected(): void
    {
        $reference = 'forged-'.bin2hex(random_bytes(4));
        $paymentId = 'pay_FORGED'.strtoupper(bin2hex(random_bytes(3)));
        $id = (int) $this->db->fetchOne(
            "INSERT INTO orders (reference, amount, currency, psp_payment_id) VALUES (?, 1000, 'EUR', ?) RETURNING id",
            [$reference, $paymentId],
        );

        $handler = $_SERVER['PSP_CALLBACK_HANDLER'] ?? 'safe';
        $this->browser->jsonRequest('POST', '/psp/callback/'.$handler, [
            'id' => 'evt_forged',
            'type' => 'payment.captured',
            'data' => ['id' => $paymentId, 'reference' => $reference, 'captured_amount' => 1000, 'currency' => 'EUR'],
        ]);
        self::assertResponseStatusCodeSame(400);

        self::assertSame('pending', $this->order($id)['status']);
    }
}
