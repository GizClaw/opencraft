#!/usr/bin/env bash
# Refreshes Casks/opencraft.rb with the version and sha256 of a released
# macOS dmg.
#
# Usage: ./scripts/update-cask.sh v0.1.0
set -euo pipefail

tag="${1:?usage: update-cask.sh <tag, e.g. v0.1.0>}"
ver="${tag#v}"
url="https://github.com/GizClaw/opencraft/releases/download/${tag}/opencraft-${ver}-macos-universal.dmg"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

echo "Downloading ${url} ..."
curl -fsSL -o "$tmp/opencraft.dmg" "$url"

sha="$(shasum -a 256 "$tmp/opencraft.dmg" | awk '{print $1}')"
sed -i.bak -E \
  -e "s/version \"[^\"]+\"/version \"${ver}\"/" \
  -e "s/sha256 \"[^\"]*\"/sha256 \"${sha}\"/" \
  Casks/opencraft.rb
rm -f Casks/opencraft.rb.bak

echo "Updated Casks/opencraft.rb (version ${ver}, sha256 ${sha})"
