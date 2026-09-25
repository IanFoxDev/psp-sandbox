# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses
[Semantic Versioning](https://semver.org/). Before 1.0, minor versions may break the API;
such changes are marked **BREAKING**.

## [Unreleased]

### Added

- Repository layout, API and scenario design documents.
- HTTP server skeleton with `/healthz` and `/version`, configuration from environment.
- PHP client skeleton: scenario catalog and Standard Webhooks signature verifier.
- Provider API: create, get, list by reference, capture, cancel and refund payments,
  `Idempotency-Key` on create and refund, optional bearer key (`PSP_API_KEY`).
- Signed callbacks (Standard Webhooks) with retries on `PSP_RETRY_SCHEDULE`, one at a
  time per payment in event order, and a log of every attempt.
- Manual clock (`PSP_CLOCK=manual`) that drives status changes, delayed callbacks and
  retries.
- Scenario `happy_path`.
