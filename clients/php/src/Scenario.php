<?php

declare(strict_types=1);

namespace PspSandbox;

/**
 * Scenario catalog of the sandbox. Keep in sync with docs/scenarios.md.
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
    case OutOfOrder = 'out_of_order';
    case AckIgnored = 'ack_ignored';
    case InvalidSignature = 'invalid_signature';
    case ServerErrorThenSuccess = 'server_error_then_success';
    case AmountMismatch = 'amount_mismatch';
    case ChargebackAfter = 'chargeback_after';

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
