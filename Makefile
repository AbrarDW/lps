# LPS build tasks. On Windows use Git Bash/WSL for make, or run the commands directly.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: all web build build-all deb pkg test run screenshots clean

all: web build

web:
	cd web && npm ci && npm run build

build:
	go build -ldflags "$(LDFLAGS)" -o bin/lpsd ./cmd/lpsd

# Cross-compile release binaries (pure Go, no CGO needed).
build-all:
	GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o bin/lpsd-windows-amd64.exe ./cmd/lpsd
	GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS) -H windowsgui" -o bin/lps-windows-amd64.exe ./cmd/lps
	GOOS=linux   GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o bin/lpsd-linux-amd64 ./cmd/lpsd
	GOOS=linux   GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o bin/lpsd-linux-arm64 ./cmd/lpsd
	GOOS=darwin  GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o bin/lpsd-darwin-arm64 ./cmd/lpsd
	GOOS=darwin  GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o bin/lpsd-darwin-amd64 ./cmd/lpsd

# Debian/Ubuntu packages (needs dpkg-deb): dist/lps_<version>_<arch>.deb.
deb:
	for arch in amd64 arm64; do \
		CGO_ENABLED=0 GOOS=linux GOARCH=$$arch go build -ldflags "$(LDFLAGS)" -o bin/lpsd-linux-$$arch ./cmd/lpsd && \
		installer/linux/build-deb.sh bin/lpsd-linux-$$arch $$arch "$(VERSION)" dist || exit 1; \
	done

# macOS installer (run on macOS): dist/LPS-<version>-macOS.pkg, universal.
pkg:
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o bin/lpsd-darwin-arm64 ./cmd/lpsd
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o bin/lpsd-darwin-amd64 ./cmd/lpsd
	lipo -create -output bin/lpsd-darwin-universal bin/lpsd-darwin-arm64 bin/lpsd-darwin-amd64
	installer/macos/build-pkg.sh bin/lpsd-darwin-universal "$(VERSION)" dist

test:
	go vet ./...
	go test ./...
	cd web && npm run typecheck

run: build
	./bin/lpsd serve

# Retake docs/images from a running LPS with demo data (see the script).
screenshots:
	scripts/screenshots/run.sh

clean:
	rm -rf bin dist
