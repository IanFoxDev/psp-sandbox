<?php

declare(strict_types=1);

namespace PspSandbox\Exception;

/**
 * No PSR-18 client or PSR-17 factories were passed and none could be discovered.
 */
final class MissingHttpClient extends \LogicException implements SandboxException
{
    public static function because(\Throwable $previous): self
    {
        return new self(
            'No PSR-18 HTTP client or PSR-17 factories found. Install guzzlehttp/guzzle, or '
            . 'symfony/http-client together with nyholm/psr7, or pass them to the Client constructor.',
            0,
            $previous,
        );
    }
}
