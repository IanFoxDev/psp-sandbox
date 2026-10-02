<?php

namespace App\Http\Controllers;

use App\Models\Order;
use App\Shop\Warehouse;
use Illuminate\Http\Request;
use Illuminate\Http\Response;
use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Log;
use Stripe\Exception\SignatureVerificationException;
use Stripe\Webhook;

/**
 * SafeCallbackController, for Stripe webhooks. The same five rules: verify the
 * signature with the SDK, find the order by our own reference (metadata), record
 * the event id and lock the order in one transaction, call other services after
 * the commit, answer 2xx only when done.
 */
class SafeStripeWebhookController
{
    public function __invoke(Request $request, Warehouse $warehouse): Response
    {
        try {
            $event = Webhook::constructEvent(
                $request->getContent(),
                (string) $request->header('Stripe-Signature'),
                (string) config('psp.stripe.webhook_secret'),
            );
        } catch (SignatureVerificationException|\UnexpectedValueException $e) {
            Log::warning('stripe webhook rejected', ['reason' => $e->getMessage()]);

            return response('invalid signature', 400);
        }

        if ($event->type !== 'payment_intent.succeeded') {
            return response()->noContent();
        }
        $intent = $event->data->object;
        $reference = $intent->metadata['reference'] ?? null;
        if ($reference === null) {
            return response()->noContent(); // not a payment of this shop
        }

        $applied = DB::transaction(function () use ($event, $intent, $reference): bool {
            $fresh = DB::table('psp_events')->insertOrIgnore([
                'event_id' => $event->id,
                'type' => $event->type,
                'received_at' => now(),
            ]);
            if ($fresh === 0) {
                return false; // already applied
            }

            $order = Order::where('reference', $reference)->lockForUpdate()->firstOrFail();
            if ($order->status === 'paid') {
                return false;
            }
            if ($intent->amount_received !== $order->amount || $intent->currency !== strtolower($order->currency)) {
                throw new \RuntimeException("Order {$order->reference}: Stripe received {$intent->amount_received} {$intent->currency}");
            }

            $order->psp_payment_id ??= $intent->id;
            $order->credited_amount += $intent->amount_received;
            $order->status = 'paid';
            $order->save();

            return true;
        });

        if ($applied) {
            $warehouse->reserve($reference);
        }

        return response()->noContent();
    }
}
