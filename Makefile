.PHONY: build install test test-go test-lua test-all clean

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

clean:
	rm -f mdp
	rm -rf dist
