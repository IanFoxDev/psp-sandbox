<?php

return [
    'url' => env('PSP_URL', 'http://localhost:8090'),
    'callback_base_url' => env('PSP_CALLBACK_BASE_URL', 'http://localhost:8000'),
    'webhook_secret' => env('PSP_WEBHOOK_SECRET'),
    // "safe" or "naive". The naive handler is here to show what goes wrong.
    'callback_handler' => env('PSP_CALLBACK_HANDLER', 'safe'),

    // The same shop on Stripe, against a second sandbox in the stripe profile.
    'stripe' => [
        'url' => env('STRIPE_API_BASE', 'http://localhost:8091'),
        'secret_key' => env('STRIPE_SECRET', 'sk_test_example'),
        'webhook_secret' => env('STRIPE_WEBHOOK_SECRET'),
        // Only for the sandbox, see App\Psp\StripeGateway.
        'sandbox_webhook_base' => env('STRIPE_SANDBOX_WEBHOOK_BASE'),
    ],
];
