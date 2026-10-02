<?php

// Webhook endpoint for check.php, served by php -S. It verifies each webhook
// with the SDK and appends the result to a file that check.php reads.

declare(strict_types=1);

require __DIR__ . '/vendor/autoload.php';

$log = getenv('HOOK_LOG') ?: '/tmp/psp-compat-hooks.jsonl';
$payload = (string) file_get_contents('php://input');
try {
    $event = \Stripe\Webhook::constructEvent(
        $payload,
        $_SERVER['HTTP_STRIPE_SIGNATURE'] ?? '',
        (string) getenv('WEBHOOK_SECRET'),
    );
    $object = $event->data->object;
    $intent = $object->object === 'payment_intent' ? $object->id : ($object->payment_intent ?? null);
    $line = ['type' => $event->type, 'intent' => $intent];
} catch (\Throwable $e) {
    $line = ['rejected' => $e->getMessage()];
    http_response_code(400);
}
file_put_contents($log, json_encode($line) . "\n", FILE_APPEND | LOCK_EX);
