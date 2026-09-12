#!/bin/sh
# my installer — curl -fsSL https://my-cli.com/install.sh | sh
#
# Re-run this script at any time to update to the latest release.
set -eu

REPO="fluxinc/my-cli"
INSTALL_DIR="${MYCLI_INSTALL_DIR:-$HOME/.local/bin}"
MANIFEST_NAME="${MYCLI_MANIFEST_NAME:-}"
MANIFEST_URL="${MYCLI_MANIFEST_URL:-}"
HARNESS_NAME="${MYCLI_HARNESS:-}"
ONBOARDING="${MYCLI_ONBOARDING:-auto}"
UPDATE_PATH="${MYCLI_UPDATE_PATH:-1}"

info() { printf '  %s\n' "$@"; }
err()  { printf 'Error: %s\n' "$@" >&2; exit 1; }

usage() {
  cat <<'EOF'
Install or update My AI.

Usage:
  install.sh [--manifest NAME GIT_URL] [--harness NAME]
             [--onboarding|--no-onboarding] [--no-path-update]

Examples:
  curl -fsSL https://my-cli.com/install.sh | sh
  curl -fsSL https://my-cli.com/install.sh | sh -s -- \
    --manifest acme git@github.com:acme/acme-manifest.git

The first form launches guided onboarding whenever local onboarding is not yet
complete. The second registers an existing organization before launching the guide. Install a
supported agent harness first (Codex, Claude Code, OpenCode, Antigravity, Grok,
or Cursor), or pass --no-onboarding for an automation-only install.
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --manifest)
      [ "$#" -ge 3 ] || err "--manifest requires NAME and GIT_URL"
      MANIFEST_NAME="$2"
      MANIFEST_URL="$3"
      shift 3
      ;;
    --harness)
      [ "$#" -ge 2 ] || err "--harness requires NAME"
      HARNESS_NAME="$2"
      shift 2
      ;;
    --onboarding)
      ONBOARDING="always"
      shift
      ;;
    --no-onboarding)
      ONBOARDING="never"
      shift
      ;;
    --no-path-update)
      UPDATE_PATH="0"
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      err "Unknown option: $1 (run with --help)"
      ;;
  esac
done

if { [ -n "$MANIFEST_NAME" ] && [ -z "$MANIFEST_URL" ]; } ||
   { [ -z "$MANIFEST_NAME" ] && [ -n "$MANIFEST_URL" ]; }; then
  err "manifest name and Git URL must be provided together"
fi

# --- Detect OS ---
OS="$(uname -s)"
case "$OS" in
  Linux*)  OS="linux"  ;;
  Darwin*) OS="darwin" ;;
  *)       err "Unsupported OS: $OS" ;;
esac

# --- Detect architecture ---
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64)  ARCH="amd64" ;;
  aarch64) ARCH="arm64" ;;
  arm64)   ARCH="arm64" ;;
  *)       err "Unsupported architecture: $ARCH" ;;
esac

info "Detected platform: ${OS}/${ARCH}"

# --- Get latest release tag ---
info "Fetching latest release..."
LATEST_URL="$(curl -fsSL -o /dev/null -w '%{url_effective}' "https://github.com/${REPO}/releases/latest")"
TAG="${LATEST_URL##*/}"

if [ -z "$TAG" ]; then
  err "Could not determine latest release tag"
fi

info "Latest release: ${TAG}"

# --- Download tarball and checksums ---
VERSION="${TAG#v}"
TARBALL="my-cli_${VERSION}_${OS}_${ARCH}.tar.gz"
BASE_URL="https://github.com/${REPO}/releases/download/${TAG}"

TMPDIR="$(mktemp -d)"
trap 'rm -rf "$TMPDIR"' EXIT

info "Downloading ${TARBALL}..."
curl -fsSL "${BASE_URL}/${TARBALL}" -o "${TMPDIR}/${TARBALL}"

info "Downloading checksums..."
curl -fsSL "${BASE_URL}/checksums.txt" -o "${TMPDIR}/checksums.txt"

# --- Verify SHA256 ---
info "Verifying checksum..."
EXPECTED="$(grep "${TARBALL}" "${TMPDIR}/checksums.txt" | awk '{print $1}')"
if [ -z "$EXPECTED" ]; then
  err "Tarball ${TARBALL} not found in checksums.txt"
fi

if command -v sha256sum >/dev/null 2>&1; then
  ACTUAL="$(sha256sum "${TMPDIR}/${TARBALL}" | awk '{print $1}')"
elif command -v shasum >/dev/null 2>&1; then
  ACTUAL="$(shasum -a 256 "${TMPDIR}/${TARBALL}" | awk '{print $1}')"
else
  err "No sha256sum or shasum found — cannot verify integrity"
fi

if [ "$EXPECTED" != "$ACTUAL" ]; then
  err "Checksum mismatch!\n  expected: ${EXPECTED}\n  actual:   ${ACTUAL}"
fi

info "Checksum verified."

# --- Extract and install ---
mkdir -p "$INSTALL_DIR"
tar -xzf "${TMPDIR}/${TARBALL}" -C "$TMPDIR"
mv "${TMPDIR}/my" "${INSTALL_DIR}/my"
chmod +x "${INSTALL_DIR}/my"

info "Installed my to ${INSTALL_DIR}/my"

# --- Install bundled self-skill ---
info "Installing bundled My AI skill into existing harnesses..."
if SELF_SKILL_OUT="$("${INSTALL_DIR}/my" skills self install --all 2>&1)"; then
  if [ -n "$SELF_SKILL_OUT" ]; then
    printf '%s\n' "$SELF_SKILL_OUT" | grep -v 'harness not present' | sed 's/^/  /' || true
  fi
else
  info "Bundled My AI skill install skipped:"
  printf '%s\n' "$SELF_SKILL_OUT" | sed 's/^/  /'
fi

# --- Make the install durable on PATH ---
export PATH="${INSTALL_DIR}:$PATH"
PATH_PROFILE=""
PATH_UPDATED="0"

add_path_line() {
  profile="$1"
  if ! { [ -f "$profile" ] && grep -Fqx "$PATH_LINE" "$profile"; }; then
    mkdir -p "$(dirname "$profile")"
    printf '\n%s\n' "$PATH_LINE" >> "$profile"
    PATH_UPDATED="1"
    PATH_PROFILE="$profile"
    info "Added ${INSTALL_DIR} to PATH in ${profile}"
  fi
}

if [ "$UPDATE_PATH" != "0" ]; then
  # Keep HOME and PATH dynamic when the profile is sourced.
  # shellcheck disable=SC2016
  PATH_LINE='export PATH="$HOME/.local/bin:$PATH"'
  if [ "$INSTALL_DIR" != "$HOME/.local/bin" ]; then
    PATH_LINE="export PATH=\"${INSTALL_DIR}:\$PATH\""
  fi
  case "${SHELL:-}" in
    */zsh)  add_path_line "${ZDOTDIR:-$HOME}/.zshrc" ;;
    */bash)
      add_path_line "$HOME/.bashrc"
      # macOS login shells read .bash_profile, not .bashrc.
      if [ "$OS" = "darwin" ]; then add_path_line "$HOME/.bash_profile"; fi ;;
    */fish)
      FISH_RC="${XDG_CONFIG_HOME:-$HOME/.config}/fish/conf.d/my-cli.fish"
      if [ ! -f "$FISH_RC" ]; then
        mkdir -p "$(dirname "$FISH_RC")"
        echo "fish_add_path -g ${INSTALL_DIR}" > "$FISH_RC"
        PATH_UPDATED="1"
        PATH_PROFILE="$FISH_RC"
        info "Added ${INSTALL_DIR} to PATH in ${FISH_RC}"
      fi ;;
    *)      add_path_line "$HOME/.profile" ;;
  esac
fi

# --- Prerequisites: report, never install ---
pkg_hint() {
  if [ "$OS" = "darwin" ]; then
    if command -v brew >/dev/null 2>&1; then echo "brew install $1"; else echo "install Homebrew (https://brew.sh), then: brew install $1"; fi
  elif command -v apt-get >/dev/null 2>&1; then echo "sudo apt-get install -y $1"
  elif command -v dnf >/dev/null 2>&1; then echo "sudo dnf install -y $1"
  else echo "install $1 with your package manager"
  fi
}

echo ""
info "Checking prerequisites..."
info "Git and gh are needed for workspace setup; the onboarding assistant can help install them."
if command -v git >/dev/null 2>&1; then
  info "[ok] git"
else
  info "[todo] git is required for manifests and workspaces: $(pkg_hint git)"
fi
if command -v gh >/dev/null 2>&1; then
  if gh auth token >/dev/null 2>&1; then
    info "[ok] gh (logged in)"
  else
    info "[todo] gh is installed but not logged in. Needed for github.com manifests and mounts: gh auth login"
  fi
else
  info "[todo] gh is not installed. Needed for github.com manifests and mounts: $(pkg_hint gh), then: gh auth login"
fi
HARNESS_FOUND=""
for cmd in claude codex opencode grok cursor-agent agy; do
  if command -v "$cmd" >/dev/null 2>&1; then
    HARNESS_FOUND="${HARNESS_FOUND:+$HARNESS_FOUND, }$cmd"
  fi
done
if [ -n "$HARNESS_FOUND" ]; then
  info "[ok] agent harness: ${HARNESS_FOUND}"
else
  info "[todo] no agent harness found (Codex, Claude Code, OpenCode, Grok, Cursor, Antigravity). Install and log in to one; it guides the rest via: my onboarding"
fi

if [ -n "$MANIFEST_NAME" ]; then
  echo ""
  info "Registering organization manifest ${MANIFEST_NAME}..."
  "${INSTALL_DIR}/my" manifests add "$MANIFEST_NAME" "$MANIFEST_URL" --no-replace
fi

RUN_ONBOARDING="0"
case "$ONBOARDING" in
  always|1|true|yes) RUN_ONBOARDING="1" ;;
  never|0|false|no)  RUN_ONBOARDING="0" ;;
  auto)
    set -- onboarding --status
    if [ -n "$MANIFEST_NAME" ]; then
      set -- "$@" --manifest "$MANIFEST_NAME"
    fi
    if ! "${INSTALL_DIR}/my" "$@" >/dev/null 2>&1; then
      RUN_ONBOARDING="1"
    fi
    ;;
  *) err "MYCLI_ONBOARDING must be auto, always, or never" ;;
esac

if [ "$RUN_ONBOARDING" = "1" ]; then
  set -- onboarding --agent
  if [ -n "$MANIFEST_NAME" ]; then
    set -- "$@" --manifest "$MANIFEST_NAME"
  fi
  if [ -n "$HARNESS_NAME" ]; then
    set -- "$@" --harness "$HARNESS_NAME"
  fi
  # Preserve real terminal descriptors. /dev/tty is a controlling-terminal
  # alias, not an event-pollable device on every OS (macOS kqueue rejects it).
  # In the curl pipeline stdin belongs to the script; find the real device
  # through a terminal output descriptor instead.
  ONBOARD_TTY=""
  if [ -t 0 ]; then
    ONBOARD_TTY="inherited"
  elif command -v tty >/dev/null 2>&1; then
    # Save the descriptor before command substitution replaces stdout with a
    # pipe. Redirecting tty from fd 1 inside $(...) would inspect that pipe.
    if [ -t 1 ]; then
      exec 3<&1
      ONBOARD_TTY="$(tty <&3)" || ONBOARD_TTY=""
      exec 3<&-
    elif [ -t 2 ]; then
      exec 3<&2
      ONBOARD_TTY="$(tty <&3)" || ONBOARD_TTY=""
      exec 3<&-
    fi
  fi
  if [ "$ONBOARD_TTY" != "inherited" ] && [ -n "$ONBOARD_TTY" ] && [ ! -c "$ONBOARD_TTY" ]; then
    ONBOARD_TTY=""
  fi
  if [ -n "$ONBOARD_TTY" ]; then
    echo ""
    info "Starting guided onboarding..."
    ONBOARD_RESULT=0
    if [ "$ONBOARD_TTY" = "inherited" ]; then
      "${INSTALL_DIR}/my" "$@" || ONBOARD_RESULT=$?
    else
      "${INSTALL_DIR}/my" "$@" <"$ONBOARD_TTY" || ONBOARD_RESULT=$?
    fi
    if [ "$ONBOARD_RESULT" != "0" ]; then
      echo ""
      info "My AI is installed; the onboarding harness exited with status ${ONBOARD_RESULT}."
      info "Onboarding paused; existing setup progress is preserved."
      info "After resolving the message above, run: my onboarding${MANIFEST_NAME:+ --manifest $MANIFEST_NAME}${HARNESS_NAME:+ --harness $HARNESS_NAME}"
      info "To choose another installed agent: my onboarding${MANIFEST_NAME:+ --manifest $MANIFEST_NAME} --harness <name>"
      info "For an explicit manual walkthrough: my onboarding${MANIFEST_NAME:+ --manifest $MANIFEST_NAME} --no-agent"
    fi
  else
    echo ""
    info "No interactive terminal is available; onboarding was not started."
    info "Run: my onboarding${MANIFEST_NAME:+ --manifest $MANIFEST_NAME}"
    if [ -n "$MANIFEST_NAME" ]; then
      info "Or, without an agent: my setup --manifest ${MANIFEST_NAME}"
    fi
  fi
fi

echo ""
if [ "$PATH_UPDATED" = "1" ]; then
  info "PATH is ready for new shells. In this shell, run:"
  info "  source \"${PATH_PROFILE}\""
fi
info "Verify the completed workspace with: my doctor"
