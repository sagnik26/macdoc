# 0006: Each package keeps its tests in `internal/<pkg>/tests/`

**Status:** Accepted; the layout is under review. **Recorded in:** `docs/DEVELOPMENT_PLAN.md` §3 ("Test layout"), commit `9a8705b`.

## Context

The project chose to keep tests apart from the code they test, as black-box tests in `package tests` that use only exported names. Fixtures live in `tests/testdata/`.

## Decision

Tests go in `internal/<pkg>/tests/*_test.go`, `package tests`. Plain `go test -cover ./internal/x` therefore reports 0% for the package under test, so `make cover` uses `-coverpkg` across packages. `scripts/imports.sh` lets `internal/cli/tests` import the executor, and the forbidden-call logic lives in `tools/forbidcheck/check` so its tests can import it.

## What it costs (measured)

`docs/experiments/S3-test-only-exports`: 263 of 627 exported identifiers (42%) are referenced from outside their own package only by tests, because black-box tests can reach only exported names. Most are in `cli` (77 of 79), `recipes` (58 of 80) and `probe` (22 of 39). Everything is under `internal/`, so none is a public API; the cost is documentation and lint noise and unclear intent, not an API commitment.

A same-directory `package x_test` would not change this, because it is also black-box. Only white-box tests (`package x`, beside the code) let those names become unexported, and that is a larger rewrite of the tests.

## Options

1. Keep the layout and document it (the default today).
2. Move to white-box tests beside the code, a package at a time.

## Decision

Keep for now. The owner decides whether the benefit of option 2 is worth the rewrite.
