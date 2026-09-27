<?php

namespace App\Psp;

use App\Models\Order;
use Illuminate\Support\Facades\Http;

/**
 * The shop's own client for the provider. Nothing here knows about the sandbox:
 * the scenario is picked by the sandbox rules file from the order reference.
 */
class PspGateway
{
    /**
     * Starts a payment and returns the provider payment id.
     */
    public function createPayment(Order $order): string
    {
        $handler = config('psp.callback_handler') === 'naive' ? 'naive' : 'safe';

        $response = Http::baseUrl(config('psp.url'))
            ->withHeaders(['Idempotency-Key' => 'order-'.$order->reference])
            ->timeout(60)
            ->post('/v1/payments', [
                'amount' => $order->amount,
                'currency' => $order->currency,
                'reference' => $order->reference,
                'callback_url' => rtrim(config('psp.callback_base_url'), '/').'/psp/callback/'.$handler,
            ])
            ->throw();

        return $response->json('id');
    }
}
