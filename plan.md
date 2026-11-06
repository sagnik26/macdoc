# macdoc: engineering plan (v2)

**Status:** in progress (see §12, the progress log). `CLAUDE.md` and `AGENTS.md` were deleted at your request on 2026-11-06; the safety rules they held remain the working constraints here and still live in `docs/SAFETY_MODEL.md` and the guard scripts: nothing runs a real cleanup on this machine, nothing is committed unless you ask, and the safety invariants override every task. This plan is the content of "Phase 11: open-source readiness" in `docs/DEVELOPMENT_PLAN.md:296`, reorganized around what has to be true, not around a checklist.

**Deeper, not bigger.** The first draft listed about 60 tasks. This one has six claims. Each claim has raw evidence, a time-boxed spike with a kill criterion, a method, an oracle (something that is neither me nor you) and a done-when. Everything else is in the appendix, deliberately deferred.

---

## 0. What this plan assumes and what it did not measure

Measured on `main` at `60a0165`, 2026-11-06:

| Fact | Value | How |
|---|---|---|
| Tests | pass with `-race`, 545 test functions in 19 test dirs | `go test -race ./...` |
| Lint | 0 issues under the repo config | `golangci-lint run` |
| Coverage | 87.6% total. `executor` 77.4%. `cmd/macdoc` 0%. `fsread` 60%. | `make cover` |
| Functions with cyclomatic > 15 | 21 | scratch gocyclo run |
| Functions with cognitive > 25 | 16 | scratch gocognit run |
| Undocumented exported symbols | 80 (41 in `cli`) | scratch revive run |
| Fold implementations | 3 (`safety.fold`, `store.Fold`, `strings.ToLower` in `engine/trend.go` and the scanner skip set) | grep |
| `Apply` signature | `Apply(ctx, cfg, plan model.Plan, approved Approvals)`: a plain struct, not a verified type | `executor.go:193` |

**Not measured, and why it matters:**
- **No real scan data exists on this machine.** `~/Library/Application Support/macdoc` is absent. So no query plan, no scan time, no RSS and no node count here is real. The recorded RSS of 24.7 MB (`docs/checkpoints/PHASE-2.md:28`) is the project's own report from another run. Workstream 0 exists to fix this.
- The state path derives from `$HOME` (`engine/state.go:37`), so I cannot redirect a scan to a scratch directory without scanning the wrong tree. I did not write into your real state.
- Where the 0.88 GB dogfood figure lives. The audit log that would prove it is not on this machine.
- Intel and any macOS other than 26 (Darwin 25.6).

---

## 1. Claim 1: every number macdoc prints is true

**Why this is first.** macdoc's output *is* numbers: reclaimable bytes, "freed now", sizes of "System Data". `AGENTS.md` invariant 10 says every number is measured, tool-reported, estimated or unknown. The scanner computes size as `st.Blocks * 512` (`scanner.go:203,462`). On APFS that over-counts in two ways, and the project's docs already name one of them as unsolved.

**Evidence.**
- `docs/DEVELOPMENT_PLAN.md:155`: "APFS clones: can't be detected cheaply."
- `docs/SAFETY_MODEL.md:107` lists "APFS clones" as an *explanation for a gap* between expected and actual freed bytes, so the gap is known and expected.
- The SDK says otherwise: `/Library/Developer/CommandLineTools/SDKs/MacOSX.sdk/usr/include/sys/attr.h` defines `ATTR_CMNEXT_PRIVATESIZE` (line 560, bytes freed if this object were deleted), `ATTR_CMNEXT_CLONEID` (565), `FSOPT_ATTR_CMN_EXTENDED` (53), and documents `getattrlistbulk(2)` (454, 590), which returns attributes for many directory entries per syscall.
- Today's scanner issues one `fstatat` per entry (`scanner.go:Fstatat`, `batch` at 427), through a hook variable, under `s.mu` for every large file.
- Folding is inconsistent: `safety/canonical.go:81` uses `norm.NFC` + `cases.Fold`; `engine/trend.go` (`nodeSizes`, `sizeIn`, `isUnder`) uses `strings.ToLower`; `store.Fold` is a third. A path can compare equal in one place and not in another.

**Spike S1 (time-box: 3 days; kill criterion below).**
1. Create a throwaway APFS volume: `hdiutil create -size 2g -type SPARSE -fs APFS -volname macdoc-oracle` and attach it. Never use a real volume.
2. Build a fixture: plain files, hardlinks, sparse files, `cp -c` clones (one file cloned 1×, 5×, 50×), an empty directory, a symlink loop.
3. Write a ~100-line Go program (outside the product module, in `tools/`) that reads those with `getattrlistbulk` + `ATTR_CMNEXT_PRIVATESIZE` + `ATTR_CMNEXT_CLONEID`.
4. For each fixture item, delete it and measure the real change in `statfs` `f_bavail × f_bsize` on the volume. That number is ground truth.
5. Compare three predictions to the truth: current `st_blocks × 512`, `PRIVATESIZE`, and the sum of `PRIVATESIZE` over a set of objects that share clones.

**Kill / go criteria (written now, not after the result):**
- *Go* if `PRIVATESIZE` predicts freed bytes within 1% on every fixture where `st_blocks` is off by more than 5%, **and** the bulk scan is no slower than 1.5× the current scanner on the benchmark in Workstream 0.
- *No-go* otherwise: keep `st_blocks`, add the "may include cloned blocks" confidence flag the docs already call for, and record the experiment in `docs/` so nobody repeats it.

**If go, implement.** A `scanner` mode behind an interface with two implementations; the cgo shim already exists (`internal/platform/darwin`, build tag `cgo`) with a stub fallback, so non-cgo builds keep working. Reclaimable-bytes for a *plan* is the sum of private sizes of its items, and plan items that share a clone ID are costed once.

**Unify folding.** One `pathkey` package (NFC + case fold) used by safety, store, trend and the scanner skip set. Add a property test that all callers agree on every generated name. **Ground truth for "is this path protected?" should not be a string compare at all:** at policy load, record `(st_dev, st_ino)` of each protected directory, and at check time walk the target's ancestors with `fstatat` and compare identities. APFS decides what "the same folder" means; we ask it. String folding becomes a fast pre-filter, never the final word. The risk this addresses is APFS's own case and normalization rules differing from `x/text` for some Unicode versions (verify on the throwaway volume with a few hard cases such as combining marks, `İ`, `ß`).

**Oracles (independent of me):**
- The kernel's free-space delta (above).
- `du -sk` and `diskutil apfs list` on fixture trees (differential test, run in CI on a macOS runner; Apple's open-source `du.c` defines the hardlink and block semantics we must match).
- `DEVELOPMENT_PLAN.md:173` already states an acceptance bound ("within ±3% of `diskutil`'s used Data volume"). Turn it into a test that runs on the fixture volume, not a sentence.

**Done when:** S1 has a recorded go/no-go; one fold implementation exists; a differential test compares macdoc totals to `du` on the fixture volume; the bound is a CI test.

---

## 2. Claim 2: the code that deletes is the most heavily verified

**Why coverage % is the wrong target.** Executor coverage is 77.4% and the list of uncovered functions is long (`moveBack`, `removeLeftoverEntry`, `realExec`, `gitClean` at 0%). But a number like 90% only says lines ran. What matters is whether a *wrong* implementation would be caught. The complexity data says the risk concentrates exactly here:

| Function | Cognitive / cyclomatic | Role |
|---|---|---|
| `safety.(*Policy).isProtectedWith` (`protected.go:105`) | 39 / – | decides what may never be touched |
| `executor.(*performer).quarantine` (`move.go:38`) | – / 22 | moves user data |
| `executor.checkContents` (`move.go:107`) | – / 21 | last check before the move |
| `executor.Restore` (`manage.go:36`) | 29 / – | puts data back |
| `executor.Apply` (`executor.go:193`) | – / 19 | entry point |
| `recipes.Validate` (`load.go:126`) | 62 / – | validates the catalog that defines allowed commands |

**Spike S2: mutation testing (time-box: 2 days).** Run `gremlins` (or `go-mutesting`) over `internal/safety`, `internal/planner`, `internal/executor`, `internal/precond` (they are small enough). Record the *survived mutants*. Each one is a place where changing the code does not fail a test, which is a concrete missing test. Fix by adding the test that kills it, not by writing tests to raise a percentage.
- *Pre-registered target:* mutation score ≥ 90% on `safety` and `planner`, ≥ 85% on `executor`; every surviving mutant either killed or explained in a comment.

**Methods.**
- **Property tests** (`pgregory.net/rapid`) for invariants that have a statement: canonicalization is idempotent; a path under a protected folder is protected after any sequence of case, normalization or `..` changes that resolves to the same inode; adding a protected folder never makes anything less protected (the config can only add protection, `AGENTS.md`); a sealed plan with any single byte changed fails `Verify`.
- **Fault injection** instead of mocks of the real filesystem: a small `fs` interface at the executor's boundary with a failing implementation (rename returns `EXDEV`, `EACCES`, short write, `ENOSPC`). The executor already has a `Config` seam (commit `d401318`); extend that rather than adding globals.
- **Crash tests:** run `Apply` in a subprocess under `MACDOC_SANDBOX`, SIGKILL it after each audit record, and assert the on-disk state is recoverable and `macdoc log` describes it. `Apply` marks the plan applied *before* acting (`executor.go:226`), which gives at-most-once; the open question is whether an interrupted plan is visible to the person. This test finds out.
- **TOCTOU tests** at every check-then-act pair: item replaced by a symlink, parent replaced by a symlink, hardlink inside a moved tree pointing outside `$HOME`, item turning into a mount point.
- **Fuzz** the untrusted parsers beyond `safety`: plan JSON through `Verify`, quarantine manifests, `config.json`, recipe YAML, and the `xcrun`/`docker` JSON parsers (`xcode.go:226,386`, `docker.go:67`), the last three of which read whatever a tool prints with no output cap.

**Make illegal states unrepresentable (type-level safety).** Today `Apply` takes `model.Plan`. `planner.Verify` returns only `error`. A reviewer must trust that `Apply` re-verifies. Spike: have `Verify` return a `Verified` value with an unexported constructor, and make `Apply` require it. Then "the executor acts only on verified plans" (invariant 3 and 7) is a compile-time fact for any caller, and `scripts/imports.sh` becomes a second line of defence instead of the only one. Same idea for IDs: `OpportunityID` and `PlanID` as distinct types, so a path cannot be passed where an ID is expected (invariant 4).

**Oracles:** mutation survivors; fuzz and property-test minimal counterexamples; an external red-team reviewer given the code and `SAFETY_MODEL.md` but *not* the conclusions of previous red-teams (the repo already records 9 + 12 findings from earlier passes in `docs/checkpoints`, which shows the method works).

**Done when:** mutation targets met; crash and TOCTOU suites pass in CI; fuzz targets exist for each parser and run 30 minutes clean; `Apply` requires a `Verified` plan (or the spike records why not).

---

## 3. Claim 3: it is fast where it has to be and nowhere else

**Principle.** No optimization without a profile of real data and a regression guard. Several things in the code look suspicious; each gets a measurement and a pre-registered threshold, and some will be rejected.

### Workstream 0: a corpus and a benchmark (prerequisite for everything in this section)
- **Correction (found while implementing):** two benchmarks already exist: `BenchmarkScanSynthetic` (`internal/scanner/tests/scanner_test.go:670`, ~100k files, reports peak heap, but building its tree takes minutes) and `BenchmarkAddNodes` (`internal/store/tests/store_test.go:796`). My earlier "none found" came from a failed grep. Extend these rather than starting over. The project also recorded one real scan (`docs/checkpoints/PHASE-2.md`): 994k files, 40-41 s, peak RSS 24.7 MB; use it as the real-data anchor until a fresh one is captured.
- Generate a deterministic synthetic home with `internal/testutil/fstree` at 10k, 100k and 1M files with realistic shape (deep `node_modules`, wide cache directories, many small files, a few huge ones). Tree construction is the slow part today, so build it once per run and reuse it.
- `BenchmarkScan` records ns/op, B/op, allocs/op and **peak RSS** (`getrusage ru_maxrss`).
- Then, *one time*, on a real Mac you control, run a full scan with `/usr/bin/time -l`, `fs_usage -w -f filesys`, and a CPU/alloc profile to capture the real node count and shape. Save only aggregate shapes (counts per depth, size histogram), never paths, so it can live in `docs/`.
- **Empirical-complexity test:** run the scan at n, 2n and 4n files and assert time grows no worse than ~n·log n (log-log slope < 1.15). This catches an accidental quadratic far better than a wall-clock threshold.
- CI: nightly `benchstat` against the last release; fail on a statistically significant regression of more than 10% time or 15% memory, and on RSS above the documented 64 MB budget.

### Hypotheses to test (each has a kill criterion)

| # | Suspicion (evidence) | Measurement | Act only if |
|---|---|---|---|
| H1 | `growing()` (`engine/trend.go:195`) is cubic: the "explained by descendants" pass loops `kept`, then `kept[:i]`, then calls `nestedIn`, which loops `kept` again. `isUnder` calls `strings.ToLower` on both arguments inside those loops. | Count of candidates K on a real two-scan trend; `BenchmarkGrowing` over K = 50, 500, 5000. | K p95 on real data > ~2,000, or the benchmark shows > 50 ms at observed K. Otherwise **do nothing** and write down why. |
| H2 | Detector node lookups use `path_fold LIKE ? ESCAPE '\'` combined with `(? = '' OR …)` and `(? < 0 OR depth <= ?)` (`store/store.go:339`). A bound-parameter test like `? = ''` is not known at plan time, so SQLite may fall back to a scan of the scan's rows for each of ~9 detector call sites (`registry.go:115,157`, `detect_*.go`). | `EXPLAIN QUERY PLAN` on a populated DB from Workstream 0; timing of 100 detector lookups. | the plan shows a full scan of `nodes` for the scan id, or lookups dominate `Opportunities` in a profile. Candidate fix: rewrite as the range form `path_fold >= 'p/' AND path_fold < 'p0'` chosen in Go (separate statements for "no prefix" vs "prefix"), which can use the primary key `(scan_id, path_fold)`. |
| H3 | `nodeSizes` loads *all* kept nodes of two scans into maps (`trend.go:168`, `NodeQuery{}` with no limit). Fine if the kept set is small; breaks the "never hold the full file tree in memory" rule if not. | Row count per scan from the real corpus; heap in `BenchmarkTrend`. | heap for Trend exceeds 20% of the 64 MB budget. |
| H4 | The scanner takes `s.mu` once per large file to append a node (`scanner.go:487`) and `strings.ToLower`s a joined path for every entry in a watched directory (`:455`). | `pprof -mutex` and `-alloc_space` on `BenchmarkScan`. | mutex contention > 5% of wall time or ToLower allocations in the top 10 of alloc_space. Candidate fix: per-worker node buffer merged at directory completion; a precomputed folded-name set compared per component. |
| H5 | One `fstatat` per entry is syscall-bound on large trees. | `fs_usage` syscall counts; compare to the `getattrlistbulk` spike (S1), which batches entries per syscall. | Decided jointly with S1: if bulk wins on speed *and* gives private sizes, adopt it for both reasons. |
| H6 | Store writes: how nodes are inserted (`store.go:184` prepared statement in a transaction) and the cost of `wal_checkpoint(TRUNCATE)` (`:469`) after a 1M-node scan. | `BenchmarkStoreSave` at 1M rows; WAL size; `sqlite3_analyzer`. | save time > 25% of scan time. |

### On "dynamic programming"
The honest answer from reading the algorithms: there is exactly one place where it applies, which is H1.
- `growing()` is an aggregation over a *tree* given as paths. The "descendants already explain this folder's growth" quantity is a classic bottom-up tree DP: process folders deepest-first, and for each keep `explained[folder] = sum of Change.Low of the nearest kept descendants`, computed once, instead of rescanning `kept` per folder. With paths sorted and a stack (or an explicit trie), the whole pass is O(K log K) and allocation-free once keys are folded once up front.
- The scanner's rollup (`release`, folding finished directories into the parent when `pending` reaches zero) is already a post-order tree DP in O(N). The hardlink set (`LinkSet.Count`) is already a hash set. There is no overlapping-subproblem structure elsewhere that a memo table would improve.
- Elsewhere the wins, if any, are data-structure and I/O wins (H2, H4, H5), not DP. The plan says so rather than forcing the technique.

**Oracle:** `benchstat` (with its significance test), the log-log scaling slope, and the nightly regression gate, none of which depend on my reading of the code.

**Done when:** the benchmark and corpus exist; every hypothesis has a recorded accept/reject with numbers; accepted ones are implemented with a before/after `benchstat` table checked into `docs/perf/`; RSS ≤ 64 MB is enforced in CI.

---

## 4. Claim 4: the structure helps a stranger change it safely

"OOP structure" in Go means small interfaces at the consumer, composition, and dependency direction, not class hierarchies. What the data says:

**Hotspots = churn × complexity** (a standard code-health method; `git log --since=2026-10-01`):

| File | Changes in 5 weeks | Worst function |
|---|---|---|
| `cli/root.go` | 17 | |
| `cli/tui.go` | 14 | `updateKey` cognitive 43, `Update`/`View`/`RunCommand` cyclomatic 16 |
| `cli/doctor.go`, `overview.go`, `explain.go` | 12, 9, 9 | `RenderOverview` 18 |
| `engine/opportunities.go` | 9 | `Classify` 23, `Opportunities` cognitive 31 |
| `engine/scan.go` | 7 | `Scan` 19 |

The safety kernel changes little but is complex (Claim 2); the UI changes a lot and is complex. Those are different problems.

**Findings.**
1. `internal/cli` is doing three jobs: command wiring, rendering, and engine/executor orchestration (`TuiModel` is one type holding all of it; `dashboard.go` is 1,193 lines). Seams exist only as exported mutable variables (`RealTTY`, `NowFunc`, `ScanRoot`), which is how 41 undocumented exports and test-only API ended up in `cli`.
2. Global registries filled by `init()` in 8 recipe files, with a `panic` on a duplicate name (`registry.go:26,44`), hide the dependency of `engine` on `recipes` and prevent parallel tests.
3. `recipes.NativeArgvAllowed` and `DryRunArgvAllowed` are exported, writable slices. They define which commands macdoc may ever run (invariant 8). Any package that imports `recipes` can append to them.
4. Constructors return unexported types from exported functions (`PlainTheme`, `ColorTheme`, `ListAVDs`), which is the signature of an API that was shaped by its tests.

**Spike S3: find the real test-only API (time-box: 1 day).** Run `deadcode` (golang.org/x/tools/cmd/deadcode) from `main` without `-test`: exported functions unreachable from `main` but referenced by `tests/` are the test-only exports. Run `apidiff` between the current exported surface and one with those removed. That produces a number: how many exports exist only so that tests in `tests/` directories can reach them. *Decision rule, set now:* if test-only exports exceed ~60, move tests next to the code; if fewer, keep `tests/` and document the deviation in `CONTRIBUTING.md`.

> **Correction after running S3 (see §12).** `deadcode` from `main` is the wrong instrument (it found only 6, because most test-only exports are also called by `main`'s own code). A type-checker scan found **263 of 627 exports (42%) are referenced from other packages only by tests**, which exceeds the threshold. But the rule's premise was wrong: a same-directory `package x_test` is still black-box, so it can only reach exported names too. Only **white-box in-package tests** (`package x`) would let those 263 become unexported. Because everything is under `internal/`, none of these exports is a public API, so the benefit is smaller than I wrote (less doc and lint noise, a clearer intent per symbol) and the cost is high (rewriting the tests' package, imports and the documented `tests/` convention). Decision is left to the owner; the default below is to keep the layout.

**Target structure (rules enforced by tooling, not by habit).**
- `depguard` / `go-arch-lint` config: `model` imports nothing internal; `safety`, `planner`, `recipes`, `store` import `model` only; `engine` imports those; `mcp` and `engine` never import `executor` (replaces the single shell-script guard with a declarative graph, and keep the script as a second guard); `cli` is the only package that renders and the only one that calls the executor.
- `cli` split into `cli/command` (cobra wiring, one file per command), `cli/render` (pure functions from result struct to string, golden-tested), and `cli/tui` (model/update/view in separate files, with `updateKey` becoming a table of key → handler).
- `engine` takes a small `Dependencies` struct (store, runner, clock, logger, registry) instead of reaching for globals. This is the pattern the repo already used once for the executor (`d401318`).
- Registries become values: `recipes.NewRegistry(...)` returns an error on duplicates; no `init()`.
- Allow-lists become unexported, with accessor functions returning copies, and one test proving a caller cannot mutate them.

**Complexity gates.** Add `gocognit` (≤ 25) and `gocyclo` (≤ 15) to `.golangci.yml` with `new-from-rev`, so new code is held to it while old hotspots are refactored in order of churn × complexity. Refactors split functions without changing behavior, one function per commit, behind the existing golden and contract tests (`TestSchemasFrozen` and the JSON validation tests already freeze the output).

**Oracles:** `deadcode` and `apidiff` (they count; I don't), the linters' complexity numbers, and the golden/contract tests that must stay unchanged across every refactor commit.

**Done when:** S3 decision recorded and executed; dependency graph enforced by config; no function above the gates in `safety`, `planner`, `executor`; no exported mutable globals; registries are values.

---

## 5. Claim 5: a stranger can understand, trust and operate it from the docs

The repo has 11 phase checkpoints, 13 task briefs and a design set, which is the *history of how it was built*. What a user, a contributor or an auditor needs is different.

**Documents that are missing or wrong (evidence):**
- README progress table leaves Phase 10 blank while `AGENTS.md` says it is built.
- `DEVELOPMENT_PLAN.md:155` states clones can't be detected cheaply (contradicted by the SDK; fixed or re-confirmed by S1).
- It promises a `log/slog` debug log (tech choices table); no package uses `slog`.
- No uninstall or reset procedure; no supported-versions statement; no troubleshooting for Full Disk Access; no threat model separate from the safety rules.

**What to write, in order of value:**
1. **Threat model** (`docs/THREAT_MODEL.md`): assets (user files, the audit log, the plan seal), adversaries (a repository whose filenames try to instruct Claude over MCP; another local process swapping a symlink; a stale or forged plan file; a malicious `config.json`), trust boundaries (disk → scanner, scanner → store, store → engine → MCP, plan file → executor), and for each boundary which invariant and which test defends it. Every row links to a test name, so the document fails review if a test is deleted. This is the document an external auditor starts from.
2. **Architecture decision records** (`docs/adr/NNNN-*.md`): one per decision already made but only recorded in chat or commit messages: TTY-only confirmation, immediate delete after quarantine move (2026-10-28), no shell, IDs not paths, why `st_blocks` (and the outcome of S1), why SQLite, why `tests/` layout (or why it changed).
3. **Package docs with invariants:** a `doc.go` per package stating what it must never do (for `safety`: "never widens protection", for `planner`: "never imports executor"), plus `Example*` tests so the godoc examples are compiled and run. Revive's `exported` rule gates the 80 missing comments; they are written package by package starting with `model`, `quarantine`, `planner`, `store`, `mcp`.
4. **Generated reference**, so docs cannot drift: the recipe table (id, tier, restore cost, native command) generated from `catalog/*.yaml`; the command reference from cobra; the JSON reference from `schema/*.json`. A CI step regenerates and fails on a diff.
5. **Operator docs:** install, uninstall (state directory, launch agent), upgrade, recovery from a corrupt `state.db`, how to read the audit log, how to file a useful issue (the diagnostic bundle with no paths), supported macOS versions.
6. **A docs test:** link checker plus a script that every exported identifier named in `docs/` still exists (`go doc`), so renames update the docs or fail CI.

**Style (Google Go guide), what is cheap and what is not.**
- Cheap and mechanical: `Id`→`ID` (3 sites), `check.CheckSource` stutter, the one malformed package comment (`tools/forbidcheck/check/run.go:1`), 2 capitalized error strings, 3 exported functions returning unexported types, `gofumpt`. Gate with revive's `exported`, `package-comments`, `var-naming`, `unexported-return`, `error-strings` via `new-from-rev`.
- `t.Parallel()`: 0 of 545 tests use it. Add it where tests only use `t.TempDir()`; tests that call `t.Setenv` stay serial and are listed. Do this after registries stop being global, or it will be flaky.
- Table-driven subtests with named fields in `recipes/tests` (111 tests) and `safety/tests` (47).

**Oracles:** the doc-regeneration diff in CI, the link checker, the requirement that every threat-model row names an existing test, and one outside reader: ask a person who has not seen the repo to follow *only* README and CONTRIBUTING to build, add a recipe in a sandbox and run its three tests, and record where they got stuck.

**Done when:** the six documents exist and are checked by CI; 0 undocumented exports; a cold read-through has been done by someone else and its findings fixed.

---

## 6. Claim 6: it can be shipped, verified and trusted by someone else

Kept short on purpose; this is the best-understood area. Depth is in making the *verification* independent.

- **Module identity first**, since it touches every import: `module macdoc` → the repository path (the remote is `github.com/sagnik26/macdoc`; confirm the owner). `-X macdoc/internal/cli.version` in the Makefile changes too. Add `debug.ReadBuildInfo` as the version fallback so `go install` builds are not reported as `dev`.
- **Reproducible build:** `-trimpath`, fixed `-buildvcs`, `CGO_ENABLED` documented (the cgo shim means universal binaries must build on a macOS runner). Two independent builders (CI and a second machine) compare SHA-256; differences are examined with `diffoscope`. A reproducible build is the strongest "someone else can check my binary" evidence available.
- **Supply chain:** `govulncheck` in CI, Dependabot for `gomod` and `github-actions`, `go-licenses` report for the 77-module graph (check licenses of the SQLite and Charm stacks), SBOM and build provenance attestation from the release job.
- **Release:** GoReleaser with darwin arm64 + amd64, checksums, changelog; a tag-triggered workflow with `contents: write` only on that job; actions pinned by SHA. Signing and notarization only if you decide to pay for a Developer ID; Apple's notarization is itself an independent malware scan. Without it, document the Gatekeeper behavior honestly.
- **CI breadth:** macOS arm64 and Intel, and at least the oldest macOS you claim to support, because probes and recipes call OS tools whose output formats change (`xcrun simctl`, `diskutil`, `tmutil`). Golden fixtures of those tools' outputs per macOS version, captured from real machines, are the raw material here, not hand-written strings.
- **Publication hygiene:** read `docs/tasks/**`, `docs/tasks/reports/**`, `docs/checkpoints/**` and `scripts/orchestration/**` for usernames, absolute paths and machine specifics; scan history, not only the tree. Decide whether process artifacts belong in a public repo.
- **Name:** `macdoc` is a "working name" in the README. Check for collisions before the first tag.

**Oracles:** the second builder's checksum; `govulncheck`'s database; Apple's notarization service; a clean-machine `go install …@v0.1.0` run by someone else.

---

## 7. Sequencing

```
S1 (APFS truth)  ─┐
S2 (mutation)     ├─ run first, in parallel: each is a time-boxed experiment with a written go/no-go
S3 (test-only API)┘
Workstream 0 (corpus + benchmark + one real scan shape)  ──► H1..H6 accept/reject
        │
        ▼
Claim 1 implementation (if S1 go) ── fold unification ── differential tests
Claim 2 implementation (mutants killed, crash/TOCTOU suites, Verified type)
Claim 4 refactors (hotspots by churn×complexity, registries as values, seams as options)
Claim 5 docs (threat model needs Claims 1–2 results; ADRs can start now)
Claim 6 release (module path first; everything else after Claims 2 and 5)
```

Rules for the order:
1. **Experiments before refactors.** S1 may change what the scanner is; refactoring the scanner before S1 risks doing it twice.
2. **Module path rename lands first** (one large mechanical commit), so all later diffs are against the final import paths.
3. **The executor stays closed to feature work** while Claim 2 runs; only tests and the `Verified` change touch it.
4. Style-only commits are separate from behavior commits. Every commit passes `make test lint importcheck forbidcheck`, and refactors must leave `schema/*.json` unchanged (`TestSchemasFrozen` without `-update`).
5. Large moves last: the `tests/` layout migration (if S3 says so) and `dashboard.go` split happen after other work so they do not cause conflicts.

---

## 8. Pre-registered thresholds (set before any result exists)

| Area | Threshold |
|---|---|
| Size prediction (S1) | within 1% of kernel free-space delta on clone fixtures, or no-go |
| Bulk scan speed | ≤ 1.5× current scanner time on the 1M-file corpus |
| Scan memory | peak RSS ≤ 64 MB at 1M files |
| Scaling | log-log slope of time vs n < 1.15 |
| Regression gate | no significant (benchstat) regression > 10% time, > 15% memory |
| Mutation score | `safety` ≥ 90%, `planner` ≥ 90%, `executor` ≥ 85% |
| Fuzz | 30 minutes per target, 0 crashers |
| Complexity | cognitive ≤ 25, cyclomatic ≤ 15 in new code; none above in `safety`/`planner`/`executor` |
| Exports | 0 undocumented; 0 exported mutable globals |
| Reproducibility | two independent builds with identical SHA-256 |

---

## 9. Risks

| Risk | Why it is real | Mitigation |
|---|---|---|
| S1 is a no-go and the work is wasted | `PRIVATESIZE` semantics may differ on snapshots or purgeable data | Time-box 3 days; the spike runs on a throwaway APFS image; a no-go still produces the documented confidence flag and an ADR |
| A refactor weakens a safety invariant | The riskiest code is also the most complex | Mutation tests and crash/TOCTOU suites land *before* any executor/safety refactor; CODEOWNERS for `executor`, `safety`, `recipes/catalog`, `scripts/*check*` |
| Type-level `Verified` plan is circumvented in tests | Tests may want to build plans by hand | An unexported constructor plus a `planner/plannertest` helper that is itself forbidden from `mcp` and `engine` by `depguard` |
| Optimization of something that does not matter | No real profile exists yet | Hypotheses H1–H6 have kill criteria; "do nothing" is a valid, recorded outcome |
| `getattrlistbulk` adds cgo/unsafe surface to a safety-sensitive binary | More code near the kernel boundary | Fuzz the attribute-buffer parser; keep the `os.Lstat`-based scanner as the fallback and the differential oracle |
| Docs generated from code expose internal wording | Public repo | Review generated docs once; keep generation scripts out of the executor's reach |
| Benchmarks are flaky on shared CI | Noisy machines | Nightly, relative to the previous run on the same runner class, with `benchstat`'s significance test; never a wall-clock threshold on PRs |
| Scope creep from the appendix | Many tempting small items | Appendix items need a measured reason to move up |

---

## 10. Appendix: deliberately deferred

These are real and probably worth doing, but depth on the six claims comes first. Each needs a measured reason to move up.

- **New recipes** (Rust `target/` and cargo registry, Go caches via `go clean`, Flutter pub cache, Maven, RubyGems, JetBrains and VS Code/Cursor caches, SwiftPM, Playwright, Colima/OrbStack/Lima disk images as advise-only). First step is a read-only path-and-size audit on a real machine, including overlap with `user.app_caches` so totals are not double counted. Each recipe needs the three tests from `docs/RECIPE_CATALOG.md` §3.
- **`macdoc reset`** to remove state and the launch agent. It is a new mutating command: it needs your approval, an `internal/executor` implementation, and an update to the invariant-1 list in `AGENTS.md` and `SAFETY_MODEL.md`.
- **`log/slog` file logging** and a path-free diagnostic bundle for issue reports.
- **Audit-log rotation** (no rotation exists today) and migration/corruption tests for `state.db`.
- **Phase 7 sign-off.** The ≥ 10 GB reclaim criterion is 0.88 GB. It needs a decision on whether the criterion stays as written or becomes measurable on a fresh machine; without it `v0.1.0` has no definition of done.

---

## 11. What I need from you

1. **Approve the six claims and the spikes as the scope**, or tell me which to cut. I'd start with S1, S2, S3 and Workstream 0 together, since nothing else is well-founded until they report.
2. **Real data:** I need one real scan's aggregate shape (counts per depth, size histogram, node count) from a Mac with a populated state. I will not write into your real state directory myself. Tell me if you want to run `macdoc doctor scan` yourself and share only the aggregates.
3. **Decisions:** repository path/owner, whether `macdoc` is the final name, and whether signing/notarization is in scope for `v0.1.0`.

---

## 12. Progress log

Updated 2026-11-06. Nothing is committed; all changes are in the working tree. `make check` (race tests, lint, import guard, forbidden-call guard) passes.

### Done

| Item | Result | Evidence |
|---|---|---|
| Module path | `module macdoc` → `github.com/sagnik26/macdoc` (owner confirmed by the user). Imports, `-X` version flag and `forbidcheck` constant updated. | `make check`; `macdoc version` still stamps the build |
| Import grouping | The rename put the module's imports between standard-library ones. Fixed with the `goimports` formatter and a local prefix. | `golangci-lint fmt` |
| A1 style and complexity gates | `revive` (exported, package-comments, var-naming, unexported-return, error-strings, context-as-argument, indent-error-flow), `gocognit` 25, `gocyclo` 15, `goimports`; gated to new code with `issues.new-from-rev: origin/main`; default exclusion presets removed so the doc-comment rule is not silenced; CI checks out full history. Verified by adding a bad function and watching both gates fail it. | `.golangci.yml`, `.github/workflows/ci.yml` |
| A2 naming | `Id`→`ID` (3 sites), `CheckSource`→`Source`, package comment of `forbidcheck/check` fixed. The touched `Source` function is annotated with its pre-existing complexity (62) for the later refactor. | `make check` |
| A3/A4 exports and docs | Every exported identifier has a doc comment (85 findings -> 0 in revive across the tree); `Theme` and `AVD` exported where exported functions returned them unexported; comments written from reading the code (one inferred claim was caught and corrected). | revive scratch config reports 0 |
| **S1 APFS truth** | **Accuracy half: go.** `PRIVATESIZE` is exact for every single-file case except hardlinks; `st_blocks` overstates clones by up to 3x; clone groups need a range. Speed half: see H5. | `docs/experiments/S1-apfs-private-size/` |
| **S3 test-only exports** | 263 of 627 exports (42%) are used outside their package only by tests. Premise of the original rule corrected (see §4). | `docs/experiments/S3-test-only-exports/` |
| **S2 mutation testing (partial)** | precond 60% → 95.4%, safety 81.9% → 89.3%, planner 47.2% → 78.1% killed; executor ~26% measured on an unfinished run. New tests for `git_clean`, exact protection reasons, error causes, `Verify` clause by clause, `Build` boundaries and Report totals. | `docs/experiments/S2-mutation-testing/` |
| **Release pipeline (claim 6, partly)** | `.goreleaser.yaml` (darwin arm64 + amd64, `-trimpath`, commit-pinned times, checksums, draft release), tag-triggered `release.yml` (tests, guards, `govulncheck`, attestation), `dependabot.yml` for Go modules and Actions, `make vulncheck` in CI, all actions pinned by commit SHA. Verified: `goreleaser check`, `actionlint`, a full local dry run for both architectures with cgo, `govulncheck` clean, and two builds of one commit giving byte-identical archives. A universal binary was tried and dropped because GoReleaser's fat wrapper is not reproducible while its slices are. | `docs/RELEASING.md` |
| **`Verified` plan type (spike, not adopted)** | `executor.Apply` already verifies unconditionally with its own store, catalog and a fresh policy, so a caller cannot skip it; a token would add compile-time evidence but no protection, at the cost of changing `Apply` for 2 production and 23 test callers. Recorded in ADR 0003. | `docs/adr/0003-executor-is-the-only-mutator.md` |
| **Parallel agents (algorithms, tests, refactors)** | Five agents in isolated worktrees, each result re-verified after transplanting onto this branch. **Scanner:** `getattrlistbulk` replaces one `fstatat` per entry; on a real tree (`/opt/homebrew`, 322,511 files) both readers give identical totals and kept nodes, and wall time falls from about 4.0 s to 2.3 s (12 alternating runs); allocations -74%. **Trend `growing`:** cubic to near-linear, 26.9 s to 5.6 ms at 5,000 folders, 100,000 random cases identical to the old code. **Node index:** per-scan in-memory index, identical to `store.Nodes` on 6,000 random queries; the gain is large only on very big scans (14 s to 1.2 s at 300k nodes) and about 25% at a realistic size, so it is capped at 50,000 nodes (about 16 MB) to respect the memory goal. **Complexity:** `Validate` 62 to 1, `check.Source` 62 to 5, `isProtectedWith` 39 to 5, no differences over 400k, 60k and 1.5M generated inputs. **Executor tests:** nine new files; kill rates per file in `docs/experiments/S2-mutation-testing`; the agent's report was interim and its classification of equivalent mutants is by reading, not machine-checked. | branches merged into `hardening/plan-v2` |
| **Bug found by mutation testing (T17)** | `InstallSchedule` followed a symlink at the launch-agent path (dangling or to another macdoc-labelled file) and wrote through it, outside the one permitted file. Reproduced by two failing tests, fixed with `Lstat` plus `O_NOFOLLOW`. | `docs/THREAT_MODEL.md` T17 |
| **One-word names (owner rule, 2026-11-06)** | Files done: 60 renamed with `git mv`, `AGENTS.md` records the rule. Identifiers: wave 1 (8 small packages) done with a type-checked renamer that refuses collisions: 58 of 60 renames applied, 376 edits in 43 files, wire formats unchanged (every renamed field has an explicit tag). Roughly a fifth of the names cannot be one word (collisions, qualifier pairs, the `Err` convention, loss of meaning); they are listed in the experiment. Wave 2 (`planner`, `safety`, `scanner`): 45 more renames. Several function names read worse as one word (`buildItem` to `item`, `validateItems` to `vet`), so functions are the likely place to keep two-word names. `store` and `probe` are not renamed: most names would collide or lose meaning. The rest of the packages are not done and need a decision. | `docs/experiments/S5-single-word-names/` |
| **Sizing stage 1 (ADR 0005, owner-approved)** | Every kept node now carries a private size (`private_bytes`, NULL when unknown), read in the same bulk call and summed up the tree; nothing user-visible changed and `bytes` is identical (10,852,208,640 on `/opt/homebrew`). Unknown is never zero: a node is unknown if any file below it is (the stat fallback, HFS+ and exFAT return no private size). Tested with real `cp -c` clones: a unique file, a clone of an outside file, and a clone group wholly inside, plus a migration test on an old-schema database. **Cost, measured:** the private-size attribute alone adds about 2.3 µs per file, +49% wall time over the bulk-only scan on a real tree, which brings the scan back to about what the original per-entry reader took. A cheaper rule "clone count 1 means private equals allocated" was checked and is **wrong**: a diverged clone has count 1 and private 8 MB of 64 MB (S1, scenario 8). Open for stage 2: hardlinks (the kernel reports the full size for each link, so a tree holding only some of an inode's links overstates; `bytes` has the same flaw today). Stages 2 and 3 are built (see the next rows). | `docs/adr/0005-size-by-allocated-blocks.md` |
| **Sizing stage 2 (ADR 0005, owner-approved)** | A suggestion made from a scan node is now sized by the space removing it is certain to free (`Node.Reclaimable`: private size when known, allocated bytes when not), so `wins`, `explain`, plans and totals no longer overstate for APFS clones; tool-reported sizes are untouched. A file with several hard links adds a known 0 to the private sum (tested with a link whose other name is outside the scanned tree). A known 0 is shown as 0 B and hidden by the 1 MB display threshold like any small item. `doctor projects` keeps `bytes` as allocated and gains `reclaimable_bytes` (additive; schema regenerated, no `schema_version` bump). `doctor explain` and the apply report say what the number means. Tested end to end with real `cp -c` clones. Open: the apply report's new sentences are untested; for a SAFE item that changed after the plan the executor still records allocated bytes; stage 3 (ranges) not started. | `docs/adr/0005-size-by-allocated-blocks.md` |
| **Background priority only for a scan (found with real data)** | The bare `macdoc doctor` stayed at 6.3 s even with the Homebrew cache warm. The engine's own work took 2.4 s (measured with stage timing and a logging runner); the rest was `runOverview` and the dashboard lowering the **whole process to background priority up front**, even when no scan runs, which deliberately slows every `diskutil`, `git` and ranking call. A variant without that call took 1.45 s. Priority is now lowered by the existing `StepScan` callback, so only a scan is throttled. After: overview **1.45 s** (was 6.3), `doctor wins` 0.8 s, `space` 1.1 s, `health` 0.4 s, `projects` 0.8 s; a real rescan still runs at nice 5. Together with the Homebrew cache, the cached sections went from about 7.5 s to about 1 s. | `internal/cli/overview.go`, `internal/cli/tests/background_test.go` |
| **Sizing stage 3: the range (built by an agent, verified here)** | `doctor explain` now says "frees at least X and at most Y" when a folder's allocated size is noticeably above its private size (more than 1% and 1 MB). It is computed **on demand for the one suggestion**, not in the scan, so the scanner's memory budget is untouched. `scanner.Measure` asks the kernel for clone IDs as well; the walk takes about what a scan of the same folder takes (0.2 s for `/Applications`, 2.3 s for `/opt/homebrew`, 5.6 s for 725k files; 12 to 17 MB peak). The clone-ID set is capped at 250,000 entries (about 15 MB; a million would be about 60 MB, over the budget); past the cap, or with an unreadable folder, only "at least X" is shown. The upper end is the smaller of two bounds, because a chain of rewritten clones makes counting each clone ID once overcount. **Checked against the kernel's free-space change on a throwaway APFS image: the true value was inside the range in all 10 scenarios** (opt-in test `MACDOC_ORACLE_VOLUME`). On this Mac the real 1.3 GB cache suggestion shows "at least 1.3 GB and at most 1.4 GB" and the JSON has `bytes_lower` and `bytes_upper` (additive). Not covered: the MCP server and the dashboard's explain pane still show the stage 2 text; snapshots and open files are outside the model; a hardlink whose other link is outside leaves the upper end at the file's full size. | `docs/adr/0005-size-by-allocated-blocks.md` |
| **Homebrew dry-run cache (built by an agent, verified here)** | The two Homebrew dry runs are cached in the database for display, valid while a cheap fingerprint of Homebrew's state is unchanged (Cellar and version folders, Caskroom, download cache, the `brew` binary; about 9 ms for 300 formulae) and for at most 10 minutes. A repeat `doctor wins` takes about 0.8 to 2.0 s instead of about 7.5 s. Any cache failure runs the real command; a failed run is never cached. Display only: checked in the code that the cache is attached in one place (`engine/opportunities.go`), plan creation forces a live run, and the executor, planner and preconditions never see it. Nine kinds of Homebrew change each invalidate it in tests. The bare overview stayed slow for a different reason, found next (background priority). | `internal/recipes/cache.go` |
| **First real scan (this Mac, 2026-11-06)** | `macdoc doctor scan`: 2,209,961 files in 47 s at background priority (the scanner alone takes 20 s and peaks at **16.6 MB**, well under the 64 MB budget). 227.1 GB measured; **18.1 GB (8%) of it is shared with clones or hardlinks**, so allocated sizes overstated by that much; all 3,232 kept nodes have a known private size. **The kept set is 3,232 nodes, not the 300,000 of my synthetic benchmarks**, so the large wins measured on synthetic data (the 1,100x prefix query, the 5,000-folder trend case, the in-memory node index) are correct but small on a real Mac; the whole database is 1 MB. **A problem found only with real data:** every cached `doctor` section takes about 7.5 s. **Correction to the first write-up of this row**, which blamed `git` and macdoc's memory: instrumenting the real suggestion pipeline with a logging command runner showed that the time is **one command, the Homebrew dry run `brew cleanup -n` (7.54 s inside macdoc, about 3.5 s on its own)**, plus `brew autoremove -n` (0.9 s); every `git`, `docker`, `xcrun` and `tmutil` call together is under 1 s. macdoc's own heap in that pipeline is **3 MB** (85 MB allocated over the run). The 150 MB I reported is `brew` itself (Ruby): `/usr/bin/time -l` reports the largest memory of any process the command waited on. Homebrew's optional switches (`HOMEBREW_NO_AUTO_UPDATE` and similar) save only about 0.2 s. A fix would cache the two Homebrew dry runs, invalidated by a fingerprint of the Cellar and download cache plus a time limit; it is not built because staleness rules need a decision. | `scripts/shape.sh` |
| **Repository hygiene (claim 6)** | `.github/CODEOWNERS` for the safety-critical paths, a pull-request template with the project's pre-merge checklist, and issue forms (bug, recipe request) with blank issues off and a link to private vulnerability reporting. YAML parses. **Not validated by GitHub:** its CODEOWNERS check only runs on the default branch, so syntax and owner access are unconfirmed until this merges. Review is not *required* unless branch protection on `main` enables code-owner review, a repository setting. | `.github/` |
| **H2 node-prefix query (claim 3)** | Confirmed and fixed: with 300,000 synthetic nodes a prefix lookup took 237 ms because the planner chose the `(scan_id, bytes)` index and read the whole scan; `+bytes` plus a primary-key range makes it 0.21 ms (about 1,100x) with identical results. The original guess (the `LIKE`) was only half the cause. Guards: semantics on edge-case names and a performance bound, with a negative control (old query: 7.7 s, fails). Real-Mac gain unknown: the kept set there is smaller. | `docs/experiments/S4-node-prefix-query/` |
| **Fold unification (claim 1)** | One definition of "the same name", `internal/pathfold` (NFC, full case folding, NFC, with an ASCII fast path), now used by `safety`, `store`, `engine`, the scanner's skip lists, `recipes` keep rules and trend lookups. Measured before the change: `STRASSE` and `straße` were the same name to `safety` but different to `store` and the scanner; an NFD `Café` differed from NFC `Café` under the scanner's, `keep.go`'s and `trend.go`'s bare lower-casing. Tests pin the agreement; a fuzz target (627k executions, no counterexample) checks idempotence and now runs in CI. **Persisted-key note:** scans stored before this change were keyed with the old store fold; they differ only for names with characters such as `ß`, `ﬁ` or `İ`, and a rescan rebuilds them. Not done: comparing device and inode identities instead of strings. | `internal/pathfold/tests` |
| **Package invariants** | Package comments for `safety`, `executor`, `store` and `recipes` now state what each must never do; process notes ("CONTRACT (tech lead)", "task P0-W1") removed from nine package comments. | `go doc` |
| **Restore tests** | Every refusal branch of `executor.Restore` is asserted, together with "nothing was moved" (`restore_test.go`). First 13 executor mutants of the rerun: 10 killed; the full rerun was still running when this was written, so the executor score is not yet updated. | `docs/experiments/S2-mutation-testing/` |
| **Threat model + ADRs** | `docs/THREAT_MODEL.md` (assets, adversaries, boundaries, 16 threats each tied to tests) and `docs/adr/` (6 records, one marked Under review). `scripts/threats.sh`, run by `make check` and CI, fails if any test they name stops existing (42 today; verified to fail on a missing one). | `make threatcheck` |
| **H5 bulk vs per-entry (partial)** | On `/opt/homebrew` (387k files, 40k dirs): `getattrlistbulk` totals match `fstatat` totals to the byte (11,988,164,608) with 5.3x fewer syscalls (80k vs 428k); CPU time about equal (4.2-4.8 s vs 4.7-5.0 s). Wall times were taken at load 22 and are not trustworthy. | `docs/experiments/S1-apfs-private-size/walk.c`; rerun on a quiet machine before relying on the timings |

### Findings that change the plan

1. **`st_blocks` is wrong for clones** and the docs' "cannot be detected cheaply" is wrong too: the SDK returns clone id, refcount and private size in the same call. Decision pending: store both allocated bytes and private bytes (schema migration, scanner change, plan costing). See S1.
2. **Test-only exports cannot be removed by moving to `x_test`**; only white-box tests do. Left to the owner (§4 correction).
3. **`git_clean` had no test at all** (see S2).
4. **A fixed-size scan budget is not at risk:** nodes are kept only above 100 MB or for recipe roots (`scanner.Options.KeepThreshold`), so `growing()` (H1) and `nodeSizes` (H3) most likely operate on a small set. They stay unoptimized until a real two-scan trend shows otherwise.
5. Two benchmarks already existed (`BenchmarkScanSynthetic`, `BenchmarkAddNodes`); §3 corrected.

### Not started

Workstream 0 on real data (needs one populated scan; none exists on this machine), H1, H3, H4 and H6 measurements, mutation-score work on `executor` (and the planner write-failure paths), package `doc.go` files for the remaining packages, signing and notarization, a Homebrew tap, a first tag, branch protection settings (a GitHub setting, not a file), the `tests/` layout decision, and the appendix.

### Open questions for the owner

1. Replace `st_blocks` sizing with private size plus clone-group ranges, behind a schema migration? (S1 says the accuracy case is proven.)
2. Keep the `tests/` layout (default) or move to white-box in-package tests (§4)?
3. *(Resolved for live files.)* Code comments, scripts, `README.md` and `CONTRIBUTING.md` now point at `docs/adr/` and `docs/SAFETY_MODEL.md`. Still open: archived task briefs, checkpoints and old plans cite `AGENTS.md` as history. Leave them, or restore a rules file for AI agents (the project had one that told them not to run `apply` or native cleanup commands)?
4. Run `macdoc doctor scan` once on a real Mac, then `scripts/shape.sh` (it works on a copy of the database and prints counts and sizes only, never a path), and share its output so H1, H3, H4 and H6 can be decided on real data.
