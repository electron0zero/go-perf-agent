# go-perf-agent. Thin CLI in cmd/go-perf-agent; engine logic and libraries in internal/; eval/ is the
# dev regression harness (`go run ./eval`, self-builds the binary), not a shipped command.
# eval/scenarios/* are fixtures (separate modules / overlays) exercised by `make eval`, never by `go test ./...`.
# Deps are vendored (vendor/), so builds are self-contained and offline-reproducible; with vendor/
# present, go build/test/vet use -mod=vendor automatically.
BINARY := go-perf-agent
# ./eval is the exact harness package (never ./eval/... - that would pull in the fixture scenarios).
PKGS := ./cmd/... ./internal/... ./eval
SRC := cmd internal eval/*.go

.PHONY: all build fmt lint test eval ci clean vendor vendor-check

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

eval:
	go run ./eval

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

# what CI runs: format check + vet, vendor consistency, unit tests, then the golden-scenario gate.
ci: lint vendor-check test eval

clean:
	rm -f $(BINARY)
