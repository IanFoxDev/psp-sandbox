<?php

namespace App\Controller;

use Doctrine\DBAL\Connection;
use Symfony\Component\HttpFoundation\Request;
use Symfony\Component\HttpFoundation\Response;
use Symfony\Component\Routing\Attribute\Route;

/**
 * How callback handlers often look in the first version. Do not copy it.
 *
 * - No signature check: anyone who can reach the URL can mark orders paid.
 * - Looks the order up by the provider payment id, which is saved only after the
 *   create call returns. A callback that arrives earlier is ignored with 204, so
 *   the provider never retries and the order stays pending.
 * - "Already paid?" check and the credit are two separate steps. Two copies of
 *   the same callback that arrive together both see "pending" and both credit.
 */
final class NaiveCallbackController
{
    #[Route('/psp/callback/naive', methods: ['POST'])]
    public function __invoke(Request $request, Connection $db): Response
    {
        $event = $request->toArray();
        if ($event['type'] !== 'payment.captured') {
            return new Response(null, 204);
        }

        $order = $db->fetchAssociative('SELECT * FROM orders WHERE psp_payment_id = ?', [$event['data']['id']]);
        if ($order === false || $order['status'] === 'paid') {
            return new Response(null, 204);
        }

        $db->executeStatement(
            "UPDATE orders SET credited_amount = credited_amount + ?, status = 'paid' WHERE id = ?",
            [$event['data']['captured_amount'], $order['id']],
        );

        return new Response(null, 204);
    }
}
