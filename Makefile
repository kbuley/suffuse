BIN     := bin
MODULE  := go.klb.dev/suffuse
BINARY  := suffuse
PROTO   := proto/clip.proto
PB_OUT  := internal/clipdpb
LDFLAGS := -ldflags "-X main.Version=$(shell git describe --tags --always --dirty 2>/dev/null || echo dev)"

.PHONY: all help build proto clean tidy install \
        build-linux build-darwin build-darwin-universal build-windows build-all

# ── default: show help ─────────────────────────────────────────────────────
all: help

help:
	@echo ""
	@echo "  suffuse build targets"
	@echo ""
	@echo "  make build                  Build for the current platform"
	@echo "  make build-darwin           Build darwin/amd64 and darwin/arm64 slices"
	@echo "  make build-darwin-universal Build a fat universal binary for macOS (lipo)"
	@echo "  make build-linux            Build linux/amd64 and linux/arm64"
	@echo "  make build-windows          Build windows/amd64"
	@echo "  make build-all              Build all platforms + macOS universal"
	@echo ""
	@echo "  make proto                  Regenerate protobuf code from proto/clip.proto"
	@echo "  make install                Build and install to /usr/local/bin"
	@echo "  make tidy                   Run go mod tidy"
	@echo "  make clean                  Remove the bin/ directory"
	@echo ""

# ── protobuf codegen ───────────────────────────────────────────────────────
# Requires: protoc + protoc-gen-go
#   macOS:  brew install protobuf && go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
#   Linux:  apt install protobuf-compiler && go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
# Or use buf (https://buf.build):  buf generate
proto:
	@mkdir -p $(PB_OUT)
	protoc \
	  --proto_path=proto \
	  --go_out=$(PB_OUT) \
	  --go_opt=paths=source_relative \
	  $(PROTO)
	@echo "Generated $(PB_OUT)/clip.pb.go"

# ── current platform ──────────────────────────────────────────────────────
build:
	@mkdir -p $(BIN)
	go build $(LDFLAGS) -o $(BIN)/$(BINARY) ./cmd/suffuse

# ── macOS ──────────────────────────────────────────────────────────────────
build-darwin:
	@mkdir -p $(BIN)
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o $(BIN)/$(BINARY)-darwin-arm64 ./cmd/suffuse
	GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o $(BIN)/$(BINARY)-darwin-amd64 ./cmd/suffuse

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
build-linux:
	@mkdir -p $(BIN)
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(BIN)/$(BINARY)-linux-amd64 ./cmd/suffuse
	GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o $(BIN)/$(BINARY)-linux-arm64 ./cmd/suffuse

# ── Windows ────────────────────────────────────────────────────────────────
build-windows:
	@mkdir -p $(BIN)
	GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o $(BIN)/$(BINARY)-windows.exe ./cmd/suffuse

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
