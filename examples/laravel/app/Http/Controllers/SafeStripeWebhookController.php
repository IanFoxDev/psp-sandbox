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
 * SafeCallbackController, for Stripe webhooks: payment_intent.succeeded for
 * direct payments and checkout.session.completed for Stripe Checkout. The same five rules: verify the
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

        // Direct payments carry the order in the PaymentIntent's metadata,
        // Checkout payments in the session's client_reference_id.
        $object = $event->data->object;
        [$reference, $amount, $currency] = match ($event->type) {
            'payment_intent.succeeded' => [$object->metadata['reference'] ?? null, $object->amount_received, $object->currency],
            'checkout.session.completed' => $object->payment_status === 'paid'
                ? [$object->client_reference_id, $object->amount_total, $object->currency]
                : [null, 0, ''],
            default => [null, 0, ''],
        };
        if ($reference === null) {
            return response()->noContent(); // not an event that pays an order of this shop
        }

        $applied = DB::transaction(function () use ($event, $object, $reference, $amount, $currency): bool {
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
            if ($amount !== $order->amount || $currency !== strtolower($order->currency)) {
                throw new \RuntimeException("Order {$order->reference}: Stripe received {$amount} {$currency}");
            }

            $order->psp_payment_id ??= $object->id;
            $order->credited_amount += $amount;
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
