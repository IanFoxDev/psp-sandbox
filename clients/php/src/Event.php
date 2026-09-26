<?php

declare(strict_types=1);

namespace PspSandbox;

use PspSandbox\Internal\Fields;

/**
 * Something that happened to a payment or refund; the body of a callback.
 */
final readonly class Event
{
    /**
     * @param array<array-key, mixed> $data snapshot of the payment or refund at that moment
     */
    public function __construct(
        public string $id,
        public string $type,
        public \DateTimeImmutable $createdAt,
        public array $data,
    ) {
    }

    /**
     * @internal
     */
    public static function fromFields(Fields $f): self
    {
        return new self($f->string('id'), $f->string('type'), $f->time('created_at'), $f->raw('data'));
    }
}
