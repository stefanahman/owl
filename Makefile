BIN ?= $(HOME)/.local/bin

.PHONY: build install test lint update-snapshots

build:
	go build -o pr-owl .

install:
	mkdir -p $(BIN)
	go build -o $(BIN)/pr-owl .

# e2e builds the binary at run time, so Go's test cache can't see its
# sources change — a cached "ok" would hide a real regression.
test:
	go test .
	go test -count=1 ./e2e

# Rewrite the screen snapshots (the real binary in a virtual terminal,
# JSON + PNG). Review the diff.
update-snapshots:
	go test -count=1 ./e2e -update

lint:
	test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }
	go vet ./...
