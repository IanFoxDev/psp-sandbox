# 0002. Sign callbacks with Standard Webhooks

Date: 2026-09-25. Status: accepted.

## Context

Every provider signs callbacks differently. The sandbox needs one scheme that is easy to
verify from any language and close enough to real providers that the verification code
in the application looks like production code.

## Decision

Use the [Standard Webhooks](https://www.standardwebhooks.com/) specification:
`webhook-id`, `webhook-timestamp`, `webhook-signature: v1,<base64 HMAC-SHA256>` over
`id.timestamp.body`, secret in `whsec_<base64>` form.

Provider-specific profiles (Stripe-like, Adyen-like) may add their own header formats
later, as separate profiles, without changing the default.

## Consequences

- Receivers can use existing Standard Webhooks libraries in most languages.
- `invalid_signature` scenarios have a precise meaning: wrong secret, stale timestamp,
  missing header.
- Applications whose real provider uses a different scheme test their dedup and state
  logic against the sandbox, and their provider-specific verifier separately.
