.PHONY: all fmt fmt-check lint test gen-bindings build-linux

all: fmt lint test

fmt:
	go fmt ./...
	npm --prefix frontend run format

# fmt-check verifies formatting without writing, for CI.
fmt-check:
	@files="$$(gofmt -l .)"; if [ -n "$$files" ]; then \
		echo "gofmt required on:"; echo "$$files"; exit 1; \
	fi
	npm --prefix frontend run format:check

lint:
	golangci-lint run ./...
	staticcheck ./...

# gen-bindings regenerates the wails TS bindings (frontend/wailsjs).
# Required before a frontend-only build on a fresh checkout, since the
# generated files are not committed.
gen-bindings:
	wails generate module

test:
	go test ./...

# build-linux produces the desktop binary for Linux (requires the
# GTK/WebKit development packages; see .github/workflows/ci.yml).
build-linux:
	wails build -platform linux/amd64 -tags webkit2_41

# build-windows produces the desktop binary for Windows. Wails embeds
# build/windows/icon.ico and cross-compiles the binary from any host.
build-windows:
	wails build -platform windows/amd64

# build-windows-installer produces the Windows NSIS installer in
# addition to the binary (requires makensis on PATH; macOS/Linux:
# `brew install nsis`).
build-windows-installer:
	wails build -platform windows/amd64 -nsis
