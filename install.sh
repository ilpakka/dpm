#!/bin/sh
# DPM installer - https://dpm.fi
# Usage: curl -sL https://dpm.fi/install.sh | sh
set -e

BASE_URL="${DPM_BASE_URL:-https://github.com/ilpakka/dpm/releases/latest/download}"
INSTALL_DIR="${DPM_INSTALL_DIR:-$HOME/.local/bin}"
CACHE_ROOT="${XDG_CACHE_HOME:-$HOME/.cache}/dpm/bootstrap"
RETRIES="${DPM_INSTALL_RETRIES:-6}"

die() {
  echo "Error: $*" >&2
  exit 1
}

log() {
  printf '%s\n' "$*" >&2
}

[ -n "$HOME" ] || die "HOME is not set"

# Detect platform
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$OS" in
  linux|darwin) ;;
  *) die "Unsupported OS: $OS" ;;
esac

ARCH=$(uname -m)
case "$ARCH" in
  x86_64|amd64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) die "Unsupported architecture: $ARCH" ;;
esac

PLATFORM="${OS}-${ARCH}"
CACHE_DIR="${CACHE_ROOT}/${PLATFORM}"
INSTALL_TMP=""

cleanup() {
  if [ -n "$INSTALL_TMP" ] && [ -f "$INSTALL_TMP" ]; then
    rm -f "$INSTALL_TMP"
  fi
}
trap cleanup EXIT HUP INT TERM

require_tools() {
  if ! command -v curl >/dev/null 2>&1 && ! command -v wget >/dev/null 2>&1; then
    die "curl or wget required"
  fi
  if ! command -v sha256sum >/dev/null 2>&1 && ! command -v shasum >/dev/null 2>&1 && ! command -v openssl >/dev/null 2>&1; then
    die "sha256sum, shasum, or openssl required for checksum verification"
  fi
}

retry() {
  max_attempts=$1
  shift
  attempt=1
  delay=1

  while [ "$attempt" -le "$max_attempts" ]; do
    if "$@"; then
      return 0
    fi

    if [ "$attempt" -lt "$max_attempts" ]; then
      log "Attempt ${attempt}/${max_attempts} failed; retrying in ${delay}s..."
      sleep "$delay"
      delay=$((delay * 2))
      if [ "$delay" -gt 8 ]; then
        delay=8
      fi
    fi
    attempt=$((attempt + 1))
  done

  return 1
}

download_plain_once() {
  url=$1
  dest=$2

  if command -v curl >/dev/null 2>&1; then
    if curl -fsSL --connect-timeout 15 -o "$dest" "$url"; then
      return 0
    else
      return $?
    fi
  fi

  if wget -q -O "$dest" "$url"; then
    return 0
  else
    return $?
  fi
}

download_binary_once() {
  url=$1
  dest=$2

  if command -v curl >/dev/null 2>&1; then
    if [ -s "$dest" ]; then
      if curl -fsSL --connect-timeout 15 -C - -o "$dest" "$url"; then
        return 0
      fi
      status=$?
      if [ "$status" -eq 33 ]; then
        log "Remote did not resume ${dest##*/}; restarting download."
        rm -f "$dest"
        if curl -fsSL --connect-timeout 15 -o "$dest" "$url"; then
          return 0
        else
          return $?
        fi
      fi
      return "$status"
    fi

    if curl -fsSL --connect-timeout 15 -o "$dest" "$url"; then
      return 0
    else
      return $?
    fi
  fi

  if wget -q -c -O "$dest" "$url"; then
    return 0
  else
    return $?
  fi
}

valid_sha256() {
  hash=$1
  [ ${#hash} -eq 64 ] || return 1
  case "$hash" in
    *[!0123456789abcdefABCDEF]*) return 1 ;;
  esac
  return 0
}

sha256_file() {
  path=$1

  if command -v sha256sum >/dev/null 2>&1; then
    set -- $(sha256sum "$path")
    printf '%s\n' "$1"
    return 0
  fi

  if command -v shasum >/dev/null 2>&1; then
    set -- $(shasum -a 256 "$path")
    printf '%s\n' "$1"
    return 0
  fi

  openssl dgst -sha256 "$path" | awk '{print $NF}'
}

verify_sha256() {
  path=$1
  expected=$2
  actual=$(sha256_file "$path" | tr '[:upper:]' '[:lower:]')
  expected=$(printf '%s' "$expected" | tr '[:upper:]' '[:lower:]')
  [ "$actual" = "$expected" ]
}

fetch_expected_checksum() {
  bin=$1
  attempts=$2
  checksum_url="${BASE_URL}/${bin}-${PLATFORM}.sha256"
  checksum_tmp="${CACHE_DIR}/.${bin}.sha256.$$"

  rm -f "$checksum_tmp"
  if ! retry "$attempts" download_plain_once "$checksum_url" "$checksum_tmp"; then
    rm -f "$checksum_tmp"
    return 1
  fi

  IFS=' 	' read -r expected _ < "$checksum_tmp" || {
    rm -f "$checksum_tmp"
    return 1
  }
  rm -f "$checksum_tmp"

  expected=$(printf '%s' "$expected" | tr '[:upper:]' '[:lower:]')
  if ! valid_sha256 "$expected"; then
    die "Invalid checksum from ${checksum_url}"
  fi

  printf '%s\n' "$expected"
}

ensure_cached_binary() {
  bin=$1
  expected=$2
  cache_path="${CACHE_DIR}/${bin}"
  part_path="${CACHE_DIR}/${bin}.part"
  bin_url="${BASE_URL}/${bin}-${PLATFORM}"

  if [ -f "$cache_path" ]; then
    if verify_sha256 "$cache_path" "$expected"; then
      log "Using verified cache: ${cache_path}"
      printf '%s\n' "$cache_path"
      return 0
    fi
    log "Cached ${bin} failed checksum; redownloading."
    rm -f "$cache_path"
  fi

  log "Downloading ${bin}..."
  if ! retry "$RETRIES" download_binary_once "$bin_url" "$part_path"; then
    die "Download failed for ${bin}; rerun installer to resume ${part_path}"
  fi

  if ! verify_sha256 "$part_path" "$expected"; then
    log "Checksum mismatch for ${bin}; restarting download once."
    rm -f "$part_path"
    if ! retry "$RETRIES" download_binary_once "$bin_url" "$part_path"; then
      die "Download failed for ${bin} after checksum retry"
    fi
    if ! verify_sha256 "$part_path" "$expected"; then
      rm -f "$part_path"
      die "Checksum mismatch for ${bin}"
    fi
  fi

  mv -f "$part_path" "$cache_path"
  chmod 0644 "$cache_path"
  log "Verified cache: ${cache_path}"
  printf '%s\n' "$cache_path"
}

install_cached_binary() {
  bin=$1
  cache_path=$2
  expected=$3
  target="${INSTALL_DIR}/${bin}"

  INSTALL_TMP=$(mktemp "${INSTALL_DIR}/.${bin}.XXXXXX") || die "Could not create install staging file"
  cp "$cache_path" "$INSTALL_TMP"
  chmod 0755 "$INSTALL_TMP"

  if ! verify_sha256 "$INSTALL_TMP" "$expected"; then
    rm -f "$INSTALL_TMP"
    INSTALL_TMP=""
    die "Checksum mismatch while staging ${bin}"
  fi

  mv -f "$INSTALL_TMP" "$target"
  INSTALL_TMP=""
  log "Installed ${bin} -> ${target}"
}

install_one() {
  bin=$1
  required=$2
  target="${INSTALL_DIR}/${bin}"
  checksum_attempts=$RETRIES

  if [ "$required" != "yes" ]; then
    checksum_attempts=1
  fi

  if ! expected=$(fetch_expected_checksum "$bin" "$checksum_attempts"); then
    if [ "$required" = "yes" ]; then
      die "Could not fetch checksum for ${bin} on ${PLATFORM}"
    fi
    log "Skipping optional ${bin}: not published for ${PLATFORM}."
    return 0
  fi

  if [ -f "$target" ] && verify_sha256 "$target" "$expected"; then
    chmod 0755 "$target"
    log "${bin} already installed and verified."
    return 0
  fi

  cache_path=$(ensure_cached_binary "$bin" "$expected")
  install_cached_binary "$bin" "$cache_path" "$expected"
}

require_tools

log "dpm installer"
log "Platform: ${PLATFORM}"
log "Source: ${BASE_URL}"
log "Install dir: ${INSTALL_DIR}"
log "Cache dir: ${CACHE_DIR}"
log ""

mkdir -p "$INSTALL_DIR" "$CACHE_DIR"

install_one dpm yes
install_one dpm-tui no

# Check PATH
case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *)
    log ""
    log "Add to your PATH:"
    log "  export PATH=\"\$HOME/.local/bin:\$PATH\""
    log ""
    log "Or add that line to ~/.bashrc / ~/.zshrc"
    ;;
esac

log ""
log "Installed DPM to ${INSTALL_DIR}/"
log "Run: dpm list --all"
