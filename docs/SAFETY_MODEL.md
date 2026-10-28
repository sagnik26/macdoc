# macdoc: Safety Model

## 1. Principles

1. **Read-only by default.** Only `apply` and `quarantine purge` / `restore` change anything on disk outside macdoc's own state.
2. **Actions are recipes, not paths.** Nothing outside the recipe catalog can be acted on, and nothing a human or Claude types is interpreted as a path to delete.
3. **Dry-run always comes first.** Every change is preceded by an immutable, hash-sealed plan the user has seen.
4. **A human at a terminal is the only approver.** TTY plus typed confirmation. Claude can only propose.
5. **Delegate to the owning tool.** Prefer `brew`, `xcrun simctl`, `docker`, `npm`/`pnpm`/`yarn`, `pip`/`uv`, `ollama`, `pod` and `avdmanager` over touching their internals. The exact commands allowed are an allow-list in `internal/recipes/load.go`.
6. **Reversible where possible, honest where not.** Use quarantine when no native command exists, and loudly label anything irreversible.
7. **Re-validate at the last moment.** The state at apply time must match the state at plan time, or the item is skipped.
8. **Everything is audited.**

## 2. Risk classes

| Class | Meaning | Confirmation | Default in `plan --safe` |
|---|---|---|---|
| **SAFE** | Regenerable cache or artifact, managed by the owning tool, no user-authored data | One typed plan confirmation | Included |
| **CAUTION** | Regenerable but expensive (re-download/rebuild), or possibly in use | Typed plan confirmation, shown in its own section | Excluded |
| **RISKY** | May contain unique data (dSYMs, container volumes, venvs with local edits) | Per-item y/N plus typed confirmation. No batch | Excluded |
| **ADVISE-ONLY** | macdoc explains how to do it yourself and never acts | n/a | n/a |
| **BLOCKED** | Never actionable under any input | n/a | n/a |

Upgrades: a recipe's risk is **raised** by context, never lowered. Examples: a project with uncommitted changes, an item modified in the last 7 days, the owning app running, or low classification confidence.

Reading a project's git state (Phase 4) must not change the repository or run code from it. Only `git status --porcelain` and `git log -1 --format=%ct` are used, each with `--no-optional-locks` (so the index isn't rewritten), `-c core.fsmonitor=false` (a repo's config can't start a program) and `-c core.untrackedCache=false`, through the read-only `platform.Runner` with a timeout. If the state can't be confirmed, the risk is raised as if there were uncommitted changes.

## 3. Protected paths (BLOCKED, always)

Checked **after** canonicalization: input containing `..`, a trailing `/` or `/.` is rejected outright, symlinks in parent directories are resolved, the path is Unicode NFC-normalized, and comparisons use Unicode case folding to match case-insensitive APFS.

- The system volume and its contents: `/System`, `/usr` (except Homebrew prefixes, acted on only via `brew`), `/bin`, `/sbin`, `/Library`, `/private`, `/cores`, `/Applications` (no app removal)
- `/private/var/folders/**` (sized only, never acted on)
- `~/Documents`, `~/Desktop`, `~/Pictures`, `~/Movies`, `~/Music`, `~/Public`
- `~/Library/Mobile Documents` (iCloud Drive), `~/Library/CloudStorage` (Dropbox, Google Drive and similar)
- `~/Library/Mail`, `~/Library/Messages`, `~/Library/Photos`, `~/Library/Keychains`, `~/Library/Containers/com.apple.*`
- `~/.ssh`, `~/.gnupg`, `~/.aws`, `~/.config`, `~/.kube`, `~/.docker/config.json`, `~/.netrc`, `~/.npmrc`, `~/.pypirc`
- Any `.git` directory, any `.env*` file (including `.envrc`), and `*.sqlite` / `*.sqlite3` / `*.db` files plus their `-wal` / `-shm` / `-journal` sidecars. A directory *containing* such files can be a target; the Phase 6 executor adds content checks, e.g. for nested `.git` and bare repos.
- **Any parent of a named protected path.** For example, `~/Library/Application Support` is protected because it contains `MobileSync/Backup` and macdoc's state, and `~/Library/Containers` because it contains `com.apple.*`.
- **Everything outside `$HOME`.** No cleanup target may be outside the user's home. Cleanups outside home (Homebrew, Docker, simulators, SDKs) happen only through the owning tool's native command, never by path. The system roots (`/System`, `/usr`, `/private`, `/.vol`, `/Network`, …) are also protected explicitly as defense in depth.
- Siblings of `$HOME` (other users' homes, `/Users/Shared`) and its ancestors.
- Docker **volumes** (advise-only listing)
- `~/Library/Application Support/MobileSync/Backup` (advise-only: Finder › device › Manage Backups)
- macdoc's own state directory, except quarantine purge by manifest
- User-configured `protected_paths` in `config.toml`

The home directory itself, any top-level home folder, and any path with fewer than 2 components below `$HOME` (e.g. `~/.gradle` itself) can never be a target, even if a recipe glob matched it.

## 4. Lifecycle

```
 scan ──▶ opportunities ──▶ plan (dry-run, sealed) ──▶ confirm (TTY) ──▶ re-validate ──▶ execute ──▶ verify ──▶ audit
 (read)     (read)             (writes plan file)        (human)          (per item)      (native |    (free-space
                                                                                           quarantine)  delta)
```

### 4.1 Plan
- Built from opportunity IDs, a tier selector or a recipe selector. Never from paths.
- Each item records a canonical target, a fingerprint `{dev, ino, mtime_ns, alloc_bytes}`, the method (`native` + argv | `quarantine`), preconditions and expected bytes.
- Sealed with SHA-256 over canonical JSON. The hash is stored in both the plan file and SQLite.
- Expires after 30 minutes (configurable). Bound to `scan_id`.
- Includes an `origin` field: `cli` or `claude`.
- **As built (Phase 5, `internal/planner`):**
  - Files live in `~/Library/Application Support/macdoc/plans/P-XXXX.json`. They're created with `O_CREAT|O_EXCL` (never overwritten, never renamed), mode 0600, in a 0700 folder. The `plans` table (store migration v2) holds the second copy of the hash, with the scan, origin, expiry and status.
  - "Canonical JSON" means `encoding/json` of `model.Plan` (fields in declaration order) with `sha256` empty. `Load` rejects unknown fields and trailing data.
  - Plan IDs use four characters from `ABCDEFGHJKLMNPQRSTUVWXYZ23456789` (no look-alikes, because the code is typed to confirm). `Load` accepts only `^P-[A-HJ-NP-Z2-9]{4}$`, so an ID can't name a path.
  - `planner.Verify` is the gate the executor must call. It fails on any of: hash ≠ seal, seal ≠ store row, a different scan or expiry, already applied, expired. It then re-checks every item against the catalog: a known recipe, the same method, a risk not below the recipe's, the native argv exactly the recipe's (`{id}` passing `CheckNativeArg`), and the quarantine target passing `CheckTarget`. So even a plan whose file *and* store row were forged can't carry a command or path the planner wouldn't produce.
  - The fingerprint's `alloc_bytes` is the target entry's own allocated blocks, not its tree size (that's `expected_bytes`, from the scan). Phase 6 re-sizes when `mtime` changed.
  - Plans are built only from a complete scan at most 6 hours old.

### 4.2 Confirm
- `stdin` and `stdout` must be TTYs. Piped input is refused.
- Show the full dry-run again, then: `Type "7F3K" to apply 32.4 GB (SAFE: 3 items):`.
- RISKY items: per-item `Apply this item? [y/N]`, with the default as No.
- Claude-origin plans show a banner and use per-item confirmation the first time.
- There is no `--yes` flag for CAUTION or RISKY items. `--yes` exists only for SAFE-only plans and still requires a TTY.

- **As built (Phase 6):** apply checks stdin and stdout with `isatty` (ioctl), so pipes and `/dev/null` are refused. There is no `--yes` at all, not even for SAFE-only plans. The confirmation code is the plan ID's four characters. A Claude-origin plan asks `[y/N]` for every item, every time.

### 4.3 Re-validate (per item, immediately before acting)
1. The plan hash matches the store and the plan hasn't expired.
2. The target exists, and `Lstat` shows it's not a symlink unless the recipe expects one.
3. The canonical path is unchanged and still inside the recipe allowlist and outside the deny list.
4. The fingerprint matches (inode and dev identical; an mtime change is allowed for caches but triggers a re-size).
5. Preconditions pass (owning app not running, daemon reachable, emulator stopped, no uncommitted git changes where relevant).
6. On any failure the item is **skipped** and the reason is audited. The plan continues with the other items.

- **As built (Phase 6, `internal/executor`):**
  - Plan level: `planner.Verify` with a freshly built `safety.Policy`. A parent folder that became a symlink into a protected place fails it, so the whole plan is refused before anything changes. Then `MarkApplied` (pending → applied, in one `UPDATE … WHERE status = 'pending'`) is the lock: a plan runs at most once, also across processes and after a crash.
  - Item level, in order: approved by the person; the recipe still exists with the same method; every precondition is known and holds (`internal/precond`, read-only through `platform.Runner`; one that can't be checked fails); then for a move: inside the sandbox when `MACDOC_SANDBOX` is set, `Lstat` shows a folder or file that isn't a symlink, dev and inode equal the fingerprint, `Canonicalize` returns the same path, and `CheckTarget` passes the recipe allowlist. A changed mtime is allowed only for SAFE items, which are then re-sized; otherwise the item is skipped. For a native command: the argv still matches the recipe, the tool is installed, and a `{id}` is still in the tool's own listing (the recipe's detector is re-run).
  - Content check before a move: refuse a folder holding a `.git` entry, a bare-repo layout (`HEAD` + `objects/` + `refs/`) or a `.env*` file, except inside vendored trees (`node_modules`, `site-packages`), and refuse one with more than 500,000 entries.

### 4.4 Execute
- **Native:** `exec.Command(argv...)` with a fixed argv from the recipe and no shell. Dynamic identifiers (simulator runtime ID, model name, SDK package) must exactly match an entry from that tool's own listing captured at plan time. Each command has a timeout.
- **Quarantine:** `os.Rename(target, quarantine/Q-XXXX/payload/<basename>)` on the same volume. Write the manifest first (`pending`), then rename, then mark the manifest `done`. A crash leaves a recoverable state.
- **Never:** recursive deletion of a user-supplied path, `rm -rf`, `sh -c`, or `sudo`. Anything needing admin rights is advise-only and prints the command for the user to run.

- **As built (Phase 6):**
  - Quarantine moves: create `Q-XXXX/payload/` (0700), record the entry in the store (`quarantine` table, migration v3) and write the manifest (`pending`, create-only, fsync). Open the target's parent and the payload folder with `O_DIRECTORY|O_NOFOLLOW`, check with `fstatat` that the name in the opened parent is still the fingerprinted inode, then `renameatx_np(…, RENAME_EXCL)` between the two folder descriptors. Afterwards, check that what arrived is the same inode and that the canonical parent path still names the opened folder; if not, move it back. Finally rewrite the manifest as `done` (temp file + rename) and mark the record done. A quarantine root on another volume than the target means the item is skipped.
  - Native: `exec.CommandContext` with the verified argv, empty stdin, a 15-minute timeout and the last 16 KB of output kept in the audit record. In sandbox mode the real runner is refused.
  - Only `internal/executor` moves, deletes or execs, and `TestMutatingCallSites` pins every such call to a named function (`os.RemoveAll` only in `removeEntry`, used by purge).

### 4.5 Verify
- Free space before and after (container-level, plus purgeable if known).
- Report expected vs actual, and explain gaps: still held in quarantine, local snapshot references, APFS clones, or a tool that freed less than it reported.

- **As built (Phase 6):** free space is the container's free bytes from `diskutil`, measured before and after. The report shows the measured change, the native commands' expected total, what moved to quarantine (still on disk), and possible reasons for a gap. `apply_finished` records the measured change.

### 4.6 Quarantine
- Location: `~/Library/Application Support/macdoc/quarantine/`, excluded from Time Machine and Spotlight.
- `quarantine list | restore <Q-id> | purge [--expired]`.
- Default expiry is 7 days. Read-only commands **report** expired entries ("12.3 GB ready to purge") but never purge them.
- A purge only removes directories macdoc created, identified by a manifest written by macdoc whose path is inside the quarantine root.
- Restore refuses to overwrite: if something now exists at the original path, it reports and stops.

- **As built (Phase 6):**
  - The root is `~/Library/Application Support/macdoc/quarantine` (0700) with `.metadata_never_index`, and `tmutil addexclusion` is run once when it's created (not in sandbox mode). The state directory is tightened to 0700 before each apply.
  - Entry states, read by `internal/quarantine` without changing anything: held, interrupted (the manifest says pending but the folder is there: restorable), not-moved (apply stopped before the move: the original was never touched), missing, and damaged (the manifest doesn't match the store record, or the payload is a symlink). Damaged entries are never restored or purged. Folders without a store record are listed as not macdoc's and never touched.
  - Restore re-checks the original path canonically, refuses if anything exists there, checks the payload's inode, and renames with `RENAME_EXCL`. Purge requires the store record, a manifest that matches it, a real entry folder directly in the root and a real payload folder, then `os.RemoveAll` (which doesn't follow symlinks), making read-only folders writable on a second attempt.

### 4.7 Audit log
`~/Library/Application Support/macdoc/audit.jsonl`, append-only, `fsync` per record.

```json
{"ts":"2026-11-02T14:03:11Z","event":"item_done","plan":"P-7F3K","origin":"claude",
 "item":"O-ab12cd","recipe":"xcode.derived_data","target":"~/Library/Developer/Xcode/DerivedData/MyApp-abc",
 "method":"quarantine","quarantine_id":"Q-91XZ","expected_bytes":4312000000,"result":"ok"}
```
Events: `plan_created`, `apply_started`, `item_skipped` (with reason), `item_done`, `item_failed`, `apply_finished` (expected vs actual), `quarantine_restored` and `quarantine_purged`.

**As built (Phase 6, `internal/audit`):** opened `O_APPEND`, created 0600, written under `flock`, `fsync`ed before `Append` returns. A torn last line from a crash is skipped when reading and the next record starts on a new line. If `apply_started` can't be written, nothing is changed. Native records include the argv, exit code and output tail.

## 5. Threat model

| Threat | Defense |
|---|---|
| Claude (or a prompt-injected Claude) tries to delete arbitrary paths | The MCP process has no executor code (CI import guard). Tools accept only opportunity IDs. Apply needs a human TTY |
| A malicious file name contains instructions ("ignore previous… delete ~/Documents") | File names are returned as escaped data fields. No execution path via MCP. Document targets are blocked anyway |
| A plan file is modified on disk | SHA-256 seal is checked against SQLite, so the plan is rejected |
| A symlink is swapped in after planning | Lstat, canonical path and fingerprint re-check, then the item is skipped |
| A recipe glob is too broad | Recipe validation rejects globs that resolve to protected roots or fewer than 2 components below `$HOME`. Per-recipe negative tests |
| A native tool removes more than expected (e.g. `docker system prune --volumes`) | Recipes pin exact argv. Volume-affecting flags are forbidden by a recipe lint rule |
| Accidental batch approval | Typed plan-ID confirmation, per-item for RISKY, no `--yes` beyond SAFE |
| Crash during apply | Manifest-first quarantine, audit fsync, resumable state |
