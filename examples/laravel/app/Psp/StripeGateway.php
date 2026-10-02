<?php

namespace App\Psp;

use App\Models\Order;
use Stripe\StripeClient;

/**
 * The shop's Stripe integration: the official SDK, nothing sandbox-specific
 * except the base URL in config. The sandbox rules file picks the scenario from
 * metadata[reference], the same way it does for the native API.
 */
class StripeGateway
{
    /**
     * Charges the order with a payment method the frontend collected and
     * returns the PaymentIntent id.
     */
    public function pay(Order $order, string $paymentMethod): string
    {
        $stripe = new StripeClient([
            'api_key' => config('psp.stripe.secret_key'),
            'api_base' => config('psp.stripe.url'),
        ]);

        $metadata = ['reference' => $order->reference];
        // Real Stripe sends webhooks to the endpoint set in the dashboard. The
        // sandbox also takes one per payment, which lets the tests point the
        // same sandbox at the naive or the safe handler.
        if ($base = config('psp.stripe.sandbox_webhook_base')) {
            $handler = config('psp.callback_handler') === 'naive' ? 'naive' : 'safe';
            $metadata['sandbox_callback_url'] = rtrim($base, '/').'/stripe/webhook/'.$handler;
        }

        $intent = $stripe->paymentIntents->create([
            'amount' => $order->amount,
            'currency' => strtolower($order->currency),
            'confirm' => true,
            'payment_method' => $paymentMethod,
            'metadata' => $metadata,
        ], ['idempotency_key' => 'order-'.$order->reference]);

        return $intent->id;
    }
}
