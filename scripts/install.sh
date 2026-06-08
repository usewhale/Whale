#!/bin/sh

set -eu

REPO_SLUG="${REPO_SLUG:-usewhale/DeepSeek-Code-Whale}"
OWNER="${OWNER:-}"
REPO="${REPO:-}"
VERSION="${VERSION:-latest}"
BIN_DIR="${BIN_DIR:-}"

if [ -n "$OWNER" ] && [ -n "$REPO" ]; then
  REPO_SLUG="$OWNER/$REPO"
fi

detect_os() {
  case "$(uname -s)" in
    Darwin) printf '%s\n' "darwin" ;;
    Linux) printf '%s\n' "linux" ;;
    *)
      printf '%s\n' "unsupported"
      return 1
      ;;
  esac
}

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64) printf '%s\n' "amd64" ;;
    arm64|aarch64) printf '%s\n' "arm64" ;;
    *)
      printf '%s\n' "unsupported"
      return 1
      ;;
  esac
}

sha256_cmd() {
  if command -v sha256sum >/dev/null 2>&1; then
    printf '%s\n' "sha256sum"
    return 0
  fi
  if command -v shasum >/dev/null 2>&1; then
    printf '%s\n' "shasum -a 256"
    return 0
  fi
  return 1
}

resolve_version() {
  if [ "$VERSION" != "latest" ]; then
    printf '%s\n' "$VERSION"
    return 0
  fi
  api_url="https://api.github.com/repos/$REPO_SLUG/releases/latest"
  release_json="$(curl -fsSL "$api_url")"
  tag="$(printf '%s\n' "$release_json" | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)"
  if [ -z "$tag" ]; then
    printf '%s\n' "failed to resolve latest release tag from $api_url" >&2
    return 1
  fi
  printf '%s\n' "$tag"
}

default_bin_dir() {
  if [ -w /usr/local/bin ]; then
    printf '%s\n' "/usr/local/bin"
    return 0
  fi
  printf '%s\n' "$HOME/.local/bin"
}

verify_checksum() {
  file="$1"
  expected="$2"
  cmd="$(sha256_cmd)" || {
    printf '%s\n' "whale install: need sha256sum or shasum to verify downloads" >&2
    return 1
  }
  actual="$(sh -c "$cmd \"$file\"" | awk '{print $1}')"
  if [ "$actual" != "$expected" ]; then
    printf '%s\n' "whale install: checksum mismatch for $(basename "$file")" >&2
    printf '%s\n' "expected: $expected" >&2
    printf '%s\n' "actual:   $actual" >&2
    return 1
  fi
}

download_asset() {
  url="$1"
  dst="$2"
  if [ -t 2 ]; then
    curl -fL "$url" -o "$dst"
    return $?
  fi
  curl -fsL "$url" -o "$dst"
}

asset_exists() {
  url="$1"
  curl -fsIL "$url" >/dev/null 2>&1
}

install_binary() {
  src="$1"
  dst="$2"
  mkdir -p "$dst"
  target="$dst/whale"
  tmp_target="$(mktemp "$dst/.whale.tmp.XXXXXX")"
  cp "$src" "$tmp_target"
  chmod 0755 "$tmp_target"
  mv -f "$tmp_target" "$target"
  printf '%s\n' "$target"
}

install_runtime() {
  src="$1"
  dst="$2"
  target="$dst/runtime"
  if [ ! -d "$src" ]; then
    return 0
  fi
  tmp_target="$(mktemp -d "$dst/.whale-runtime.tmp.XXXXXX")"
  cp -R "$src/." "$tmp_target/"
  find "$tmp_target" -type f -name zsh -exec chmod 0755 {} \;
  rm -rf "$target"
  mv "$tmp_target" "$target"
}

warn_if_shadowed() {
  target="$1"
  hash -r 2>/dev/null || true
  resolved="$(command -v whale 2>/dev/null || true)"
  if [ -n "$resolved" ] && [ "$resolved" != "$target" ]; then
    printf '\n%s\n' "Warning: 'whale' resolves to $resolved, not $target."
    printf '%s\n' "A different install may shadow this one in PATH."
    printf '%s\n' "Run '$target --version' to use this install directly, or adjust PATH so $(dirname "$target") comes before $(dirname "$resolved")."
  fi
}

OS="$(detect_os)" || {
  printf '%s\n' "whale install: unsupported OS: $(uname -s)" >&2
  exit 1
}

ARCH="$(detect_arch)" || {
  printf '%s\n' "whale install: unsupported architecture: $(uname -m)" >&2
  exit 1
}

if [ -z "$BIN_DIR" ]; then
  BIN_DIR="$(default_bin_dir)"
fi

if [ "$VERSION" = "latest" ]; then
  printf '%s\n' "Resolving latest whale release..."
fi
RESOLVED_VERSION="$(resolve_version)"
ASSET_NAME="whale-$OS-$ARCH"
ARCHIVE_NAME="$ASSET_NAME.tar.gz"
BASE_URL="https://github.com/$REPO_SLUG/releases/download/$RESOLVED_VERSION"
TMPDIR="$(mktemp -d 2>/dev/null || mktemp -d -t whale-install)"
trap 'rm -rf "$TMPDIR"' EXIT INT TERM

ASSET_PATH="$TMPDIR/$ASSET_NAME"
ARCHIVE_PATH="$TMPDIR/$ARCHIVE_NAME"
CHECKSUMS_PATH="$TMPDIR/checksums.txt"
EXTRACT_DIR="$TMPDIR/extract"

printf '%s\n' "Installing whale $RESOLVED_VERSION for $OS/$ARCH"
if asset_exists "$BASE_URL/$ARCHIVE_NAME"; then
  printf '%s\n' "Downloading $ARCHIVE_NAME..."
  download_asset "$BASE_URL/$ARCHIVE_NAME" "$ARCHIVE_PATH"
  DOWNLOAD_NAME="$ARCHIVE_NAME"
  DOWNLOAD_PATH="$ARCHIVE_PATH"
else
  printf '%s\n' "Archive not found; downloading $ASSET_NAME..."
  download_asset "$BASE_URL/$ASSET_NAME" "$ASSET_PATH"
  DOWNLOAD_NAME="$ASSET_NAME"
  DOWNLOAD_PATH="$ASSET_PATH"
fi
printf '%s\n' "Downloading checksums.txt..."
curl -fsSL "$BASE_URL/checksums.txt" -o "$CHECKSUMS_PATH"

EXPECTED_SUM="$(awk -v asset="$DOWNLOAD_NAME" '$2 == asset || $2 ~ "/"asset"$" {print $1}' "$CHECKSUMS_PATH")"
if [ -z "$EXPECTED_SUM" ]; then
  printf '%s\n' "whale install: could not find checksum for $DOWNLOAD_NAME" >&2
  exit 1
fi

printf '%s\n' "Verifying checksum..."
verify_checksum "$DOWNLOAD_PATH" "$EXPECTED_SUM"
if [ "$DOWNLOAD_NAME" = "$ARCHIVE_NAME" ]; then
  printf '%s\n' "Extracting $ARCHIVE_NAME..."
  mkdir -p "$EXTRACT_DIR"
  tar -xzf "$ARCHIVE_PATH" -C "$EXTRACT_DIR"
  ASSET_PATH="$EXTRACT_DIR/$ASSET_NAME"
  RUNTIME_PATH="$EXTRACT_DIR/runtime"
  if [ ! -f "$ASSET_PATH" ]; then
    printf '%s\n' "whale install: archive did not contain $ASSET_NAME" >&2
    exit 1
  fi
else
  RUNTIME_PATH=""
fi
printf '%s\n' "Installing to $BIN_DIR/whale..."
TARGET="$(install_binary "$ASSET_PATH" "$BIN_DIR")"
install_runtime "$RUNTIME_PATH" "$BIN_DIR"

printf '%s\n' "Installed whale $RESOLVED_VERSION to $TARGET"
"$TARGET" --version
warn_if_shadowed "$TARGET"

case ":$PATH:" in
  *:"$BIN_DIR":*) ;;
  *)
    printf '\n%s\n' "Add $BIN_DIR to your PATH to run 'whale' directly."
    ;;
esac
