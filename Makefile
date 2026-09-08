.PHONY: all fmt fmt-check lint check-boundaries test test-yoloonly \
	gen-bindings build-macos

all: fmt lint check-boundaries test

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

# check-boundaries enforces the internal dependency-direction rules:
# capabilities/foundation must not import orchestration/adapters, and
# orchestration must not import adapters. Kept as its own target so CI
# can gate on it without the full lint toolchain.
check-boundaries:
	./scripts/check-boundaries.sh

# gen-bindings regenerates the Wails v3 TS bindings (frontend/bindings)
# for the desktop services registered in main.go and desktop.RegisterServices.
gen-bindings:
	wails3 generate bindings -d frontend/bindings -ts -i ./...

test:
	go test ./...

# test-yoloonly runs the suite under the yoloonly build profile tag.
# Keep it green before releasing any of the build-yolo-* desktop
# targets; confined-mode tests are skipped by design in that profile.
test-yoloonly:
	go test -tags yoloonly ./...

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
WINDOWS_LDFLAGS := -s -w -X github.com/GizClaw/opencraft/internal/foundation/version.ServiceVersion=$(VERSION)
MACOS_LDFLAGS := -s -w -X github.com/GizClaw/opencraft/internal/foundation/version.ServiceVersion=$(VERSION)

# Local Go toolchains newer than the go.mod version (e.g. Homebrew Go
# 1.27) link against macOS 13 while Wails still passes a 10.13 minimum,
# producing "built for newer macOS" ld warnings. Pin macOS desktop builds
# to the repository's Go version and silence the harmless duplicate
# -lobjc warning emitted by newer Xcode linkers.
GOMOD_GO_VERSION := $(shell awk '/^go /{print $$2; exit}' go.mod)
GO_TOOLCHAIN ?= go$(GOMOD_GO_VERSION)
MACOS_CGO_LDFLAGS ?= -Wl,-no_warn_duplicate_libraries

# build-macos produces the v3 desktop binary for the current macOS
# architecture. .app bundling / cross-platform packaging is re-added in
# Phase 4 with the wails3 Taskfile tasks.
build-macos:
	npm --prefix frontend ci
	npm --prefix frontend run build
	go build -tags production -trimpath -buildvcs=false \
		-ldflags "$(MACOS_LDFLAGS)" \
		-o build/bin/opencraft .
