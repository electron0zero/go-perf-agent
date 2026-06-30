# go-perf-agent. The engine is one package at the repo root; eval/scenarios/* are fixtures
# (separate modules / overlays) exercised by `make eval`, never by `go test ./...`.
BINARY := go-perf-agent

.PHONY: all build fmt lint test eval ci clean

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

# what CI runs: format check + vet, unit tests, then the golden-scenario engine gate.
ci: lint test eval

clean:
	rm -f $(BINARY)
