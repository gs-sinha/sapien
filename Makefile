BINARY  := sapien
BIN_DIR := bin
CMD     := ./cmd/sapien

VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -X github.com/growsimplee/sapien/internal/cli.Version=$(VERSION) \
           -X github.com/growsimplee/sapien/internal/cli.Commit=$(COMMIT) \
           -X github.com/growsimplee/sapien/internal/cli.Date=$(DATE)

# Extra build tags (none needed by default).
GOTAGS :=

.PHONY: build test cover vet fmt-check fixtures clean release-snapshot bench lint ui

build:
	mkdir -p $(BIN_DIR)
	go build -tags '$(GOTAGS)' -ldflags '$(LDFLAGS)' -o $(BIN_DIR)/$(BINARY) $(CMD)

# ui builds the web inspector (PLAN §34c) into internal/ui/dist, which
# go:embed picks up on the next `make build`. Not a dependency of build:
# internal/ui/dist ships a committed placeholder so `go build`/`go test`
# never need Node; run this target yourself (needs Node 16+) whenever
# ui/'s sources change, then rebuild sapien to embed the result.
ui:
	cd ui && npm ci && npm run build

test:
	go test -tags '$(GOTAGS)' ./... -race -cover

cover:
	go test -tags '$(GOTAGS)' ./... -race -coverprofile=coverage.out
	go tool cover -html=coverage.out -o coverage.html

vet:
	go vet -tags '$(GOTAGS)' ./...

fmt-check:
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt needed on:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi

fixtures:
	go run ./cmd/sapien-fixtures

clean:
	rm -rf $(BIN_DIR) coverage.out coverage.html

# release-snapshot builds every release artifact locally (no publish, no
# tag required) via goreleaser, for testing .goreleaser.yaml changes.
release-snapshot:
	@if command -v goreleaser >/dev/null 2>&1; then \
		goreleaser release --snapshot --clean; \
	else \
		echo "goreleaser not found on PATH; install it from https://goreleaser.com/install/ to use this target" >&2; \
		exit 1; \
	fi

# bench runs the benchmark-style tests that skip themselves under
# `go test -short` but otherwise run as part of `make test` too: catalog's
# 1000-operation Apply, the openapi ingester's large generated spec, and
# search's timing-at-scale check (PLAN.md §32). This target isolates them,
# without -race, so their wall-clock thresholds mean what they say --
# -race's overhead alone is enough to blow past catalog's 3s budget.
bench:
	go test -tags '$(GOTAGS)' ./internal/catalog/... ./internal/ingest/openapi/... ./internal/search/... -run 'Large|TimingAtScale' -v

# lint mirrors CI's fmt-check + vet under one name.
lint: fmt-check vet
