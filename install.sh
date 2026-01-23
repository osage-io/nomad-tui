#!/usr/bin/env bash
# Install script for nomad-tui
# Usage: curl -fsSL https://raw.githubusercontent.com/osage-io/nomad-tui/main/install.sh | bash

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Configuration
REPO="osage-io/nomad-tui"
BINARY_NAME="nomad-tui"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"

# Detect OS and architecture
detect_platform() {
    local os
    local arch

    # Detect OS
    case "$(uname -s)" in
        Darwin)
            os="darwin"
            ;;
        Linux)
            os="linux"
            ;;
        *)
            echo -e "${RED}Error: Unsupported operating system: $(uname -s)${NC}"
            echo "nomad-tui is only available for macOS and Linux"
            exit 1
            ;;
    esac

    # Detect architecture
    case "$(uname -m)" in
        x86_64)
            arch="amd64"
            ;;
        amd64)
            arch="amd64"
            ;;
        arm64)
            arch="arm64"
            ;;
        aarch64)
            arch="arm64"
            ;;
        *)
            echo -e "${RED}Error: Unsupported architecture: $(uname -m)${NC}"
            echo "nomad-tui is only available for amd64 and arm64"
            exit 1
            ;;
    esac

    echo "${os}-${arch}"
}

# Get latest release version
get_latest_version() {
    local version
    version=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
    
    if [ -z "$version" ]; then
        echo -e "${RED}Error: Could not fetch latest version${NC}"
        exit 1
    fi
    
    echo "$version"
}

# Download and install
install_nomad_tui() {
    local platform
    local version
    local download_url
    local temp_dir
    
    echo -e "${GREEN}Installing nomad-tui...${NC}"
    echo ""
    
    # Detect platform
    platform=$(detect_platform)
    echo "Detected platform: $platform"
    
    # Get latest version
    version=$(get_latest_version)
    echo "Latest version: $version"
    
    # Construct download URL
    download_url="https://github.com/${REPO}/releases/download/${version}/${BINARY_NAME}-${version}-${platform}.tar.gz"
    
    # Create temporary directory
    temp_dir=$(mktemp -d)
    trap 'rm -rf "$temp_dir"' EXIT
    
    # Download archive
    echo ""
    echo "Downloading from: $download_url"
    if ! curl -fsSL "$download_url" -o "${temp_dir}/${BINARY_NAME}.tar.gz"; then
        echo -e "${RED}Error: Download failed${NC}"
        echo "URL: $download_url"
        echo ""
        echo "This may mean:"
        echo "  - No release exists for your platform ($platform)"
        echo "  - The latest release hasn't finished building yet"
        echo "  - Network connectivity issues"
        exit 1
    fi
    
    # Extract archive
    echo "Extracting archive..."
    tar -xzf "${temp_dir}/${BINARY_NAME}.tar.gz" -C "$temp_dir"
    
    # Create install directory if it doesn't exist
    if [ ! -d "$INSTALL_DIR" ]; then
        echo "Creating install directory: $INSTALL_DIR"
        mkdir -p "$INSTALL_DIR"
    fi
    
    # Install binary
    echo "Installing to: ${INSTALL_DIR}/${BINARY_NAME}"
    mv "${temp_dir}/${BINARY_NAME}-${platform}" "${INSTALL_DIR}/${BINARY_NAME}"
    chmod +x "${INSTALL_DIR}/${BINARY_NAME}"
    
    echo ""
    echo -e "${GREEN}✓ Installation complete!${NC}"
    echo ""
    echo "Binary installed to: ${INSTALL_DIR}/${BINARY_NAME}"
    echo ""
    
    # Check if install directory is in PATH
    if [[ ":$PATH:" != *":$INSTALL_DIR:"* ]]; then
        echo -e "${YELLOW}Warning: ${INSTALL_DIR} is not in your PATH${NC}"
        echo ""
        echo "Add it to your PATH by adding this to your shell profile:"
        echo "  export PATH=\"\$PATH:${INSTALL_DIR}\""
        echo ""
    fi
    
    echo "Run '${BINARY_NAME} -h' to get started!"
}

# Main
main() {
    install_nomad_tui
}

main "$@"
