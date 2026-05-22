#!/bin/sh

set -eu

ROOT_DIR="$(CDPATH= cd "$(dirname "$0")/.." && pwd)"
TMPDIR="$(mktemp -d 2>/dev/null || mktemp -d -t whale-install-test)"
trap 'rm -rf "$TMPDIR"' EXIT INT TERM

STUB_DIR="$TMPDIR/stubs"
SHADOW_DIR="$TMPDIR/shadow"
INSTALL_DIR="$TMPDIR/install"
mkdir -p "$STUB_DIR" "$SHADOW_DIR" "$INSTALL_DIR"

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
  */checksums.txt)
    printf '%s  whale-darwin-arm64\n' "$WHALE_TEST_ASSET_SHA" >"$dst"
    ;;
  */whale-darwin-arm64)
    cp "$WHALE_TEST_ASSET" "$dst"
    ;;
  *)
    printf 'unexpected curl args: %s\n' "$*" >&2
    exit 1
    ;;
esac
EOF
chmod 0755 "$STUB_DIR/curl"

cat >"$TMPDIR/whale-darwin-arm64" <<'EOF'
#!/bin/sh
printf '%s\n' 'v9.9.9'
EOF
chmod 0755 "$TMPDIR/whale-darwin-arm64"

ASSET_SHA="$(shasum -a 256 "$TMPDIR/whale-darwin-arm64" | awk '{print $1}')"

OUTPUT="$(
  PATH="$SHADOW_DIR:$STUB_DIR:/usr/bin:/bin" \
  VERSION="v9.9.9" \
  BIN_DIR="$INSTALL_DIR" \
  WHALE_TEST_ASSET="$TMPDIR/whale-darwin-arm64" \
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

printf '%s\n' "install.sh shadow warning test passed"
