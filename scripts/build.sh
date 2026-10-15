#!/usr/bin/env bash
# Build the towertail-sampler binary for the target triples.
#
# Usage: scripts/build.sh [--out DIR] [triple ...]
#   No triple builds all 7. DIR defaults to dist in the repo root.
#
# Output: DIR/<triple>/towertail-sampler[.exe], DIR/manifest.json, DIR/sample-v1.json.
# The manifest lists all binaries in DIR, also the ones from earlier builds.
#
# Env:
#   SIGN_IDENTITY  when set (not "-"), signs the darwin binaries with the
#                  hardened runtime and a secure timestamp.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
MODULE="github.com/towertail/sampler"

# triple          goos    goarch  goarm
TARGETS=(
  "linux-amd64    linux   amd64"
  "linux-arm64    linux   arm64"
  "linux-armv7    linux   arm     7"
  "darwin-arm64   darwin  arm64"
  "darwin-amd64   darwin  amd64"
  "windows-amd64  windows amd64"
  "windows-arm64  windows arm64"
)

usage() {
  echo "usage: scripts/build.sh [--out DIR] [triple ...]" >&2
  echo "triples:" >&2
  for row in "${TARGETS[@]}"; do
    read -r t _ <<<"$row"
    echo "  $t" >&2
  done
}

OUT_DIR="$REPO_ROOT/dist"
REQUESTED=()
while [[ $# -gt 0 ]]; do
  case "$1" in
    --out)
      [[ $# -ge 2 ]] || { usage; exit 2; }
      OUT_DIR="$2"
      shift 2
      ;;
    --out=*)
      OUT_DIR="${1#--out=}"
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    -*)
      echo "error: unknown flag $1" >&2
      usage
      exit 2
      ;;
    *)
      REQUESTED+=("$1")
      shift
      ;;
  esac
done

if ! command -v go >/dev/null 2>&1; then
  echo "error: go toolchain not found in PATH" >&2
  exit 1
fi

mkdir -p "$OUT_DIR"
OUT_DIR="$(cd "$OUT_DIR" && pwd)"
cd "$REPO_ROOT"

if VERSION="$(git -C "$REPO_ROOT" describe --tags --dirty --match 'v*' 2>/dev/null)"; then
  VERSION="${VERSION#v}"
else
  VERSION="0.0.0-dev"
fi
SHA="$(git -C "$REPO_ROOT" rev-parse --short=7 HEAD 2>/dev/null || echo dev)"

bin_path() {
  local triple="$1" ext=""
  [[ "$triple" == windows-* ]] && ext=".exe"
  echo "$OUT_DIR/$triple/towertail-sampler${ext}"
}

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

build_one() {
  local triple="$1" goos="$2" goarch="$3" goarm="${4:-}"
  local dest
  dest="$(bin_path "$triple")"
  rm -rf "${OUT_DIR:?}/$triple"
  mkdir -p "$OUT_DIR/$triple"

  echo "→ $triple  (GOOS=$goos GOARCH=$goarch${goarm:+ GOARM=$goarm})"
  env -i \
    PATH="$PATH" HOME="$HOME" \
    ${TEMP:+TEMP="$TEMP"} ${TMP:+TMP="$TMP"} ${TMPDIR:+TMPDIR="$TMPDIR"} \
    ${LOCALAPPDATA:+LOCALAPPDATA="$LOCALAPPDATA"} \
    ${USERPROFILE:+USERPROFILE="$USERPROFILE"} \
    ${GOCACHE:+GOCACHE="$GOCACHE"} ${GOMODCACHE:+GOMODCACHE="$GOMODCACHE"} \
    ${GOPATH:+GOPATH="$GOPATH"} ${GOPROXY:+GOPROXY="$GOPROXY"} \
    ${GOTOOLCHAIN:+GOTOOLCHAIN="$GOTOOLCHAIN"} \
    CGO_ENABLED=0 \
    GOOS="$goos" GOARCH="$goarch" ${goarm:+GOARM="$goarm"} \
    go build -trimpath \
      -ldflags="-s -w -X $MODULE/internal/version.Version=$VERSION -X $MODULE/internal/version.SHA=$SHA" \
      -o "$dest" ./cmd/sampler

  local size
  size="$(wc -c <"$dest" | tr -d ' ')"
  echo "  built $dest ($((size / 1024 / 1024)) MB)"
}

for want in "${REQUESTED[@]+"${REQUESTED[@]}"}"; do
  found=0
  for row in "${TARGETS[@]}"; do
    read -r t _ <<<"$row"
    [[ "$t" == "$want" ]] && found=1
  done
  if [[ $found -eq 0 ]]; then
    echo "error: unknown triple '$want'" >&2
    usage
    exit 2
  fi
done

BUILT=()
for row in "${TARGETS[@]}"; do
  read -r triple goos goarch goarm <<<"$row"
  if [[ ${#REQUESTED[@]} -gt 0 && " ${REQUESTED[*]} " != *" $triple "* ]]; then
    continue
  fi
  build_one "$triple" "$goos" "$goarch" "$goarm"
  BUILT+=("$triple")
done

if [[ -n "${SIGN_IDENTITY:-}" && "$SIGN_IDENTITY" != "-" ]]; then
  for triple in "${BUILT[@]}"; do
    [[ "$triple" == darwin-* ]] || continue
    echo "→ signing $triple"
    codesign --force --options runtime --timestamp --sign "$SIGN_IDENTITY" "$(bin_path "$triple")"
  done
fi

MANIFEST="$OUT_DIR/manifest.json"
{
  printf '{\n'
  printf '  "version": "%s",\n' "$VERSION"
  printf '  "sha": "%s",\n' "$SHA"
  printf '  "binaries": {\n'
  first=1
  for row in "${TARGETS[@]}"; do
    read -r triple _ <<<"$row"
    bin="$(bin_path "$triple")"
    [[ -f "$bin" ]] || continue
    [[ $first -eq 1 ]] || printf ',\n'
    printf '    "%s": "%s"' "$triple" "$(sha256_of "$bin")"
    first=0
  done
  printf '\n  }\n}\n'
} >"$MANIFEST"
echo "manifest: $MANIFEST"

cp "$REPO_ROOT/testdata/sample-v1.json" "$OUT_DIR/sample-v1.json"
echo "fixture: $OUT_DIR/sample-v1.json"
