<?php

namespace App\Models;

use Illuminate\Database\Eloquent\Model;

/**
 * @property int $id
 * @property string $reference
 * @property int $amount
 * @property string $currency
 * @property string $status
 * @property string|null $psp_payment_id
 * @property int $credited_amount
 */
class Order extends Model
{
    protected $fillable = ['reference', 'amount', 'currency'];

    protected $attributes = ['status' => 'pending', 'credited_amount' => 0];

    protected function casts(): array
    {
        return ['amount' => 'integer', 'credited_amount' => 'integer'];
    }
}
