<?php

// Runs stripe-php against psp-sandbox in the stripe profile: the calls a
// backend makes, and the webhooks it gets, verified by the SDK in hook.php.
// See compat/README.md.

declare(strict_types=1);

require __DIR__ . '/vendor/autoload.php';

use Stripe\Exception\ApiErrorException;
use Stripe\Exception\CardException;
use Stripe\StripeClient;

$base = (string) getenv('PSP_URL');
$hookUrl = (string) getenv('HOOK_URL');
$log = getenv('HOOK_LOG') ?: '/tmp/psp-compat-hooks.jsonl';

function client(string $base, int $retries, int $timeout = 30): StripeClient
{
    $http = new \Stripe\HttpClient\CurlClient();
    $http->setTimeout($timeout);
    \Stripe\ApiRequestor::setHttpClient($http);

    return new StripeClient(['api_key' => 'sk_test_compat', 'api_base' => $base, 'max_network_retries' => $retries]);
}

$failed = false;
function check(string $name, callable $f): void
{
    global $failed;
    try {
        $f();
        echo "ok   $name\n";
    } catch (\Throwable $e) {
        $failed = true;
        echo "FAIL $name: " . $e->getMessage() . "\n";
    }
}
function expect(bool $cond, string $what): void
{
    if (!$cond) {
        throw new \RuntimeException($what);
    }
}
$params = fn (string $pm, array $metadata = []): array => [
    'amount' => 1000,
    'currency' => 'eur',
    'confirm' => true,
    'payment_method' => $pm,
    'metadata' => ['sandbox_callback_url' => $hookUrl] + $metadata,
];

// Start from an empty sandbox, so the counts below hold on reruns.
$ctx = stream_context_create(['http' => ['method' => 'POST', 'ignore_errors' => true]]);
file_get_contents($base . '/_sandbox/reset', false, $ctx);
if (!str_contains($http_response_header[0] ?? '', '204')) {
    echo 'FAIL reset the sandbox: ' . ($http_response_header[0] ?? 'no answer') . "\n";
    exit(1);
}

// Some calls in stripe-php 22.0.0 read the global \Stripe\Stripe::$apiBase
// instead of the client's api_base (see the paging check). Point it nowhere
// first, so that no request from this script can reach the real Stripe.
\Stripe\Stripe::$apiBase = 'http://127.0.0.1:9';

// stripe-php does not retry by default (max_network_retries = 0).
$stripe = client($base, 0);
$paid = null;
$declined = null;

check('card payment succeeds', function () use ($stripe, $params, &$paid) {
    $paid = $stripe->paymentIntents->create($params('pm_card_visa'));
    expect($paid->status === 'succeeded', "status $paid->status");
});
check('decline is a card error with the intent', function () use ($stripe, $params, &$declined) {
    try {
        $stripe->paymentIntents->create($params('pm_card_visa_chargeDeclinedInsufficientFunds'));
    } catch (CardException $e) {
        expect($e->getStripeCode() === 'card_declined' && $e->getDeclineCode() === 'insufficient_funds',
            'got ' . $e->getStripeCode() . ' ' . $e->getDeclineCode());
        $declined = $e->getError()->payment_intent;
        expect($declined !== null && $declined->id !== null, 'no payment_intent in the error');
        return;
    }
    throw new \RuntimeException('no error');
});
check('retry after decline with another card', function () use ($stripe, &$declined) {
    $pi = $stripe->paymentIntents->confirm($declined->id, ['payment_method' => 'pm_card_visa']);
    expect($pi->status === 'succeeded', "status $pi->status");
});
check('manual capture of part of the amount', function () use ($stripe, $params) {
    $pi = $stripe->paymentIntents->create($params('pm_card_visa') + ['capture_method' => 'manual']);
    expect($pi->status === 'requires_capture', "authorize: $pi->status");
    $pi = $stripe->paymentIntents->capture($pi->id, ['amount_to_capture' => 600]);
    expect($pi->status === 'succeeded' && $pi->amount_received === 600, "status $pi->status, received $pi->amount_received");
});
check('partial refund', function () use ($stripe, &$paid) {
    $r = $stripe->refunds->create(['payment_intent' => $paid->id, 'amount' => 300]);
    expect($r->status === 'succeeded', "status $r->status");
});
check('server error reaches a client without retries', function () use ($stripe, $params) {
    try {
        $stripe->paymentIntents->create($params('pm_card_visa', ['sandbox_scenario' => 'server_error_then_success', 'order' => 'no-retry']));
    } catch (ApiErrorException $e) {
        expect($e->getHttpStatus() === 503, 'status ' . $e->getHttpStatus());
        return;
    }
    throw new \RuntimeException('no error');
});
check('SDK retry gets through server_error_then_success', function () use ($base, $params) {
    $pi = client($base, 2)->paymentIntents->create($params('pm_card_visa', ['sandbox_scenario' => 'server_error_then_success', 'order' => 'retry']));
    expect($pi->status === 'succeeded', "status $pi->status");
});
// stripe-php adds an Idempotency-Key to a POST only when the global
// Stripe::$maxNetworkRetries is above 0; max_network_retries on the client
// retries without one. After a timeout each retry is a new payment. The
// check pins that; if a new stripe-php sends the key, docs/stripe.md needs
// updating.
check('client-level retries after a timeout create new payments', function () use ($base, $params) {
    try {
        client($base, 2, 1)->paymentIntents->create($params('pm_card_visa', ['sandbox_scenario' => 'timeout_then_success; delay=3s', 'order' => 'timeout-1']));
    } catch (ApiErrorException $e) {
        expect(str_contains($e->getMessage(), 'retried 2 times'), $e->getMessage());
        return;
    }
    throw new \RuntimeException('the retry got through: stripe-php now sends the key, update docs/stripe.md');
});
check('with the global retry setting the retry gets the stored answer', function () use ($base, $params) {
    \Stripe\Stripe::setMaxNetworkRetries(2);
    try {
        $pi = client($base, 2, 1)->paymentIntents->create($params('pm_card_visa', ['sandbox_scenario' => 'timeout_then_success; delay=3s', 'order' => 'timeout-2']));
    } finally {
        \Stripe\Stripe::setMaxNetworkRetries(0);
    }
    expect($pi->status === 'succeeded', "status $pi->status");
});
// nextPage() and autoPagingIterator() send the request for the next page to
// the global \Stripe\Stripe::$apiBase, not to the client's api_base.
check('paging goes to the global api base', function () use ($stripe) {
    try {
        iterator_to_array($stripe->paymentIntents->all(['limit' => 2])->autoPagingIterator(), false);
    } catch (ApiErrorException $e) {
        expect(str_contains($e->getMessage(), '127.0.0.1:9'), $e->getMessage());
        return;
    }
    throw new \RuntimeException('paging used api_base: update docs/stripe.md');
});
check('list pages through all intents with the global api base set', function () use ($base, $stripe) {
    \Stripe\Stripe::$apiBase = $base;
    try {
        $seen = [];
        foreach ($stripe->paymentIntents->all(['limit' => 2])->autoPagingIterator() as $pi) {
            expect(!isset($seen[$pi->id]), "$pi->id twice");
            $seen[$pi->id] = true;
        }
    } finally {
        \Stripe\Stripe::$apiBase = 'http://127.0.0.1:9';
    }
    // paid, declined, manual, the retried one, three from the retries
    // without a key and one from the retry with it.
    expect(count($seen) === 8, count($seen) . ' intents, want 8');
});
check('webhooks verify with \Stripe\Webhook::constructEvent', function () use ($log, &$paid, &$declined) {
    $want = [
        ['payment_intent.succeeded', $paid->id], ['charge.refunded', $paid->id],
        ['payment_intent.payment_failed', $declined->id], ['payment_intent.succeeded', $declined->id],
    ];
    $deadline = microtime(true) + 10;
    foreach ($want as [$type, $intent]) {
        while (true) {
            $lines = array_map(fn ($l) => json_decode($l, true), file($log, FILE_IGNORE_NEW_LINES) ?: []);
            $rejected = array_filter($lines, fn ($l) => isset($l['rejected']));
            expect($rejected === [], 'rejected: ' . json_encode(array_values($rejected)));
            if (array_filter($lines, fn ($l) => ($l['type'] ?? '') === $type && ($l['intent'] ?? '') === $intent) !== []) {
                break;
            }
            expect(microtime(true) < $deadline, "no $type for $intent");
            usleep(50_000);
        }
    }
});
check('events retrieve', function () use ($stripe) {
    $list = $stripe->events->all(['type' => 'charge.refunded', 'limit' => 1]);
    expect(count($list->data) === 1, 'no charge.refunded event');
    $ev = $stripe->events->retrieve($list->data[0]->id);
    expect($ev->type === 'charge.refunded', "type $ev->type");
});

// 3DS and Checkout. They come after the list check, which counts intents.
function control(string $base, string $path, array $body): void
{
    $ctx = stream_context_create(['http' => ['method' => 'POST', 'ignore_errors' => true,
        'header' => "Content-Type: application/json\r\n", 'content' => json_encode($body)]]);
    $answer = file_get_contents($base . $path, false, $ctx);
    expect(str_contains($http_response_header[0] ?? '', '200'), $path . ': ' . ($http_response_header[0] ?? '') . ' ' . $answer);
}
$threeDS = null;
check('3DS card stops in requires_action with a redirect', function () use ($stripe, $params, &$threeDS) {
    $threeDS = $stripe->paymentIntents->create($params('pm_card_threeDSecure2Required') + ['return_url' => 'https://shop.test/return']);
    $na = $threeDS->next_action;
    expect($threeDS->status === 'requires_action' && $na !== null && $na->type === 'redirect_to_url'
        && $na->redirect_to_url->url !== '' && $na->redirect_to_url->return_url === 'https://shop.test/return',
        "got $threeDS->status");
});
check('authentication completes the payment', function () use ($stripe, $base, &$threeDS) {
    control($base, '/_sandbox/payments/' . $threeDS->id . '/authenticate', ['result' => 'success']);
    $pi = $stripe->paymentIntents->retrieve($threeDS->id);
    expect($pi->status === 'succeeded', "status $pi->status");
});
$sessionParams = fn (): array => [
    'mode' => 'payment',
    'success_url' => 'https://shop.test/done?session={CHECKOUT_SESSION_ID}',
    'line_items' => [['price_data' => ['currency' => 'eur', 'unit_amount' => 700, 'product_data' => ['name' => 'Tea']], 'quantity' => 2]],
    'payment_intent_data' => ['metadata' => ['sandbox_callback_url' => $hookUrl]],
];
$session = null;
check('checkout session completes when the customer pays', function () use ($stripe, $base, $sessionParams, &$session) {
    $session = $stripe->checkout->sessions->create($sessionParams());
    expect($session->status === 'open' && $session->url !== null && $session->amount_total === 1400, "created $session->status");
    control($base, '/_sandbox/checkout/' . $session->id . '/pay', ['payment_method' => 'pm_card_visa']);
    $session = $stripe->checkout->sessions->retrieve($session->id);
    expect($session->status === 'complete' && $session->payment_status === 'paid' && $session->payment_intent !== null,
        "after pay $session->status $session->payment_status");
});
check('checkout session expires', function () use ($stripe, $sessionParams) {
    $cs = $stripe->checkout->sessions->create($sessionParams());
    $expired = $stripe->checkout->sessions->expire($cs->id);
    expect($expired->status === 'expired', "status $expired->status");
});
check('3DS and checkout webhooks verify', function () use ($log, &$threeDS, &$session) {
    $want = [['payment_intent.requires_action', $threeDS->id], ['checkout.session.completed', $session->payment_intent]];
    $deadline = microtime(true) + 10;
    foreach ($want as [$type, $intent]) {
        while (true) {
            $lines = array_map(fn ($l) => json_decode($l, true), file($log, FILE_IGNORE_NEW_LINES) ?: []);
            $rejected = array_filter($lines, fn ($l) => isset($l['rejected']));
            expect($rejected === [], 'rejected: ' . json_encode(array_values($rejected)));
            if (array_filter($lines, fn ($l) => ($l['type'] ?? '') === $type && ($l['intent'] ?? '') === $intent) !== []) {
                break;
            }
            expect(microtime(true) < $deadline, "no $type for $intent");
            usleep(50_000);
        }
    }
});

exit($failed ? 1 : 0);
