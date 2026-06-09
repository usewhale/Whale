#!/bin/sh

set -eu

ROOT_DIR="$(CDPATH= cd "$(dirname "$0")/.." && pwd)"
TMPDIR="$(mktemp -d 2>/dev/null || mktemp -d -t whale-install-test)"
trap 'rm -rf "$TMPDIR"' EXIT INT TERM

STUB_DIR="$TMPDIR/stubs"
SHADOW_DIR="$TMPDIR/shadow"
INSTALL_DIR="$TMPDIR/install"
mkdir -p "$STUB_DIR" "$SHADOW_DIR" "$INSTALL_DIR"

case "$(uname -s)" in
  Darwin) OS_NAME="darwin" ;;
  Linux) OS_NAME="linux" ;;
  *)
    printf 'unsupported test OS: %s\n' "$(uname -s)" >&2
    exit 1
    ;;
esac

case "$(uname -m)" in
  x86_64|amd64) ARCH_NAME="amd64" ;;
  arm64|aarch64) ARCH_NAME="arm64" ;;
  *)
    printf 'unsupported test architecture: %s\n' "$(uname -m)" >&2
    exit 1
    ;;
esac

ASSET_NAME="whale-$OS_NAME-$ARCH_NAME"

cat >"$SHADOW_DIR/whale" <<'EOF'
#!/bin/sh
printf '%s\n' 'v0.0.1'
EOF
chmod 0755 "$SHADOW_DIR/whale"

cat >"$STUB_DIR/curl" <<'EOF'
#!/bin/sh
dst=""
url=""
prev=""
for arg in "$@"; do
  if [ "$prev" = "-o" ]; then
    dst="$arg"
    prev=""
    continue
  fi
  case "$arg" in
    -o)
      prev="-o"
      ;;
    http://*|https://*)
      url="$arg"
      ;;
  esac
done

case "$*" in
  *"-I"*)
    exit 22
    ;;
esac

case "$url" in
  */releases/latest)
    printf '{"tag_name":"v9.9.9"}\n'
    ;;
  */checksums.txt)
    printf '%s  %s\n' "$WHALE_TEST_ASSET_SHA" "$WHALE_TEST_ASSET_NAME" >"$dst"
    ;;
  */"$WHALE_TEST_ASSET_NAME")
    cp "$WHALE_TEST_ASSET" "$dst"
    ;;
  *)
    printf 'unexpected curl args: %s\n' "$*" >&2
    exit 1
    ;;
esac
EOF
chmod 0755 "$STUB_DIR/curl"

cat >"$TMPDIR/$ASSET_NAME" <<'EOF'
#!/bin/sh
printf '%s\n' 'v9.9.9'
EOF
chmod 0755 "$TMPDIR/$ASSET_NAME"

cat >"$INSTALL_DIR/whale" <<'EOF'
#!/bin/sh
printf '%s\n' 'v0.0.0'
EOF
chmod 0755 "$INSTALL_DIR/whale"
ln "$INSTALL_DIR/whale" "$INSTALL_DIR/whale.oldlink"
mkdir -p "$INSTALL_DIR/runtime"
printf '%s\n' "existing runtime" >"$INSTALL_DIR/runtime/zsh"

if command -v sha256sum >/dev/null 2>&1; then
  ASSET_SHA="$(sha256sum "$TMPDIR/$ASSET_NAME" | awk '{print $1}')"
else
  ASSET_SHA="$(shasum -a 256 "$TMPDIR/$ASSET_NAME" | awk '{print $1}')"
fi

if LEGACY_VERSION_OUTPUT="$(VERSION="v9.9.9" "$ROOT_DIR/scripts/install.sh" 2>&1)"; then
  printf '%s\n' "install accepted legacy VERSION unexpectedly" >&2
  exit 1
fi
printf '%s\n' "$LEGACY_VERSION_OUTPUT" | grep -F "VERSION is no longer supported" >/dev/null

if LEGACY_BIN_DIR_OUTPUT="$(BIN_DIR="$INSTALL_DIR" "$ROOT_DIR/scripts/install.sh" 2>&1)"; then
  printf '%s\n' "install accepted legacy BIN_DIR unexpectedly" >&2
  exit 1
fi
printf '%s\n' "$LEGACY_BIN_DIR_OUTPUT" | grep -F "BIN_DIR is no longer supported" >/dev/null

OUTPUT="$(
  PATH="$SHADOW_DIR:$STUB_DIR:/usr/bin:/bin" \
  WHALE_INSTALL_DIR="$INSTALL_DIR" \
  WHALE_TEST_ASSET="$TMPDIR/$ASSET_NAME" \
  WHALE_TEST_ASSET_NAME="$ASSET_NAME" \
  WHALE_TEST_ASSET_SHA="$ASSET_SHA" \
  "$ROOT_DIR/scripts/install.sh"
)"

printf '%s\n' "$OUTPUT" | grep -F "Installed whale v9.9.9 to $INSTALL_DIR/whale" >/dev/null
printf '%s\n' "$OUTPUT" | grep -F "Warning: 'whale' resolves to $SHADOW_DIR/whale, not $INSTALL_DIR/whale." >/dev/null
printf '%s\n' "$OUTPUT" | grep -F "A different install may shadow this one in PATH." >/dev/null

if [ "$("$INSTALL_DIR/whale" --version)" != "v9.9.9" ]; then
  printf '%s\n' "installed whale did not run" >&2
  exit 1
fi

if [ "$("$INSTALL_DIR/whale.oldlink" --version)" != "v0.0.0" ]; then
  printf '%s\n' "install overwrote the old whale inode instead of replacing it" >&2
  exit 1
fi

if [ "$(cat "$INSTALL_DIR/runtime/zsh")" != "existing runtime" ]; then
  printf '%s\n' "install removed existing runtime when asset did not provide one" >&2
  exit 1
fi

printf '%s\n' "install.sh shadow warning test passed"
