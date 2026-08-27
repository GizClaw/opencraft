.PHONY: all fmt fmt-check lint test build-linux

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

test:
	go test ./...

# build-linux produces the desktop binary for Linux (requires the
# GTK/WebKit development packages; see .github/workflows/ci.yml).
build-linux:
	wails build -platform linux/amd64 -tags webkit2_41
