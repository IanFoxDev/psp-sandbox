<?php

declare(strict_types=1);

namespace PspSandbox;

/**
 * Scenarios the sandbox server supports. Parameters and behavior:
 * https://github.com/IanFoxDev/psp-sandbox/blob/master/docs/scenarios.md
 *
 * A case is added in the client release that goes with the server release
 * implementing it, so every case here works against the matching server.
 */
enum Scenario: string
{
    case HappyPath = 'happy_path';
    case Declined = 'declined';
    case DuplicateCallback = 'duplicate_callback';
    case CallbackBeforeResponse = 'callback_before_response';
    case TimeoutThenSuccess = 'timeout_then_success';
    case LostCallback = 'lost_callback';
    case DelayedCallback = 'delayed_callback';
    case ChargebackAfter = 'chargeback_after';
    case ServerErrorThenSuccess = 'server_error_then_success';

    public const string HEADER = 'X-Sandbox-Scenario';

    /**
     * Value for the X-Sandbox-Scenario header, e.g. "duplicate_callback; times=3".
     *
     * @param array<string, scalar> $params
     */
    public function header(array $params = []): string
    {
        $parts = [$this->value];
        foreach ($params as $key => $value) {
            $parts[] = $key . '=' . match (true) {
                is_bool($value) => $value ? 'true' : 'false',
                default => (string) $value,
            };
        }

        return implode('; ', $parts);
    }
}
