<?php

namespace App\Http\Controllers;

use App\Models\Order;
use App\Psp\PspGateway;
use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;

class CheckoutController
{
    public function __invoke(Request $request, PspGateway $psp): JsonResponse
    {
        $input = $request->validate([
            'reference' => ['required', 'string', 'max:64'],
            'amount' => ['required', 'integer', 'min:1'],
            'currency' => ['required', 'string', 'size:3'],
        ]);

        $order = Order::create($input);

        // The provider answers after it has already sent the callback in some cases
        // (callback_before_response). Until this line runs, the order has no
        // psp_payment_id, so a callback handler must not rely on it.
        $order->psp_payment_id = $psp->createPayment($order);
        $order->save();

        return response()->json($order->fresh(), 201);
    }
}
