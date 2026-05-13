.PHONY: build install install-dev test test-go test-lua test-all lint fmt fmt-check clean

PREFIX ?= $(HOME)/.local
BIN := $(PREFIX)/bin

# Injected into main.version via -ldflags so `mdp update` can compare
# against release tags. Falls back to (devel) outside a git checkout.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "(devel)")
LDFLAGS := -s -w -X main.version=$(VERSION)
GOFLAGS := -trimpath -buildvcs=false

build:
	go build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o mdp ./cmd/mdp

# rm -f clears any md-preview.nvim symlink before install follows it.
install:
	mkdir -p $(BIN)
	rm -f $(BIN)/mdp
	GOBIN=$(BIN) go install $(GOFLAGS) -ldflags '$(LDFLAGS)' ./cmd/mdp
	@echo "[mdp] installed $(BIN)/mdp ($(VERSION))"

# Install the current branch as `mdp-dev` so it can run side-by-side
# with a stable `mdp` install. Version is stamped with branch + short
# sha so `mdp-dev version` reveals what's actually running.
DEV_BRANCH := $(shell git rev-parse --abbrev-ref HEAD 2>/dev/null || echo detached)
DEV_SHA := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DEV_VERSION := dev-$(DEV_BRANCH)-$(DEV_SHA)
install-dev:
	mkdir -p $(BIN)
	go build $(GOFLAGS) -ldflags '-s -w -X main.version=$(DEV_VERSION)' \
		-o $(BIN)/mdp-dev ./cmd/mdp
	@echo "[mdp] installed $(BIN)/mdp-dev ($(DEV_VERSION))"

test: test-go

test-go:
	go test ./...

test-lua:
	nvim --headless --noplugin -u tests/minimal_init.lua \
		-c "PlenaryBustedDirectory tests/spec { minimal_init = 'tests/minimal_init.lua' }"

test-all: test-go test-lua

fmt:
	gofmt -w .

fmt-check:
	@diff=$$(gofmt -l .); \
	if [ -n "$$diff" ]; then \
		echo "files not gofmt-clean:"; \
		echo "$$diff"; \
		exit 1; \
	fi

lint:
	go vet ./...

clean:
	rm -f mdp
	rm -rf dist
