# Contributing

## Running locally

You need Docker. Go and PHP are optional: without a local Go toolchain the Makefile runs Go inside a container.

```bash
make test          # Go tests with the race detector
make vet           # go vet
make fmt           # gofmt, rewrites files
make lint          # golangci-lint, in Docker
make openapi-lint  # lint docs/openapi.yaml, in Docker
make build         # Docker image psp-sandbox:dev
make run           # build and start the sandbox on :8090 with docker compose
make php-test      # PHP client tests (needs PHP 8.3+ and Composer)
```

The PHP integration tests run only when `PSP_SANDBOX_URL` points at a running sandbox.
With a local Go toolchain:

```bash
PSP_CALLBACK_URL=http://127.0.0.1:1/ PSP_RETRY_SCHEDULE=0s go run ./cmd/psp-sandbox &
cd clients/php && PSP_SANDBOX_URL=http://127.0.0.1:8090 vendor/bin/phpunit
```

## Proposing a scenario

The most useful contributions are new failure scenarios taken from real provider
behavior. Before writing code, open an issue with the "Scenario request" template and
describe:

1. What the provider did (sequence of responses and callbacks, with timings).
2. What broke in your application.
3. How a test would check that the application handles it.

## Adding a scenario

1. Implement the `Scenario` interface in `internal/scenario/<name>.go`.
2. Register it in the catalog with its parameters and defaults.
3. Add a test in `internal/scenario/<name>_test.go` that drives the sandbox over HTTP
   and asserts on the callbacks received by a test server.
4. Add a row to `docs/scenarios.md`, to the scenario table in `README.md`, and a case
   to `clients/php/src/Scenario.php`.
5. Add a line to `CHANGELOG.md` under `[Unreleased]`.

## Changing the API

`docs/openapi.yaml` describes every route except the web UI. `go test` fails when a
route is registered but not in the spec, or the other way round, and when the server
sends a field that a schema does not describe. Update the spec in the same pull request
as the code, along with `docs/api.md`.

## Pull requests

- One logical change per pull request.
- Commit messages follow [Conventional Commits](https://www.conventionalcommits.org/)
  (`feat:`, `fix:`, `docs:`, `ci:`).
- CI must be green: tests, lint, PHP client tests.
- Changes to the PHP client go to this repository. `ianfoxdev/psp-sandbox-php` is a
  read-only mirror.
