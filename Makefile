GO      ?= go
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X macdoc/internal/cli.version=$(VERSION)

.PHONY: build install uninstall test lint importcheck forbidcheck fuzz check clean

build:
	$(GO) build -ldflags "$(LDFLAGS)" -o bin/macdoc ./cmd/macdoc

# Install into a directory on your PATH (default ~/.local/bin).
BINDIR ?= $(HOME)/.local/bin

install: build
	mkdir -p $(BINDIR)
	install -m 0755 bin/macdoc $(BINDIR)/macdoc
	@echo "installed $(BINDIR)/macdoc"

uninstall:
	rm -f $(BINDIR)/macdoc

test:
	$(GO) test -race ./...

lint:
	golangci-lint run ./...

importcheck:
	./scripts/importcheck.sh

forbidcheck:
	./scripts/forbidcheck.sh

fuzz:
	$(GO) test -run='^$$' -fuzz=FuzzCanonicalize -fuzztime=30s ./internal/safety
	$(GO) test -run='^$$' -fuzz=FuzzCheckTarget -fuzztime=30s ./internal/safety

check: test lint importcheck forbidcheck

clean:
	rm -rf bin
