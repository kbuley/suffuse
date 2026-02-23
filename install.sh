#!/usr/bin/env sh
# install.sh — download and install suffuse
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/kbuley/suffuse/main/install.sh | sh
#
# Non-interactive env overrides:
#   SUFFUSE_VERSION    specific version, e.g. v0.2.0 (default: latest)
#   SUFFUSE_BIN_DIR    override install directory
#   SUFFUSE_SCOPE      'user' or 'system' (Linux only, skips prompt)
#   SUFFUSE_NO_LOCAL   set to 1 to configure server with --no-local (relay mode)
#   SUFFUSE_NO_SERVICE set to 1 to skip service installation

set -eu

REPO="kbuley/suffuse"
BIN_NAME="suffuse"
NO_SERVICE="${SUFFUSE_NO_SERVICE:-0}"

# ── Helpers ───────────────────────────────────────────────────────────────────

info()  { printf '\033[1;34m==>\033[0m %s\n' "$*" >&2; }
ok()    { printf '\033[1;32m  ✓\033[0m %s\n' "$*" >&2; }
warn()  { printf '\033[1;33m  !\033[0m %s\n' "$*" >&2; }
die()   { printf '\033[1;31mERROR:\033[0m %s\n' "$*" >&2; exit 1; }
ask()   { printf '\033[1;33m  ?\033[0m %s ' "$*" >&2; }

need() { command -v "$1" >/dev/null 2>&1 || die "Required command not found: $1"; }

is_interactive() { [ -t 0 ] && [ -t 1 ]; }

sed_inplace() {
    if sed --version 2>/dev/null | grep -q GNU; then
        sed -i "$1" "$2"
    else
        sed -i '' "$1" "$2"
    fi
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
        x86_64|amd64)  echo amd64 ;;
        aarch64|arm64) echo arm64 ;;
        *)             die "Unsupported architecture: $(uname -m)" ;;
    esac
}

# ── Linux install scope ───────────────────────────────────────────────────────

choose_linux_scope() {
    if [ -n "${SUFFUSE_SCOPE:-}" ]; then
        case "$SUFFUSE_SCOPE" in
            user|system) echo "$SUFFUSE_SCOPE"; return ;;
            *) die "SUFFUSE_SCOPE must be 'user' or 'system'" ;;
        esac
    fi

    [ -n "${SUFFUSE_BIN_DIR:-}" ] && { echo "user"; return; }

    if ! is_interactive; then
        warn "Non-interactive install — defaulting to per-user installation."
        warn "Set SUFFUSE_SCOPE=system for a system-wide install."
        echo "user"; return
    fi

    printf '\n'
    info "How should suffuse be installed?"
    printf '\n'
    printf '  [1] Per-user   (recommended for most cases)\n'
    printf '        Binary:   ~/.local/bin/suffuse\n'
    printf '        Service:  systemd user unit — isolated to your login session\n'
    printf '        Sudo:     not required\n'
    printf '\n'
    printf '  [2] System-wide\n'
    printf '        Binary:   /usr/local/bin/suffuse\n'
    printf '        Service:  systemd system unit — shared across all users\n'
    printf '        Sudo:     required\n'
    printf '        Note:     appropriate for a relay/hub or single-user machine.\n'
    printf '                  On multi-user servers all SSH sessions share one clipboard.\n'
    printf '\n'

    while true; do
        ask "Choice [1/2] (default: 1):"
        read -r choice </dev/tty
        case "${choice:-1}" in
            1) echo "user";   return ;;
            2) echo "system"; return ;;
            *) warn "Please enter 1 or 2" ;;
        esac
    done
}

ask_no_local() {
    if [ -n "${SUFFUSE_NO_LOCAL:-}" ]; then
        case "$SUFFUSE_NO_LOCAL" in
            1|yes|true) echo "yes"; return ;;
            *)          echo "no";  return ;;
        esac
    fi

    ! is_interactive && { echo "no"; return; }

    printf '\n'
    info "Does this machine have a local display and clipboard?"
    printf '\n'
    printf '  [1] Yes — sync this machine'"'"'s clipboard with connected peers\n'
    printf '  [2] No  — headless relay only (--no-local), just route clipboard events\n'
    printf '\n'

    while true; do
        ask "Choice [1/2] (default: 1):"
        read -r choice </dev/tty
        case "${choice:-1}" in
            1) echo "no";  return ;;
            2) echo "yes"; return ;;
            *) warn "Please enter 1 or 2" ;;
        esac
    done
}

# ── Resolve paths ─────────────────────────────────────────────────────────────

bin_dir_for() {
    [ -n "${SUFFUSE_BIN_DIR:-}" ] && { echo "$SUFFUSE_BIN_DIR"; return; }
    case "$1" in
        user)   echo "${HOME}/.local/bin" ;;
        system) echo "/usr/local/bin" ;;
    esac
}

# ── Resolve version ───────────────────────────────────────────────────────────

resolve_version() {
    [ -n "${SUFFUSE_VERSION:-}" ] && { echo "$SUFFUSE_VERSION"; return; }
    curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
        | grep '"tag_name"' \
        | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/'
}

# ── Download + verify + extract ───────────────────────────────────────────────

download_binary() {
    local version="$1" os="$2" arch="$3"
    local filename="${BIN_NAME}-${os}-${arch}"
    local tmp
    tmp="$(mktemp -d)"

    info "Downloading ${filename} ${version}"
    curl -fsSL --progress-bar \
        -o "${tmp}/${filename}" \
        "https://github.com/${REPO}/releases/download/${version}/${filename}"
    curl -fsSL \
        -o "${tmp}/checksums.txt" \
        "https://github.com/${REPO}/releases/download/${version}/checksums.txt"

    info "Verifying checksum"
    local expected actual
    # checksums.txt format: "<hash>  <filename>" (two spaces)
    expected="$(awk -v f="${filename}" '$2 == f {print $1}' "${tmp}/checksums.txt")"
    [ -n "$expected" ] || die "Checksum not found for ${filename} in checksums.txt"

    if command -v sha256sum >/dev/null 2>&1; then
        actual="$(sha256sum "${tmp}/${filename}" | awk '{print $1}')"
    elif command -v shasum >/dev/null 2>&1; then
        actual="$(shasum -a 256 "${tmp}/${filename}" | awk '{print $1}')"
    else
        die "Neither sha256sum nor shasum found"
    fi
    [ "$actual" = "$expected" ] || die "Checksum mismatch
  expected: ${expected}
  got:      ${actual}"
    ok "Checksum verified"

    echo "${tmp}/${filename}"
}

# ── Install binary ────────────────────────────────────────────────────────────

install_binary() {
    local src="$1" bin_dir="$2" use_sudo="$3"
    local dst="${bin_dir}/${BIN_NAME}"

    info "Installing to ${dst}"
    if [ "$use_sudo" = "yes" ]; then
        sudo mkdir -p "$bin_dir"
        sudo cp "$src" "$dst"
        sudo chmod 755 "$dst"
    else
        mkdir -p "$bin_dir"
        cp "$src" "$dst"
        chmod 755 "$dst"
    fi
    ok "Installed ${dst}"

    case ":${PATH}:" in
        *":${bin_dir}:"*) ;;
        *) warn "${bin_dir} is not in your PATH — add to your shell profile:"
           warn "  export PATH=\"${bin_dir}:\$PATH\""
           ;;
    esac
}

# ── Service: macOS ────────────────────────────────────────────────────────────

install_service_darwin() {
    local bin_dst="$1"
    local label="dev.klb.suffuse"
    local plist_dst="${HOME}/Library/LaunchAgents/${label}.plist"
    local plist_src
    plist_src="$(mktemp)"

    curl -fsSL \
        "https://raw.githubusercontent.com/${REPO}/main/contrib/launchd/dev.klb.suffuse.plist" \
        -o "$plist_src"
    sed_inplace \
        "s|<string>/usr/local/bin/suffuse</string>|<string>${bin_dst}</string>|g" \
        "$plist_src"

    info "Installing launchd user agent"
    mkdir -p "${HOME}/Library/LaunchAgents"

    if launchctl list "$label" >/dev/null 2>&1; then
        launchctl unload "$plist_dst" 2>/dev/null || true
    fi

    cp "$plist_src" "$plist_dst"
    chmod 644 "$plist_dst"
    launchctl load "$plist_dst"
    rm -f "$plist_src"
    ok "launchd agent installed and loaded"
    printf '\n  Stop:    launchctl unload ~/Library/LaunchAgents/%s.plist\n' "$label"
    printf '  Start:   launchctl load  ~/Library/LaunchAgents/%s.plist\n' "$label"
    printf '  Logs:    tail -f /tmp/suffuse.log\n'
    printf '  Config:  ~/.config/suffuse/suffuse.toml\n\n'
}

# ── Service: Linux system ─────────────────────────────────────────────────────

install_service_linux_system() {
    local bin_dst="$1" no_local="$2"
    local unit_dir="/etc/systemd/system"
    local svc
    svc="$(mktemp)"

    curl -fsSL \
        "https://raw.githubusercontent.com/${REPO}/main/contrib/systemd/suffuse.service" \
        -o "$svc"

    local exec_start="${bin_dst} server"
    [ "$no_local" = "yes" ] && exec_start="${exec_start} --no-local"
    sed_inplace "s|ExecStart=.*|ExecStart=${exec_start}|" "$svc"

    info "Installing systemd system service"
    sudo install -m 644 "$svc" "${unit_dir}/suffuse.service"
    sudo systemctl daemon-reload
    sudo systemctl enable suffuse.service
    rm -f "$svc"
    ok "systemd system service installed and enabled"
    printf '\n  Start:   sudo systemctl start suffuse\n'
    printf '  Status:  sudo systemctl status suffuse\n'
    printf '  Logs:    journalctl -u suffuse -f\n'
    printf '  Config:  /etc/suffuse/suffuse.toml\n\n'
}

# ── Service: Linux user ───────────────────────────────────────────────────────

install_service_linux_user() {
    local bin_dst="$1"

    if ! command -v systemctl >/dev/null 2>&1; then
        warn "systemctl not found — skipping service installation."
        warn "Start manually: ${bin_dst} server"
        return
    fi

    if ! systemctl --user status >/dev/null 2>&1; then
        warn "systemd user session not available."
        warn "Start manually: ${bin_dst} server"
        warn "To enable lingering (start on boot without login):"
        warn "  sudo loginctl enable-linger $(id -un)"
        return
    fi

    local unit_dir="${HOME}/.config/systemd/user"
    local svc
    svc="$(mktemp)"

    curl -fsSL \
        "https://raw.githubusercontent.com/${REPO}/main/contrib/systemd/suffuse.service" \
        -o "$svc"

    sed_inplace "s|ExecStart=.*|ExecStart=${bin_dst} server|" "$svc"
    sed_inplace '/^ProtectSystem/d'  "$svc"
    sed_inplace '/^ProtectHome/d'    "$svc"
    sed_inplace '/^PrivateTmp/d'     "$svc"
    sed_inplace '/^ReadWritePaths/d' "$svc"
    sed_inplace 's|WantedBy=multi-user.target|WantedBy=default.target|' "$svc"

    info "Installing systemd user service"
    mkdir -p "$unit_dir"
    install -m 644 "$svc" "${unit_dir}/suffuse.service"
    systemctl --user daemon-reload
    systemctl --user enable suffuse.service
    rm -f "$svc"
    ok "systemd user service installed and enabled"
    printf '\n  Start:   systemctl --user start suffuse\n'
    printf '  Status:  systemctl --user status suffuse\n'
    printf '  Logs:    journalctl --user -u suffuse -f\n'
    printf '  Config:  ~/.config/suffuse/suffuse.toml\n\n'
}

# ── Main ──────────────────────────────────────────────────────────────────────

main() {
    need curl
    need tar

    local os arch version tmp_bin bin_dir bin_dst
    local scope="user" use_sudo="no" no_local="no"

    os="$(detect_os)"
    arch="$(detect_arch)"
    version="$(resolve_version)"

    printf '\n'
    info "suffuse ${version} — ${os}/${arch}"

    case "$os" in
        linux)
            scope="$(choose_linux_scope)"
            [ "$scope" = "system" ] && use_sudo="yes"
            [ "$scope" = "system" ] && no_local="$(ask_no_local)"
            ;;
        darwin)
            ;;
    esac

    bin_dir="$(bin_dir_for "$scope")"
    bin_dst="${bin_dir}/${BIN_NAME}"

    tmp_bin="$(download_binary "$version" "$os" "$arch")"
    install_binary "$tmp_bin" "$bin_dir" "$use_sudo"
    rm -rf "$(dirname "$tmp_bin")"

    if [ "$NO_SERVICE" = "1" ]; then
        ok "Binary installed. Skipping service setup (SUFFUSE_NO_SERVICE=1)."
        return
    fi

    case "${os}:${scope}" in
        darwin:*)     install_service_darwin        "$bin_dst" ;;
        linux:system) install_service_linux_system  "$bin_dst" "$no_local" ;;
        linux:user)   install_service_linux_user    "$bin_dst" ;;
    esac

    ok "suffuse ${version} installed successfully"
}

main "$@"
