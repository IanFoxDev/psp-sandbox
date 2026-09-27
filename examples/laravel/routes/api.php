<?php

use App\Http\Controllers\CheckoutController;
use App\Http\Controllers\NaiveCallbackController;
use App\Http\Controllers\SafeCallbackController;
use Illuminate\Support\Facades\Route;

Route::post('/orders', CheckoutController::class);
Route::post('/psp/callback/safe', SafeCallbackController::class);
Route::post('/psp/callback/naive', NaiveCallbackController::class);
