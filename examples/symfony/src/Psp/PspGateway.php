<?php

namespace App\Psp;

use Symfony\Contracts\HttpClient\HttpClientInterface;

/**
 * The shop's own client for the provider. Nothing here knows about the sandbox:
 * the scenario is picked by the sandbox rules file from the order reference.
 */
final class PspGateway
{
    public function __construct(
        private readonly HttpClientInterface $http,
        private readonly string $pspUrl,
        private readonly string $callbackBaseUrl,
        private readonly string $callbackHandler,
    ) {
    }

    /**
     * Starts a payment and returns the provider payment id.
     *
     * @param array{id: int, reference: string, amount: int, currency: string} $order
     */
    public function createPayment(array $order): string
    {
        $handler = $this->callbackHandler === 'naive' ? 'naive' : 'safe';

        $response = $this->http->request('POST', rtrim($this->pspUrl, '/').'/v1/payments', [
            'headers' => ['Idempotency-Key' => 'order-'.$order['reference']],
            'timeout' => 60,
            'json' => [
                'amount' => $order['amount'],
                'currency' => $order['currency'],
                'reference' => $order['reference'],
                'callback_url' => rtrim($this->callbackBaseUrl, '/').'/psp/callback/'.$handler,
            ],
        ]);

        return $response->toArray()['id'];
    }
}
