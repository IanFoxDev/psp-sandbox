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
    case OutOfOrder = 'out_of_order';
    case ThreeDSecure = 'three_d_secure';
    case InvalidSignature = 'invalid_signature';
    case AckIgnored = 'ack_ignored';
    case AmountMismatch = 'amount_mismatch';
    case StatusRegression = 'status_regression';

    public const string HEADER = 'X-Sandbox-Scenario';

    /** Metadata key the stripe profile reads the scenario from. */
    public const string METADATA_KEY = 'sandbox_scenario';

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

    /**
     * Metadata that picks this scenario in the stripe profile, for SDKs that
     * cannot add a header, e.g. stripe-php:
     *
     *     $stripe->paymentIntents->create([
     *         'amount' => 1000, 'currency' => 'eur', 'confirm' => true, 'payment_method' => 'pm_card_visa',
     *         'metadata' => ['order_id' => '42'] + Scenario::DuplicateCallback->metadata(['times' => 3]),
     *     ]);
     *
     * @param array<string, scalar> $params
     *
     * @return array{sandbox_scenario: string}
     */
    public function metadata(array $params = []): array
    {
        return [self::METADATA_KEY => $this->header($params)];
    }
}
