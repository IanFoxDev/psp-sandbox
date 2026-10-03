<?php

declare(strict_types=1);

namespace PspSandbox;

enum PaymentStatus: string
{
    case Pending = 'pending';
    case RequiresAction = 'requires_action';
    case Authorized = 'authorized';
    case Captured = 'captured';
    case PartiallyRefunded = 'partially_refunded';
    case Refunded = 'refunded';
    case Failed = 'failed';
    case Canceled = 'canceled';
    case Disputed = 'disputed';
    case ChargebackLost = 'chargeback_lost';
    case ChargebackWon = 'chargeback_won';
}
