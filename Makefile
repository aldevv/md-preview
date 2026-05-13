.PHONY: build install test test-go test-lua test-all lint fmt fmt-check clean pandoc-wasm

PREFIX ?= $(HOME)/.local
BIN := $(PREFIX)/bin

# Injected into main.version via -ldflags so `mdp update` can compare
# against release tags. Falls back to (devel) outside a git checkout.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "(devel)")
LDFLAGS := -s -w -X main.version=$(VERSION)
GOFLAGS := -trimpath -buildvcs=false

# pandoc.wasm is the runtime LaTeX engine. The file is committed to
# git (58 MB blob) so `go install github.com/aldevv/md-preview/cmd/mdp@latest`
# and the release-tarball download path both Just Work without a
# separate fetch step. The `pandoc-wasm` target below is for
# refreshing or upgrading the artifact; bumps the pinned sha256 too.
PANDOC_WASM_VERSION := 3.9.0.2
PANDOC_WASM := internal/render/latex/wasm/pandoc.wasm
PANDOC_WASM_SHA256 := internal/render/latex/wasm/pandoc.wasm.sha256

build:
	go build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o mdp ./cmd/mdp

# rm -f clears any md-preview.nvim symlink before install follows it.
install:
	mkdir -p $(BIN)
	rm -f $(BIN)/mdp
	GOBIN=$(BIN) go install $(GOFLAGS) -ldflags '$(LDFLAGS)' ./cmd/mdp
	@echo "[mdp] installed $(BIN)/mdp ($(VERSION))"

test: test-go

test-go:
	go test ./...

# Re-fetch pandoc.wasm from the pinned upstream release, verify the
# sha256 of the uncompressed bytes (matches upstream's published
# hash), then gzip -9 the file and drop the raw copy. The repo ships
# pandoc.wasm.gz, not pandoc.wasm: storing gzipped saves ~42 MB
# (~58 MB -> ~16 MB) and the browser transparently decompresses via
# Content-Encoding: gzip in WebAssembly.instantiateStreaming.
#
# Use this when bumping PANDOC_WASM_VERSION above. Commit the new
# pandoc.wasm.gz + pandoc.wasm.sha256 together.
PANDOC_WASM_GZ := $(PANDOC_WASM).gz

pandoc-wasm:
	@mkdir -p $(dir $(PANDOC_WASM))
	@echo "[mdp] downloading pandoc.wasm $(PANDOC_WASM_VERSION) from GitHub release"
	@tmpzip=$$(mktemp --suffix=.zip); \
	curl -fsSL -o "$$tmpzip" \
		"https://github.com/jgm/pandoc/releases/download/$(PANDOC_WASM_VERSION)/pandoc.wasm.zip" || \
		(rm -f "$$tmpzip"; echo "[mdp] download failed"; exit 1); \
	tmpdir=$$(mktemp -d); \
	unzip -q -o "$$tmpzip" -d "$$tmpdir" || (rm -rf "$$tmpdir" "$$tmpzip"; exit 1); \
	mv "$$tmpdir/pandoc.wasm" $(PANDOC_WASM); \
	rm -rf "$$tmpdir" "$$tmpzip"
	@cd $(dir $(PANDOC_WASM)) && sha256sum pandoc.wasm > pandoc.wasm.sha256
	@gzip -9 -f $(PANDOC_WASM)
	@echo "[mdp] pandoc.wasm.gz updated:"
	@ls -la $(PANDOC_WASM_GZ)
	@echo "[mdp] uncompressed sha256 (matches upstream):"
	@cat $(PANDOC_WASM_SHA256)

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
