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

    public function testMetadataForTheStripeProfile(): void
    {
        self::assertSame(
            ['sandbox_scenario' => 'declined; reason=expired_card'],
            Scenario::Declined->metadata(['reason' => 'expired_card']),
        );
        self::assertSame(['sandbox_scenario' => 'happy_path'], Scenario::HappyPath->metadata());
    }
}
