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
# -names keeps the method name in the call payload so the Playwright mock can
# route by service/method instead of numeric IDs.
gen-bindings:
	wails3 generate bindings -d frontend/bindings -ts -i -names ./...

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

MACOS_LDFLAGS := -s -w -X github.com/GizClaw/opencraft/internal/foundation/version.ServiceVersion=$(VERSION)

# build-macos produces the v3 desktop binary for the current macOS
# architecture. Use `wails3 task package` for .app bundling.
build-macos:
	npm --prefix frontend ci
	npm --prefix frontend run build
	go build -tags production -trimpath -buildvcs=false \
		-ldflags "$(MACOS_LDFLAGS)" \
		-o build/bin/opencraft .
