<?php

namespace Tests\Feature;

use App\Models\Order;
use Illuminate\Support\Str;
use PspSandbox\Client;
use PspSandbox\Testing\InteractsWithSandbox;
use Tests\TestCase;

/**
 * PspCallbackTest again, with the shop on stripe-php and a second sandbox in the
 * stripe profile. The mistakes and the fixes are the same; only the event names,
 * the signature and the SDK differ.
 *
 * With PSP_CALLBACK_HANDLER=naive every test here fails.
 */
class StripeWebhookTest extends TestCase
{
    use InteractsWithSandbox;

    private ?Client $stripeSandbox = null;

    protected function sandbox(): Client
    {
        return $this->stripeSandbox ??= new Client((string) env('PSP_STRIPE_SANDBOX_URL', 'http://localhost:8091'));
    }

    private function checkout(string $prefix): Order
    {
        $response = $this->postJson('/orders/stripe', [
            'reference' => $prefix.Str::lower(Str::random(10)),
            'amount' => 1000,
            'currency' => 'EUR',
            'payment_method' => 'pm_card_visa',
        ]);
        $response->assertCreated();

        return Order::findOrFail($response->json('id'));
    }

    public function test_duplicate_webhooks_credit_the_order_once(): void
    {
        // duplicate_callback; times=5; parallel=true: payment_intent.created once,
        // then charge.succeeded and payment_intent.succeeded five times each.
        $order = $this->checkout('dup-');

        $this->waitForDeliveries($order->psp_payment_id, 11);

        $order->refresh();
        $this->assertSame('paid', $order->status);
        $this->assertSame(1000, $order->credited_amount, 'the same event was applied more than once');
    }

    public function test_webhook_that_arrives_before_the_confirm_response_is_not_lost(): void
    {
        // callback_before_response: the webhooks are handled, then the SDK call returns.
        $order = $this->checkout('early-');

        $this->waitForDeliveries($order->psp_payment_id, 3);

        $order->refresh();
        $this->assertSame('paid', $order->status, 'the early webhook was dropped');
        $this->assertSame(1000, $order->credited_amount);
    }

    public function test_unsigned_webhook_is_rejected(): void
    {
        $order = Order::create(['reference' => 'forged-'.Str::random(8), 'amount' => 1000, 'currency' => 'EUR']);
        $order->psp_payment_id = 'pi_FORGED'.Str::upper(Str::random(6));
        $order->save();

        $this->postJson('/stripe/webhook/'.config('psp.callback_handler'), [
            'id' => 'evt_forged',
            'object' => 'event',
            'type' => 'payment_intent.succeeded',
            'data' => ['object' => [
                'id' => $order->psp_payment_id, 'object' => 'payment_intent',
                'amount_received' => 1000, 'currency' => 'eur',
                'metadata' => ['reference' => $order->reference],
            ]],
        ])->assertStatus(400);

        $this->assertSame('pending', $order->refresh()->status);
    }
}
