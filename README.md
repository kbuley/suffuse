# suffuse

![suffuse](suffuse.png)

**Copy here. Paste there.**

Shared clipboard over TCP — synchronises the system clipboard across machines,
containers, and SSH sessions.

### Why do it?

I do most of my development inside devcontainers — Neovim running in a Docker
container on my Mac. It’s a great setup until I need to copy something out of
the editor and paste it somewhere on the host, or vice versa. The clipboard
just doesn’t cross that boundary. I end up doing ridiculous things:
screenshotting text, mailing myself snippets, or just retyping things I can
already see on screen.

I looked for a solution and found nothing that wasn’t either tied to a specific
editor, required a GUI, or involved so much configuration that it wasn’t worth
it. So I built suffuse.

### Why 'suffuse'?

The name comes from the word “suffuse” — to spread through or over something.
That’s all it does: whatever you copy spreads to wherever you’re working.

### Why port 8752?

Every service on a network needs a port number, right? I decided to map the word **CLIP** to its alphabetical positions and treat the resulting sequence as coefficients in a **Base 13** expansion:

| Letter    | Alphabet Position | Calculation      | Result   |
| :-------- | :---------------- | :--------------- | :------- |
| **C**     | 3                 | $3 \times 13^3$  | 6591     |
| **L**     | 12                | $12 \times 13^2$ | 2028     |
| **I**     | 9                 | $9 \times 13^1$  | 117      |
| **P**     | 16                | $16 \times 13^0$ | 16       |
| **Total** |                   |                  | **8752** |

**The Formula:**
$$3(13^3) + 12(13^2) + 9(13^1) + 16(13^0) = 8752$$

This provides a unique, non-standard port and a cool origin story.

## Installation

### One-line install (Linux + macOS)

```sh
curl -fsSL https://raw.githubusercontent.com/kbuley/suffuse/main/install.sh | sh
```

This downloads the correct binary for your platform, verifies its checksum, installs
it to `/usr/local/bin`, and sets up a system service that starts on boot.

Options:

```sh
# Install a specific version
SUFFUSE_VERSION=v0.2.0 curl -fsSL .../install.sh | sh

# Custom install dir
SUFFUSE_BIN_DIR=~/.local/bin curl -fsSL .../install.sh | sh

# Binary only, skip service setup
SUFFUSE_NO_SERVICE=1 curl -fsSL .../install.sh | sh
```

### Upgrade

Re-run the install command. If a service is already registered it will be
reloaded automatically with the new binary — no need to reinstall the service.

```sh
curl -fsSL https://raw.githubusercontent.com/kbuley/suffuse/main/install.sh | sh
```

### Uninstall

```sh
curl -fsSL https://raw.githubusercontent.com/kbuley/suffuse/main/install.sh | sh -s -- uninstall
```

This stops and removes the service and binary. Config files are left in place.

### Windows

Download `suffuse-windows.exe` from the [latest release], rename it to
`suffuse.exe`, place it somewhere on your `PATH`, then register it as a service:

```powershell
# Run as Administrator
.\contrib\windows\install-service.ps1 -BinPath "C:\Program Files\suffuse\suffuse.exe"
Start-Service SuffuseServer
```

### Manual / from source

```sh
# Build for current platform
make build

# Install to /usr/local/bin
make install
```

Cross-compilation: `make build-darwin-universal`, `make build-linux`, `make build-windows`.

## Quick start

```sh
# Run the server (with local clipboard integration)
suffuse server

# Copy from another machine or container
echo "hello" | suffuse copy --host 192.168.1.10

# Paste on another machine or container
suffuse paste --host 192.168.1.10

# Show connected peers
suffuse status --host 192.168.1.10
```

## How it works

`suffuse server` runs on a machine with a display (or headlessly with `--no-local`
as a pure relay). Every connected peer shares a clipboard — copy on one, paste on
any other.

```
┌──────────────┐   TLS/gRPC   ┌──────────────┐
│  macOS host  │◄────────────►│  Linux VM    │
│  (server)    │              │  (client)    │
└──────────────┘              └──────────────┘
       │          HTTP/JSON (grpc-gateway)
       └─────────────────────────────────────► Neovim plugin
```

Both gRPC and HTTP/JSON are served on the same port over TLS. The Neovim plugin
connects via HTTP/JSON; the CLI uses gRPC.

### Transport security

All TCP connections use TLS with a key derived from `--token`. Same token on both
sides → handshake succeeds, traffic encrypted. Different tokens → handshake fails
immediately. If no token is set, the default passphrase `suffuse` is used —
traffic is still encrypted, but any other suffuse instance with the default can
connect. Set a custom token to restrict access to known peers.

## Configuration

Precedence (lowest → highest):

1. `/etc/suffuse/suffuse.toml`
2. `~/.config/suffuse/suffuse.toml`
3. `SUFFUSE_*` environment variables
4. CLI flags

See [`suffuse.toml.example`](suffuse.toml.example) for all options with documentation.

### Key options

| Flag / Env                                  | Default        | Description                          |
| ------------------------------------------- | -------------- | ------------------------------------ |
| `--addr` / `SUFFUSE_ADDR`                   | `0.0.0.0:8752` | Server listen address                |
| `--token` / `SUFFUSE_TOKEN`                 | `suffuse`      | Shared secret for TLS + auth         |
| `--source` / `SUFFUSE_SOURCE`               | hostname       | Name shown in peer lists             |
| `--no-local` / `SUFFUSE_NO_LOCAL`           | false          | Disable local clipboard (relay-only) |
| `--upstream-host` / `SUFFUSE_UPSTREAM_HOST` | —              | Federate with another suffuse server |
| `--upstream-port` / `SUFFUSE_UPSTREAM_PORT` | `8752`         | Upstream server port                 |

For `copy`, `paste`, `status`:

| Flag / Env                  | Default    | Description                                           |
| --------------------------- | ---------- | ----------------------------------------------------- |
| `--host` / `SUFFUSE_HOST`   | auto-probe | Server host (probes docker/podman/localhost if unset) |
| `--port` / `SUFFUSE_PORT`   | `8752`     | Server port                                           |
| `--token` / `SUFFUSE_TOKEN` | `suffuse`  | Must match the server token                           |

## Service management

### macOS (launchd)

The install script sets this up automatically. Manually:

```sh
cp contrib/launchd/dev.klb.suffuse.plist ~/Library/LaunchAgents/
launchctl load ~/Library/LaunchAgents/dev.klb.suffuse.plist

# Logs
tail -f /tmp/suffuse.log
```

### Linux (systemd)

The install script sets this up automatically. Manually:

```sh
sudo cp contrib/systemd/suffuse.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now suffuse

# Logs
journalctl -u suffuse -f
```

Customise via environment variables in the unit file or `/etc/suffuse/suffuse.toml`.

### Windows (SCM)

```powershell
# Install and start
.\contrib\windows\install-service.ps1 -Token "mysecret"
Start-Service SuffuseServer

# Logs
Get-EventLog -LogName Application -Source SuffuseServer -Newest 50
```

Config file locations:

| Scenario                   | Path                                       |
| -------------------------- | ------------------------------------------ |
| Service (SYSTEM account)   | `C:\ProgramData\suffuse\suffuse.toml`      |
| Interactive CLI (per-user) | `%APPDATA%\suffuse\suffuse.toml`           |
| Override                   | `suffuse --config C:\path\to\suffuse.toml` |

The install script creates `C:\ProgramData\suffuse\` automatically.

## Federation

Connect two suffuse servers so their clipboard events flow both ways:

```sh
# On the secondary node
suffuse server --upstream-host hub.example.com --upstream-port 8752 --token mysecret
```

Or in `suffuse.toml`:

```toml
upstream-host = "hub.example.com"
upstream-port = 8752
token         = "mysecret"
```

## Neovim plugin

See [suffuse.nvim](https://github.com/kbuley/suffuse.nvim) for the companion
Neovim plugin, which registers itself as a clipboard provider and keeps the
editor clipboard in sync via the watch stream.

## Development

### Regenerating proto

`gen/` is committed so regular builds don't require protobuf tooling. When
the schema changes:

```sh
make proto-install-tools   # first time only
make proto                 # regenerate
make proto-check           # verify gen/ is current (also runs in CI)
```

### Project layout

```
cmd/suffuse/        CLI (server, copy, paste, status)
internal/
  clip/             System clipboard backend
  federation/       Upstream federation client
  grpcservice/      ClipboardService gRPC server
  hub/              Central clipboard broker
  ipc/              Unix socket for local CLI tools
  localpeer/        Local clipboard ↔ hub bridge
  logging/          Structured logging
  tlsconf/          Deterministic TLS from passphrase
gen/suffuse/v1/     Generated protobuf / gRPC / gateway code
proto/suffuse/v1/   Proto source
contrib/
  launchd/          macOS launchd agent
  systemd/          Linux systemd unit
  windows/          Windows service installer
```

## License

GPLv3

[latest release]: https://github.com/kbuley/suffuse/releases/latest
