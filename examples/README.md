# Examples

Minimal applications wired to the sandbox. Each one starts with `docker compose up` and
has a test suite that runs the v0.1 scenarios against a payment callback handler.

| Directory | Stack | Status |
|---|---|---|
| `laravel/` | Laravel 12, PHPUnit, PostgreSQL | planned for v0.1 |
| `symfony/` | Symfony 7.4, PHPUnit, PostgreSQL | planned for v0.1 |

Each example shows two handlers side by side: a naive one that fails the
`duplicate_callback` and `callback_before_response` tests, and a correct one that passes.
