# Architecture

One Go binary, no external services. State is kept in memory; the sandbox is meant to be
recreated for every test run.

## Layout

```
cmd/psp-sandbox/        main: config, wiring, HTTP server, graceful shutdown
internal/config/        env and flag parsing into a Config struct
internal/clock/         Clock interface: real time or manual (advanced by the control API)
internal/payment/       domain: Payment, Refund, statuses, allowed transitions
internal/ids/            prefixed ids (pay_, evt_, ...), reproducible with PSP_SEED
internal/store/         in-memory storage for payments, refunds, events, idempotency keys
internal/scenario/      Scenario interface, catalog, header parsing, rules file
internal/signing/       Standard Webhooks signer
internal/callback/      dispatcher: per-payment ordered queue, retries, delivery log
internal/engine/        creates payments, runs scenario steps on the clock, emits events
internal/app/           wiring from Config to an http.Handler, shared by main and tests
internal/sandboxtest/   test helpers: a full sandbox plus a signed-callback receiver
internal/api/           provider API handlers (/v1/*)
internal/control/       control API handlers (/_sandbox/*)
internal/httpx/         JSON and error helpers shared by both APIs
internal/ui/            embedded web UI (html/template and forms, no JavaScript)
scenarios/              example rules files
clients/php/            PHP client, published as a separate package
examples/               Laravel and Symfony apps wired to the sandbox
```

## Request flow

```
POST /v1/payments
  -> api: validate, check Idempotency-Key in store
  -> scenario: header > rule > default, parse params
  -> payment: create in `pending`, save
  -> scenario.OnCreate(ctx) decides:
       - what to answer and when (normal, delayed, 5xx, connection reset)
       - which events to schedule and at what offsets
  -> engine applies status transitions at their time on the sandbox clock
  -> each transition emits an event -> callback dispatcher
  -> dispatcher asks scenario.Deliveries(event) how to send it
       (once, N copies, parallel, delayed, bad signature, dropped)
  -> signer -> HTTP POST -> delivery log
```

Steps due at offset zero are applied before the create call answers. Response timings
(holding the answer, closing the connection) use the wall clock, because they are about
the network. Everything about the payment (status changes, delayed callbacks, retries)
uses the sandbox clock, so `PSP_CLOCK=manual` controls it. The `webhook-timestamp` of a
callback is always wall-clock time: receivers compare it with their own clock.

The `Scenario` interface has two hooks: one for the synchronous part (the HTTP answer and
the schedule of status changes) and one for the asynchronous part (how each event is
delivered). Most scenarios override only one of them.

## Why these choices

- **Go, standard library first.** One static binary, small image, fast start in CI.
  The only dependency is a YAML parser for the rules file
  ([ADR 0004](adr/0004-yaml-parser.md)). See [ADR 0001](adr/0001-go-single-binary.md).
- **Standard Webhooks for signing.** Receivers can use an existing library, and the
  scheme is close to what Stripe and Svix do. See [ADR 0002](adr/0002-standard-webhooks.md).
- **PHP client in the same repository.** Scenario names and the signing scheme change
  together with the server. See [ADR 0003](adr/0003-php-client-in-monorepo.md).
- **In-memory state.** Tests need a clean sandbox, not durability. A persistent store
  can be added behind the `store` interfaces if someone needs a long-running instance.
- **Manual clock.** Scenarios with delays of hours (chargebacks) are tested without
  waiting: the test advances the clock.
