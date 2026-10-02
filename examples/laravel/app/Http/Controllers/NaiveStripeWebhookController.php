<?php

namespace App\Http\Controllers;

use App\Models\Order;
use App\Shop\Warehouse;
use Illuminate\Http\Request;
use Illuminate\Http\Response;

/**
 * The same first version as NaiveCallbackController, written for Stripe. Do
 * not copy it.
 *
 * - No Stripe-Signature check: anyone who can reach the URL can mark orders paid.
 * - Looks the order up by the PaymentIntent id, saved only after the create call
 *   returns. A webhook that comes earlier is answered 200 and dropped.
 * - "Already paid?" and the credit are separate steps with a call in between, so
 *   copies of payment_intent.succeeded that arrive together all credit.
 */
class NaiveStripeWebhookController
{
    public function __invoke(Request $request, Warehouse $warehouse): Response
    {
        if ($request->json('type') !== 'payment_intent.succeeded') {
            return response()->noContent();
        }
        $intent = $request->json('data.object');

        $order = Order::where('psp_payment_id', $intent['id'])->first();
        if ($order === null || $order->status === 'paid') {
            return response()->noContent();
        }

        $warehouse->reserve($order->reference);
        Order::whereKey($order->id)->increment('credited_amount', $intent['amount_received'], ['status' => 'paid']);

        return response()->noContent();
    }
}
