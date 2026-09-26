<?php

declare(strict_types=1);

namespace PspSandbox\Internal;

use PspSandbox\Exception\UnexpectedResponse;

/**
 * Typed access to a decoded JSON object. Not part of the public API.
 *
 * @internal
 */
final readonly class Fields
{
    /**
     * @param array<array-key, mixed> $data
     */
    public function __construct(private array $data, private string $what)
    {
    }

    public static function decode(string $json, string $what): self
    {
        try {
            $data = json_decode($json, true, 512, JSON_THROW_ON_ERROR);
        } catch (\JsonException $e) {
            throw new UnexpectedResponse(sprintf('%s is not valid JSON: %s', $what, $e->getMessage()), 0, $e);
        }
        if (!is_array($data)) {
            throw new UnexpectedResponse(sprintf('%s is not a JSON object.', $what));
        }

        return new self($data, $what);
    }

    public function string(string $key): string
    {
        $value = $this->data[$key] ?? null;
        if (!is_string($value)) {
            throw $this->invalid($key, 'a string');
        }

        return $value;
    }

    public function optionalString(string $key): ?string
    {
        $value = $this->data[$key] ?? null;
        if ($value === null || $value === '') {
            return null;
        }
        if (!is_string($value)) {
            throw $this->invalid($key, 'a string');
        }

        return $value;
    }

    public function int(string $key): int
    {
        $value = $this->data[$key] ?? null;
        if (!is_int($value)) {
            throw $this->invalid($key, 'an integer');
        }

        return $value;
    }

    public function optionalInt(string $key): ?int
    {
        return array_key_exists($key, $this->data) && $this->data[$key] !== null ? $this->int($key) : null;
    }

    public function bool(string $key): bool
    {
        $value = $this->data[$key] ?? null;
        if (!is_bool($value)) {
            throw $this->invalid($key, 'a boolean');
        }

        return $value;
    }

    public function time(string $key): \DateTimeImmutable
    {
        $value = $this->string($key);
        try {
            return new \DateTimeImmutable($value);
        } catch (\Exception $e) {
            throw new UnexpectedResponse(sprintf('%s.%s is not a timestamp: %s', $this->what, $key, $value), 0, $e);
        }
    }

    public function object(string $key): self
    {
        $value = $this->data[$key] ?? null;
        if (!is_array($value)) {
            throw $this->invalid($key, 'an object');
        }

        return new self($value, $this->what . '.' . $key);
    }

    /**
     * @return array<string, string>
     */
    public function stringMap(string $key): array
    {
        $value = $this->data[$key] ?? [];
        if (!is_array($value)) {
            throw $this->invalid($key, 'an object');
        }
        $out = [];
        foreach ($value as $k => $v) {
            if (!is_string($v)) {
                throw $this->invalid($key . '.' . $k, 'a string');
            }
            $out[(string) $k] = $v;
        }

        return $out;
    }

    /**
     * @return list<self>
     */
    public function list(string $key): array
    {
        $value = $this->data[$key] ?? null;
        if (!is_array($value) || !array_is_list($value)) {
            throw $this->invalid($key, 'a list');
        }
        $out = [];
        foreach ($value as $i => $item) {
            if (!is_array($item)) {
                throw $this->invalid($key . '[' . $i . ']', 'an object');
            }
            $out[] = new self($item, sprintf('%s.%s[%d]', $this->what, $key, $i));
        }

        return $out;
    }

    /**
     * @return array<array-key, mixed>
     */
    public function raw(string $key): array
    {
        $value = $this->data[$key] ?? null;
        if (!is_array($value)) {
            throw $this->invalid($key, 'an object');
        }

        return $value;
    }

    /**
     * @return array<array-key, mixed>
     */
    public function all(): array
    {
        return $this->data;
    }

    private function invalid(string $key, string $want): UnexpectedResponse
    {
        return new UnexpectedResponse(sprintf('%s.%s must be %s.', $this->what, $key, $want));
    }
}
