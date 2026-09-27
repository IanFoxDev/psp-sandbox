<?php

namespace Tests\Feature;

use App\Models\Order;
use Illuminate\Support\Str;
use PspSandbox\Testing\InteractsWithSandbox;
use Tests\TestCase;

/**
 * Runs the checkout against psp-sandbox. The sandbox picks the scenario from the
 * order reference (see ../scenarios.yaml) and calls back the running app, so the
 * handler under test is the real HTTP endpoint, not a mocked one.
 *
 * With PSP_CALLBACK_HANDLER=naive every test here fails.
 */
class PspCallbackTest extends TestCase
{
    use InteractsWithSandbox;

    private function checkout(string $prefix, int $amount = 1000): Order
    {
        $response = $this->postJson('/orders', [
            'reference' => $prefix.Str::lower(Str::random(10)),
            'amount' => $amount,
            'currency' => 'EUR',
        ]);
        $response->assertCreated();

        return Order::findOrFail($response->json('id'));
    }

    public function test_duplicate_callbacks_credit_the_order_once(): void
    {
        // duplicate_callback; times=5; parallel=true: five copies at the same moment.
        $order = $this->checkout('dup-');

        $deliveries = $this->waitForDeliveries($order->psp_payment_id, 5);
        $this->assertCount(5, $deliveries);
        $this->assertCount(1, array_unique(array_map(fn ($d) => $d->eventId, $deliveries)), 'copies of one event');

        $order->refresh();
        $this->assertSame('paid', $order->status);
        $this->assertSame(1000, $order->credited_amount, 'the same event was applied more than once');
    }

    public function test_callback_that_arrives_before_the_create_response_is_not_lost(): void
    {
        // callback_before_response: the callback is delivered, then the create call returns.
        $order = $this->checkout('early-');

        $this->waitForDeliveries($order->psp_payment_id, 1);

        $order->refresh();
        $this->assertSame('paid', $order->status, 'the early callback was dropped');
        $this->assertSame(1000, $order->credited_amount);
    }

    public function test_unsigned_callback_is_rejected(): void
    {
        $order = Order::create(['reference' => 'forged-'.Str::random(8), 'amount' => 1000, 'currency' => 'EUR']);
        $order->psp_payment_id = 'pay_FORGED'.Str::upper(Str::random(6));
        $order->save();

        $this->postJson('/psp/callback/'.config('psp.callback_handler'), [
            'id' => 'evt_forged',
            'type' => 'payment.captured',
            'data' => [
                'id' => $order->psp_payment_id, 'reference' => $order->reference,
                'captured_amount' => 1000, 'currency' => 'EUR',
            ],
        ])->assertStatus(400);

        $this->assertSame('pending', $order->refresh()->status);
    }
}
