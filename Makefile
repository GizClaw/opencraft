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

# Release version injected into the binary via -X. Prefer the nearest
# git tag (v-prefix stripped); fall back to the code default 0.1.0
# when there are no tags.
VERSION ?= $(shell git describe --tags --abbrev=0 2>/dev/null | sed 's/^v//')
ifeq ($(strip $(VERSION)),)
VERSION := 0.1.0
endif

# Shared Windows build flags: strip symbols (-s -w), pin the version,
# strip local build paths (-trimpath), and UPX-compress the binary.
# wails adds -s -w and -H windowsgui itself in production mode; the
# explicit flags keep the intent visible and the version injection
# works in every mode.
WINDOWS_LDFLAGS := -s -w -X github.com/GizClaw/opencraft/internal/app.ServiceVersion=$(VERSION)

# build-linux produces the desktop binary for Linux (requires the
# GTK/WebKit development packages; see .github/workflows/ci.yml).
build-linux:
	wails build -platform linux/amd64 -tags webkit2_41

# build-windows produces the desktop binary for Windows. Wails embeds
# build/windows/icon.ico and cross-compiles the binary from any host.
build-windows:
	rm -f OpenCraft-res.syso
	wails build -platform windows/amd64 -trimpath -upx \
		-ldflags "$(WINDOWS_LDFLAGS)"
	rm -f OpenCraft-res.syso

# build-windows-installer produces the Windows NSIS installer in
# addition to the binary (requires makensis on PATH; macOS/Linux:
# `brew install nsis`).
build-windows-installer:
	rm -f OpenCraft-res.syso
	wails build -platform windows/amd64 -nsis -trimpath -upx \
		-ldflags "$(WINDOWS_LDFLAGS)"
	rm -f OpenCraft-res.syso
