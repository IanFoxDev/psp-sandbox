<?php

declare(strict_types=1);

namespace PspSandbox;

enum DeliveryStatus: string
{
    /** Waiting for its first attempt or for a retry. */
    case Pending = 'pending';
    /** A 2xx answer was received. */
    case Succeeded = 'succeeded';
    /** Every attempt of the retry schedule failed. */
    case Failed = 'failed';
    /** The scenario never sends it, as in lost_callback. */
    case Dropped = 'dropped';

    public function isFinal(): bool
    {
        return $this !== self::Pending;
    }
}
