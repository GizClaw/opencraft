#!/usr/bin/env bash
# stage-runtimes.sh downloads and stages compressed bundled language
# runtimes described by runtimes/manifest.json into <root>/archives.
# The app extracts one family lazily when a bundled fallback is needed.
#
# Usage:
#   scripts/stage-runtimes.sh <platform> [runtime-root] [manifest]
#
# platform is one of darwin-arm64, darwin-amd64, linux-amd64,
# windows-amd64. macOS universal builds run the script once per
# platform into the same runtime root.
set -euo pipefail

platform="${1:?usage: stage-runtimes.sh <platform> [root] [manifest]}"
root="${2:-build/bin/runtime}"
manifest="${3:-runtimes/manifest.json}"
launcher_dir="$root/launcher"

python3 - "$manifest" "$platform" "$root" <<'PY'
import hashlib
import json
import os
import sys
import urllib.request

manifest_path, platform, root = sys.argv[1:]
with open(manifest_path, encoding="utf-8") as f:
    manifest = json.load(f)

def verify(data, expected):
    digest = hashlib.sha256(data).hexdigest()
    if digest.lower() != expected.lower():
        raise SystemExit(
            f"stage-runtimes: sha256 mismatch: got {digest}, want {expected}"
        )

for family, entry in manifest.items():
    if family == "schema_version":
        continue
    if platform not in entry.get("urls", {}):
        continue
    url = entry["urls"][platform]
    expected = entry["sha256"][platform]
    print(f"stage-runtimes: {family} {entry['version']} ({platform})")
    with urllib.request.urlopen(url, timeout=300) as resp:
        data = resp.read()
    verify(data, expected)
    target = os.path.join(root, "archives", family, platform)
    os.makedirs(os.path.dirname(target), exist_ok=True)
    with open(target, "wb") as out:
        out.write(data)

print(f"stage-runtimes: staged compressed runtimes under {root}/archives")
PY

mkdir -p "$launcher_dir"
cp "$manifest" "$root/manifest.json"
launcher_bin="$launcher_dir/runtime-launcher"
case "$platform" in
  windows-*) launcher_bin="$launcher_dir/runtime-launcher.exe" ;;
esac
if command -v go >/dev/null 2>&1; then
  go build -o "$launcher_bin" ./internal/toolchain/launchermain
else
  echo "stage-runtimes: go missing; launcher binary not built" >&2
  exit 1
fi

case "$platform" in
  windows-*)
    # Windows has no reliable unprivileged symlink; hard-link one
    # launcher binary per tool name (copies remain the fallback).
    python3 - "$launcher_dir" "$launcher_bin" <<'PY'
import os
import shutil
import sys

launcher_dir, launcher_bin = sys.argv[1:]
for tool in ("python", "python3", "node", "npm",
             "npx", "corepack", "uv", "uvx"):
    dest = os.path.join(launcher_dir, tool + ".exe")
    if os.path.exists(dest):
        os.unlink(dest)
    try:
        os.link(launcher_bin, dest)
    except OSError:
        shutil.copy2(launcher_bin, dest)
PY
    ;;
  *)
    for tool in python python3 node npm npx corepack uv uvx; do
      ln -sf runtime-launcher "$launcher_dir/$tool"
    done
    ;;
esac
