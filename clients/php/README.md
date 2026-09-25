# psp-sandbox-php

PHP client for [psp-sandbox](https://github.com/ianfoxdev/psp-sandbox), a fake payment
provider that fails on demand.

> This repository is a read-only mirror of `clients/php` in the main repository.
> Please open issues and pull requests there.

```bash
composer require --dev ianfoxdev/psp-sandbox-php
```

## Verify callbacks

The sandbox signs callbacks with [Standard Webhooks](https://www.standardwebhooks.com/).
The verifier has no dependencies:

```php
use PspSandbox\Webhook\Verifier;
use PspSandbox\Webhook\InvalidSignature;

$verifier = new Verifier('whsec_dGVzdC1zZWNyZXQ=');

try {
    $verifier->verify($request->getContent(), $request->headers->all());
} catch (InvalidSignature $e) {
    return new Response('', 400);
}
```

## Pick a scenario

```php
use PspSandbox\Scenario;

$header = Scenario::DuplicateCallback->header(['times' => 3, 'parallel' => true]);
// "duplicate_callback; times=3; parallel=true"
```

An HTTP client for the provider and control APIs, and PHPUnit helpers such as
`waitForDeliveries()`, are planned for v0.1. See the main repository for the roadmap.

## License

MIT
