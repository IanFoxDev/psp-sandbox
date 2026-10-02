<?php

namespace App\Http\Controllers;

use App\Models\Order;
use App\Psp\StripeGateway;
use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;

class StripeCheckoutController
{
    public function __invoke(Request $request, StripeGateway $stripe): JsonResponse
    {
        $input = $request->validate([
            'reference' => ['required', 'string', 'max:64'],
            'amount' => ['required', 'integer', 'min:1'],
            'currency' => ['required', 'string', 'size:3'],
            // Collected by Stripe.js in the browser; tests pass a test card such as pm_card_visa.
            'payment_method' => ['required', 'string'],
        ]);

        $order = Order::create($input);

        // As with the native API, the webhooks may come before this call returns
        // (callback_before_response): the order has no psp_payment_id until then.
        $order->psp_payment_id = $stripe->pay($order, $input['payment_method']);
        $order->save();

        return response()->json($order->fresh(), 201);
    }
}
