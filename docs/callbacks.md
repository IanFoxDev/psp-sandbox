# Callbacks and signing

This page describes the native profile. In the stripe profile the bodies are Stripe
events signed with `Stripe-Signature`, see [stripe.md](stripe.md#webhooks); delivery and
retries below are the same.

## Event body

```json
{
  "id": "evt_01J9Z3M4T7A1",
  "type": "payment.captured",
  "created_at": "2026-09-25T10:00:00Z",
  "data": { "...": "payment or refund object, as returned by the API" }
}
```

Event types: `payment.authorized`, `payment.captured`, `payment.failed`,
`payment.canceled`, `refund.succeeded`, `refund.failed`, `chargeback.opened`,
`chargeback.closed`.

The event `id` is stable across retries and duplicates. A correct receiver deduplicates
on it (or on payment id plus status), which is exactly what `duplicate_callback` tests.

## Signing

The sandbox follows [Standard Webhooks](https://www.standardwebhooks.com/):

```
webhook-id: evt_01J9Z3M4T7A1
webhook-timestamp: 1790330400
webhook-signature: v1,<base64(HMAC-SHA256(secret, id + "." + timestamp + "." + body))>
```

- The secret is `PSP_WEBHOOK_SECRET` without the `whsec_` prefix, base64-decoded.
- Receivers should reject timestamps older than 5 minutes.
- Any Standard Webhooks library verifies these callbacks. The PHP client ships its own
  small verifier (`PspSandbox\Webhook\Verifier`) with no dependencies.

## Delivery and retries

- A delivery succeeds on any `2xx` within 10 seconds. Redirects are not followed: a
  `3xx` counts as a failure.
- `PSP_RETRY_SCHEDULE` (default `0s,5s,30s,2m,10m,1h`) lists the pause before each
  attempt, the first one included. The default makes 6 attempts over about 1h12m.
  `PSP_RETRY_SCHEDULE=0s` means one attempt and no retries, which is handy in CI.
- Each pause gets 0 to 10% of jitter added, never subtracted.
- After the last attempt the delivery is marked `failed`. It can still be replayed
  through `POST /_sandbox/deliveries/{id}/replay`.
- Deliveries for one payment are sent in event order, one at a time, unless a scenario
  says otherwise (`duplicate_callback` with `parallel=true`, or `out_of_order`, which
  reverses the order and lists deliveries in event order with later attempt times). A later event, or a
  replay, waits until the delivery in front of it has succeeded or used up its
  attempts. With the receiver down and the default schedule that can be an hour: use
  a short `PSP_RETRY_SCHEDULE` in tests, or a manual clock.
- Requests carry `Content-Type: application/json` and `User-Agent: psp-sandbox/<version>`.

Without `PSP_CALLBACK_URL` and without `callback_url` on the payment, events are
recorded but not delivered, and the deliveries list stays empty. The sandbox warns
about it at startup.

Every attempt is recorded: request headers and body, response status, response body
(first 4 KB), latency, error. Visible in the UI and at
`GET /_sandbox/payments/{id}/deliveries`.
