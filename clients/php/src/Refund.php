<?php

declare(strict_types=1);

namespace PspSandbox;

use PspSandbox\Internal\Fields;

final readonly class Refund
{
    public function __construct(
        public string $id,
        public string $paymentId,
        /** pending, succeeded or failed. */
        public string $status,
        public int $amount,
        public string $currency,
        public ?string $reference,
        public ?string $failureReason,
        public \DateTimeImmutable $createdAt,
        public \DateTimeImmutable $updatedAt,
        public bool $replayed = false,
    ) {
    }

    /**
     * @internal
     */
    public static function fromFields(Fields $f, bool $replayed = false): self
    {
        return new self(
            id: $f->string('id'),
            paymentId: $f->string('payment_id'),
            status: $f->string('status'),
            amount: $f->int('amount'),
            currency: $f->string('currency'),
            reference: $f->optionalString('reference'),
            failureReason: $f->optionalString('failure_reason'),
            createdAt: $f->time('created_at'),
            updatedAt: $f->time('updated_at'),
            replayed: $replayed,
        );
    }
}
