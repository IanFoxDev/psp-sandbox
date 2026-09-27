<?php

namespace App\Http\Controllers;

use App\Models\Order;
use App\Shop\Warehouse;
use Illuminate\Http\Request;
use Illuminate\Http\Response;

/**
 * How callback handlers often look in the first version. Do not copy it.
 *
 * - No signature check: anyone who can reach the URL can mark orders paid.
 * - Looks the order up by the provider payment id, which is saved only after the
 *   create call returns. A callback that arrives earlier is ignored with 200, so
 *   the provider never retries and the order stays pending.
 * - "Already paid?" check and the credit are two separate steps with a call to
 *   another service in between. Copies of the same callback that arrive together
 *   all see "pending" and all credit.
 */
class NaiveCallbackController
{
    public function __invoke(Request $request, Warehouse $warehouse): Response
    {
        if ($request->json('type') !== 'payment.captured') {
            return response()->noContent();
        }

        $order = Order::where('psp_payment_id', $request->json('data.id'))->first();
        if ($order === null || $order->status === 'paid') {
            return response()->noContent();
        }

        $warehouse->reserve($order->reference);
        Order::whereKey($order->id)->increment('credited_amount', $request->json('data.captured_amount'), ['status' => 'paid']);

        return response()->noContent();
    }
}
