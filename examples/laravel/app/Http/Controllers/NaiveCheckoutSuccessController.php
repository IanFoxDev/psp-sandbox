<?php

namespace App\Http\Controllers;

use App\Models\Order;
use App\Shop\Warehouse;
use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use Stripe\StripeClient;

/**
 * How the first version of a Stripe Checkout integration often fulfils the
 * order: on the page the customer lands on after paying. Do not copy it.
 *
 * - A customer who pays and closes the tab never lands here, so the order stays
 *   pending although the money was taken. The webhook that says so is ignored.
 * - The page is not an event: a reload, the back button or a shared link runs
 *   the fulfilment again.
 */
class NaiveCheckoutSuccessController
{
    public function __invoke(Request $request, Warehouse $warehouse): JsonResponse
    {
        $stripe = new StripeClient([
            'api_key' => config('psp.stripe.secret_key'),
            'api_base' => config('psp.stripe.url'),
        ]);
        $session = $stripe->checkout->sessions->retrieve((string) $request->query('session_id'));
        $order = Order::where('reference', $session->client_reference_id)->firstOrFail();

        if ($session->payment_status === 'paid') {
            $warehouse->reserve($order->reference);
            Order::whereKey($order->id)->increment('credited_amount', $session->amount_total, ['status' => 'paid']);
        }

        return response()->json(['status' => $order->fresh()->status]);
    }
}
