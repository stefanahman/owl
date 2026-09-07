BIN ?= $(HOME)/.local/bin

.PHONY: build install install-macos test lint update-snapshots

build:
	go build -o pr-owl .

install:
	mkdir -p $(BIN)
	go build -o $(BIN)/pr-owl .

# The optional yabai/Ghostty glue (see contrib/macos/README.md).
install-macos: install
	install -m 755 contrib/macos/bin/pr-reviews-focus contrib/macos/bin/ws-review $(BIN)/

# e2e builds the binary at run time, so Go's test cache can't see its
# sources change — a cached "ok" would hide a real regression.
test:
	go test .
	go test -count=1 ./e2e

# Rewrite the golden frames (model level) and the screen snapshots
# (real binary in a virtual terminal, JSON + PNG). Review the diff.
update-snapshots:
	go test . -run TestGoldenFrames -update
	go test -count=1 ./e2e -update

lint:
	test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }
	go vet ./...
	shellcheck contrib/macos/bin/* contrib/macos/yabai/*
	for f in contrib/macos/bin/*; do test -x "$$f" || { echo "$$f is not executable"; exit 1; }; done
