<?php

declare(strict_types=1);

namespace PspSandbox\Exception;

/**
 * The sandbox answered with an error: {"error": {"code": ..., "message": ...}}.
 */
final class ApiError extends \RuntimeException implements SandboxException
{
    public function __construct(
        public readonly int $status,
        public readonly string $errorCode,
        string $message,
    ) {
        parent::__construct(sprintf('%d %s: %s', $status, $errorCode, $message), $status);
    }
}
