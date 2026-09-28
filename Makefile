# Uses the local Go toolchain when available, otherwise runs the same commands in Docker.
GO_IMAGE ?= golang:1.27
ifeq ($(shell command -v go 2>/dev/null),)
GO = docker run --rm -v $(CURDIR):/src -v psp-sandbox-gocache:/root/.cache -w /src $(GO_IMAGE)
else
GO =
endif
LINT_IMAGE ?= golangci/golangci-lint:v2.4.0

.PHONY: test fmt vet lint build run php-test

test:
	$(GO) go test -race ./...

fmt:
	$(GO) gofmt -l -w .

vet:
	$(GO) go vet ./...

lint:
	docker run --rm -v $(CURDIR):/src -w /src $(LINT_IMAGE) golangci-lint run

build:
	docker build -t psp-sandbox:dev .

run:
	docker compose up --build

php-test:
	cd clients/php && composer install --quiet && vendor/bin/phpunit
