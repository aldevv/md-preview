.PHONY: build install test test-go test-lua test-all lint fmt fmt-check clean

PREFIX ?= $(HOME)/.local
BIN := $(PREFIX)/bin

# Injected into main.version via -ldflags so `mdp update` can compare
# against release tags. Falls back to (devel) outside a git checkout.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "(devel)")
LDFLAGS := -s -w -X main.version=$(VERSION)

build:
	go build -ldflags '$(LDFLAGS)' -o mdp ./cmd/mdp

# rm -f clears any md-preview.nvim symlink before install follows it.
install:
	mkdir -p $(BIN)
	rm -f $(BIN)/mdp
	GOBIN=$(BIN) go install -ldflags '$(LDFLAGS)' ./cmd/mdp
	@echo "[mdp] installed $(BIN)/mdp ($(VERSION))"

test: test-go

test-go:
	go test ./...

test-lua:
	nvim --headless --noplugin -u tests/minimal_init.lua \
		-c "PlenaryBustedDirectory tests/spec { minimal_init = 'tests/minimal_init.lua' }"

test-all: test-go test-lua

# Auto-format Go sources in place.
fmt:
	gofmt -w .

# Verify Go sources are gofmt-clean (CI uses this).
fmt-check:
	@diff=$$(gofmt -l .); \
	if [ -n "$$diff" ]; then \
		echo "files not gofmt-clean:"; \
		echo "$$diff"; \
		exit 1; \
	fi

# Static analysis.
lint:
	go vet ./...

clean:
	rm -f mdp
	rm -rf dist
