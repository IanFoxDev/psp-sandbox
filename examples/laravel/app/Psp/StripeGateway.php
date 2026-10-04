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

    /**
     * Starts Stripe Checkout for the order and returns the session id and the
     * URL of the hosted page.
     *
     * @return array{id: string, url: string}
     */
    public function checkout(Order $order): array
    {
        $stripe = new StripeClient([
            'api_key' => config('psp.stripe.secret_key'),
            'api_base' => config('psp.stripe.url'),
        ]);
        $handler = config('psp.callback_handler') === 'naive' ? 'naive' : 'safe';

        $params = [
            'mode' => 'payment',
            'client_reference_id' => $order->reference,
            'line_items' => [[
                'price_data' => [
                    'currency' => strtolower($order->currency),
                    'unit_amount' => $order->amount,
                    'product_data' => ['name' => 'Order '.$order->reference],
                ],
                'quantity' => 1,
            ]],
            'success_url' => rtrim(config('psp.callback_base_url'), '/').'/checkout/success/'.$handler
                .'?session_id={CHECKOUT_SESSION_ID}',
        ];
        // See pay(): only for the sandbox.
        if ($base = config('psp.stripe.sandbox_webhook_base')) {
            $params['payment_intent_data'] = ['metadata' => [
                'sandbox_callback_url' => rtrim($base, '/').'/stripe/webhook/'.$handler,
            ]];
        }

        $session = $stripe->checkout->sessions->create($params, ['idempotency_key' => 'checkout-'.$order->reference]);

        return ['id' => $session->id, 'url' => (string) $session->url];
    }
}
