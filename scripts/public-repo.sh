#!/usr/bin/env bash
# Writes the public release repository's files into a checkout of it: the
# README without its source-only parts, SECURITY.md, CHANGELOG.md, LICENSE and
# the images the README shows; with a version and the app's zip, also the cask.
#
#   scripts/public-repo.sh <checkout>
#   scripts/public-repo.sh <checkout> <version> <Portside-version.zip> <owner/repo>
set -euo pipefail
cd "$(dirname "$0")/.."

dest="${1:?usage: public-repo.sh <checkout> [<version> <zip> <owner/repo>]}"
[ -d "$dest/.git" ] || { echo "$dest is not a git checkout" >&2; exit 1; }

# Lines between <!-- source-only --> and <!-- /source-only --> stay here.
awk '/^<!-- source-only -->$/ {skip = 1; next} /^<!-- \/source-only -->$/ {skip = 0; next} !skip' README.md > "$dest/README.md"
if grep -q 'source-only' "$dest/README.md"; then
  echo "README.md has an unmatched source-only marker" >&2
  exit 1
fi
cp SECURITY.md CHANGELOG.md LICENSE "$dest/"

# The images the README references, and nothing else from design/.
# Earlier images go first, through git, so a removed one does not linger.
git -C "$dest" rm -rq --ignore-unmatch design
grep -oE '(src|srcset)="design/[^"]+"' "$dest/README.md" | sed -E 's/^[a-z]+="//; s/"$//' | sort -u |
  while read -r img; do
    mkdir -p "$dest/$(dirname "$img")"
    cp "$img" "$dest/$img"
  done

[ $# -ge 4 ] || exit 0
version="$2" zip="$3" repo="$4"
sha=$(shasum -a 256 "$zip" | cut -d' ' -f1)
# The app's bundle ID is the service label plus ".app" (internal/app).
label=$(sed -n 's/^[[:space:]]*ServiceLabel = "\(.*\)"$/\1/p' internal/app/app.go)
[ -n "$label" ] || { echo "ServiceLabel not found in internal/app/app.go" >&2; exit 1; }
mkdir -p "$dest/Casks"
sed -e "s/__VERSION__/$version/" -e "s/__SHA256__/$sha/" -e "s#__REPO__#$repo#" \
  -e "s/__BUNDLE_ID__/$label.app/" -e "s/__SERVICE_LABEL__/$label/" \
  packaging/homebrew/portside.rb.tmpl > "$dest/Casks/portside.rb"
