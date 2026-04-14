#!/bin/bash
# Dumb Packet Manager (dpm) Installation Script
# Usage: curl -sL https://run.dpm.fi | sh

set -e

VERSION="v0.0.2 beta"
INSTALL_DIR="${HOME}/.dpm"
BIN_DIR="${INSTALL_DIR}/bin"
REPO_URL="https://github.com/secuirell/secuirell/releases/download"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
RESET='\033[0m'

echo -e "${CYAN}"
cat << "EOF"
     _                 
  __| |_ __  _ __ ___  
 / _` | '_ \| '_ ` _ \ 
| (_| | |_) | | | | | |
 \__,_| .__/|_| |_| |_|
      |_|              
      Dumb Packet Manager
EOF
echo -e "${RESET}"

echo -e "${GREEN}dpm Installer v${VERSION}${RESET}"
echo ""

# Detect OS
detect_os() {
    case "$(uname -s)" in
        Linux*)     echo "linux";;
        Darwin*)    echo "darwin";;
        *)          echo "unknown";;
    esac
}

# Detect Architecture
detect_arch() {
    case "$(uname -m)" in
        x86_64|amd64) echo "amd64";;
        aarch64|arm64) echo "arm64";;
        *)            echo "unknown";;
    esac
}

OS=$(detect_os)
ARCH=$(detect_arch)

echo -e "${CYAN}→ Detected platform: ${OS}-${ARCH}${RESET}"

if [ "$OS" = "unknown" ] || [ "$ARCH" = "unknown" ]; then
    echo -e "${RED}✗ Unsupported platform: ${OS}-${ARCH}${RESET}"
    echo "  Supported platforms:"
    echo "    - linux-amd64, linux-arm64"
    echo "    - darwin-amd64, darwin-arm64 (macOS)"
    exit 1
fi

echo -e "${CYAN}→ Creating installation directory: ${INSTALL_DIR}${RESET}"
mkdir -p "${BIN_DIR}"
mkdir -p "${INSTALL_DIR}/tools"
mkdir -p "${INSTALL_DIR}/profiles"

# Download binary (placeholder - would download from releases)
echo -e "${CYAN}→ Downloading dpm binary...${RESET}"
echo -e "${YELLOW}  [Note: In production, this would download from:${RESET}"
echo -e "${YELLOW}   ${REPO_URL}/v${VERSION}/dpm-${OS}-${ARCH}]${RESET}"
echo ""
echo -e "${YELLOW}  For now, this is a demo installation script.${RESET}"
echo -e "${YELLOW}  To test the TUI locally, run: ./dpm${RESET}"
echo ""

# Create stub lockfile
cat > "${INSTALL_DIR}/lockfile.json" << 'LOCKFILE'
{
  "version": "v0.0.2 beta",
  "platform": "PLATFORM",
  "arch": "ARCH",
  "tools": [],
  "last_sync": "TIMESTAMP"
}
LOCKFILE

# Replace placeholders
sed -i.bak "s/PLATFORM/${OS}/g" "${INSTALL_DIR}/lockfile.json"
sed -i.bak "s/ARCH/${ARCH}/g" "${INSTALL_DIR}/lockfile.json"
sed -i.bak "s/TIMESTAMP/$(date -u +%Y-%m-%dT%H:%M:%SZ)/g" "${INSTALL_DIR}/lockfile.json"
rm -f "${INSTALL_DIR}/lockfile.json.bak"

# Create env.sh
cat > "${INSTALL_DIR}/env.sh" << 'ENVSCRIPT'
# dpm environment setup
# Source this file to add dpm to your PATH

export DPM_HOME="$HOME/.dpm"
export PATH="$DPM_HOME/bin:$PATH"

# Bash completion (future)
# complete -C dpm dpm
ENVSCRIPT

echo -e "${GREEN}✓ Installation directory created${RESET}"
echo ""
echo -e "${CYAN}→ Setup complete!${RESET}"
echo ""
echo -e "${YELLOW}Next steps:${RESET}"
echo "  1. Add dpm to your PATH:"
echo -e "     ${CYAN}source ~/.dpm/env.sh${RESET}"
echo ""
echo "  2. Add to your shell profile (~/.bashrc or ~/.zshrc):"
echo -e "     ${CYAN}echo 'source ~/.dpm/env.sh' >> ~/.bashrc${RESET}"
echo ""
echo "  3. For demo purposes, copy the binary:"
echo -e "     ${CYAN}cp ./dpm ~/.dpm/bin/${RESET}"
echo ""
echo "  4. Start the TUI:"
echo -e "     ${CYAN}dpm${RESET}"
echo ""
echo -e "${GREEN}For more information, visit: https://dpm.fi/docs${RESET}"
