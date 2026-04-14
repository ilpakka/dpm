#!/bin/bash
# dpm (Dumb Packet Manager) Server-Side Menu
# Simple text-based menu for SSH connections

VERSION="v0.0.2 beta"
BUNDLES_DIR="/home/dpm/bundles"

# Colors for better readability
RESET="\033[0m"
BOLD="\033[1m"
CYAN="\033[36m"
GREEN="\033[32m"
YELLOW="\033[33m"

clear

echo -e "${CYAN}${BOLD}"
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
echo -e "${GREEN}SecOps Tool Distribution Platform${RESET}"
echo -e "Version: ${VERSION}"
echo -e "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""
echo -e "${YELLOW}Welcome to dpm Preview Menu${RESET}"
echo "This is a demo server. For full functionality, install the local TUI:"
echo -e "${CYAN}curl -sL https://get.dpm.iskff.fi | sh${RESET}"
echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""

function show_tools() {
    echo -e "${BOLD}Available Tools:${RESET}"
    echo ""
    echo "  1. ripgrep (v14.1.0)"
    echo "     Fast line-oriented search tool (Rust)"
    echo "     Platforms: linux-amd64, linux-arm64, darwin-amd64, darwin-arm64"
    echo ""
    echo "  2. yara (v4.5.0)"
    echo "     Pattern matching for malware research (C)"
    echo "     Platforms: linux-amd64, linux-arm64, darwin-amd64, darwin-arm64"
    echo ""
    echo "  [More tools coming in future releases]"
    echo ""
}

function show_checksums() {
    echo -e "${BOLD}Bundle Checksums (SHA256):${RESET}"
    echo ""
    echo "ripgrep-linux-amd64.tar.gz:"
    echo "  [checksum will be generated during build]"
    echo ""
    echo "yara-linux-amd64.tar.gz:"
    echo "  [checksum will be generated during build]"
    echo ""
    echo "Note: All bundles are signed with Cosign/Sigstore"
    echo ""
}

function download_info() {
    echo -e "${BOLD}Download Offline Bundle:${RESET}"
    echo ""
    echo "To download a complete offline bundle, use:"
    echo -e "${CYAN}curl -o dpm-offline.tar.gz https://get.dpm.iskff.fi/bundle/offline-latest.tar.gz${RESET}"
    echo ""
    echo "Or install the local TUI for full functionality:"
    echo -e "${CYAN}curl -sL https://get.dpm.iskff.fi | sh${RESET}"
    echo ""
}

function main_menu() {
    while true; do
        echo ""
        echo -e "${BOLD}Menu:${RESET}"
        echo "  [1] Preview available tools"
        echo "  [2] Download offline bundle (info)"
        echo "  [3] Show bundle checksums"
        echo "  [4] Exit"
        echo ""
        read -p "Select [1-4]: " choice
        
        case $choice in
            1)
                echo ""
                show_tools
                ;;
            2)
                echo ""
                download_info
                ;;
            3)
                echo ""
                show_checksums
                ;;
            4)
                echo ""
                echo "Thanks for trying dpm!"
                echo "Install the full client: curl -sL https://get.dpm.iskff.fi | sh"
                echo ""
                exit 0
                ;;
            *)
                echo ""
                echo "Invalid choice. Please select 1-4."
                ;;
        esac
        
        read -p "Press Enter to continue..."
        clear
        echo -e "${CYAN}dpm.iskff.fi - Preview Menu${RESET}"
        echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    done
}

main_menu
