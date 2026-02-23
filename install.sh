#!/usr/bin/env sh
# install.sh — download and install suffuse
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/kbuley/suffuse/main/install.sh | sh
#
# Options (env vars):
#   SUFFUSE_VERSION   specific version to install, e.g. v0.2.0 (default: latest)
#   SUFFUSE_BIN_DIR   where to install the binary (default: /usr/local/bin)
#   SUFFUSE_NO_SERVICE  set to 1 to skip service installation
#
# What this does:
#   1. Detects OS and architecture
#   2. Downloads the binary from GitHub Releases
#   3. Verifies the SHA-256 checksum
#   4. Installs the binary to SUFFUSE_BIN_DIR
#   5. Installs and enables the system service (unless SUFFUSE_NO_SERVICE=1)

set -eu

REPO="kbuley/suffuse"
BIN_NAME="suffuse"
BIN_DIR="${SUFFUSE_BIN_DIR:-/usr/local/bin}"
NO_SERVICE="${SUFFUSE_NO_SERVICE:-0}"

# ── Helpers ───────────────────────────────────────────────────────────────────

info()  { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
ok()    { printf '\033[1;32m  ✓\033[0m %s\n' "$*"; }
die()   { printf '\033[1;31mERROR:\033[0m %s\n' "$*" >&2; exit 1; }

need() {
    command -v "$1" >/dev/null 2>&1 || die "Required command not found: $1"
}

# ── Detect platform ───────────────────────────────────────────────────────────

detect_os() {
    case "$(uname -s)" in
        Linux)  echo linux ;;
        Darwin) echo darwin ;;
        *)      die "Unsupported OS: $(uname -s)" ;;
    esac
}

detect_arch() {
    case "$(uname -m)" in
        x86_64|amd64) echo amd64 ;;
        aarch64|arm64) echo arm64 ;;
        *) die "Unsupported architecture: $(uname -m)" ;;
    esac
}

# ── Resolve version ───────────────────────────────────────────────────────────

resolve_version() {
    if [ -n "${SUFFUSE_VERSION:-}" ]; then
        echo "$SUFFUSE_VERSION"
        return
    fi
    need curl
    curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
        | grep '"tag_name"' \
        | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/'
}

# ── Download + verify ─────────────────────────────────────────────────────────

download_binary() {
    local version="$1" os="$2" arch="$3"
    local filename="${BIN_NAME}-${os}-${arch}"
    local url="https://github.com/${REPO}/releases/download/${version}/${filename}"
    local checksums_url="https://github.com/${REPO}/releases/download/${version}/checksums.txt"
    local tmp
    tmp="$(mktemp -d)"

    info "Downloading ${filename} ${version}"
    curl -fsSL --progress-bar -o "${tmp}/${filename}" "$url"
    curl -fsSL -o "${tmp}/checksums.txt" "$checksums_url"

    info "Verifying checksum"
    # Extract the expected checksum for this file
    local expected
    expected="$(grep "${filename}" "${tmp}/checksums.txt" | awk '{print $1}')"
    if [ -z "$expected" ]; then
        die "Checksum not found for ${filename} in checksums.txt"
    fi

    # Verify using sha256sum or shasum depending on what's available
    if command -v sha256sum >/dev/null 2>&1; then
        echo "${expected}  ${tmp}/${filename}" | sha256sum --check --quiet \
            || die "Checksum verification failed"
    elif command -v shasum >/dev/null 2>&1; then
        echo "${expected}  ${tmp}/${filename}" | shasum -a 256 --check --quiet \
            || die "Checksum verification failed"
    else
        die "Neither sha256sum nor shasum found — cannot verify checksum"
    fi
    ok "Checksum verified"

    echo "${tmp}/${filename}"
}

# ── Install binary ────────────────────────────────────────────────────────────

install_binary() {
    local src="$1"
    local dst="${BIN_DIR}/${BIN_NAME}"

    info "Installing to ${dst}"
    if [ -w "$BIN_DIR" ]; then
        install -m 755 "$src" "$dst"
    else
        sudo install -m 755 "$src" "$dst"
    fi
    ok "Installed ${dst}"
}

# ── Service installation ──────────────────────────────────────────────────────

install_service_linux() {
    local bin_dst="${BIN_DIR}/${BIN_NAME}"
    local service_src unit_dir

    # Download the service unit from the repo
    unit_dir="/etc/systemd/system"
    service_src="$(mktemp)"
    curl -fsSL \
        "https://raw.githubusercontent.com/${REPO}/main/contrib/systemd/suffuse.service" \
        -o "$service_src"

    # Patch ExecStart to use the actual install path
    sed -i "s|ExecStart=.*|ExecStart=${bin_dst} server|" "$service_src"

    info "Installing systemd service"
    if [ -w "$unit_dir" ]; then
        install -m 644 "$service_src" "${unit_dir}/suffuse.service"
        systemctl daemon-reload
        systemctl enable suffuse.service
    else
        sudo install -m 644 "$service_src" "${unit_dir}/suffuse.service"
        sudo systemctl daemon-reload
        sudo systemctl enable suffuse.service
    fi
    rm -f "$service_src"
    ok "systemd service installed and enabled"
    printf '\n  Start with:   sudo systemctl start suffuse\n'
    printf '  Status:       sudo systemctl status suffuse\n'
    printf '  Logs:         journalctl -u suffuse -f\n'
    printf '  Config:       /etc/suffuse/suffuse.toml\n\n'
}

install_service_darwin() {
    local bin_dst="${BIN_DIR}/${BIN_NAME}"
    local plist_src plist_dst label

    label="dev.klb.suffuse"
    plist_dst="${HOME}/Library/LaunchAgents/${label}.plist"
    plist_src="$(mktemp)"

    curl -fsSL \
        "https://raw.githubusercontent.com/${REPO}/main/contrib/launchd/dev.klb.suffuse.plist" \
        -o "$plist_src"

    # Patch the binary path in ProgramArguments
    sed -i '' "s|<string>/usr/local/bin/suffuse</string>|<string>${bin_dst}</string>|g" \
        "$plist_src"

    info "Installing launchd agent"
    mkdir -p "${HOME}/Library/LaunchAgents"

    # Unload existing agent if present
    if launchctl list "$label" >/dev/null 2>&1; then
        launchctl unload "$plist_dst" 2>/dev/null || true
    fi

    install -m 644 "$plist_src" "$plist_dst"
    launchctl load "$plist_dst"
    rm -f "$plist_src"
    ok "launchd agent installed and loaded"
    printf '\n  Start/stop:   launchctl load/unload ~/Library/LaunchAgents/%s.plist\n' "$label"
    printf '  Logs:         tail -f /tmp/suffuse.log\n'
    printf '  Config:       ~/.config/suffuse/suffuse.toml\n\n'
}

# ── Main ──────────────────────────────────────────────────────────────────────

main() {
    need curl

    local os arch version tmp_bin

    os="$(detect_os)"
    arch="$(detect_arch)"
    version="$(resolve_version)"

    info "suffuse ${version} — ${os}/${arch}"

    tmp_bin="$(download_binary "$version" "$os" "$arch")"
    install_binary "$tmp_bin"
    rm -rf "$(dirname "$tmp_bin")"

    if [ "$NO_SERVICE" = "1" ]; then
        ok "Binary installed. Skipping service installation (SUFFUSE_NO_SERVICE=1)"
        return
    fi

    case "$os" in
        linux)  install_service_linux ;;
        darwin) install_service_darwin ;;
    esac

    ok "suffuse ${version} installed successfully"
}

main "$@"
