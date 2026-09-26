<?php

declare(strict_types=1);

namespace PspSandbox\Exception;

/**
 * The answer does not look like the sandbox API: wrong URL, proxy page, or a client
 * that does not match the server version.
 */
final class UnexpectedResponse extends \RuntimeException implements SandboxException
{
}
