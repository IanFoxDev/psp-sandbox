<?php

declare(strict_types=1);

namespace PspSandbox;

use PspSandbox\Internal\Fields;

/**
 * A payment as the sandbox returns it. Amounts are in minor units.
 */
final readonly class Payment
{
    /**
     * @param array<string, string> $metadata
     */
    public function __construct(
        public string $id,
        public PaymentStatus $status,
        public int $amount,
        public int $capturedAmount,
        public int $refundedAmount,
        public string $currency,
        public ?string $reference,
        public bool $manualCapture,
        public string $scenario,
        public ?string $failureReason,
        public \DateTimeImmutable $createdAt,
        public \DateTimeImmutable $updatedAt,
        public array $metadata,
        /** The create answer was replayed for a repeated Idempotency-Key. */
        public bool $replayed = false,
    ) {
    }

    /**
     * @internal
     */
    public static function fromFields(Fields $f, bool $replayed = false): self
    {
        $status = PaymentStatus::tryFrom($f->string('status'))
            ?? throw new Exception\UnexpectedResponse(sprintf('Unknown payment status "%s".', $f->string('status')));

        return new self(
            id: $f->string('id'),
            status: $status,
            amount: $f->int('amount'),
            capturedAmount: $f->int('captured_amount'),
            refundedAmount: $f->int('refunded_amount'),
            currency: $f->string('currency'),
            reference: $f->optionalString('reference'),
            manualCapture: $f->string('capture') === 'manual',
            scenario: $f->string('scenario'),
            failureReason: $f->optionalString('failure_reason'),
            createdAt: $f->time('created_at'),
            updatedAt: $f->time('updated_at'),
            metadata: $f->stringMap('metadata'),
            replayed: $replayed,
        );
    }
}
