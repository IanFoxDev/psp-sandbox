<?php

namespace App\Http\Controllers;

use App\Models\Order;
use Illuminate\Http\Request;
use Illuminate\Http\Response;
use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Log;
use PspSandbox\Webhook\InvalidSignature;
use PspSandbox\Webhook\Verifier;

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
class SafeCallbackController
{
    public function __invoke(Request $request): Response
    {
        try {
            (new Verifier(config('psp.webhook_secret')))->verify($request->getContent(), $request->headers->all());
        } catch (InvalidSignature $e) {
            Log::warning('psp callback rejected', ['reason' => $e->getMessage()]);

            return response('invalid signature', 400);
        }

        $event = $request->json()->all();
        if ($event['type'] !== 'payment.captured') {
            return response()->noContent();
        }
        $payment = $event['data'];

        DB::transaction(function () use ($event, $payment) {
            $fresh = DB::table('psp_events')->insertOrIgnore([
                'event_id' => $event['id'],
                'type' => $event['type'],
                'received_at' => now(),
            ]);
            if ($fresh === 0) {
                return; // already applied
            }

            $order = Order::where('reference', $payment['reference'])->lockForUpdate()->firstOrFail();
            if ($order->status === 'paid') {
                return;
            }
            if ($payment['captured_amount'] !== $order->amount || $payment['currency'] !== $order->currency) {
                throw new \RuntimeException("Order {$order->reference}: provider captured {$payment['captured_amount']} {$payment['currency']}");
            }

            $order->psp_payment_id ??= $payment['id'];
            $order->credited_amount += $payment['captured_amount'];
            $order->status = 'paid';
            $order->save();
        });

        return response()->noContent();
    }
}
