<?php

use Illuminate\Database\Migrations\Migration;
use Illuminate\Database\Schema\Blueprint;
use Illuminate\Support\Facades\Schema;

return new class extends Migration
{
    public function up(): void
    {
        Schema::create('orders', function (Blueprint $table) {
            $table->id();
            $table->string('reference')->unique();
            $table->bigInteger('amount');
            $table->string('currency', 5);
            $table->string('status')->default('pending');
            $table->string('psp_payment_id')->nullable()->unique();
            // What the shop has credited to the customer. Must never exceed amount.
            $table->bigInteger('credited_amount')->default(0);
            $table->timestamps();
        });

        // Ids of provider events already applied. The primary key is the deduplication.
        Schema::create('psp_events', function (Blueprint $table) {
            $table->string('event_id')->primary();
            $table->string('type');
            $table->timestamp('received_at');
        });
    }

    public function down(): void
    {
        Schema::dropIfExists('psp_events');
        Schema::dropIfExists('orders');
    }
};
