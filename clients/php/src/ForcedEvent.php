<?php

declare(strict_types=1);

namespace PspSandbox;

/**
 * Result of Client::forceEvent(): the payment after the change and the event sent.
 */
final readonly class ForcedEvent
{
    public function __construct(
        public Payment $payment,
        public Event $event,
    ) {
    }
}
