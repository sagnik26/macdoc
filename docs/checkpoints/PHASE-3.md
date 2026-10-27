# Checkpoint: Phase 3 (Recipes → `macdoc wins`) ✅ · USER REVIEW MILESTONE

**Date:** 2026-10-27 · **main:** see `git log` · **Status:** complete, awaiting user review before Phase 4

## Built
- **Recipe system** (`internal/recipes`), tech lead:
  - Schema, loader, and the generic `children`/`path` detectors.
  - Scan keep-rules (recipe folders are kept even under 100 MB).
  - A **safety lint** that every recipe must pass at load time:
    - native and dry-run commands must **exactly match an allow-list**
    - allow patterns must be `~`-relative, at least 2 deep, with no `..`, and can't reach protected folders, `.git`, `.env*` or SQLite files
    - preconditions use a strict character set
- **46 recipes** across Xcode/simulators, CocoaPods, Android/Gradle, Node, Homebrew, Docker, Python, AI models and System (P3-W1, P3-W2).
- **Classifier and ranking** (`engine.Opportunities`, P3-W3):
  - Runs detectors in parallel with timeouts. Detector errors and panics become warnings.
  - Drops items for: a missing tool, an unsafe identifier, a reserved word, a failed `CheckTarget`, being too recently used, or an unknown age when a minimum idle time applies.
  - Raises risk for anything changed in the last 7 days.
  - Merges overlaps and duplicates (keeping the higher risk), and subtracts Docker's dangling images from unused images.
  - Deterministic IDs, then ranking by tier, then by size × confidence.
- **Commands:** `macdoc wins [--all] [--tier]`, `macdoc storage [--large]`, `macdoc dev [toolchain]`, `macdoc explain <id>`. All read-only, all `--json`.

## Real Mac result (scan S-SEWMUIAF, 1.0M files, 46 s)
```
Free & safe ─ 825 MB     uv cache 394 MB · Homebrew old versions 412 MB · 4 old app logs 19.6 MB
Worth considering ─ 553 MB  Android build-tools 34.0.0 / 35.0.0 (~390 MB) · Node v20.20.2 via nvm 161 MB
Your call (advice only) ─ 48.9 GB  leftover iOS simulator runtimes 33.7 GB + dyld caches 15.2 GB
```
- **The big win on this Mac is the 48.9 GB of leftover simulator data.** It's advice-only, because Xcode isn't installed (only the Command Line Tools), so `simctl` can't remove it. `explain` gives the exact steps.
- `storage` decodes "System Data": swap/VM 4.3 GB, purgeable 3.2 GB, `/private/var/folders` 3.9 GB, CoreSimulator 15.2 GB, and about 36 GB not visible without Full Disk Access.
- `wins` takes about 6 s, of which about 4 s is Homebrew's own dry-run.

## Safety work this phase
| Source | Findings | Outcome |
|---|---|---|
| Tech-lead real-data run | `node_modules` detector flagged **global CLIs, npm inside nvm, Cursor extension internals, app internals** | Fixed (P3-W2 round 2): project dirs only (package.json + lockfile, not hidden/Library, ≥ 50 MB) |
| P3-R1 red team | **12 findings**, including the high-severity *command lint was a deny-list* (`xcrun python3 -c …`, `brew ruby -e`, `docker volume prune` passed), AVD registry pointing into `~/Documents`, FIFO hangs, size overflow | All fixed (P3-W4). Exact command allow-lists; 27 red-team tests pass |
| P3-R1 judgments | Downloads, app caches, venv | Downloads quarantine **installers/archives only** (others are advice: "may be the only copy"); app caches need 14 days idle and exclude JetBrains/Google; venvs are RISKY |
| Fixture capture | Querying pnpm/yarn **downloaded binaries via Corepack** | Rule: detectors never run pnpm/yarn/npm to discover things |

## Tasks
P3-W1 (1 round), P3-W2 (2 rounds), P3-W3 (2 rounds), P3-R1 (review), P3-W4 (1 round, resumed after a usage limit). W1 and W2 ran in parallel, as did W3 alongside R1 and W4. Reports are in `docs/tasks/reports/`.

## Known gaps / polish candidates
- Tiny items show up, such as an 8 KB pip cache that got raised to CAUTION because it was used recently. A minimum display size (e.g. 1 MB) would cut the noise.
- Project-aware recipes (stale `node_modules` by git activity, iOS Pods, RN/Android build folders) arrive in **Phase 4**.
- Nothing can be deleted yet. `plan` is Phase 5 and `apply` is Phase 6.

## Try it
```
cd ~/Desktop/macdoc && make build
./bin/macdoc scan          # ~45 s, background priority
./bin/macdoc wins          # ranked suggestions
./bin/macdoc explain <id>  # what / why / how to restore
./bin/macdoc storage       # categories + "System Data" decoded
./bin/macdoc dev           # by toolchain
```
