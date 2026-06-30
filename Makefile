# go-perf-agent. Thin CLI in cmd/go-perf-agent; engine logic and libraries in internal/; e2e/ is the
# dev end-to-end harness (`go run ./e2e`, self-builds the binary), not a shipped command.
# e2e/scenarios/* are fixtures (separate modules / overlays) exercised by `make e2e`, never by `go test ./...`.
# Deps are vendored (vendor/), so builds are self-contained and offline-reproducible; with vendor/
# present, go build/test/vet use -mod=vendor automatically.
BINARY := go-perf-agent
# ./e2e is the exact harness package (never ./e2e/... - that would pull in the fixture scenarios).
PKGS := ./cmd/... ./internal/... ./e2e
SRC := cmd internal e2e/*.go
COVERAGE := coverage.out

.PHONY: all build fmt lint test cover e2e ci clean vendor vendor-check

all: build

build:
	go build -o $(BINARY) ./cmd/go-perf-agent

fmt:
	goimports -w $(SRC)

lint:
	@unformatted=$$(goimports -l $(SRC)); \
	if [ -n "$$unformatted" ]; then echo "goimports needed on:"; echo "$$unformatted"; exit 1; fi
	go vet $(PKGS)

test:
	go test $(PKGS)

# coverage summary: per-package % (from go test), then the uncovered funcs (0.0%) and the total.
cover:
	@go test -coverprofile=$(COVERAGE) $(PKGS)
	@echo "--- uncovered functions (0.0%) ---"
	@go tool cover -func=$(COVERAGE) | awk '$$3 == "0.0%"'
	@go tool cover -func=$(COVERAGE) | tail -1

e2e:
	go run ./e2e eval
	go run ./e2e smoke

# refresh vendored deps after changing go.mod
vendor:
	go mod tidy
	go mod vendor

# fail if go.mod/go.sum/vendor drifted from the source of truth (run `make vendor` and commit)
vendor-check:
	go mod tidy
	go mod vendor
	@if [ -n "$$(git status --porcelain -- go.mod go.sum vendor/)" ]; then \
		echo "vendor out of sync - run 'make vendor' and commit:"; \
		git status --porcelain -- go.mod go.sum vendor/; exit 1; \
	fi

# what CI runs: format check + vet, vendor consistency, unit tests, then the end-to-end gate.
ci: lint vendor-check test e2e

clean:
	rm -f $(BINARY) $(COVERAGE)
