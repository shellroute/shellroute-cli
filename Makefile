VERSION ?= dev
INSTALL_METHOD ?=
LDFLAGS := -s -w -X github.com/shellroute/shellroute-cli/internal/cli.rawVersion=$(VERSION)
ifneq ($(INSTALL_METHOD),)
  LDFLAGS += -X github.com/shellroute/shellroute-cli/internal/cli.installMethod=$(INSTALL_METHOD)
endif

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o shellroute ./cmd/shellroute

test:
	go test -race ./...

lint:
	go vet ./...
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed on:"; gofmt -l .; exit 1)

audit-public:
	@bash scripts/audit-public.sh

.PHONY: build test lint audit-public
