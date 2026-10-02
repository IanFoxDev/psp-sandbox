<?php

use App\Http\Controllers\CheckoutController;
use App\Http\Controllers\NaiveCallbackController;
use App\Http\Controllers\NaiveStripeWebhookController;
use App\Http\Controllers\SafeCallbackController;
use App\Http\Controllers\SafeStripeWebhookController;
use App\Http\Controllers\StripeCheckoutController;
use Illuminate\Support\Facades\Route;

Route::post('/orders', CheckoutController::class);
Route::post('/psp/callback/safe', SafeCallbackController::class);
Route::post('/psp/callback/naive', NaiveCallbackController::class);

Route::post('/orders/stripe', StripeCheckoutController::class);
Route::post('/stripe/webhook/safe', SafeStripeWebhookController::class);
Route::post('/stripe/webhook/naive', NaiveStripeWebhookController::class);
