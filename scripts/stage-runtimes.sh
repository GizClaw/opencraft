#!/usr/bin/env bash
# stage-runtimes.sh downloads and stages the bundled language/tool
# runtimes described by runtimes/manifest.json into <root>/runtime.
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
import io
import json
import os
import pathlib
import shutil
import sys
import tarfile
import tempfile
import urllib.request
import zipfile

manifest_path, platform, root = sys.argv[1:]
with open(manifest_path, encoding="utf-8") as f:
    manifest = json.load(f)

# Layout directories use major.minor for python/go/uv and major for node.
def version_dir(family, version):
    parts = version.split(".")
    if family == "node":
        return parts[0]
    return ".".join(parts[:2])

def family_name(family):
    return {"python": "python", "go": "go", "node": "node", "uv": "uv"}[family]

def verify(data, expected):
    digest = hashlib.sha256(data).hexdigest()
    if digest.lower() != expected.lower():
        raise SystemExit(
            f"stage-runtimes: sha256 mismatch: got {digest}, want {expected}"
        )

def extract(data, target):
    target = pathlib.Path(target)
    target.mkdir(parents=True, exist_ok=True)
    bio = io.BytesIO(data)

    def write_file(dest, reader, mode=None):
        dest.parent.mkdir(parents=True, exist_ok=True)
        with reader() as src, open(dest, "wb") as out:
            out.write(src.read())
        if mode:
            os.chmod(dest, mode & 0o777)

    if data[:2] == b"PK":
        with zipfile.ZipFile(bio) as zf:
            for member in zf.infolist():
                parts = pathlib.PurePosixPath(member.filename).parts
                if len(parts) <= 1 or member.is_dir():
                    continue
                dest = target.joinpath(*parts[1:])
                mode = (member.external_attr >> 16) & 0o777
                write_file(dest, lambda m=member: zf.open(m), mode)
        return
    with tarfile.open(fileobj=bio, mode="r:*") as tf:
        for member in tf.getmembers():
            parts = pathlib.PurePosixPath(member.name).parts
            if len(parts) <= 1:
                continue
            dest = target.joinpath(*parts[1:])
            if member.isdir():
                dest.mkdir(parents=True, exist_ok=True)
                continue
            if member.issym():
                dest.parent.mkdir(parents=True, exist_ok=True)
                dest.symlink_to(member.linkname)
                continue
            if not member.isfile():
                continue
            write_file(dest, lambda m=member: tf.extractfile(m), member.mode)

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
    target = os.path.join(
        root, family_name(family), version_dir(family, entry["version"]), platform
    )
    extract(data, target)

print(f"stage-runtimes: staged runtimes under {root}")
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
for tool in ("python", "python3", "go", "gofmt", "node", "npm",
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
    for tool in python python3 go gofmt node npm npx corepack uv uvx; do
      ln -sf runtime-launcher "$launcher_dir/$tool"
    done
    ;;
esac
