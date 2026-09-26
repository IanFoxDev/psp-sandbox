<?php

declare(strict_types=1);

namespace PspSandbox\Exception;

/**
 * A wait helper gave up before the sandbox reached the expected state.
 */
final class Timeout extends \RuntimeException implements SandboxException
{
}
