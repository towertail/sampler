#!/usr/bin/env bash
# Write manifest.json from a GoReleaser dist directory.
#
# Usage: scripts/manifest.sh [DIST]
#   DIST defaults to dist. Run it after GoReleaser, so that the hashes
#   are of the signed darwin binaries.
#
# Output: DIST/manifest.json =
#   {"version":"<Version>","sha":"<ShortCommit>","binaries":{"<triple>":"<sha256>"}}
set -euo pipefail

DIST="${1:-dist}"
META="$DIST/metadata.json"
ARTIFACTS="$DIST/artifacts.json"

for f in "$META" "$ARTIFACTS"; do
  [[ -f "$f" ]] || { echo "error: $f not found" >&2; exit 1; }
done
command -v jq >/dev/null 2>&1 || { echo "error: jq not found in PATH" >&2; exit 1; }

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

# Artifact paths are relative to the directory that holds DIST.
ROOT="$(cd "$DIST/.." && pwd)"
binaries='{}'
while IFS=$'\t' read -r name path; do
  triple="${name#towertail-sampler-}"
  triple="${triple%.exe}"
  binaries="$(jq -c --arg t "$triple" --arg h "$(sha256_of "$ROOT/$path")" '. + {($t): $h}' <<<"$binaries")"
done < <(jq -r '.[] | select(.type == "Binary" and .extra.Format == "binary") | [.name, .path] | @tsv' "$ARTIFACTS")

n="$(jq 'length' <<<"$binaries")"
[[ "$n" == 7 ]] || { echo "error: found $n binaries, want 7" >&2; exit 1; }

jq -n \
  --arg version "$(jq -r .version "$META")" \
  --arg sha "$(jq -r '.commit[0:7]' "$META")" \
  --argjson binaries "$binaries" \
  '{version: $version, sha: $sha, binaries: ($binaries | to_entries | sort_by(.key) | from_entries)}' >"$DIST/manifest.json"
echo "manifest: $DIST/manifest.json"
