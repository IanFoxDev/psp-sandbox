# 0003. Keep the PHP client in this repository

Date: 2026-09-25. Status: accepted.

## Context

The PHP client mirrors the scenario catalog, the control API and the signing scheme.
When a scenario is added or renamed, the server and the client must change together.
Packagist requires `composer.json` at the root of the repository it reads.

## Decision

Develop the client in `clients/php/`. On every tag, a workflow splits that directory into
the read-only repository `ghuser/psp-sandbox-php`, which is what Packagist tracks.
Symfony and Laravel publish their components the same way.

The client version follows the server version: client `0.3.x` talks to server `0.3.x`.

## Consequences

- One pull request changes the server, the client and the docs together.
- Issues and pull requests are accepted only in the main repository; the split
  repository says so in its README.
- The split needs a deploy key or token with write access to the client repository.
