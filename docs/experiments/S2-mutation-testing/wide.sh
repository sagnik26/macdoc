#!/bin/bash
# Like exec.sh but runs the tests of every package that can exercise the mutant.
restore() { [ -f "$MUTATE_ORIGINAL.tmp" ] && mv "$MUTATE_ORIGINAL.tmp" "$MUTATE_ORIGINAL"; }
trap 'restore; exit 3' SIGHUP SIGINT SIGTERM
DIFF=$(diff -u "$MUTATE_ORIGINAL" "$MUTATE_CHANGED")
mv "$MUTATE_ORIGINAL" "$MUTATE_ORIGINAL.tmp"; cp "$MUTATE_CHANGED" "$MUTATE_ORIGINAL"
M=github.com/sagnik26/macdoc/internal
OUT=$(go test -count=1 -timeout 180s $MUTATE_PACKAGE/tests $M/engine/tests $M/mcp/tests $M/cli/tests $M/executor/tests 2>&1); RC=$?
restore
if [ $RC -eq 0 ]; then echo "$DIFF"; exit 1; fi
if echo "$OUT" | grep -qE "\[build failed\]|\[setup failed\]"; then exit 2; fi
exit 0
