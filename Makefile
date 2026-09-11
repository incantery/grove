# grove — build and local install.

BINDIR ?= $(HOME)/.local/bin

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build test install

build:
	go build -ldflags "$(LDFLAGS)" -o grove ./cmd/grove

test:
	go vet ./...
	go test ./...

install: build test
	@mkdir -p $(BINDIR)
	install -m 0755 grove $(BINDIR)/grove
	@echo "grove: installed $(BINDIR)/grove"
