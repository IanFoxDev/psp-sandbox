<?php

namespace App\Shop;

/**
 * Stands in for the rest of a real callback handler: a call to the warehouse,
 * loyalty or email service. That is where handlers spend their time, and that
 * time is the window in which parallel duplicates overlap.
 */
final class Warehouse
{
    public const LATENCY_MS = 200;

    public function reserve(string $orderReference): void
    {
        usleep(self::LATENCY_MS * 1000);
    }
}
