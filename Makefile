.PHONY: all fmt fmt-check lint check-boundaries test test-yoloonly \
	gen-bindings gen-proto gen-proto-check build-macos

all: fmt lint check-boundaries test

# GOFMT is the gofmt of the toolchain go.mod pins. Naming the toolchain
# explicitly matters: GOTOOLCHAIN=auto only switches *up*, so a machine
# with a newer Go than go.mod asks for (1.27 against 1.25) keeps using
# its own gofmt, and the two format differently (1.27 de-indents nested
# composite literals that 1.25 keeps). That is how a change can pass
# `make fmt-check` locally and fail CI on the same commit.
GO_TOOLCHAIN := go$(shell sed -n 's/^go[[:space:]]\{1,\}//p' go.mod | head -1)
GOFMT := $(shell GOTOOLCHAIN=$(GO_TOOLCHAIN) go env GOROOT)/bin/gofmt

fmt:
	golangci-lint fmt ./...
	$(GOFMT) -w .
	npm --prefix frontend run format

# fmt-check verifies formatting without writing, for CI. It checks gofmt
# with the pinned toolchain's own binary; the import grouping rule
# (golangci-lint's goimports, see .golangci.yml) is enforced by `make
# lint` instead, because a locally installed golangci-lint may embed a
# different Go release than go.mod pins.
fmt-check:
	@files="$$($(GOFMT) -l .)"; if [ -n "$$files" ]; then \
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

# gen-proto regenerates the execd wire protocol (protobuf). The child
# is always the same binary as the parent, and the Hello handshake
# compares ProtocolVersion, so the schema is rewritten wholesale.
#
# protoc itself is an external build dependency (brew install protobuf /
# apt-get install protobuf-compiler); protoc-gen-go is pinned here so the
# checked-in execd.pb.go cannot drift with whatever plugin is on PATH.
PROTOC_GEN_GO_VERSION := v1.36.11

gen-proto:
	@tmpdir="$$(mktemp -d)"; trap 'rm -rf "$$tmpdir"' EXIT; \
	GOBIN="$$tmpdir" go install \
		google.golang.org/protobuf/cmd/protoc-gen-go@$(PROTOC_GEN_GO_VERSION); \
	protoc --plugin=protoc-gen-go="$$tmpdir/protoc-gen-go" \
		--go_out=. --go_opt=paths=source_relative \
		internal/capabilities/execd/execd.proto

# gen-proto-check fails when execd.pb.go no longer matches execd.proto.
# It generates into a temp directory and leaves the worktree alone, so a
# host protoc that formats things differently cannot "pass" by dirtying
# the checked-in file. The protoc version comment is ignored: the plugin
# (which shapes the code) is pinned above, while protoc comes from the
# host.
gen-proto-check:
	@tmpdir="$$(mktemp -d)"; trap 'rm -rf "$$tmpdir"' EXIT; \
	GOBIN="$$tmpdir/bin" go install \
		google.golang.org/protobuf/cmd/protoc-gen-go@$(PROTOC_GEN_GO_VERSION); \
	mkdir -p "$$tmpdir/out"; \
	protoc --plugin=protoc-gen-go="$$tmpdir/bin/protoc-gen-go" \
		--go_out="$$tmpdir/out" --go_opt=paths=source_relative \
		internal/capabilities/execd/execd.proto; \
	git show :internal/capabilities/execd/execd.pb.go \
		| grep -v '^//[[:space:]]*protoc' > "$$tmpdir/want.go"; \
	grep -v '^//[[:space:]]*protoc' \
		"$$tmpdir/out/internal/capabilities/execd/execd.pb.go" > "$$tmpdir/got.go"; \
	if ! diff -u "$$tmpdir/want.go" "$$tmpdir/got.go" > "$$tmpdir/diff"; then \
		echo "execd.pb.go is out of sync with execd.proto; run 'make gen-proto'"; \
		head -40 "$$tmpdir/diff"; \
		exit 1; \
	fi

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
