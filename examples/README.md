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
writes. `parallel=true` makes that happen on every run, not once a month in production.

## Run

```bash
cd laravel   # or symfony
docker compose up -d --build --wait
docker compose exec app vendor/bin/phpunit                                  # safe: passes
docker compose exec -e PSP_CALLBACK_HANDLER=naive app vendor/bin/phpunit    # naive: 3 failures
docker compose down -v
```

While it runs, the sandbox UI at http://localhost:8090/_sandbox/ shows every payment and
every callback attempt. Both examples use port 8090, so run one at a time.

## How it is wired

- `compose.yaml` starts PostgreSQL, the sandbox (built from this repository) and the
  app under `php -S` with 8 workers, so parallel callbacks really run in parallel.
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
