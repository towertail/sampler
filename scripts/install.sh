#!/bin/sh
# Install the Towertail sampler for the current user and start it.
# No sudo. The sampler joins with a one-use join token, runs in the
# background and starts again after a reboot.
#
# Usage: install.sh --join <token> [--name <name>] [--endpoint <url>]
#                   [--version <x.y.z>] [--dry-run]
set -eu

endpoint="https://api.towertail.com"
releases="https://github.com/towertail/sampler/releases"
join=""
name=""
version=""
dry=0

die() {
	echo "install.sh: $*" >&2
	exit 1
}

usage() {
	echo "Usage: install.sh --join <token> [--name <name>] [--endpoint <url>] [--version <x.y.z>] [--dry-run]"
}

need_value() {
	[ "$1" -ge 2 ] || die "$2 needs a value"
}

while [ $# -gt 0 ]; do
	case "$1" in
	--join) need_value $# "$1"; join=$2; shift 2 ;;
	--name) need_value $# "$1"; name=$2; shift 2 ;;
	--endpoint) need_value $# "$1"; endpoint=$2; shift 2 ;;
	--version) need_value $# "$1"; version=$2; shift 2 ;;
	--dry-run) dry=1; shift ;;
	-h | --help) usage; exit 0 ;;
	*) die "unknown argument: $1" ;;
	esac
done
[ -n "$join" ] || die "--join <token> is required"

case "$(uname -s)" in
Linux) os=linux ;;
Darwin) os=darwin ;;
*) die "unsupported OS: $(uname -s)" ;;
esac
case "$(uname -m)" in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
armv7* | armv8l) arch=armv7 ;;
*) die "unsupported CPU: $(uname -m)" ;;
esac
if [ "$os" = darwin ] && [ "$arch" = armv7 ]; then
	die "unsupported CPU: $(uname -m)"
fi

triple="$os-$arch"
asset="towertail-sampler-$triple"
if [ -n "$version" ]; then
	base="$releases/download/v${version#v}"
else
	base="$releases/latest/download"
fi

if [ "$dry" = 1 ]; then
	echo "triple=$triple"
	echo "url=$base/$asset"
	echo "manifest=$base/manifest.json"
	exit 0
fi

fetch() {
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL -o "$2" "$1"
	elif command -v wget >/dev/null 2>&1; then
		wget -q -O "$2" "$1"
	else
		die "curl or wget is required"
	fi
}

sha256() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d ' ' -f 1
	else
		shasum -a 256 "$1" | cut -d ' ' -f 1
	fi
}

dir="$HOME/.towertail/bin"
bin="$dir/towertail-sampler"
mkdir -p "$dir"
tmp=$(mktemp "$dir/.install.XXXXXX")
manifest=$(mktemp "$dir/.manifest.XXXXXX")
trap 'rm -f "$tmp" "$manifest"' EXIT

fetch "$base/$asset" "$tmp"
fetch "$base/manifest.json" "$manifest"
want=$(sed -n 's/.*"'"$triple"'"[[:space:]]*:[[:space:]]*"\([0-9a-f]\{64\}\)".*/\1/p' "$manifest" | head -n 1)
[ -n "$want" ] || die "manifest has no checksum for $triple"
[ "$(sha256 "$tmp")" = "$want" ] || die "checksum of $asset does not match the manifest"

chmod 0755 "$tmp"
mv -f "$tmp" "$bin"

set -- start --endpoint "$endpoint" --join "$join" --boot auto
[ -z "$name" ] || set -- "$@" --name "$name"
"$bin" "$@"
"$bin" status
