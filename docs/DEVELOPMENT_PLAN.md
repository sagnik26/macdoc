# macdoc: Full Development Plan

## 1. Goals and non-goals

### Goals
1. Answer **"Why is my storage full?"** accurately, including APFS purgeable space, local snapshots, swap and "System Data".
2. Find **stale or regenerable** space across developer tooling, with honest reclaimable sizes.
3. Produce **dry-run cleanup plans** with a risk level, reclaimable bytes and restore cost for each item.
4. Execute plans **only** after explicit confirmation in the terminal, using native tools or quarantine, with re-validation and an audit log.
5. Explain **why the Mac is slow** (memory pressure, swap, and the link between low disk and a hanging machine) without acting on processes.
6. Run comfortably on 8 GB of RAM. Target **≤ 64 MB RSS** during a full crawl and **< 15 MB** for read-only commands that use cached state.
7. Be **C-ready**: a later MCP server reuses the engine without changes to the safety model.

### Non-goals (permanent)
- Automatic or scheduled deletion, or a deletion daemon
- Killing processes, "freeing RAM", purging memory, or changing sysctl/kernel tuning
- Touching the sealed system volume, SIP paths or `/private/var/folders` internals
- Uninstalling apps, or deleting documents, photos, mail or iCloud Drive files
- Accepting filesystem paths from Claude or any other non-human input
- Telemetry or network calls (except native tools that fetch on their own, which never happens during macdoc commands)

---

## 2. Architecture

```
                ┌──────────────────────────── macdoc binary ────────────────────────────┐
                │                                                                        │
  Terminal ───▶ │  cmd/cli  (cobra)                     cmd/mcp (Phase C, stdio)          │ ◀── Claude
                │     │                                     │                             │
                │     ▼                                     ▼                             │
                │  ┌──────────────────────── engine (public API) ───────────────────┐     │
                │  │  probe      scanner      recipes     opportunities   planner    │     │
                │  │  (system)   (full crawl) (catalog)   (classify/rank) (dry-run)  │     │
                │  └───────────────────────────────┬────────────────────────────────┘     │
                │                                  │                                      │
                │                       store (SQLite: scans, nodes,                      │
                │                       opportunities, plans, audit index)                │
                │                                                                        │
                │  executor  ◀── only reachable from `cli apply` after TTY confirmation   │
                │  (native runner, quarantine mover, re-validator, audit writer)          │
                └────────────────────────────────────────────────────────────────────────┘
```

### Key architectural rules
1. **The engine is UI-agnostic.** The CLI and MCP layers only call engine functions and render the results.
2. **The executor is isolated.** It lives in `internal/executor` and is imported **only** by the `apply` and `quarantine purge` commands. The MCP package must not import it, and CI enforces this with an import-graph check (see §8).
3. **IDs, not paths, cross boundaries.** Plans contain opportunity IDs. Paths are resolved again from the store and re-validated by the executor.
4. **Everything is structured.** Every command renders from a typed result struct and can emit versioned JSON (`schema_version`).
5. **Recipes are data.** They're defined in embedded YAML (`recipes/*.yaml`) and validated at startup. Go code provides detectors and validators that are referenced by name.

### Tech choices (Go)

| Concern | Choice | Notes |
|---|---|---|
| CLI framework | `spf13/cobra` | Standard choice, gives shell completions |
| Styling | `charmbracelet/lipgloss` | Tables and colors. Respects `NO_COLOR` |
| TUI (Phase 10) | `charmbracelet/bubbletea` | Only for the interactive cleanup picker |
| Storage | `modernc.org/sqlite` | Pure Go, so no cgo. WAL mode |
| Recipe files | `gopkg.in/yaml.v3` + `embed` | Validated against a Go struct at startup |
| MCP (Phase 8) | Official `modelcontextprotocol/go-sdk` | stdio transport |
| macOS APIs | Optional cgo shim (`internal/platform/darwin`, build tag `cgo`) | For purgeable capacity (`NSURLVolumeAvailableCapacityForImportantUsageKey`) and Spotlight last-used date. Falls back to `diskutil -plist` and `mdls` |
| Hashing | `crypto/sha256` | Plan integrity |
| Logging | `log/slog` | Debug log to file only, never to stdout |

### On-disk layout

```
~/Library/Application Support/macdoc/
  state.db              SQLite (scans, nodes, opportunities, plans index)
  plans/P-XXXX.json     immutable plan files (hash-sealed)
  audit.jsonl           append-only audit log
  quarantine/           same-volume quarantine (instant rename, excluded from TM + Spotlight)
    Q-XXXX/manifest.json + payload/
  config.toml           thresholds, extra protected paths, staleness days
~/Library/Logs/macdoc/  debug logs (rotated)
```

---

## 3. Project layout

```
macdoc/
  cmd/macdoc/main.go             entry; wires cobra
  internal/cli/                  one file per command; rendering only
  internal/engine/               public façade used by cli + mcp
  internal/probe/                hardware, memory, swap, APFS, snapshots
  internal/scanner/              full-crawl walker, aggregation, hardlink tracking
  internal/recipes/              loader, schema, detectors, validators
    catalog/*.yaml               embedded recipe definitions
  internal/opportunity/          classify, size, staleness, rank, IDs
  internal/projects/             project discovery + staleness (git, lockfiles)
  internal/planner/              plan building, hashing, expiry, dry-run render
  internal/executor/             ⚠ only deletion-capable package
    native.go                    allowlisted command runner (no shell)
    quarantine.go                rename-into-quarantine, manifests, restore, purge
    validate.go                  pre-execution re-validation (TOCTOU)
  internal/safety/               protected-path deny list, path canonicalization
  internal/audit/                JSONL writer + reader
  internal/store/                SQLite schema + migrations
  internal/platform/darwin/      cgo shim (optional) + command fallbacks
  internal/mcp/                  Phase C: MCP tools (must NOT import executor)
  testdata/fixtures/             fake home trees for tests
  scripts/importcheck.sh         CI guard on import graph
```

---

## 4. Phases, tasks, acceptance criteria

Estimates assume part-time work (~15–20 h/week).

### Phase 0: Foundations (~0.5 wk)
**Tasks**
- Initialize the Go module, cobra skeleton, `make` targets (build, test, lint, importcheck) and `golangci-lint`.
- Set up a GitHub Actions macOS runner covering build, unit tests, race tests and the import guard.
- Implement `internal/safety` first: path canonicalization (`filepath.EvalSymlinks` plus `Clean`), the deny list, and the "is under allowed root" check, with exhaustive tests.
- Build a fixture builder that creates fake home trees in `t.TempDir()`. Every path-dependent component accepts a `Root`/`Home` override.
- Add a `MACDOC_SANDBOX=<dir>` dev mode that makes the executor refuse any path outside `<dir>`.

**Done when:** CI is green, the safety package has 100% branch coverage, and the import guard fails if `internal/mcp` imports `internal/executor`.

### Phase 1: System probe → `macdoc doctor` (~1 wk)
**Tasks**
- Hardware: `sysctl hw.model hw.memsize hw.ncpu machdep.cpu.brand_string`, plus `sw_vers`.
- Memory: `vm_stat` and `sysctl vm.swapusage` parsed into a struct. Get the memory pressure level from `memory_pressure -Q` or `kern.memorystatus_vm_pressure_level`.
- Disk: `diskutil info -plist /` and `diskutil apfs list -plist`, for container size and free space, volumes (Data, VM, Preboot, Recovery), and VM volume size (swap on disk).
- Purgeable: use the cgo shim when available, otherwise report "purgeable: unknown" rather than guessing.
- Local snapshots: `tmutil listlocalsnapshots /`. Snapshot size can't be read directly, so estimate it via `diskutil apfs listSnapshots` where available, otherwise show the count only.
- Top memory processes: `ps -axo pid,rss,comm` (read-only), grouped by app (Docker VM, qemu/emulator, Xcode, node, ollama, Electron helpers).
- Render `doctor`: a one-screen health summary with disk, RAM, swap and snapshots, the 3 most important findings, and next commands.
- Commands are run through a `Runner` interface with fixture-based fakes in tests. Every external command gets a timeout of 5–10 s.

**Done when:** `doctor` returns in < 1.5 s on the real Mac, every field is either a value or an explicit "unknown (reason)", `--json` is valid against the schema, and parsers have golden tests from captured real outputs.

### Phase 2: Full-crawl scanner + store (~1.5 wk)
**Crawl design (8 GB-safe)**
- **Root:** `/System/Volumes/Data`, staying on one device (`st_dev`). Paths are mapped back through firmlinks (`/System/Volumes/Data/Users/x` → `/Users/x`) so nothing is double-counted via `/`.
- **Skip list:** `.Spotlight-V100`, `.fseventsd`, `.DocumentRevisions-V100`, `/private/var/vm` (counted via the probe), `/private/var/folders` (sized as one opaque bucket, never descended for actions), mounted network volumes, and the macdoc quarantine folder (reported separately).
- **Walker:** a bounded worker pool (default 4, `--workers`) using `os.ReadDir` plus `Lstat`. Symlinks are never followed.
- **Size:** allocated bytes = `Stat_t.Blocks × 512`, not logical size, which handles sparse files like `Docker.raw`.
- **Hard links:** files with `nlink > 1` are tracked by `(dev, ino)` in a compact set and counted once.
- **APFS clones:** can't be detected cheaply. Record a confidence flag ("may include cloned blocks") on Xcode/simulator recipes where clones are common.
- **Streaming aggregation:** sizes roll up bottom-up as directories finish. Only directories that are ≥ `keep_threshold` (default 100 MB), recipe-matched, or project roots are kept, so memory stays bounded however many files there are.
- **Priority:**
  - Re-exec under `taskpolicy -b` (background CPU and I/O QoS) unless `--foreground`.
  - Also call `setpriority(PRIO_PROCESS, 0, 10)`.
- **Memory:** `debug.SetMemoryLimit(64 << 20)` (GOMEMLIMIT), with a `debug.FreeOSMemory()` call after the crawl.
- **Progress:** a live single-line progress display (files/s, GB seen, current top dir). Ctrl-C saves a partial scan marked `incomplete`.
- **Permissions:** EPERM/EACCES directories are recorded as `unmeasured` with a reason. If key paths are unreadable, show a one-time hint about granting Full Disk Access to the Terminal app.
- **Two-phase UX:** probe results print immediately and the crawl streams afterwards. If the crawl takes over 20 s, show "results so far" for known recipe roots, which are crawled first in priority order.
- **Cache:** results go to SQLite (`scans`, `nodes`). Read-only commands reuse the last complete scan if it's less than `max_age` old (default 6 h) and print its age. `--rescan` forces a new scan.

**Store schema (initial)**
- `scans(id, started_at, finished_at, status, total_bytes, unmeasured_count, macdoc_version)`
- `nodes(scan_id, path, bytes, file_count, mtime_max, kind, recipe_id NULL, project_id NULL)`
- `opportunities(id, scan_id, recipe_id, target_path, bytes, risk, stale_days, restore_cost, confidence, explain_json)`
- `plans(id, created_at, origin, expires_at, sha256, status)`
- `history(scan_id, category, bytes)`, used for trends

**Done when:**
- A full crawl of the real Mac finishes without UI lag, with peak RSS ≤ 64 MB measured via `/usr/bin/time -l`.
- Totals are within ±3% of `diskutil`'s used Data volume figure, or the difference is explained (snapshots, purgeable, unmeasured).
- Fixture tests cover symlink loops, hard links, sparse files, permission-denied directories, and cancellation.

### Phase 3: Recipe engine + catalog v1 → `wins`, `storage`, `dev`, `explain` (~1.5 wk)
**Tasks**
- Define the recipe YAML schema (see RECIPE_CATALOG.md §1) and loader. Recipe lint, from the P0-R1 findings:
  - allow patterns must be `~`-relative (outside-home targets are native-only)
  - no `..` in patterns
  - no pattern broad enough to match at depth < 2 below home
  - config `protected_paths` must not contain `..` and must not be an ancestor of home (P0-W3 Q4). Also reject these in `NewPolicy` Validation runs at startup, and a bad recipe is a startup error in dev and is skipped with a warning in release.
- Detectors are named Go functions such as `glob`, `xcode_device_support_versions`, `android_unused_system_images`, `docker_df`, `ollama_list`, `hf_cache_scan` and `node_modules_in_projects`.
- Native-tool sizing: wherever the tool can report its own usage (`docker system df -v`, `brew cleanup -n`, `ollama list`, `hf cache scan`, `xcrun simctl runtime list -j`), use it and cross-check against crawl data.
- Classifier: map crawl nodes to recipes and produce opportunities with stable IDs: `O-` + base32(sha256(recipe_id + canonical_target + scan_id))[:6].
- Ranking: tiers come first (SAFE, then CAUTION, then RISKY/ADVISE). Within a tier, sort by `reclaimable_bytes × confidence × staleness_factor`. Restore cost is shown but not weighted in v1.
- "System Data" decoder: attribute snapshots, swap/VM, `/private/var/folders`, caches, logs, Docker, simulators and unknown, then show the remainder honestly as "unattributed".
- Unknown large items from the full crawl appear in `storage --large` as **ADVISE-ONLY**. With no recipe there's no action.
- Commands:
  - `wins`: tiered top opportunities.
  - `storage`: category breakdown.
  - `dev`: per-toolchain breakdown.
  - `explain <id>`: what it is, why it exists, how it regenerates, restore cost, and the exact command or quarantine move.

**Done when:** all ~35 v1 recipes have fixture tests, `wins` on the real Mac lists only items whose paths pass the safety check, and every opportunity has a non-empty explanation and restore cost.

### Phase 4: Projects & staleness → `projects` (~0.5–1 wk)
**Tasks**
- Discover project roots during the crawl: directories containing `.git`, `package.json`, `Podfile`, `build.gradle(.kts)`, `*.xcodeproj` / `*.xcworkspace`, `pyproject.toml` or `requirements.txt`.
- Staleness signal: the most recent of the git last-commit time (`git log -1 --format=%ct`, run lazily and only for projects with ≥ 200 MB of artifacts), the max mtime of source files (excluding artifact directories), and Spotlight `kMDItemLastUsedDate` where available. Don't use `atime`.
- Per-project artifact weight: `node_modules`, `ios/Pods`, `ios/build`, `android/build`, `android/.gradle`, `.venv`, `dist`, `.next`, `.expo`, plus matching DerivedData folders (mapped via `info.plist` `WorkspacePath` in each DerivedData folder).
- Thresholds come from config, defaulting to 90 days idle for CAUTION suggestions.
- `projects` output: project, idle days, artifact GB, an artifact breakdown, and an "uncommitted changes" flag (`git status --porcelain`, read-only), which raises the risk level for that project's `.venv` and other non-regenerable items.

**Done when:** the mapping from DerivedData to projects works on the real Mac, and projects with uncommitted changes never get SAFE-tier artifact suggestions except for pure caches.

### Phase 5: Planner → `plan` (~1 wk)
**Tasks**
- Input is opportunity IDs (`plan O-ab12 O-cd34`), a tier selector (`plan --safe`), or a recipe selector (`plan --recipe xcode.derived_data`). It never takes paths.
- Plan building: resolve IDs from the **latest complete scan**. Reject unknown or stale IDs (from scans older than `max_age`).
- Plan file contents: `id`, `created_at`, `expires_at` (default 30 min), `origin` (`cli` or `claude`), `scan_id`, items (opportunity ID, recipe ID, canonical target, expected bytes, risk, method, the native command argv or quarantine destination, preconditions, and the target's fingerprint `{dev, ino, mtime, size}`), and totals.
- Sealing: SHA-256 over the canonical JSON, stored in the store as well as the file. Apply refuses a plan if the hashes differ.
- Dry-run render: grouped by risk, showing exact commands and paths, the "freed now" vs "freed after quarantine expiry" split, irreversible markers, and a snapshot caveat when local snapshots exist.

**Done when:** a plan file edited by hand is rejected, an expired plan is rejected, and the dry-run output matches exactly what the executor would do (verified by a parity test against a recording executor).

### Phase 6: Executor + quarantine + audit → `apply`, `quarantine`, `log` (~1.5 wk)
**Tasks**
- **TTY gate:** `apply` requires `isatty(stdin)` and `isatty(stdout)`, has no `--yes` flag for RISKY items, and asks for a typed confirmation (the plan ID suffix). RISKY items also need per-item y/N.
- **Pre-execution re-validation, per item:**
  - The target still exists, its fingerprint is unchanged, and it still canonicalizes to the same path.
  - It still passes the recipe allowlist and the global deny list.
  - The recipe's preconditions are met (e.g. Xcode not running for DerivedData, Docker daemon reachable for docker recipes, Gradle daemons stopped for `~/.gradle`, Android emulator not running for AVDs).
  - If any check fails, **skip** that item, log it, and continue. Never "fix up" a target.
- **Red-team carry-overs from P0-R1** (see `docs/tasks/reports/P0-R1.md`):
  - Re-canonicalize and compare immediately before acting.
  - Open the parent directory and use `O_NOFOLLOW` plus `renameatx_np` relative to the parent fd, to close the TOCTOU window.
  - Before quarantining a directory, check its contents: refuse if it contains a `.git` directory or a bare-repo layout (`HEAD` + `objects/` + `refs/`) outside known artifact dirs, or a `.env*` file.
  - Act only on the canonical string.
  - Build a **fresh `safety.Policy` right before applying** (P0-W3 Q2), so protected folders that became symlinks since planning are covered at their real location.
- **Native runner:** `exec.Command` with a fixed argv from the recipe. No shell and no string interpolation of untrusted values (identifiers like a simulator runtime ID are validated against the tool's own listing first). Per-command timeout, with stdout and stderr captured to the audit log.
- **Quarantine mover:** `os.Rename` into `quarantine/Q-XXXX/payload/` (same APFS volume, so it's instant). Write `manifest.json` with the original path, recipe, plan ID, bytes and `expires_at` (default 7 days). The quarantine root gets `tmutil addexclusion` and `.metadata_never_index`. A cross-volume target is refused, and the recipe must offer a native method instead.
- `quarantine` commands:
  - `list`: shows each entry and its expiry.
  - `restore <Q-id>`: renames the entry back, refusing if something now exists at the original path.
  - `purge [--expired]`: needs a typed confirmation and is audited.
  - Read-only commands only *report* expired quarantine.
- **Verify:** measure container free space before and after, report actual vs expected bytes, and explain the gap (quarantine still holding, snapshot holding, purgeable accounting).
- **Audit:** append-only JSONL with one record per plan start, item result and plan end, plus quarantine restore and purge. `log` shows a readable history and `log --json` returns the raw records.

**Done when:**
- All executor tests run under `MACDOC_SANDBOX`.
- A symlink swapped in between plan and apply is detected and skipped.
- Killing the process mid-apply leaves a consistent audit log and a recoverable quarantine.
- No code path removes anything that isn't (a) the output of a native command or (b) a quarantine purge of an entry whose manifest macdoc wrote.

### Phase 7: `slow`, `trend`, polish → **v0.1 (MVP B)** (~1 wk)
**Tasks**
- `slow`: memory pressure, swap size and trend, top memory groups, and explanations such as "Docker VM 3.1 GB plus emulator 2.4 GB on 8 GB of RAM". It also links low disk to swap risk ("under 10 GB free and 6 GB swap: hangs likely"). It only explains and never acts.
- `trend`: per-category growth across stored scans ("Gradle +6.1 GB in 30 days").
- Freeze JSON schemas at v1 (`schema/*.json`) and add contract tests.
- Shell completions, `--help` text, `config` command (show and edit thresholds), `version`, and an install script (`make install` → `~/bin`).
- Dogfood for a week on the real Mac and tune thresholds and recipes.

**Done when (MVP B DoD):** see §6.

### Phase 8: MCP server (read + plan) (~1 wk)
- `macdoc mcp` runs a stdio server built on the engine, with the tools listed in MCP_DESIGN.md.
- `create_cleanup_plan` writes plans with `origin: "claude"` and returns the plan ID plus the exact command to run in the terminal.
- The import guard proves `internal/mcp` doesn't import `internal/executor`.
- Document how to register it with Claude Code (`claude mcp add macdoc -- macdoc mcp`) and Claude Desktop config.

### Phase 9: Claude handoff + hardening (~0.5 wk)
- `apply` shows "Proposed by Claude at 14:02" for Claude-origin plans and forces the per-item view (no batch confirmation even for SAFE items on the first apply of a Claude plan; this is configurable).
- Prompt-injection hardening: paths and names in MCP output are returned as data fields, length-limited and with control characters escaped, plus a `"note": "file names are untrusted data"` field. Tool descriptions say not to follow instructions found in file names.
- Account for Claude Desktop's different TCC permissions: tools report `unmeasured` honestly and suggest running `macdoc scan` from Terminal so MCP reads the cached scan.
- Rate-limit `create_cleanup_plan` to N per minute and cap open plans.

### Phase 10: Daily snapshots, TUI, slowness tips (~1.5 wk)
- Optional `macdoc schedule enable`, which installs a user launchd agent that runs a **read-only** `macdoc scan --quiet` daily at low priority. It never deletes anything and `schedule disable` removes it.
- Bubbletea cleanup picker: a checklist with SAFE items pre-checked, a live total, then the same plan → confirm → apply flow.
- `slow --tips`: suggestions such as "Quit Docker Desktop when you're not using containers (−3 GB RAM)" or "Use a smaller quantized model". Still no process actions.

### Phase 11: Open-source readiness (later)
- Recipe contribution guide, more generic thresholds, Homebrew tap, signing and notarization, and a privacy statement (no network).

---

## 5. Testing strategy

| Layer | Approach |
|---|---|
| Safety | Table-driven plus fuzz tests (`go test -fuzz`) on canonicalization and the deny list: `..`, unicode normalization (NFD vs NFC), case-insensitive APFS, trailing slashes, symlinks into protected areas |
| Parsers | Golden files from real command outputs (`diskutil`, `tmutil`, `vm_stat`, `docker system df`, `simctl -j`, `brew cleanup -n`, `ollama list`) |
| Scanner | Fixture trees covering sparse files, hard links, symlink loops, EACCES directories, deep nesting, 1M-file synthetic tree (benchmark RSS and time) |
| Recipes | Each recipe gets one positive fixture, one negative (look-alike path outside allowlist) and one precondition-failure test |
| Planner/executor | Recording executor for dry-run parity, sandbox-only real executor tests, and TOCTOU tests (swap a dir for a symlink after planning, change mtime, delete target) |
| Crash safety | Kill -9 during apply, then verify audit log consistency and quarantine manifests |
| Contract | JSON schema validation of every `--json` output, and later of the MCP tool outputs |
| Import guard | CI script fails if `internal/mcp` or `internal/engine` imports `internal/executor` |
| Manual QA | Run read-only commands on the real Mac for a week before enabling `apply`. The first real applies are SAFE-only |

---

## 6. MVP B Definition of Done

- [ ] `doctor`, `wins`, `storage`, `dev`, `projects`, `slow`, `explain`, `trend`, `plan`, `apply`, `quarantine`, `log` all work, each with `--json`.
- [ ] A full crawl stays within the 64 MB RSS budget, runs at background QoS, and the machine stays usable during it.
- [ ] Every reported number is either measured or explicitly "unknown (reason)".
- [ ] Plans are ID-only, hash-sealed and expiring. Apply needs a TTY and typed confirmation.
- [ ] The executor re-validates every item, and removal happens only through native commands or quarantine.
- [ ] The audit log records every action. Quarantine restore works.
- [ ] Fuzz and TOCTOU tests pass, and the import guard is in CI.
- [ ] At least 10 GB has been reclaimed on the real Mac with no regrets during the dogfood week.

---

## 7. Risk register

| Risk | Impact | Likelihood | Mitigation |
|---|---|---|---|
| Full crawl thrashes an 8 GB Mac | High | Medium | Background QoS, 4 workers, streaming aggregation, memory limit, priority ordering, cached results |
| Deleted space not freed (snapshots, clones, quarantine) | Medium | High | Report "freed now" vs "freed later", show snapshot caveats, verify step explains the gap |
| Wrong classification deletes needed data | High | Low | Recipes are allowlisted, deny list applies after canonicalization, uncommitted-git checks, RISKY items get per-item confirmation, quarantine allows undo |
| TOCTOU or symlink attack between plan and apply | High | Low | Fingerprint and canonical path re-check, never follow symlinks, skip on mismatch |
| Native tool behavior changes across versions | Medium | Medium | Parse JSON output where available, version-detect tools, golden tests, prefer the tool's own dry-run (`brew cleanup -n`) |
| Full Disk Access not granted | Medium | High | Explicit `unmeasured` states and a one-time FDA hint. Never report zero |
| Claude-host TCC differs from Terminal | Medium | High (C) | MCP reads cached scans, suggests running `macdoc scan` in Terminal |
| Prompt injection via file names | Medium | Medium (C) | No execution via MCP at all, escaped data fields, ID-only plans |
| Scope creep into "performance booster" | Medium | Medium | Non-goals list. `slow` stays explain-only |

---

## 8. Engineering conventions

- `internal/executor` is the **only** package allowed to call `os.Remove*`, `os.Rename` (outside the store and plans directories), or `exec.Command` for mutating commands. A lint rule (custom `go vet` analyzer or grep in CI) flags these calls elsewhere.
- All external commands go through `platform.Runner` with an argv allowlist and timeouts.
- No `sh -c`, ever.
- Every user-facing number carries its source (`measured`, `tool-reported`, `estimated`, `unknown`).
- Errors are values shown to the user in plain language, and the debug log has the details.

---

## 9. Remaining open questions

1. **Direct delete for pure caches?** Quarantining DerivedData or `~/.npm` means space isn't freed until expiry. Should recipes be allowed to opt into `delete` (SAFE, regenerable, native-equivalent only), confirmed per plan? *Suggested: yes, off by default, toggled in `config`.*
2. **Quarantine expiry default:** 3, 7 or 14 days? *Suggested: 7.* Also, should quarantine have a size cap after which the oldest entries are proposed for purge?
3. **Staleness threshold:** 60, 90 or 180 days idle? *Suggested: 90, configurable per recipe.*
4. **Local Time Machine snapshots:** advise-only, or a CAUTION recipe using `tmutil thinlocalsnapshots`? *Suggested: CAUTION, with an explanation that these are your local backups.*
5. **Final name:** `macdoc` is the working name.
