# SDK compatibility

The stripe profile is checked with the official SDKs, not only with its own tests:
[php](php/check.php) (stripe-php 22.0.0), [go](go/main.go) (stripe-go v87.0.0) and
[node](node/check.mjs) (stripe-node 23.0.0), all on API version `2026-09-30.endive`.

Each one runs the calls a backend makes against a sandbox started with
`PSP_PROFILE=stripe`, and receives the webhooks on its own endpoint, verified with the
SDK's `constructEvent`:

- a card payment, a decline as the SDK's card error, a retry with another card;
- manual capture of part of the amount, a partial refund;
- `server_error_then_success` with and without SDK retries;
- `timeout_then_success`: a retry after a client timeout gets the stored answer;
- paging through a list, retrieving an event.

```bash
make compat
```

CI runs the same on every change to the server or to this folder.

## What the checks found in the SDKs

These are pinned by the checks, so a new SDK version that changes them fails here
first.

- **stripe-go does not retry 5xx answers.** An error answer becomes `*stripe.Error`,
  whose `canRetry` allows only 429 `lock_timeout`, so `Stripe-Should-Retry` and the
  status are never looked at. It does retry network errors and timeouts.
  Reported as [stripe-go#2466](https://github.com/stripe/stripe-go/issues/2466).
- **stripe-php sends `Idempotency-Key` only with the global retry setting.**
  `max_network_retries` on `StripeClient` retries POSTs without a key; only
  `\Stripe\Stripe::setMaxNetworkRetries()` adds one. After a timeout, each retry
  without a key is a new payment.
  Reported as [stripe-php#2173](https://github.com/stripe/stripe-php/issues/2173).
- **stripe-php pages through lists on the global API base.** `autoPagingIterator()`
  and `nextPage()` send the next page to `\Stripe\Stripe::$apiBase`, not to the client's
  `api_base`. With the sandbox, set both.
  Reported as [stripe-php#2174](https://github.com/stripe/stripe-php/issues/2174).
