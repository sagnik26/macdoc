# Checkpoint: Phase 4 (Projects & staleness → `macdoc doctor projects`) ✅

**Date:** 2026-10-28 · **main:** see `git log` · **Status:** complete. Phase 5 (planner) is next and starts only when the user asks.

## Built
- **`internal/projects`** discovers projects from the artifact folders the scan keeps:
  - node_modules, .next, .expo (next to `package.json`), .venv (with `pyvenv.cfg`, next to a Python marker)
  - `ios/Pods` or `Pods` (next to a `Podfile`), `ios/build` (Podfile or Xcode project)
  - `android/build`, `android/app/build`, `android/.gradle` (Gradle files present)
  - Xcode DerivedData, mapped to its project through `info.plist` `WorkspacePath`; folders whose workspace is gone are reported separately.
  Markers are checked with `Lstat`, so symlinked markers don't count. Hidden and Library trees, dependency folders and `lib/node_modules` are skipped.
- **Idle time** is the newest of: the last commit touching the project, the newest source change (a bounded walk that skips artifacts, `.git` and caches) and the newest build output. Access times are never used. If the walk is cut short, the time is used only when git confirms the tree is clean; otherwise it's unknown, and unknown never counts as stale.
- **Git state** comes from read-only git with no optional locks and no fsmonitor, limited to the project folder. Untracked `.DS_Store` files are ignored. git runs for projects with ≥ 50 MB of artifacts.
- **Recipes:**
  - new: `rn.ios_pods_stale` (CAUTION), `rn.native_build_stale` (SAFE), `node.next_cache_stale` (SAFE)
  - `node.node_modules_stale` and `python.venv_stale` now count project activity, so a project committed to last week doesn't get its old dependencies flagged
  - `xcode.derived_data` notes the project it belongs to, or that the project no longer exists
- **Risk raise:** uncommitted changes, or a git state that couldn't be confirmed, raise a project artifact (`Finding.MinRisk`). Native build and `.next` go SAFE→CAUTION; Pods go CAUTION→RISKY. `Opportunity.risk_reason` records why, and `explain` shows it. Projects in protected folders (~/Desktop, ~/Documents, …) are listed but never suggested.
- **`macdoc doctor projects [--idle N]`** (with `--json`) and a **Projects** tab in the dashboard (keys `1`–`6`, `/projects`).
- **Items under 1 MB are hidden** from suggestions, with a note giving the count. Unknown sizes always show. (Phase 3 review: approved.)
- Table fitting: a wrapping column never splits a word when the table can fit without doing so, and no-break spaces keep a folder and its size together.

## Real Mac result (scan S-YDUZSYYY, 1.04M files, 43 s scan)
```
 Projects ────────────────────────────────────────────────── 540 MB in 2
  ~/Desktop/release-checklist-tool · node
    idle 1 day (file change) · 416 MB · git clean
  ~/Desktop/release-checklist-tool/web · node
    idle 1 day (file change) · 124 MB · git clean
```
Both projects are active and in ~/Desktop (protected), so they're listed and not suggested. That's correct. The first run reported "uncommitted changes" that turned out to be untracked `.DS_Store` files, which led to the Finder-file rule.
- Kept scan nodes are unchanged (1,771). There were no Pods or native build folders under home to add.
- Peak RSS of a full `doctor scan` is 105 MB, against 115 MB before this phase. That figure is the whole run (detectors, Homebrew's dry-run, report). The scanner's own budget (≤ 64 MB, 24.7 MB measured in Phase 2) is unaffected.

## Done-when
| Criterion | Status |
|---|---|
| Projects with uncommitted changes never get SAFE-tier artifact suggestions | ✅ `TestProjectRecipesEndToEnd` (real catalog, store and engine): clean gives SAFE, dirty gives CAUTION/RISKY with the reason |
| DerivedData → project mapping works on the real Mac | ⚠ Unit-tested (`TestDerivedDataMapping`: existing, deleted and binary-plist cases). It can't be checked on this Mac, which has only the Command Line Tools and no DerivedData. Check it once Xcode is installed |

## Tests added
`internal/projects` covers discovery, every artifact kind, look-alikes (9 cases), activity sources, git states including `.DS_Store`, the git skip threshold, the walk limit, protected projects, DerivedData, Keep and the porcelain parser. `internal/recipes` covers the three new recipes (positive, look-alike negative, and git-state and unknown-idle failures), `withProject`, DerivedData notes and the `dirty_risk` lint. `internal/engine` covers MinRisk in `classify` and the end-to-end test. `internal/cli` covers the printed `projects` output, `--idle`, the Projects tab and column fitting.

## Not done (by design or later)
- No `config.toml` yet. Idle thresholds are the recipes' `stale_days` (90).
- Spotlight `kMDItemLastUsedDate` isn't used, and `dist` isn't counted (see the as-built note in DEVELOPMENT_PLAN.md §4).
- Native Android projects with build folders at the project root (not under `android/`) aren't matched yet.
- `nvm.old_versions` doesn't yet read projects' `.nvmrc`.

## Try it
```
macdoc doctor scan          # rescan so project folders are kept
macdoc doctor projects      # or: macdoc → 4
macdoc doctor projects --idle 90 --json
```
