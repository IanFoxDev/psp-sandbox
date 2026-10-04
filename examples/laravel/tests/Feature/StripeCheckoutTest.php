<?php

namespace Tests\Feature;

use App\Models\Order;
use Illuminate\Support\Str;
use PspSandbox\Client;
use PspSandbox\Testing\InteractsWithSandbox;
use Tests\TestCase;

/**
 * Stripe Checkout against the sandbox in the stripe profile. The order must be
 * fulfilled by the checkout.session.completed webhook, once, whatever the
 * customer's browser does after paying.
 *
 * With PSP_CALLBACK_HANDLER=naive both tests fail.
 */
class StripeCheckoutTest extends TestCase
{
    use InteractsWithSandbox;

    private ?Client $stripeSandbox = null;

    protected function sandbox(): Client
    {
        return $this->stripeSandbox ??= new Client((string) env('PSP_STRIPE_SANDBOX_URL', 'http://localhost:8091'));
    }

    private function checkout(string $prefix): Order
    {
        $response = $this->postJson('/orders/stripe-checkout', [
            'reference' => $prefix.Str::lower(Str::random(10)),
            'amount' => 1000,
            'currency' => 'EUR',
        ]);
        $response->assertCreated();
        $this->assertStringContainsString('/_sandbox/ui/checkout/', $response->json('checkout_url'));

        return Order::findOrFail($response->json('order.id'));
    }

    public function test_customer_who_closes_the_tab_after_paying_still_gets_the_order(): void
    {
        // dup-: every webhook comes five times at once, checkout.session.completed too.
        $order = $this->checkout('dup-');

        // The customer pays on the hosted page and closes the tab:
        // success_url is never visited.
        $intent = $this->sandbox()->payCheckout($order->psp_payment_id);
        // payment_intent.created, then charge.succeeded, payment_intent.succeeded
        // and checkout.session.completed five times each.
        $this->waitForDeliveries($intent, 16);

        $order->refresh();
        $this->assertSame('paid', $order->status, 'the order waits for a customer who is gone');
        $this->assertSame(1000, $order->credited_amount, 'the same event was applied more than once');
    }

    public function test_reloading_the_success_page_does_not_fulfil_the_order_again(): void
    {
        $order = $this->checkout('reload-');
        $intent = $this->sandbox()->payCheckout($order->psp_payment_id);
        $this->waitForDeliveries($intent, 4);

        $page = '/checkout/success/'.config('psp.callback_handler').'?session_id='.$order->psp_payment_id;
        $this->getJson($page)->assertOk();
        $this->getJson($page)->assertOk(); // reload

        $order->refresh();
        $this->assertSame('paid', $order->status);
        $this->assertSame(1000, $order->credited_amount, 'the page fulfilled the order on every visit');
    }
}
