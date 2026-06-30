# go-perf-agent. The engine is one package at the repo root; eval/scenarios/* are fixtures
# (separate modules / overlays) exercised by `make eval`, never by `go test ./...`.
# Deps are vendored (vendor/), so builds are self-contained and offline-reproducible; with vendor/
# present, go build/test/vet use -mod=vendor automatically.
BINARY := go-perf-agent

.PHONY: all build fmt lint test eval ci clean vendor vendor-check

all: build

build:
	go build -o $(BINARY) .

fmt:
	goimports -w *.go

lint:
	@unformatted=$$(goimports -l *.go); \
	if [ -n "$$unformatted" ]; then echo "goimports needed on:"; echo "$$unformatted"; exit 1; fi
	go vet .

test:
	go test .

eval: build
	./$(BINARY) eval

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
