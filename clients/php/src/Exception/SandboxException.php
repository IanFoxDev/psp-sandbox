<?php

declare(strict_types=1);

namespace PspSandbox\Exception;

/**
 * Marker for every exception thrown by the client, except PSR-18 network errors,
 * which are passed through as they are (a scenario may close the connection on purpose).
 */
interface SandboxException extends \Throwable
{
}
