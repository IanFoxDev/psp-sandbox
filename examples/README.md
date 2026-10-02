# Examples

Two small shops, one on Laravel 12 and one on Symfony 7.4, both on PostgreSQL. Each has
a checkout that calls the provider and two callback handlers for `payment.captured`:

- `naive`: the handler many projects start with. It checks "already paid?" before
  crediting, trusts the request, and finds the order by the provider payment id.
- `safe`: verifies the signature, records the event id under a primary key, locks the
  order row and finds the order by the shop's own reference.

The same three tests run against both handlers:

| Test | Scenario (picked by `scenarios.yaml` from the reference) | `naive` | `safe` |
|---|---|---|---|
| duplicate callbacks credit the order once | `duplicate_callback`, 5 copies sent at once | credited 2 to 5 times the amount | pass |
| a callback that arrives before the create response is not lost | `callback_before_response` | order stays `pending` | pass |
| an unsigned callback is rejected | none, the test posts a forged callback | `204`, order would be paid | pass |

The "already paid?" check does stop copies that arrive one after another. It does not
stop copies that arrive together: each request reads `pending` before any of them
writes. Real handlers make that window wide by calling other services between the check
and the write; here `Warehouse::reserve()` stands in for that call and takes 200 ms.
`parallel=true` makes the copies overlap on every run, not once a month in production.
The safe handler calls the warehouse after its transaction commits, and only for the
copy that applied the event.

## The Laravel shop on Stripe

The Laravel example also pays through the official `stripe/stripe-php`, against a second
sandbox started with `PSP_PROFILE=stripe` (service `psp-stripe`, UI on port 8091). The
only sandbox-specific line in the shop code is `api_base`. The same rules file picks the
scenario, from `metadata[reference]`.

`NaiveStripeWebhookController` and `SafeStripeWebhookController` handle
`payment_intent.succeeded` with the same mistakes and the same fixes as the native
pair; the safe one verifies `Stripe-Signature` with `\Stripe\Webhook::constructEvent()`.
`tests/Feature/StripeWebhookTest.php` runs the three tests above against them, so the
naive handler fails six tests in Laravel and three in Symfony.

With `duplicate_callback` each copy is a pair of Stripe events (`charge.succeeded` and
`payment_intent.succeeded`), so five copies are ten deliveries plus
`payment_intent.created`.

## Run

```bash
cd laravel   # or symfony
docker compose up -d --build --wait
docker compose exec app vendor/bin/phpunit                                  # safe: passes
docker compose exec -e PSP_CALLBACK_HANDLER=naive app vendor/bin/phpunit    # naive: 6 failures (Symfony: 3)
docker compose down -v
```

While it runs, the sandbox UI at http://localhost:8090/_sandbox/ shows every payment and
every callback attempt; the Stripe one is on port 8091. Both examples use port 8090,
so run one at a time.

## How it is wired

- `compose.yaml` starts PostgreSQL, the sandbox (built from this repository) and the
  app under FrankenPHP with 16 threads, so parallel callbacks really run in parallel.
  The PHP built-in server is not enough for this even with `PHP_CLI_SERVER_WORKERS`:
  one worker may accept all copies and handle them one by one, and the race is gone.
- The app calls the provider at `PSP_URL` and registers
  `PSP_CALLBACK_BASE_URL/psp/callback/{naive|safe}` as the callback URL, chosen by
  `PSP_CALLBACK_HANDLER`.
- The tests start a checkout in-process, then use the PHP client
  (`PspSandbox\Testing\InteractsWithSandbox`) to wait until the sandbox has delivered
  the callbacks to the running app, and check the order in the database.
- The PHP client comes from `../../clients/php` through a Composer path repository, so
  the whole repository is mounted into the app container.

Files worth reading: `app/Http/Controllers/*CallbackController.php` (Laravel),
`src/Controller/*CallbackController.php` (Symfony), and the tests in `tests/`.
