.PHONY: build install test test-go test-lua test-all lint fmt fmt-check clean

build:
	go build -o mdp ./cmd/mdp

install:
	go install ./cmd/mdp

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
