# Laravel example

A Laravel 12 shop with a naive and a safe payment callback handler. See
[../README.md](../README.md) for what the tests show and how to run them.

- `app/Http/Controllers/CheckoutController.php`: creates the order, then the payment.
- `app/Http/Controllers/NaiveCallbackController.php`: what not to do, with the reasons.
- `app/Http/Controllers/SafeCallbackController.php`: signature, event id, row lock.
- `tests/Feature/PspCallbackTest.php`: the three tests.
