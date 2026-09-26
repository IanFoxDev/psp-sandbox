# 0004. Parse the rules file with go.yaml.in/yaml/v3

Date: 2026-09-26. Status: accepted.

## Context

The rules file (`PSP_SCENARIOS_FILE`) is YAML, because people already keep it next to
`compose.yaml` and write it by hand. The standard library has no YAML parser, so this is
the first dependency of the server (see ADR 0001).

What the parser must do:

- Reject unknown keys. A rule with `ammount: 1313` that silently matches every payment
  is worse than no rule.
- Report duplicate keys and syntax errors with a line number.
- Give raw scalar text for `params`, so `delay: 30s` and `times: 3` go through the same
  validation as the `X-Sandbox-Scenario` header.
- Pull in nothing else.

Candidates:

- `go.yaml.in/yaml/v3`: the continuation of `gopkg.in/yaml.v3`, now maintained by the
  YAML organization. Same API that most Go developers have used. No dependencies.
  `Decoder.KnownFields(true)` rejects unknown keys, `yaml.Node` gives raw scalars.
- `github.com/goccy/go-yaml`: faster and has error messages with a source excerpt.
  Larger API, and the speed does not matter for a file read once at startup.
- `gopkg.in/yaml.v3`: archived upstream, no fixes.

## Decision

Use `go.yaml.in/yaml/v3` with strict decoding. Parameters are decoded as `yaml.Node` and
must be scalars; their text goes to the same catalog validation as header parameters.
The file is read once at startup, and any error stops the sandbox.

## Consequences

- One small, dependency-free module in `go.mod`. The image stays a single static binary.
- Error messages for rule problems are ours (`rule 2: unknown scenario "nope"`), syntax
  errors come from the parser with a line number.
- Changing the rules needs a restart. Reloading on change can come later if someone asks
  for it; tests usually start a fresh container anyway.
