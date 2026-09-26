<?php

declare(strict_types=1);

namespace PspSandbox;

use PspSandbox\Internal\Fields;

final readonly class ClockState
{
    public function __construct(
        public \DateTimeImmutable $now,
        /** The sandbox runs with PSP_CLOCK=manual. */
        public bool $manual,
    ) {
    }

    /**
     * @internal
     */
    public static function fromFields(Fields $f): self
    {
        return new self($f->time('now'), $f->bool('manual'));
    }
}
