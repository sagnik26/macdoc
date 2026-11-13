# 0011: App bundles in /Applications and Application Support leftovers

**Status:** Accepted (owner decision, 2026-11-14, both questions answered yes). Supersedes the "`/Applications` stays protected" and "Application Support is left out" parts of ADR 0008 and the matching line in ADR 0009. Implemented. **Recorded in:** `internal/safety/safety.go` (`AppsPattern`, `SetAppsFolder`), `internal/recipes/load.go`, `internal/recipes/leftovers.go`, `internal/recipes/catalog/apps.yaml`, `docs/SAFETY_MODEL.md` §3, `docs/RECIPE_CATALOG.md`.

## Context

ADR 0008 let macdoc remove leftovers and apps in `~/Applications` but left two questions for the owner: app bundles in `/Applications`, a protected root outside `$HOME`, and leftovers in `~/Library/Application Support`, a protected parent of macdoc's own state and `MobileSync/Backup`.

## Decision

1. **One exception to rule 6 and `docs/SAFETY_MODEL.md` §3.** `/Applications/<Name>.app`, exactly one level below `/Applications`, may be a cleanup target. It is allowed by one pattern, `safety.AppsPattern` (`/Applications/*.app`), and only by a RISKY quarantine recipe, `apps.system_bundle`, so each app needs its own typed/y confirmation. The pattern must not be widened.
2. The detector and `CheckTarget` both refuse: Apple apps (`com.apple.*`), Mac App Store apps (`Contents/_MASReceipt`), symlinks, and bundles the user neither owns nor can write to as a group. The precondition `app_bundle_not_running` is checked right before the move. Restore puts the bundle back through the same check.
3. A root-owned bundle that the system refuses to move fails cleanly: the item is skipped and audited. There is no privileged path and no `sudo` for this (ADR 0009 is not extended).
4. Everything else in `/Applications`, anything deeper, and every other protected root stays protected.
5. **Application Support leftovers.** Recipe `apps.support_leftovers` (RISKY, quarantine, detector `app_leftovers`) offers folders in `~/Library/Application Support` that are named by a reverse-DNS bundle identifier, have no installed app or LaunchAgent owner, are unchanged for 180 days and are at least 1 MB. Precondition `no_installed_app`. Its allow pattern `~/Library/Application Support/*` is accepted by the recipe lint only for a RISKY quarantine `app_leftovers` recipe.

## Why the exception is exact

- The only pattern that reaches outside `$HOME` matches a name ending in `.app` one level down. It cannot match `/Applications` itself, a sibling, a file inside a bundle, or a nested folder.
- A name must be reverse-DNS for Application Support, and `CheckTarget` still protects macdoc's own folder and `MobileSync`, so neither can match.
- RISKY plus quarantine means a typed/y confirmation per item and a restore path (ADR 0004). Restore is checked like the move.
- Rules 1 to 5 are unchanged: IDs not paths, terminal approval, the executor as the only mutator, a recipe for every action, re-validation before acting.

## Consequences

- The set of locations macdoc may move things from grows by two. The lint, `CheckTarget` and the tests below pin both, and a change to either pattern needs a new record.
- An app's settings and caches stay behind when its bundle is moved. Once no installed app owns them they appear as leftovers on a later scan.
- Application Support data can be documents. The recipe is RISKY with a 180-day age and the restore note says the quarantine copy is the only way back until it is deleted.

## Not decided here

- Privileged removal of root-owned bundles.
- Mac App Store apps (`Contents/_MASReceipt`) stay refused.

Tests: `internal/safety/tests.TestAppBundleException`, `internal/safety/tests.TestAppBundleExceptionFollowsTheFixtureFolder`, `internal/recipes/tests.TestSystemAppBundles`, `internal/recipes/tests.TestSupportLeftovers`, `internal/recipes/tests.TestR1LintAllowsProtectedPatterns`, `internal/executor/tests.TestSystemAppBundleMoveAndRestore`, `internal/executor/tests.TestUserAppBundleMovedOnlyWhenClosed`.
