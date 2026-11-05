# Testing

Part of the [macdoc architecture guide](../../README.md#architecture).

## 13. Testing

- **Layout.** Tests live in `internal/<pkg>/tests/` as `package tests` and use only
  exported names. Fixtures sit in `tests/testdata/`. Path-dependent code takes a root or
  home override so it can be tested against fixture trees.
- **Counts.** About 560 test functions and 840 runs. Two tests need a real Mac and are
  skipped unless `MACDOC_REAL_SCAN` or `MACDOC_REAL_WINS` is set.
- **Executor tests** run only inside `MACDOC_SANDBOX` with trees from `t.TempDir()`.
  They use `executor.Config.StageHook` to simulate a crash or a swap at a precise step
  (`pending`, `opened`, `moved`, `done`, `purged-payload`). Four tests fail if the hook
  is disabled, which shows they depend on it.
- **Red-team tests** exist for the safety layers (symlink swaps, ancestor swaps,
  protected-path tricks, purge ordering).
- **Fuzzing:** `FuzzCanonicalize` and `FuzzCheckTarget` in `internal/safety`, run for
  ten seconds in CI.
- **Contract tests:** `TestSchemasFrozen`, `TestEveryJSONCommandHasASchema`,
  `TestJSONOutputMatchesSchema`, and `TestSchemaValidatorRejects` keep `--json` output
  and `schema/` in step.
- **Guard tests:** `tools/forbidcheck` and `scripts/importcheck.sh` have their own tests,
  including a self-test that the import guard really fails on a violation.
- **Coverage:** `make cover` measures across packages, because tests in `tests/` don't
  count toward the package they exercise.
