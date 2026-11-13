# 0010: macdoc reports slimmable universal apps and never removes slices

**Status:** Accepted (decision from experiment S6, 2026-11-14). **Recorded in:** `docs/experiments/slim-universal-apps.md`, `docs/plans/mole-gaps.md` phase 7.

## Context

Phase 7a added read-only advice: `lipo -archs` and `lipo -detailed_info` show how much of each universal app is its Intel slice. Phase 7b would act on it, as Mole's `mo slim` does. The plan required an experiment first. It tested thinning with `lipo` and re-signing with `codesign --force --deep --sign -` on copies of a Developer-ID notarized app (Rectangle, OnyX), a Mac App Store app (Snip My) and an ad-hoc app.

## Decision

1. macdoc does **not** remove architecture slices from any app. There is no recipe, flag or tool for it. Slimming stays advice only: `doctor software` shows the estimated saving and the person decides.
2. macdoc never re-signs another vendor's app. Re-signing changes the Team ID and drops the Developer ID certificate and notarization (experiment: `spctl` rejected the re-signed Rectangle and OnyX). That breaks Gatekeeper, app groups, keychain access groups, TCC grants and Sparkle update checks, and it can't be undone by macdoc.
3. Thinning with no re-sign kept signatures valid in the experiment, but it is not adopted. It edits files inside a signed bundle in a protected root (`/Applications`), has no restore path that keeps the saving (ADR 0004), may break Rosetta use and delta updates, and launch was not verified.

## Consequences

- The number shown for slimming stays an estimate (AGENTS rule 7), from the app's own binary.
- No change to the executor, the protected-path list or the recipe catalog.
- Phase 7b is closed. A person who wants the space can use the vendor's own universal-to-arm64 download or reinstall.

## Revisit when

All of these hold: the executor can run a vendor-supplied slimming step with a fixed argv, a restore path exists that doesn't cost the saved space back, `/Applications` handling is decided (ADR 0008 "Not decided here"), and a launch test per app class passes. That needs a new record that supersedes this one.
