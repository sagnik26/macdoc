# S3: how many exports exist only for tests?

Tests live in `internal/<pkg>/tests/` (package `tests`), so they can reach only exported names. This experiment counts exported identifiers and methods that no non-test package outside their own references.

`deadcode` from `main` is the wrong instrument: it found 6, because many test-only exports are also called by `main`'s own code. This tool type-checks the module and records, for each exported object, who uses it from another package.

```sh
cd docs/experiments/S3-test-only-exports
go build -o /tmp/exportscan . && /tmp/exportscan ../../.. github.com/sagnik26/macdoc
```

This directory is its own Go module, so `go build ./...` at the repository root ignores it.

## Result (2026-11-06, `60a0165` plus the module rename)

627 exported identifiers and methods; **263 (42%)** are referenced from outside their package only by test packages. See `result.txt`. Largest: `cli` 77 of 79, `recipes` 58 of 80, `probe` 22 of 39, `engine` 25 of 92, `safety` 13 of 22.

## What it does and does not mean

- A same-directory `package x_test` is also black-box, so moving files does not reduce exports. Only white-box tests (`package x`, in the same directory) would let these 263 become unexported.
- Everything is under `internal/`, so none of this is a public API. The cost is noise (extra doc comments, lint findings, unclear intent), not an API commitment.
- The tool counts references; it does not know whether an export is also needed for another reason (an interface, JSON encoding, reflection).
