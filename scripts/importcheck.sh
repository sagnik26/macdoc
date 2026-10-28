#!/usr/bin/env bash
# importcheck: fail if any package other than internal/executor (and its
# subpackages), internal/cli and cmd/macdoc depends on internal/executor,
# including through tests. internal/mcp, internal/engine and internal/planner
# are asserted explicitly (AGENTS.md invariant 3; the planner builds what the
# executor runs, so it must not reach the executor either).
#
# Usage:
#   scripts/importcheck.sh              check the module containing this script
#   scripts/importcheck.sh --self-test  prove the check fails on a violation
set -euo pipefail

GO=${GO:-go}

# base_of strips go list test-variant decorations:
#   "m/p [m/p.test]" -> "m/p", "m/p.test" -> "m/p", "m/p_test" -> "m/p"
base_of() {
	local p=${1%% \[*}
	p=${p%.test}
	p=${p%_test}
	printf '%s' "$p"
}

# chain prints the shortest import path from pkg (a go list ImportPath,
# possibly a test variant) to the executor package.
chain() {
	local pkg=$1 base=$2 target=$3
	"$GO" list -test -deps -f '{{.ImportPath}}|{{join .Imports ","}}' "$base" 2>/dev/null |
		awk -F'|' -v start="$pkg" -v target="$target" '
		{ imports[$1] = $2 }
		function istarget(p) { return p == target || index(p, target " [") == 1 }
		END {
			q[0] = start; head = 0; tail = 1; seen[start] = 1
			while (head < tail) {
				cur = q[head++]
				if (istarget(cur)) {
					path = cur
					while (cur in prev) { cur = prev[cur]; path = cur " -> " path }
					print "    " path
					exit
				}
				n = split(imports[cur], next_, ",")
				for (i = 1; i <= n; i++) {
					x = next_[i]
					if (x != "" && !(x in seen)) { seen[x] = 1; prev[x] = cur; q[tail++] = x }
				}
			}
			print "    (import chain not found; see: go list -deps -test " start ")"
		}'
}

# check runs in the current directory's module. Exit 0 clean, 1 violation.
check() {
	local module target listing line imp deps base reported="" failed=0
	module=$("$GO" list -m)
	target="$module/internal/executor"
	listing=$("$GO" list -test -f '{{.ImportPath}}|{{join .Deps " "}}' ./...)

	while IFS= read -r line; do
		[ -n "$line" ] || continue
		imp=${line%%|*}
		deps=" ${line#*|} "
		base=$(base_of "$imp")
		case "$base" in
		"$target" | "$target"/* | "$module/internal/cli" | "$module/cmd/macdoc") continue ;;
		esac
		case "$deps" in
		*" $target "* | *" $target ["*) ;;
		*) continue ;;
		esac
		case " $reported " in *" $base "*) continue ;; esac
		reported="$reported $base"
		failed=1
		case "$base" in
		"$module/internal/mcp" | "$module/internal/mcp"/* | "$module/internal/engine" | "$module/internal/engine"/*)
			echo "importcheck: INVARIANT 3 VIOLATED: $base depends on $target"
			echo "  internal/mcp and internal/engine must never import the executor (AGENTS.md invariant 3)."
			;;
		*)
			echo "importcheck: $base depends on $target"
			echo "  only internal/cli and cmd/macdoc may import the executor."
			;;
		esac
		echo "  import chain ($imp):"
		chain "$imp" "$base" "$target"
	done <<<"$listing"

	# Explicit assertion, independent of the loop above: the packages that
	# matter most must exist and must not reach the executor.
	local p d
	for p in "$module/internal/mcp" "$module/internal/engine" "$module/internal/planner"; do
		[ "$p" = "$module/internal/planner" ] && [ ! -d internal/planner ] && continue # self-test modules have none
		# go list only warns (exit 0) when a pattern matches nothing.
		if ! d=$("$GO" list -deps -test "$p/..." 2>/dev/null) || [ -z "$d" ]; then
			echo "importcheck: cannot list $p/... (missing package?)"
			failed=1
			continue
		fi
		if printf '%s\n' "$d" | grep -Eq "^$target( \[.*\])?\$"; then
			echo "importcheck: INVARIANT 3 VIOLATED: $p/... (with tests) depends on $target"
			failed=1
		fi
	done

	if [ "$failed" -ne 0 ]; then
		echo "importcheck: FAIL"
		return 1
	fi
	echo "importcheck: ok (no package outside internal/executor, internal/cli and cmd/macdoc depends on $target)"
}

# self_test builds throwaway modules and asserts check fails on each kind of
# violation and passes on a clean tree.
self_test() {
	local tmp out
	tmp=$(mktemp -d "${TMPDIR:-/tmp}/importcheck.XXXXXX")
	# shellcheck disable=SC2064
	trap "rm -rf '$tmp'" EXIT
	export GOWORK=off GOFLAGS=-mod=mod

	w() { mkdir -p "$tmp/$(dirname "$1")" && printf '%s\n' "$2" >"$tmp/$1"; }
	w go.mod 'module example.com/st'
	w internal/executor/e.go 'package executor
func Apply() {}'
	w internal/executor/sub/s.go 'package sub
import _ "example.com/st/internal/executor"'
	w internal/cli/c.go 'package cli
import _ "example.com/st/internal/executor"'
	w cmd/macdoc/main.go 'package main
import _ "example.com/st/internal/cli"
func main() {}'
	w internal/engine/e.go 'package engine'
	w internal/mcp/m.go 'package mcp'
	w internal/ok/ok.go 'package ok'

	expect_pass() {
		if ! out=$(cd "$tmp" && check 2>&1); then
			echo "self-test FAIL ($1): expected pass, got:"
			echo "$out"
			exit 1
		fi
	}
	expect_fail() {
		local name=$1
		shift
		if out=$(cd "$tmp" && check 2>&1); then
			echo "self-test FAIL ($name): expected failure, got:"
			echo "$out"
			exit 1
		fi
		if [ -n "${IMPORTCHECK_VERBOSE:-}" ]; then
			printf -- '--- %s ---\n%s\n' "$name" "$out"
		fi
		local want
		for want in "$@"; do
			if ! printf '%s\n' "$out" | grep -qF -- "$want"; then
				echo "self-test FAIL ($name): output lacks \"$want\":"
				echo "$out"
				exit 1
			fi
		done
		if printf '%s\n' "$out" | grep -qE 'st/(internal/cli|cmd/macdoc|internal/executor/sub) depends'; then
			echo "self-test FAIL ($name): exempt package reported:"
			echo "$out"
			exit 1
		fi
	}

	expect_pass "clean tree"

	w internal/mcp/m.go 'package mcp
import _ "example.com/st/internal/executor"'
	expect_fail "direct mcp import" \
		"INVARIANT 3 VIOLATED: example.com/st/internal/mcp depends on" \
		"example.com/st/internal/mcp -> example.com/st/internal/executor"
	w internal/mcp/m.go 'package mcp'

	w internal/helper/h.go 'package helper
import _ "example.com/st/internal/executor"'
	w internal/engine/e.go 'package engine
import _ "example.com/st/internal/helper"'
	expect_fail "transitive engine import" \
		"INVARIANT 3 VIOLATED: example.com/st/internal/engine depends on" \
		"example.com/st/internal/engine -> example.com/st/internal/helper -> example.com/st/internal/executor" \
		"importcheck: example.com/st/internal/helper depends on"
	w internal/engine/e.go 'package engine'
	rm -r "$tmp/internal/helper"

	w internal/engine/sub/x_test.go 'package sub_test
import _ "example.com/st/internal/executor"'
	w internal/engine/sub/x.go 'package sub'
	expect_fail "test-only import in engine subpackage" \
		"INVARIANT 3 VIOLATED: example.com/st/internal/engine/sub depends on" \
		"INVARIANT 3 VIOLATED: example.com/st/internal/engine/... (with tests)"
	rm -r "$tmp/internal/engine/sub"

	w internal/ok/ok.go 'package ok
import _ "example.com/st/internal/cli"'
	expect_fail "import via exempt cli" \
		"importcheck: example.com/st/internal/ok depends on" \
		"example.com/st/internal/ok -> example.com/st/internal/cli -> example.com/st/internal/executor"
	w internal/ok/ok.go 'package ok'

	rm -r "$tmp/internal/mcp"
	expect_fail "missing mcp package" "cannot list example.com/st/internal/mcp/..."
	w internal/mcp/m.go 'package mcp'

	expect_pass "clean tree again"
	echo "importcheck self-test: ok"
}

case "${1:-}" in
--self-test) self_test ;;
"")
	cd "$(dirname "$0")/.."
	check
	;;
*)
	echo "usage: $0 [--self-test]" >&2
	exit 2
	;;
esac
