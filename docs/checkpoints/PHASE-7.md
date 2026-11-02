# Checkpoint: Phase 7 (`slow`, `trend`, polish → v0.1) 🟡

**Date:** 2026-10-28 · **main:** see `git log` · **Status:** built and tested; the dogfood week (DEVELOPMENT_PLAN §4, Phase 7 last task) started on 2026-10-28 and runs to 2026-11-04. Phase 7 is complete when that week is done and the MVP B checklist (§6) holds on the real Mac.

## Built
- **`macdoc doctor slow [--days 7]`** (`engine.Slow`, `internal/cli/slow.go`). It only explains; it never quits or changes an app.
  - A one-line verdict, from memory pressure, swap and free disk. Low disk plus swap ≥ 2 GiB is critical: macOS may hang if swap can't grow.
  - The biggest users against RAM size, e.g. "Docker 3.1 GB and Android Emulator 2.4 GB on 8.0 GB of RAM (67% together)".
  - Every group of at least 1% of RAM (always at least 3), with what known groups are. That covers Docker / the Virtualization VM, emulators, simulators, Xcode, Node, Ollama, Gradle/Java, browsers, Electron editors and Spotlight.
  - Wired and compressed memory, plus an RSS caveat.
  - **Swap history:** each `slow` and each overview stores one vitals sample (at most one per 5 minutes, newest 2,000 kept) in a new `vitals` table (store v4). `slow` shows the peak, the lowest value and the critical-pressure checks over the window.
  - Process grouping: Apple's `com.apple.Virtualization.VirtualMachine`, which Docker Desktop, OrbStack, Colima and UTM all use, is now the "Virtual machine" group.
- **`macdoc doctor trend [--days 30]`** (`engine.Trend`, `internal/cli/trend.go`). It compares the latest complete scan with the newest one at least N days older, or with the oldest kept (with a note).
  - Per well-known area (Xcode, iOS simulators, Android, Gradle, Node, CocoaPods, Homebrew, Docker, Python, AI models, Rust/Go, app caches, Downloads, Trash, device backups, your files).
  - Up to 8 folders that grew ≥ 500 MB:
    - It keeps the deepest folder that explains ≥ 80% of a growth.
    - It drops a parent once its kept children explain all but 500 MB.
    - It skips space that only moved (a new folder whose parent didn't grow, like an app update swapping version folders).
  - **Honest sizes:** scans store folders ≥ 100 MiB (105 MB), so a folder missing from a scan is a range [0, 105 MB]. Sizes and changes are `engine.Range` and render as "<105 MB", "≥1.1 GB", "+3.9 GB or more" or "under 422 MB", never as zero.
  - **Retention:** the 10 newest complete scans, plus the newest of each calendar week for 13 weeks (`store.PruneScansWeekly`), so a 30-day window has something to compare.
- **Dashboard:**
  - Health shows "Is memory slowing it down?" (verdict, biggest users, swap line) and a "What it is" column for memory users.
  - Space shows "What grew" (areas that changed, then the growing folders).
  - `/slow` and `/trend` open those tabs.
  - Nothing is probed twice: `engine.SlowFrom` reuses the overview's health check.
- **`macdoc config`** (`internal/config`, `internal/cli/config.go`):
  - `plan_minutes` (5–120, default 30) and `native_timeout_minutes` (1–60, default 15).
  - `protect` / `unprotect`: extra folders macdoc must never touch. These are add-only on top of the built-in list, and `..` is rejected.
  - `config.Policy` feeds them into the safety policy used by opportunities, planning and the executor's own re-check. An unreadable or invalid file is an error at all three, so it fails closed.
  - There is no `edit`, because opening an editor would run a program outside the executor. `config path` prints the file.
- **Frozen JSON:** `schema/*.json` holds one JSON Schema per `--json` command (18), generated from the Go types. Contract tests:
  - `TestSchemasFrozen`: the generated schema must equal the committed file; `-update` is for additive changes only.
  - `TestEveryJSONCommandHasASchema`: every `writeJSON` call is registered.
  - `TestJSONOutputMatchesSchema`: real output from 12 commands validates.
  - `TestSchemaValidatorRejects`: the validator does catch mismatches.
- **Polish:**
  - Builds strip symbols (`-s -w`): 16 MB → 10 MB.
  - Completions through `macdoc completion zsh|bash|fish` (README has the setup).
  - Steps for advice-only items in `explain` (done just before this phase).

## Done-when (MVP B, §6), where it stands
| Criterion | State |
|---|---|
| `doctor`, `wins`, `storage`, `dev`, `projects`, `slow`, `explain`, `trend`, `plan`, `apply`, `quarantine`, `log` all work, with `--json` | ✅ (`apply` is TTY-only by design: no `--json`) |
| Full crawl within 64 MB RSS, background QoS, machine usable | ✅ since Phase 2; the scanner is unchanged |
| Every number measured or "unknown (reason)" | ✅; trend adds bounded ranges for folders a scan didn't store |
| Plans ID-only, sealed, expiring; apply needs a TTY and confirmation | ✅ (the dashboard's Enter-twice for SAFE/CAUTION is the user's decision of 2026-10-28) |
| Executor re-validates, acts only through native commands or quarantine | ✅; now also against configured protected folders (`TestConfigProtectAtApply`) |
| Audit log records every action; restore works | ✅ |
| Fuzz and TOCTOU tests pass; import guard in CI | ✅ `make check` |
| ≥ 10 GB reclaimed on the real Mac with no regrets during the dogfood week | 🟡 0.88 GB so far through macdoc (7 applies on 2026-10-28, all `ok`, measured). About 39 GB of simulator files was also freed outside macdoc on the same day; `trend` shows it. **Update 2026-11-02:** the audit log has no applies after 2026-10-28, so the total through macdoc is still 0.88 GB |

## Real Mac (read-only checks)
- `doctor slow`: "Memory is tight" at pressure *warn*, with 3.8 GB swap on 8 GB of RAM. Biggest users: Google Chrome 2.1 GB, Cursor 837 MB and claude 410 MB (40% together).
- `doctor trend`: with 18 hours of scans it says so. It shows iOS simulators −39.2 GB or more and app caches +669 MB. Chrome.app grew by keeping a third version folder (real growth, so it's listed).
- `config`: shows the defaults and the built-in protected list; nothing was written.

## Not done (by design or later)
- No `nodes(path_fold)` index (planned for `History`): `trend` reads two scans by `scan_id`, which the primary key already covers, so the index would only grow the database.
- `slow --tips` (what to quit, smaller models) is Phase 10.
- JSON command names stay as they were (`wins`, `explain`, `storage` without `doctor`), to avoid churning the contract just as it's frozen.
- Threshold tuning from the dogfood week.
