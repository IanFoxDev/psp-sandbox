<?php

declare(strict_types=1);

namespace PspSandbox\Tests\Webhook;

use PHPUnit\Framework\TestCase;
use PspSandbox\Webhook\InvalidSignature;
use PspSandbox\Webhook\Verifier;

final class VerifierTest extends TestCase
{
    private const string SECRET = 'whsec_dGVzdC1zZWNyZXQ=';
    private const int NOW = 1790330400;
    private const string BODY = '{"id":"evt_1","type":"payment.captured"}';

    public function testAcceptsValidSignature(): void
    {
        $this->verifier()->verify(self::BODY, $this->headers());
        $this->addToAssertionCount(1);
    }

    public function testHeaderNamesAreCaseInsensitiveAndMayBeLists(): void
    {
        $headers = [];
        foreach ($this->headers() as $name => $value) {
            $headers[ucwords($name, '-')] = [$value];
        }

        $this->verifier()->verify(self::BODY, $headers);
        $this->addToAssertionCount(1);
    }

    public function testAcceptsOneOfSeveralSignatures(): void
    {
        $headers = $this->headers();
        $headers['webhook-signature'] = 'v1,bm9wZQ== ' . $headers['webhook-signature'];

        $this->verifier()->verify(self::BODY, $headers);
        $this->addToAssertionCount(1);
    }

    public function testRejectsTamperedBody(): void
    {
        $this->expectException(InvalidSignature::class);
        $this->verifier()->verify(self::BODY . ' ', $this->headers());
    }

    public function testRejectsWrongSecret(): void
    {
        $this->expectException(InvalidSignature::class);
        (new Verifier('whsec_b3RoZXI=', now: static fn (): int => self::NOW))->verify(self::BODY, $this->headers());
    }

    public function testRejectsStaleTimestamp(): void
    {
        $this->expectException(InvalidSignature::class);
        $this->verifier()->verify(self::BODY, $this->headers(timestamp: self::NOW - 301));
    }

    public function testRejectsMissingHeader(): void
    {
        $headers = $this->headers();
        unset($headers['webhook-signature']);

        $this->expectException(InvalidSignature::class);
        $this->verifier()->verify(self::BODY, $headers);
    }

    private function verifier(): Verifier
    {
        return new Verifier(self::SECRET, now: static fn (): int => self::NOW);
    }

    /**
     * @return array<string, string>
     */
    private function headers(int $timestamp = self::NOW): array
    {
        $key = base64_decode('dGVzdC1zZWNyZXQ=', true);
        self::assertIsString($key);
        $signature = base64_encode(hash_hmac('sha256', 'evt_1.' . $timestamp . '.' . self::BODY, $key, true));

        return [
            'webhook-id' => 'evt_1',
            'webhook-timestamp' => (string) $timestamp,
            'webhook-signature' => 'v1,' . $signature,
        ];
    }
}
