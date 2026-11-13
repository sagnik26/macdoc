# 0007: Discovered software is advisory until a recipe matches it

**Status:** Accepted (owner decision, 2026-11-13). Implemented: discovery and attribution (`internal/inventory`), the store snapshot (migration 7), `macdoc doctor software`, the overview and Space summaries, and the `match:` field (`docs/RECIPE_CATALOG.md`). Discovery also finds programs on `PATH` (`tool:` matches), and a recipe's toolchain is derived from its match through the `familyOf` table (see `docs/RECIPE_CATALOG.md`). **Recorded in:** `internal/inventory`, `docs/RECIPE_CATALOG.md`, `docs/SAFETY_MODEL.md` §4.

## Context

macdoc knew about developer tools only through hand-written recipes: each recipe named a path or a native tool, so software that nothing named was invisible. A new toolchain (Rust, Flutter, JetBrains, or an app that writes caches) never appeared, and the owner asked for a system that finds newly installed software and the data it leaves behind without a hardcoded list.

## Decision

Finding software and acting on it are separate steps.

1. `internal/inventory` discovers software from the machine (installed apps, package managers, and a small set of read-only queries) and attributes folders from the latest scan to it, with the evidence and a confidence for each match. It only reads.
2. Everything it finds is **advisory** (shown with its size, evidence and install date). It produces no removal.
3. A removal still comes only from a recipe (ADR 0002, AGENTS rule 4). A recipe may name software through a `match:` field instead of a hardcoded tool, but the recipe, not the discovery, decides the action, the risk and the restore cost.
4. Each scan's software inventory is stored, so "new since the last scan" is a diff of two snapshots.

## Consequences

- Newly installed software is visible at once, with no code change. It can't be removed until a recipe covers it, which keeps the removal surface as it is.
- Attribution can be wrong. Every match shows its evidence and confidence, and a match below the threshold is reported as unattributed rather than assigned.
- Discovery adds time to a scan. Each source has a 10-second budget; a source that doesn't finish is reported as unknown, never as zero.
- A new command (`doctor software`) is an additive change to the frozen schemas, so it needs no `schema_version` bump.
- The package managers are a fixed list of read-only queries in code. The software they report is not a hardcoded list.

## Not decided here

Removing leftovers of software that is already uninstalled, and asking the MCP server for software data. Both need their own decision.
