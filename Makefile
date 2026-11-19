# LPS build tasks. On Windows use Git Bash/WSL for make, or run the commands directly.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: all web build build-all test run clean

all: web build

web:
	cd web && npm ci && npm run build

build:
	go build -ldflags "$(LDFLAGS)" -o bin/lpsd ./cmd/lpsd

# Cross-compile release binaries (pure Go, no CGO needed).
build-all:
	GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o bin/lpsd-windows-amd64.exe ./cmd/lpsd
	GOOS=linux   GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o bin/lpsd-linux-amd64 ./cmd/lpsd
	GOOS=linux   GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o bin/lpsd-linux-arm64 ./cmd/lpsd
	GOOS=darwin  GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o bin/lpsd-darwin-arm64 ./cmd/lpsd
	GOOS=darwin  GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o bin/lpsd-darwin-amd64 ./cmd/lpsd

test:
	go vet ./...
	go test ./...
	cd web && npm run typecheck

run: build
	./bin/lpsd serve

clean:
	rm -rf bin
