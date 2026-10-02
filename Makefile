# Uses the local Go toolchain when available, otherwise runs the same commands in Docker.
GO_IMAGE ?= golang:1.27
ifeq ($(shell command -v go 2>/dev/null),)
GO = docker run --rm -v $(CURDIR):/src -v psp-sandbox-gocache:/root/.cache -w /src $(GO_IMAGE)
else
GO =
endif
LINT_IMAGE ?= golangci/golangci-lint:v2.4.0
REDOCLY_IMAGE ?= redocly/cli:2.57.0
# Stripe's OpenAPI spec for the contract test of the stripe profile (ADR 0005).
# The test checks the file's sha256, so change both together.
STRIPE_OPENAPI_COMMIT ?= 6f855712dfc6a235a407136e630bf36a01c069a3
STRIPE_SPEC = .cache/stripe-openapi/spec3.json

.PHONY: test fmt vet lint openapi-lint stripe-spec contract compat build run php-test

test:
	$(GO) go test -race ./...

fmt:
	$(GO) gofmt -l -w .

vet:
	$(GO) go vet ./...

lint:
	docker run --rm -v $(CURDIR):/src -w /src $(LINT_IMAGE) golangci-lint run

# Rules in redocly.yaml. Routes and response fields are checked by go test.
openapi-lint:
	docker run --rm -v $(CURDIR):/spec -w /spec $(REDOCLY_IMAGE) lint docs/openapi.yaml

stripe-spec:
	@mkdir -p $(dir $(STRIPE_SPEC))
	@test -f $(STRIPE_SPEC) || curl -fsSL -o $(STRIPE_SPEC) \
		https://raw.githubusercontent.com/stripe/openapi/$(STRIPE_OPENAPI_COMMIT)/openapi/spec3.json

# Every answer and event of the stripe profile against Stripe's own schemas.
# The path is relative to internal/stripe, where go test runs.
contract: stripe-spec
	$(GO) env STRIPE_OPENAPI_SPEC=../../$(STRIPE_SPEC) go test -count=1 -run 'Contract|Validator' -v ./internal/stripe/

# Official Stripe SDKs against the stripe profile, see compat/README.md.
compat:
	@set -e; for sdk in php go node; do \
		docker compose -f compat/compose.yaml up --build --quiet-pull --exit-code-from $$sdk $$sdk \
			|| { docker compose -f compat/compose.yaml down -v; exit 1; }; \
	done; docker compose -f compat/compose.yaml down -v

build:
	docker build -t psp-sandbox:dev .

run:
	docker compose up --build

php-test:
	cd clients/php && composer install --quiet && vendor/bin/phpunit
