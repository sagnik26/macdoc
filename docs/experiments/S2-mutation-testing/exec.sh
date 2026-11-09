#!/bin/bash
# Mutation exec for the tests/ layout: run <pkg>/tests against the mutant.
# exit 0 = killed, 1 = survived, 2 = skipped (did not compile).
[ -z "$MUTATE_CHANGED" ] && { echo "MUTATE_CHANGED unset"; exit 3; }
restore() { [ -f "$MUTATE_ORIGINAL.tmp" ] && mv "$MUTATE_ORIGINAL.tmp" "$MUTATE_ORIGINAL"; }
trap 'restore; exit 3' SIGHUP SIGINT SIGTERM
DIFF=$(diff -u "$MUTATE_ORIGINAL" "$MUTATE_CHANGED")
mv "$MUTATE_ORIGINAL" "$MUTATE_ORIGINAL.tmp"
cp "$MUTATE_CHANGED" "$MUTATE_ORIGINAL"
OUT=$(go test -count=1 -timeout "${MUTATE_TIMEOUT:-120}s" "$MUTATE_PACKAGE/tests" 2>&1)
RC=$?
restore
if [ $RC -eq 0 ]; then echo "$DIFF"; exit 1; fi
if echo "$OUT" | grep -qE "\[build failed\]|\[setup failed\]|cannot use|declared and not used|imported and not used|undefined:"; then exit 2; fi
exit 0
