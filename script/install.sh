#!/usr/bin/env bash

set -e

BINARY_NAME="luckfox-config"
REPO="LuckfoxTECH/luckfox-config-go"
INSTALL_DIR="/usr/bin"
INSTALL_DIR_SET=false
FORCE_VERSION=""
USE_SUDO=false

usage() {
    echo "Usage: $0 [OPTIONS]"
    echo ""
    echo "Options:"
    echo "  -v, --version VERSION        Specify version/tag to install (default: latest)"
    echo "  -r, --repo OWNER/REPO        GitHub repo (default: ${REPO})"
    echo "  -d, --install-dir DIR        Install to custom directory (default: auto)"
    echo "  -h, --help                   Show this help message"
    echo ""
}

while [[ $# -gt 0 ]]; do
    case $1 in
        -v|--version)
            FORCE_VERSION="$2"
            shift 2
            ;;
        -r|--repo)
            REPO="$2"
            shift 2
            ;;
        -d|--install-dir)
            INSTALL_DIR="$2"
            INSTALL_DIR_SET=true
            shift 2
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            echo "Unknown option: $1"
            usage
            exit 1
            ;;
    esac
done

check_dependencies() {
    local missing=""

    if ! command -v curl &> /dev/null; then
        missing="$missing curl"
    fi

    if [ -n "$missing" ]; then
        echo "Error: Missing dependencies:$missing"
        echo "Please install them first"
        exit 1
    fi
}

detect_os() {
    case "$(uname -s)" in
        Linux*)     echo "linux" ;;
        *)          echo "unsupported" ;;
    esac
}

detect_arch() {
    case "$(uname -m)" in
        aarch64|arm64)  echo "arm64" ;;
        armv7|armv7l)   echo "arm" ;;
        *)              echo "unsupported" ;;
    esac
}

get_latest_tag() {
    curl -sL --fail "https://api.github.com/repos/${REPO}/releases/latest" \
        | sed -n 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' \
        | head -n 1
}

download_binary() {
    local tag="$1"
    local arch="$2"
    local filename="${BINARY_NAME}-${arch}"
    local url="https://github.com/${REPO}/releases/download/${tag}/${filename}"

    echo "Downloading ${BINARY_NAME} ${tag} for linux-${arch}..."
    if curl -#L --fail "$url" -o "/tmp/${filename}"; then
        echo "Downloaded to /tmp/${filename}"
        return 0
    else
        echo "Error: Failed to download from ${url}"
        echo "Please check if the release asset exists for your platform"
        return 1
    fi
}

check_install_method_auto() {
    if [ "$(id -u)" = "0" ]; then
        USE_SUDO=false
        INSTALL_DIR="/usr/bin"
        return 0
    fi

    if ! command -v sudo &> /dev/null; then
        USE_SUDO=false
        INSTALL_DIR="$HOME/.local/bin"
        return 0
    fi

    if sudo -n true 2>/dev/null; then
        USE_SUDO=true
        INSTALL_DIR="/usr/bin"
        return 0
    fi

    USE_SUDO=false
    INSTALL_DIR="$HOME/.local/bin"
    return 0
}

check_install_method_custom() {
    if [ "$(id -u)" = "0" ]; then
        USE_SUDO=false
        return 0
    fi

    if [ -w "$INSTALL_DIR" ]; then
        USE_SUDO=false
        return 0
    fi

    if command -v sudo &> /dev/null && sudo -n true 2>/dev/null; then
        USE_SUDO=true
        return 0
    fi

    echo "Error: No permission to install to ${INSTALL_DIR}, and sudo is not available"
    exit 1
}

ensure_install_method() {
    if [ "$INSTALL_DIR_SET" = true ]; then
        check_install_method_custom
    else
        check_install_method_auto
    fi
}

add_to_path() {
    local bin_dir="$1"
    local shell_rc=""

    if [ "$bin_dir" = "/usr/bin" ]; then
        return 0
    fi

    case ":$PATH:" in
        *:${bin_dir}:*)
            return 0
            ;;
    esac

    local current_shell=""
    current_shell="$(ps -p $$ -o comm= 2>/dev/null || true)"

    if [ -z "$current_shell" ]; then
        if [ -n "$ZSH_VERSION" ]; then
            current_shell="zsh"
        elif [ -n "$BASH_VERSION" ]; then
            current_shell="bash"
        fi
    fi

    if [ "$current_shell" = "zsh" ]; then
        shell_rc="$HOME/.zshrc"
    elif [ "$current_shell" = "bash" ]; then
        if [ -f "$HOME/.bashrc" ]; then
            shell_rc="$HOME/.bashrc"
        elif [ -f "$HOME/.bash_profile" ]; then
            shell_rc="$HOME/.bash_profile"
        fi
    fi

    if [ -n "$shell_rc" ]; then
        if grep -q "${bin_dir}" "$shell_rc" 2>/dev/null; then
            echo "${bin_dir} is configured in ${shell_rc}, but not in current PATH"
            echo "Please run: source ${shell_rc}"
        else
            echo "" >> "$shell_rc"
            echo "export PATH=\"${bin_dir}:\$PATH\"" >> "$shell_rc"
            echo "Added ${bin_dir} to PATH in ${shell_rc}"
            echo "Please run: source ${shell_rc}"
        fi
    else
        echo "Warning: Unable to detect shell config file"
        echo "Please add the following line to your shell config file:"
        echo "  export PATH=\"${bin_dir}:\$PATH\""
    fi
}

main() {
    echo "${BINARY_NAME} Installer"
    echo "======================"
    echo ""

    check_dependencies

    local os=""
    os="$(detect_os)"
    if [ "$os" = "unsupported" ]; then
        echo "Error: Unsupported operating system: $(uname -s)"
        exit 1
    fi

    local arch=""
    arch="$(detect_arch)"
    if [ "$arch" = "unsupported" ]; then
        echo "Error: Unsupported architecture: $(uname -m) (only arm/arm64 are supported)"
        exit 1
    fi

    ensure_install_method
    local tag=""
    if [ -n "$FORCE_VERSION" ]; then
        tag="$FORCE_VERSION"
    else
        tag="$(get_latest_tag || true)"
        if [ -z "$tag" ]; then
            echo "Error: Failed to determine latest release tag from GitHub"
            echo "Please specify a version with --version"
            exit 1
        fi
    fi

    echo "Detected: ${os}-${arch}"
    echo "GitHub repo: ${REPO}"
    echo "Tag: ${tag}"
    echo "Install dir: ${INSTALL_DIR}"
    echo ""

    if ! download_binary "$tag" "$arch"; then
        if [ -n "$FORCE_VERSION" ] && [[ ! "$FORCE_VERSION" =~ ^v ]]; then
            tag="v${FORCE_VERSION}"
            download_binary "$tag" "$arch" || exit 1
        else
            exit 1
        fi
    fi

    local filename="${BINARY_NAME}-${arch}"

    if [ ! -d "$INSTALL_DIR" ]; then
        echo "Creating install directory: ${INSTALL_DIR}"
        if [ "$USE_SUDO" = true ]; then
            sudo mkdir -p "$INSTALL_DIR" || { echo "Error: Failed to create ${INSTALL_DIR}"; exit 1; }
        else
            mkdir -p "$INSTALL_DIR" || { echo "Error: Failed to create ${INSTALL_DIR}"; exit 1; }
        fi
    fi

    echo "Installing to ${INSTALL_DIR}/${BINARY_NAME}..."
    if [ "$USE_SUDO" = true ]; then
        sudo mv "/tmp/${filename}" "$INSTALL_DIR/${BINARY_NAME}" || { echo "Error: Failed to move binary"; exit 1; }
        sudo chmod +x "$INSTALL_DIR/${BINARY_NAME}" || { echo "Error: Failed to set permissions"; exit 1; }
    else
        mv "/tmp/${filename}" "$INSTALL_DIR/${BINARY_NAME}" || { echo "Error: Failed to move binary"; exit 1; }
        chmod +x "$INSTALL_DIR/${BINARY_NAME}" || { echo "Error: Failed to set permissions"; exit 1; }
    fi

    echo "Installed ${BINARY_NAME} to ${INSTALL_DIR}/${BINARY_NAME}"
    echo ""

    add_to_path "$INSTALL_DIR"

    if "${INSTALL_DIR}/${BINARY_NAME}" --version >/dev/null 2>&1; then
        echo "Version check:"
        "${INSTALL_DIR}/${BINARY_NAME}" --version || true
        echo ""
    fi

    echo "======================"
    echo "Installation complete!"
    echo ""
    echo "Next steps:"
    echo "  1. Make sure ${INSTALL_DIR} is in your PATH"
    echo "  2. Run '${BINARY_NAME} --help' to get started"
    echo ""
}

main
