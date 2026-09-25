<?php

declare(strict_types=1);

namespace PspSandbox\Webhook;

/**
 * Verifies Standard Webhooks signatures (https://www.standardwebhooks.com/).
 */
final readonly class Verifier
{
    private const string PREFIX = 'whsec_';

    private string $key;

    public function __construct(
        string $secret,
        private int $toleranceSeconds = 300,
        private ?\Closure $now = null,
    ) {
        $encoded = str_starts_with($secret, self::PREFIX) ? substr($secret, strlen(self::PREFIX)) : $secret;
        $key = base64_decode($encoded, true);
        if ($key === false || $key === '') {
            throw new \InvalidArgumentException('Webhook secret must be base64, optionally prefixed with "whsec_".');
        }
        $this->key = $key;
    }

    /**
     * @param array<string, string|list<string>> $headers header names are matched case-insensitively
     *
     * @throws InvalidSignature
     */
    public function verify(string $body, array $headers): void
    {
        $headers = array_change_key_case($headers, CASE_LOWER);
        $id = self::header($headers, 'webhook-id');
        $timestamp = self::header($headers, 'webhook-timestamp');
        $signatures = self::header($headers, 'webhook-signature');

        if (!ctype_digit($timestamp)) {
            throw new InvalidSignature('Invalid webhook-timestamp.');
        }
        $now = $this->now !== null ? ($this->now)() : time();
        if (!is_int($now)) {
            throw new \LogicException('Clock must return a unix timestamp.');
        }
        if (abs($now - (int) $timestamp) > $this->toleranceSeconds) {
            throw new InvalidSignature('Timestamp is outside the tolerance window.');
        }

        $expected = base64_encode(hash_hmac('sha256', $id . '.' . $timestamp . '.' . $body, $this->key, true));

        foreach (explode(' ', $signatures) as $candidate) {
            [$version, $signature] = array_pad(explode(',', $candidate, 2), 2, '');
            if ($version === 'v1' && hash_equals($expected, $signature)) {
                return;
            }
        }

        throw new InvalidSignature('No matching signature.');
    }

    /**
     * @param array<string, string|list<string>> $headers
     */
    private static function header(array $headers, string $name): string
    {
        $value = $headers[$name] ?? null;
        if (is_array($value)) {
            $value = $value[0] ?? null;
        }
        if ($value === null || $value === '') {
            throw new InvalidSignature(sprintf('Missing %s header.', $name));
        }

        return $value;
    }
}
