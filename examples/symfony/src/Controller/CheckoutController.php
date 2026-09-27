<?php

namespace App\Controller;

use App\Psp\PspGateway;
use Doctrine\DBAL\Connection;
use Symfony\Component\HttpFoundation\JsonResponse;
use Symfony\Component\HttpFoundation\Request;
use Symfony\Component\Routing\Attribute\Route;

final class CheckoutController
{
    #[Route('/orders', methods: ['POST'])]
    public function __invoke(Request $request, Connection $db, PspGateway $psp): JsonResponse
    {
        $input = $request->toArray();
        if (!is_string($input['reference'] ?? null) || !is_int($input['amount'] ?? null) || $input['amount'] < 1
            || !is_string($input['currency'] ?? null) || strlen($input['currency']) !== 3) {
            return new JsonResponse(['error' => 'reference, amount and currency are required'], 422);
        }

        $id = (int) $db->fetchOne(
            'INSERT INTO orders (reference, amount, currency) VALUES (?, ?, ?) RETURNING id',
            [$input['reference'], $input['amount'], $input['currency']],
        );

        // The provider answers after it has already sent the callback in some cases
        // (callback_before_response). Until this line runs, the order has no
        // psp_payment_id, so a callback handler must not rely on it.
        $paymentId = $psp->createPayment(['id' => $id] + $input);
        $db->executeStatement('UPDATE orders SET psp_payment_id = ? WHERE id = ?', [$paymentId, $id]);

        return new JsonResponse($db->fetchAssociative('SELECT * FROM orders WHERE id = ?', [$id]), 201);
    }
}
