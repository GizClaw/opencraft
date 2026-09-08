#!/bin/sh
# check-boundaries enforces the internal package layering rules
# (AGENTS.md §4): bottom layers must never import the layers above
# them.
#
#   capabilities, foundation  -> adapters, orchestration, testing: forbidden
#   orchestration             -> adapters, testing: forbidden
#   adapters                  -> testing: forbidden
#
# The check inspects production (non-test) imports of every package
# under internal/. Test-only imports are intentionally excluded: unit
# tests of a capability may legitimately exercise higher layers, but
# the shipped dependency graph must stay clean.
#
# Usage: scripts/check-boundaries.sh   (run from the repo root)
set -eu

cd "$(dirname "$0")/.."

mod=$(go list -m -f '{{.Path}}')
prefix="$mod/internal/"

# group returns the top-level layer of an internal import path
# (e.g. "capabilities/sessions/state" -> "capabilities").
group() {
	case "$1" in
		$prefix*) echo "$1" | sed "s|$prefix||" | cut -d/ -f1 ;;
		*) echo "" ;;
	esac
}

# banned <layer> prints the layers <layer> may not import, one per line.
banned() {
	case "$1" in
		capabilities|foundation) echo "adapters orchestration testing" ;;
		orchestration) echo "adapters testing" ;;
		adapters) echo "testing" ;;
		*) echo "" ;;
	esac
}

tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT

# One line per package: <package> <space-separated direct imports>.
go list -f '{{.ImportPath}} {{join .Imports " "}}' ./internal/... >"$tmp"

status=0
while read -r pkg rest; do
	src=$(group "$pkg")
	[ -n "$src" ] || continue
	for imp in $rest; do
		dst=$(group "$imp")
		[ -n "$dst" ] || continue
		[ "$src" = "$dst" ] && continue
		for rule in $(banned "$src"); do
			if [ "$dst" = "$rule" ]; then
				echo "boundary violation: $pkg imports $imp (layers: $src -> $dst)"
				status=1
			fi
		done
	done
done <"$tmp"
exit $status
