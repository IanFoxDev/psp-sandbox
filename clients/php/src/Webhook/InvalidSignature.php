<?php

declare(strict_types=1);

namespace PspSandbox\Webhook;

use PspSandbox\Exception\SandboxException;

/**
 * The callback is not signed by the sandbox: wrong secret, changed body, missing
 * header, or a timestamp outside the tolerance window. Answer it with 4xx.
 */
final class InvalidSignature extends \RuntimeException implements SandboxException
{
}
