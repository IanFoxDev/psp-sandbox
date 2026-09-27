# Symfony example

A Symfony 7.4 shop with a naive and a safe payment callback handler, on plain DBAL. See
[../README.md](../README.md) for what the tests show and how to run them.

- `src/Controller/CheckoutController.php`: creates the order, then the payment.
- `src/Controller/NaiveCallbackController.php`: what not to do, with the reasons.
- `src/Controller/SafeCallbackController.php`: signature, event id, row lock.
- `tests/PspCallbackTest.php`: the three tests.

The image sets `variables_order = "EGPCS"` (see `../php.Dockerfile`). Without `E`,
environment variables do not reach `$_SERVER` under a web server SAPI, and Symfony
would take the values from `.env` instead of the ones set in the environment.
