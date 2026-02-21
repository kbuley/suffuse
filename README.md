# suffuse

Shared clipboard over TCP — synchronises the system clipboard across machines,
containers, and SSH sessions using gRPC.

## How it works

Run `suffuse server` on the host that owns a display (or as a pure relay with
`--no-local`). Run `suffuse client` on each container or remote machine. Every
copy on any connected peer is immediately pushed to all others.

Both **gRPC** (binary) and **HTTP/JSON** (grpc-gateway) are served on a single
port via cmux. Go clients use gRPC directly; Neovim plugins or other language
clients can use plain HTTP against the same address.

### Wire protocol

```
┌──────────────┐   gRPC stream   ┌──────────────┐   local clipboard
│  macOS host  │◄────────────────►│  Linux VM    │   (golang.design/x/clipboard)
│  (server)    │                 │  (client)    │
└──────────────┘                 └──────────────┘
       │                 HTTP/JSON (grpc-gateway)
       └──────────────────────────────────────────► Neovim plugin / scripts
```

### RPCs

| Method   | Transport         | Description                                      |
|----------|-------------------|--------------------------------------------------|
| `Copy`   | Unary (POST /v1/copy)   | Publish clipboard content to all peers     |
| `Paste`  | Unary (POST /v1/paste)  | Retrieve latest clipboard, MIME-filtered   |
| `Watch`  | Server-stream (GET /v1/watch) | Push events as clipboard changes     |
| `Status` | Unary (GET /v1/status) | List connected peers                      |

`Watch` accepts two client hints:

- `accept` — MIME filter applied server-side before sending (e.g. `["text/plain"]`
  so a Neovim plugin never receives images).
- `metadata_only` — if true, only `available_types` is populated; the client
  calls `Paste` on demand. Useful when the receiver only occasionally needs the
  content.

### Authentication

A shared secret is passed as a gRPC/HTTP `Authorization: Bearer <token>` header.
Set `token` in `suffuse.toml` or via `SUFFUSE_TOKEN` / `--token`.

## Installation

```bash
# Build current platform
make build

# Install to /usr/local/bin
make install
```

Cross-compilation targets: `make build-darwin`, `make build-darwin-universal`,
`make build-linux` (Docker), `make build-windows`.

## Configuration

Config is loaded from (lowest → highest precedence):

1. `/etc/suffuse/suffuse.toml`
2. `~/.config/suffuse/suffuse.toml`
3. `--config /path/to/file`
4. `SUFFUSE_*` environment variables
5. CLI flags

See `suffuse.toml.example` for all options.

### Quick start

```bash
# Server (with local clipboard)
suffuse server --addr 0.0.0.0:8752 --token mysecret

# Client on another machine / container
suffuse client --server host:8752 --token mysecret

# CLI copy/paste (like pbcopy / pbpaste)
echo "hello" | suffuse copy --server host:8752 --token mysecret
suffuse paste --server host:8752 --token mysecret

# Peer list
suffuse status --server host:8752 --token mysecret
```

### Neovim (HTTP/JSON)

```lua
-- Copy selection to suffuse
vim.keymap.set("v", "<leader>yy", function()
  local text = vim.fn.getreg('"')
  vim.fn.system({
    "curl", "-s", "-X", "POST",
    "http://localhost:8752/v1/copy",
    "-H", "Content-Type: application/json",
    "-H", "Authorization: Bearer mysecret",
    "-d", vim.json.encode({
      source = vim.fn.hostname(),
      clipboard = "default",
      items = {{ mime = "text/plain", data = vim.base64.encode(text) }},
    }),
  })
end)
```

`bytes` fields in the JSON gateway are automatically base64-encoded/decoded by
grpc-gateway — `data` must be sent as a base64 string and is returned as one.

## Development

### Regenerating proto

The generated files in `gen/` are committed so regular builds don't require
protobuf tooling. When the schema changes, regenerate with:

```bash
# Install buf (first time only)
make proto-install-tools

# Regenerate
make proto

# Verify gen/ is current (also runs in CI)
make proto-check
```

The proto source lives at `proto/suffuse/v1/suffuse.proto`. `buf.gen.yaml`
uses buf's remote plugin registry — no local `protoc-gen-go*` binaries needed.

### Project layout

```
cmd/suffuse/          CLI commands (server, client, copy, paste, status)
internal/
  clip/               System clipboard backend (golang.design/x/clipboard)
  grpcservice/        ClipboardService gRPC server implementation
  hub/                Central clipboard broker (transport-agnostic)
  localpeer/          hub.Peer that owns the server's local clipboard
  logging/            Structured logging setup (slog + tinter)
gen/suffuse/v1/       Generated protobuf + gRPC + gateway Go code
proto/suffuse/v1/     Proto source (suffuse.proto)
contrib/
  launchd/            macOS launchd plists (server + client)
  systemd/            Linux systemd units
  windows/            Windows service wrappers
```

## Service management

### macOS (launchd)

```bash
cp contrib/launchd/dev.klb.suffuse.server.plist ~/Library/LaunchAgents/
launchctl load ~/Library/LaunchAgents/dev.klb.suffuse.server.plist
```

### Linux (systemd)

See `contrib/systemd/` for unit files.

## License

MIT
