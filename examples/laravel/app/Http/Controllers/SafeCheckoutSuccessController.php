<?php

namespace App\Http\Controllers;

use App\Models\Order;
use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;

/**
 * The page after Stripe Checkout only shows where the order stands. The order
 * is fulfilled by the checkout.session.completed webhook, which comes whether
 * or not the customer comes back, and at most once per event id.
 */
class SafeCheckoutSuccessController
{
    public function __invoke(Request $request): JsonResponse
    {
        $order = Order::where('psp_payment_id', (string) $request->query('session_id'))->firstOrFail();

        return response()->json(['status' => $order->status]);
    }
}
