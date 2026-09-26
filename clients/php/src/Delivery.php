<?php

declare(strict_types=1);

namespace PspSandbox;

use PspSandbox\Internal\Fields;

/**
 * One copy of one event sent to one URL, with every attempt.
 */
final readonly class Delivery
{
    /**
     * @param array<array-key, mixed> $body  the callback body as sent
     * @param list<Attempt>          $attempts
     */
    public function __construct(
        public string $id,
        public string $eventId,
        public string $eventType,
        public string $paymentId,
        public string $url,
        /** Copy number within duplicate_callback, 1 otherwise. */
        public int $copy,
        /** Id of the delivery this one repeats, set by replay. */
        public ?string $replayOf,
        public DeliveryStatus $status,
        public \DateTimeImmutable $createdAt,
        public array $body,
        public array $attempts,
    ) {
    }

    /**
     * @internal
     */
    public static function fromFields(Fields $f): self
    {
        $status = DeliveryStatus::tryFrom($f->string('status'))
            ?? throw new Exception\UnexpectedResponse(sprintf('Unknown delivery status "%s".', $f->string('status')));

        return new self(
            id: $f->string('id'),
            eventId: $f->string('event_id'),
            eventType: $f->string('event_type'),
            paymentId: $f->string('payment_id'),
            url: $f->string('url'),
            copy: $f->int('copy'),
            replayOf: $f->optionalString('replay_of'),
            status: $status,
            createdAt: $f->time('created_at'),
            body: $f->raw('body'),
            attempts: array_map(Attempt::fromFields(...), $f->list('attempts')),
        );
    }
}
