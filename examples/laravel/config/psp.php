<?php

return [
    'url' => env('PSP_URL', 'http://localhost:8090'),
    'callback_base_url' => env('PSP_CALLBACK_BASE_URL', 'http://localhost:8000'),
    'webhook_secret' => env('PSP_WEBHOOK_SECRET'),
    // "safe" or "naive". The naive handler is here to show what goes wrong.
    'callback_handler' => env('PSP_CALLBACK_HANDLER', 'safe'),
];
