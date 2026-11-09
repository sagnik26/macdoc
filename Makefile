GO      ?= go
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
# -s -w drop the symbol and debug tables (about 16 MB → 10 MB); panics still
# print a stack trace.
LDFLAGS := -s -w -X github.com/sagnik26/macdoc/internal/cli.version=$(VERSION)

.PHONY: build install uninstall test cover lint importcheck forbidcheck threatcheck vulncheck fuzz check clean

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

# Coverage across packages. Tests live in each package's tests/ folder, so a
# plain `go test -cover ./internal/x` reports 0% for the package they test.
cover:
	$(GO) test -count=1 -coverpkg=./internal/...,./tools/...,./cmd/... -coverprofile=coverage.out ./...
	$(GO) tool cover -func=coverage.out | tail -1

lint:
	golangci-lint run ./...

importcheck:
	./scripts/imports.sh

forbidcheck:
	./scripts/forbidden.sh

# Every test that docs/THREAT_MODEL.md names must still exist.
threatcheck:
	./scripts/threats.sh

# Known vulnerabilities in what the module actually calls (pinned version).
vulncheck:
	$(GO) run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...

fuzz:
	$(GO) test -run='^$$' -fuzz=FuzzCanonicalize -fuzztime=30s ./internal/safety
	$(GO) test -run='^$$' -fuzz=FuzzCheckTarget -fuzztime=30s ./internal/safety

check: test lint importcheck forbidcheck threatcheck

clean:
	rm -rf bin
