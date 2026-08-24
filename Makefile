.PHONY: all fmt lint test build-linux

all: fmt lint test

fmt:
	go fmt ./...

lint:
	golangci-lint run ./...
	staticcheck ./...

test:
	go test ./...

# build-linux produces the desktop binary for Linux (requires the
# GTK/WebKit development packages; see .github/workflows/ci.yml).
build-linux:
	wails build -platform linux/amd64
