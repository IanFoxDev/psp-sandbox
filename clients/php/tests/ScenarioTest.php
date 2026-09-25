<?php

declare(strict_types=1);

namespace PspSandbox\Tests;

use PHPUnit\Framework\TestCase;
use PspSandbox\Scenario;

final class ScenarioTest extends TestCase
{
    public function testHeaderWithoutParams(): void
    {
        self::assertSame('lost_callback', Scenario::LostCallback->header());
    }

    public function testHeaderWithParams(): void
    {
        self::assertSame(
            'duplicate_callback; times=3; parallel=true',
            Scenario::DuplicateCallback->header(['times' => 3, 'parallel' => true]),
        );
    }
}
