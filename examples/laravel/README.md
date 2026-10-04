# Laravel example

A Laravel 12 shop with a naive and a safe payment callback handler. See
[../README.md](../README.md) for what the tests show and how to run them.

- `app/Http/Controllers/CheckoutController.php`: creates the order, then the payment.
- `app/Http/Controllers/NaiveCallbackController.php`: what not to do, with the reasons.
- `app/Http/Controllers/SafeCallbackController.php`: signature, event id, row lock.
- `tests/Feature/PspCallbackTest.php`: the three tests.
- `app/Psp/StripeGateway.php`, `app/Http/Controllers/*StripeWebhookController.php`,
  `tests/Feature/StripeWebhookTest.php`: the same shop on stripe-php, against the
  sandbox in the stripe profile.
- `app/Http/Controllers/StripeCheckoutSessionController.php`, `*CheckoutSuccessController.php`,
  `tests/Feature/StripeCheckoutTest.php`: Stripe Checkout, fulfilled on the success page
  (naive) or on `checkout.session.completed` (safe).
