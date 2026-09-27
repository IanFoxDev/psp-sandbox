<?php

namespace App\Controller;

use Doctrine\DBAL\Connection;
use Psr\Log\LoggerInterface;
use PspSandbox\Webhook\InvalidSignature;
use PspSandbox\Webhook\Verifier;
use Symfony\Component\HttpFoundation\Request;
use Symfony\Component\HttpFoundation\Response;
use Symfony\Component\Routing\Attribute\Route;

/**
 * A callback handler that survives duplicates, early callbacks and forged requests.
 *
 * 1. Verify the signature before reading the body.
 * 2. Find the order by our own reference, which exists before the provider is called.
 * 3. In one transaction: record the event id (primary key, so a duplicate is a no-op
 *    even when copies arrive at the same moment), lock the order row, check the amount,
 *    apply the change once.
 * 4. Answer 2xx only after the transaction commits. Anything unexpected is a 5xx,
 *    so the provider retries instead of the event being lost.
 */
final class SafeCallbackController
{
    public function __construct(
        private readonly Connection $db,
        private readonly LoggerInterface $logger,
        private readonly string $webhookSecret,
    ) {
    }

    #[Route('/psp/callback/safe', methods: ['POST'])]
    public function __invoke(Request $request): Response
    {
        try {
            (new Verifier($this->webhookSecret))->verify($request->getContent(), $request->headers->all());
        } catch (InvalidSignature $e) {
            $this->logger->warning('psp callback rejected', ['reason' => $e->getMessage()]);

            return new Response('invalid signature', 400);
        }

        $event = $request->toArray();
        if ($event['type'] !== 'payment.captured') {
            return new Response(null, 204);
        }
        $payment = $event['data'];

        $this->db->transactional(function (Connection $db) use ($event, $payment): void {
            $fresh = $db->executeStatement(
                'INSERT INTO psp_events (event_id, type) VALUES (?, ?) ON CONFLICT (event_id) DO NOTHING',
                [$event['id'], $event['type']],
            );
            if ($fresh === 0) {
                return; // already applied
            }

            $order = $db->fetchAssociative('SELECT * FROM orders WHERE reference = ? FOR UPDATE', [$payment['reference']]);
            if ($order === false) {
                throw new \RuntimeException("Unknown order {$payment['reference']}");
            }
            if ($order['status'] === 'paid') {
                return;
            }
            if ($payment['captured_amount'] !== (int) $order['amount'] || $payment['currency'] !== $order['currency']) {
                throw new \RuntimeException("Order {$order['reference']}: provider captured {$payment['captured_amount']} {$payment['currency']}");
            }

            $db->executeStatement(
                "UPDATE orders SET credited_amount = credited_amount + ?, status = 'paid',
                    psp_payment_id = COALESCE(psp_payment_id, ?) WHERE id = ?",
                [$payment['captured_amount'], $payment['id'], $order['id']],
            );
        });

        return new Response(null, 204);
    }
}
