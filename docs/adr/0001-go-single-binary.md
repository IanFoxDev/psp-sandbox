# 0001. Go, single static binary, standard library first

Date: 2026-09-25. Status: accepted.

## Context

The sandbox runs in CI next to the application under test. It has to start in well under
a second, use little memory, and be trivial to add to any `compose.yaml`. Users write
their applications in PHP, Node, Python, Java; the sandbox language does not matter to
them, only the image does.

The work is mostly concurrent I/O: holding responses, sending callbacks in parallel,
timers for retries and delays.

## Decision

Write the server in Go as one static binary. Use the standard library for HTTP
(`net/http` with method and path patterns), logging (`log/slog`), templates and embedding
the UI. Every third-party dependency needs a short note in this directory. The only one
planned for v0.1 is a YAML parser for the rules file.

The final image is based on `gcr.io/distroless/static` and runs as non-root.

## Consequences

- Image size stays around 10 MB, start time is dominated by container startup.
- Goroutines and timers make scenarios like parallel duplicate callbacks and held
  responses straightforward.
- PHP developers who want to add a scenario have to write Go. The scenario interface is
  kept small to lower that barrier, and CONTRIBUTING has a step-by-step example.
