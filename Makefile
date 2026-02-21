BIN     := bin
MODULE  := go.klb.dev/suffuse
BINARY  := suffuse
LDFLAGS := -ldflags "-X main.Version=$(shell git describe --tags --always --dirty 2>/dev/null || echo dev)"

# Proto-related tools.
PROTO_DIR := proto
GEN_DIR   := gen

.PHONY: all help proto proto-install-tools proto-check lint vet build install tidy clean \
        build-linux build-linux-native build-linux-amd64 build-linux-arm64 \
        build-darwin build-darwin-universal build-windows build-all

# ── default: show help ─────────────────────────────────────────────────────
all: help

help:
	@echo ""
	@echo "  suffuse build targets"
	@echo ""
	@echo "  make proto                  Regenerate gRPC/gateway code from proto (requires buf)"
	@echo "  make proto-install-tools    Install buf + protoc plugins needed for proto generation"
	@echo "  make proto-check            Verify committed gen/ matches proto sources"
	@echo "  make lint                   Run go vet + staticcheck + golangci-lint (via go tool)"
	@echo "  make vet                    Run go vet only"
	@echo "  make build                  Build for the current platform"
	@echo "  make build-darwin           Build darwin/arm64 and darwin/amd64 (requires macOS + Xcode)"
	@echo "  make build-darwin-universal Build a fat universal binary for macOS (lipo)"
	@echo "  make build-linux            Build linux/amd64 + linux/arm64 via Docker"
	@echo "  make build-linux-native     Build linux/amd64 + linux/arm64 natively (requires libx11-dev)"
	@echo "  make build-linux-amd64      Build linux/amd64 only (requires libx11-dev + x86_64-linux-gnu-gcc)"
	@echo "  make build-linux-arm64      Build linux/arm64 only (requires libx11-dev + aarch64-linux-gnu-gcc)"
	@echo "  make build-windows          Build windows/amd64"
	@echo "  make build-all              Build all platforms + macOS universal"
	@echo ""
	@echo "  make install                Build and install to /usr/local/bin"
	@echo "  make tidy                   Run go mod tidy"
	@echo "  make clean                  Remove the bin/ directory"
	@echo ""

# ── proto generation ───────────────────────────────────────────────────────
# Preferred tool: buf (https://buf.build/docs/installation).
# gen/ is committed to the repo so regular builds don't require buf.
#
# If buf is not installed, run:  make proto-install-tools
proto:
	@command -v buf >/dev/null 2>&1 || \
	  (echo "buf not found. Run: make proto-install-tools  (or: brew install bufbuild/buf/buf)"; exit 1)
	@test -f buf.lock || buf dep update
	buf generate
	@echo "proto generation complete — gen/ updated."

# Install buf and the Go protoc plugins required by buf.gen.yaml.
proto-install-tools:
	@echo "Installing buf..."
	@if command -v brew >/dev/null 2>&1; then \
	  brew install bufbuild/buf/buf; \
	else \
	  curl -sSL "https://github.com/bufbuild/buf/releases/latest/download/buf-$(shell uname -s)-$(shell uname -m)" \
	    -o /usr/local/bin/buf && chmod +x /usr/local/bin/buf; \
	fi
	@echo "buf installed: $$(buf --version)"
	@echo ""
	@echo "Note: buf.gen.yaml uses remote plugins (buf.build/*) so no additional"
	@echo "local protoc plugin installation is required."

# Verify that committed gen/ is up-to-date with proto sources.
# Useful in CI to catch forgotten regeneration.
proto-check:
	@command -v buf >/dev/null 2>&1 || \
	  (echo "buf not found. Run: make proto-install-tools"; exit 1)
	@echo "Checking proto generation is up to date..."
	@tmpdir=$$(mktemp -d) && \
	  buf generate --output "$$tmpdir" && \
	  diff -rq $(GEN_DIR)/ "$$tmpdir/" && \
	  rm -rf "$$tmpdir" && \
	  echo "OK: gen/ is up to date." || \
	  (rm -rf "$$tmpdir"; echo "ERROR: gen/ is out of date. Run 'make proto' and commit the result."; exit 1)

# ── lint / vet ─────────────────────────────────────────────────────────────
vet:
	go vet ./...

lint: vet
	go tool staticcheck ./...
	go tool golangci-lint run ./...

# ── current platform ───────────────────────────────────────────────────────
build: tidy
	@test -f $(GEN_DIR)/suffuse/v1/suffuse.pb.go || \
	  (echo "ERROR: gen/ is empty. Run 'make proto' first."; exit 1)
	@mkdir -p $(BIN)
	go build $(LDFLAGS) -o $(BIN)/$(BINARY) ./cmd/suffuse

# ── macOS ──────────────────────────────────────────────────────────────────
build-darwin: tidy
	@mkdir -p $(BIN)
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=1 go build $(LDFLAGS) -o $(BIN)/$(BINARY)-darwin-arm64 ./cmd/suffuse
	GOOS=darwin GOARCH=amd64 CGO_ENABLED=1 go build $(LDFLAGS) -o $(BIN)/$(BINARY)-darwin-amd64 ./cmd/suffuse

build-darwin-universal: build-darwin
ifeq ($(shell uname),Darwin)
	lipo -create -output $(BIN)/$(BINARY)-darwin-universal \
	  $(BIN)/$(BINARY)-darwin-arm64 \
	  $(BIN)/$(BINARY)-darwin-amd64
	@echo "Universal binary: $(BIN)/$(BINARY)-darwin-universal"
	@lipo -info $(BIN)/$(BINARY)-darwin-universal
else
	@echo "Skipping universal binary (lipo requires macOS)"
endif

# ── Linux ──────────────────────────────────────────────────────────────────
# CGo is required for golang.design/x/clipboard (X11 headers).
# build-linux uses Docker so cross-compilation works from any host.
# build-linux-native runs directly on a Linux host with libx11-dev installed.
build-linux: tidy
	@echo "Building linux targets via Docker"
	docker run --rm \
	  --user root \
	  -v "$(PWD)":/src \
	  -w /src \
	  -e GOPATH=/go \
	  golang:1.25-bookworm \
	  bash -c "apt-get update -q && apt-get install -y -q libx11-dev gcc-x86-64-linux-gnu gcc-aarch64-linux-gnu && make build-linux-native"

build-linux-native: tidy
	@mkdir -p $(BIN)
	GOOS=linux GOARCH=amd64 CGO_ENABLED=1 CC=x86_64-linux-gnu-gcc go build $(LDFLAGS) -o $(BIN)/$(BINARY)-linux-amd64 ./cmd/suffuse
	GOOS=linux GOARCH=arm64 CGO_ENABLED=1 CC=aarch64-linux-gnu-gcc go build $(LDFLAGS) -o $(BIN)/$(BINARY)-linux-arm64 ./cmd/suffuse

build-linux-amd64: tidy
	@mkdir -p $(BIN)
	GOOS=linux GOARCH=amd64 CGO_ENABLED=1 CC=x86_64-linux-gnu-gcc go build $(LDFLAGS) -o $(BIN)/$(BINARY)-linux-amd64 ./cmd/suffuse

build-linux-arm64: tidy
	@mkdir -p $(BIN)
	GOOS=linux GOARCH=arm64 CGO_ENABLED=1 CC=aarch64-linux-gnu-gcc go build $(LDFLAGS) -o $(BIN)/$(BINARY)-linux-arm64 ./cmd/suffuse

# ── Windows ────────────────────────────────────────────────────────────────
build-windows: tidy
	@mkdir -p $(BIN)
	GOOS=windows GOARCH=amd64 CGO_ENABLED=1 go build $(LDFLAGS) -o $(BIN)/$(BINARY)-windows.exe ./cmd/suffuse

# ── all platforms ──────────────────────────────────────────────────────────
build-all: build-darwin-universal build-linux build-windows

# ── install ────────────────────────────────────────────────────────────────
install: build
	install -m 755 $(BIN)/$(BINARY) /usr/local/bin/$(BINARY)

# ── maintenance ────────────────────────────────────────────────────────────
tidy:
	go mod tidy

clean:
	rm -rf $(BIN)
