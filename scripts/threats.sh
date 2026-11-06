#!/usr/bin/env bash
# threatcheck: every test that docs/THREAT_MODEL.md or an ADR in docs/adr names
# must still exist.
#
# A reference looks like `internal/mcp/tests.TestToolList`: a package path
# ending in /tests, a dot, then a Test or Fuzz function. The check fails if
# that function is no longer declared in that directory, so deleting or
# renaming a test that backs a defense also breaks the build until the threat
# model is updated.
set -euo pipefail
cd "$(dirname "$0")/.."

doc="docs/THREAT_MODEL.md and docs/adr"
refs=$(cat docs/THREAT_MODEL.md docs/adr/*.md | grep -oE '[a-z][a-z0-9_/]*/tests\.(Test|Fuzz)[A-Za-z0-9_]+' | sort -u)
if [ -z "$refs" ]; then
	echo "threatcheck: $doc names no tests; that is itself an error" >&2
	exit 1
fi

missing=0
count=0
while IFS= read -r ref; do
	dir=${ref%%.*}
	name=${ref#*.}
	count=$((count + 1))
	if ! grep -qE "^func ${name}\(" "$dir"/*_test.go 2>/dev/null; then
		echo "threatcheck: $ref is named in $doc but not declared in $dir" >&2
		missing=1
	fi
done <<<"$refs"

if [ "$missing" -ne 0 ]; then
	exit 1
fi
echo "threatcheck: ok ($count tests named in $doc exist)"
