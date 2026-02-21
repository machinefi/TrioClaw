#!/bin/sh
# TrioClaw installer — downloads the latest release binary from GitHub.
#
# Usage:
#   curl -sSL https://raw.githubusercontent.com/machinefi/TrioClaw/main/install.sh | sh
#
# Options (env vars):
#   TRIOCLAW_VERSION  - specific version to install (default: latest)
#   INSTALL_DIR       - where to install (default: /usr/local/bin)

set -e

REPO="machinefi/TrioClaw"
BINARY="trioclaw"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"

# --- Detect OS and architecture ---

detect_platform() {
    OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
    ARCH="$(uname -m)"

    case "$OS" in
        darwin)  OS="darwin" ;;
        linux)   OS="linux" ;;
        mingw*|msys*|cygwin*) OS="windows" ;;
        *)
            echo "Error: unsupported OS: $OS"
            exit 1
            ;;
    esac

    case "$ARCH" in
        x86_64|amd64)  ARCH="amd64" ;;
        arm64|aarch64) ARCH="arm64" ;;
        *)
            echo "Error: unsupported architecture: $ARCH"
            exit 1
            ;;
    esac

    PLATFORM="${OS}-${ARCH}"
}

# --- Get version ---

get_version() {
    if [ -n "$TRIOCLAW_VERSION" ]; then
        VERSION="$TRIOCLAW_VERSION"
        return
    fi

    echo "Fetching latest release..."
    VERSION="$(curl -sSL -H "Accept: application/vnd.github.v3+json" \
        "https://api.github.com/repos/${REPO}/releases/latest" \
        | grep '"tag_name"' | head -1 | sed 's/.*"tag_name": *"//;s/".*//')"

    if [ -z "$VERSION" ]; then
        echo "Error: could not determine latest version."
        echo "Check https://github.com/${REPO}/releases or set TRIOCLAW_VERSION."
        exit 1
    fi
}

# --- Download and install ---

install() {
    SUFFIX=""
    if [ "$OS" = "windows" ]; then
        SUFFIX=".exe"
    fi

    FILENAME="${BINARY}-${PLATFORM}${SUFFIX}"
    URL="https://github.com/${REPO}/releases/download/${VERSION}/${FILENAME}"

    echo "Downloading ${BINARY} ${VERSION} for ${PLATFORM}..."
    echo "  ${URL}"

    TMPDIR="$(mktemp -d)"
    TMPFILE="${TMPDIR}/${BINARY}${SUFFIX}"

    HTTP_CODE="$(curl -sSL -w "%{http_code}" -o "$TMPFILE" "$URL")"

    if [ "$HTTP_CODE" != "200" ]; then
        rm -rf "$TMPDIR"
        echo ""
        echo "Error: download failed (HTTP ${HTTP_CODE})."
        echo ""
        echo "The release asset '${FILENAME}' may not exist yet."
        echo "Available releases: https://github.com/${REPO}/releases"
        echo ""
        echo "To build from source instead:"
        echo "  git clone https://github.com/${REPO}.git"
        echo "  cd TrioClaw && make build"
        exit 1
    fi

    chmod +x "$TMPFILE"

    # Install — use sudo if needed
    if [ -w "$INSTALL_DIR" ]; then
        mv "$TMPFILE" "${INSTALL_DIR}/${BINARY}${SUFFIX}"
    else
        echo "Installing to ${INSTALL_DIR} (requires sudo)..."
        sudo mv "$TMPFILE" "${INSTALL_DIR}/${BINARY}${SUFFIX}"
    fi

    rm -rf "$TMPDIR"
}

# --- Verify ---

verify() {
    if command -v "$BINARY" >/dev/null 2>&1; then
        INSTALLED="$("$BINARY" version 2>/dev/null | head -1)"
        echo ""
        echo "Installed: ${INSTALLED}"
        echo "Location:  $(command -v "$BINARY")"
        echo ""
        echo "Next steps:"
        echo "  trioclaw doctor              # check dependencies"
        echo "  trioclaw snap --analyze 'what do you see?'  # test camera + VLM"
        echo "  trioclaw pair --gateway ws://host:18789     # pair with OpenClaw"
    else
        echo ""
        echo "Installed to: ${INSTALL_DIR}/${BINARY}"
        echo ""
        echo "Make sure ${INSTALL_DIR} is in your PATH, then run:"
        echo "  trioclaw doctor"
    fi
}

# --- Main ---

main() {
    echo "TrioClaw Installer"
    echo "=================="
    echo ""

    detect_platform
    echo "Platform: ${PLATFORM}"

    get_version
    echo "Version:  ${VERSION}"
    echo ""

    install
    verify
}

main
