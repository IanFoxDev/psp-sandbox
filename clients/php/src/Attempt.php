<?php

declare(strict_types=1);

namespace PspSandbox;

use PspSandbox\Internal\Fields;

/**
 * One HTTP request of a callback delivery.
 */
final readonly class Attempt
{
    /**
     * @param array<string, string> $requestHeaders
     */
    public function __construct(
        public int $n,
        public \DateTimeImmutable $at,
        public array $requestHeaders,
        /** Null when no answer was received. */
        public ?int $statusCode,
        /** First 4 KB of the answer. */
        public ?string $responseBody,
        public int $latencyMs,
        /** Network error, if any. */
        public ?string $error,
    ) {
    }

    /**
     * @internal
     */
    public static function fromFields(Fields $f): self
    {
        return new self(
            n: $f->int('n'),
            at: $f->time('at'),
            requestHeaders: $f->stringMap('request_headers'),
            statusCode: $f->optionalInt('status_code'),
            responseBody: $f->optionalString('response_body'),
            latencyMs: $f->int('latency_ms'),
            error: $f->optionalString('error'),
        );
    }
}
