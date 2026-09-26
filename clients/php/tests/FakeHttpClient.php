<?php

declare(strict_types=1);

namespace PspSandbox\Tests;

use Nyholm\Psr7\Response;
use Psr\Http\Client\ClientInterface;
use Psr\Http\Message\RequestInterface;
use Psr\Http\Message\ResponseInterface;

/**
 * Records requests and answers with queued responses.
 */
final class FakeHttpClient implements ClientInterface
{
    /** @var list<RequestInterface> */
    public array $requests = [];

    /** @var list<ResponseInterface> */
    private array $responses = [];

    /**
     * @param array<string, string> $headers
     */
    public function queue(int $status, mixed $body = null, array $headers = []): self
    {
        $raw = is_string($body) ? $body : ($body === null ? '' : json_encode($body, JSON_THROW_ON_ERROR));
        $this->responses[] = new Response($status, $headers + ['Content-Type' => 'application/json'], $raw);

        return $this;
    }

    public function sendRequest(RequestInterface $request): ResponseInterface
    {
        $this->requests[] = $request;
        $response = array_shift($this->responses);
        if ($response === null) {
            throw new \LogicException('No response queued for ' . $request->getMethod() . ' ' . $request->getUri());
        }

        return $response;
    }

    public function last(): RequestInterface
    {
        return $this->requests[count($this->requests) - 1] ?? throw new \LogicException('No requests sent.');
    }

    /**
     * @return array<array-key, mixed>
     */
    public function lastBody(): array
    {
        $body = json_decode((string) $this->last()->getBody(), true, 512, JSON_THROW_ON_ERROR);

        return is_array($body) ? $body : throw new \LogicException('Body is not an object.');
    }
}
