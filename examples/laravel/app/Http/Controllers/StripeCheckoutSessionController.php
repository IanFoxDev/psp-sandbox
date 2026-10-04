<?php

namespace App\Http\Controllers;

use App\Models\Order;
use App\Psp\StripeGateway;
use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;

/**
 * Starts Stripe Checkout: the browser goes to the returned URL, pays there and
 * comes back to success_url. Or does not come back at all.
 */
class StripeCheckoutSessionController
{
    public function __invoke(Request $request, StripeGateway $stripe): JsonResponse
    {
        $input = $request->validate([
            'reference' => ['required', 'string', 'max:64'],
            'amount' => ['required', 'integer', 'min:1'],
            'currency' => ['required', 'string', 'size:3'],
        ]);

        $order = Order::create($input);
        $session = $stripe->checkout($order);
        $order->psp_payment_id = $session['id'];
        $order->save();

        return response()->json(['order' => $order->fresh(), 'checkout_url' => $session['url']], 201);
    }
}
