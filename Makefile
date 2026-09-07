BIN ?= $(HOME)/.local/bin

.PHONY: build install test lint update-snapshots

# The version a local build reports; releases set it from the tag.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

build:
	go build -ldflags "-X main.version=$(VERSION)" -o pr-owl .

install:
	mkdir -p $(BIN)
	go build -ldflags "-X main.version=$(VERSION)" -o $(BIN)/pr-owl .

# e2e builds the binary at run time, so Go's test cache can't see its
# sources change — a cached "ok" would hide a real regression.
test:
	go test .
	go test -count=1 -timeout 120s ./e2e

# Rewrite the screen snapshots (the real binary in a virtual terminal,
# JSON + PNG). Review the diff.
update-snapshots:
	go test -count=1 -timeout 120s ./e2e -update

lint:
	test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }
	go vet ./...
