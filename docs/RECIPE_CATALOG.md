# macdoc: Recipe Catalog v1

Recipes are the **only** things macdoc can act on. Each recipe is an embedded YAML file, validated at startup.

## 1. Recipe schema (fields)

| Field | Purpose |
|---|---|
| `id` | Stable dotted ID, e.g. `xcode.derived_data` |
| `category` / `toolchain` | For `reclaim --categories` and `reclaim --toolchains` grouping |
| `detector` | Named Go detector + params (glob roots, tool listing) |
| `allow` | Path patterns the target **must** match after canonicalization |
| `keep_files` | Small files the scanner must keep so a detector can see them: a literal `~`-relative folder and an extension, like `~/Downloads/*.dmg` (case-insensitive, files directly in that folder, any size). A bad entry fails the catalog load |
| `risk` | Base class: SAFE / CAUTION / RISKY / ADVISE / BLOCKED |
| `method` | `native` (argv template) or `quarantine` |
| `native.argv` | Fixed argv. Placeholders only for identifiers validated against the tool's own listing |
| `native.dry_run_argv` | Tool's own dry-run if it has one (e.g. `brew cleanup -n`) |
| `regenerable` | `true` on a SAFE recipe whose data the tool simply rebuilds (a cache). Using it recently does not raise it to CAUTION (the 7-day rule), because an active Mac rewrites caches all the time; preconditions such as "app not running" still apply. Lint refuses it on any other tier |
| `preconditions` | Named checks: `app_not_running:Xcode`, `docker_reachable`, `emulator_not_running`, `gradle_daemons_stopped`, `git_clean`, `no_installed_app` |
| `staleness` | Signal + threshold (e.g. `mtime_max > 60d`) |
| `restore` | How it comes back plus cost: `rebuild` / `redownload:<GB>` / `irreplaceable` |
| `explain` | What it is, why it's there, and what removing it means, in plain language |
| `confidence_notes` | e.g. "may include APFS clones" |
| `steps` | Optional numbered steps the user can follow by hand, shown in `explain` under "How to free it yourself". Every ADVISE recipe has them. Text in `` `backticks` `` is a command to copy; macdoc never runs it |

## 2. Catalog

Legend for method: **N** = native command, **Q** = quarantine, **A** = advise-only (macdoc prints instructions).

### Xcode / Apple

| ID | Target | Risk | Method | Preconditions | Restore |
|---|---|---|---|---|---|
| `xcode.derived_data` | `~/Library/Developer/Xcode/DerivedData/<project>-*` per project, stale > 30d. Mapped to its project via `info.plist` `WorkspacePath` (note says which project, or that it no longer exists) | SAFE (CAUTION if < 7d) | Q | Xcode not running | Rebuild (minutes) |
| `xcode.device_support` | `~/Library/Developer/Xcode/{iOS,watchOS,tvOS} DeviceSupport/<ver>` for OS versions older than the newest per major | SAFE | Q | Xcode not running | Re-copied on next device connect |
| `xcode.simulators_unavailable` | Simulators for runtimes no longer installed | SAFE | N `xcrun simctl delete unavailable` | none | n/a (already unusable) |
| `xcode.sim_runtimes_old` | Old simulator runtimes from `xcrun simctl runtime list -j` | CAUTION | N `xcrun simctl runtime delete <id>` | Simulator not running | Re-download (5–8 GB each) |
| `xcode.sim_runtimes_orphaned` | Simulator runtimes and their caches left from an Xcode that was removed or isn't selected (only the Command Line Tools active). System-owned, so `simctl` can't remove them | ADVISE | A (steps: install and select Xcode, delete in Settings › Components) | n/a | Re-download from Xcode › Settings › Components |
| `xcode.sim_devices_idle` | Individual simulator devices idle > 90d | CAUTION | N `xcrun simctl delete <udid>` | Simulator not running | Recreate (app data lost) |
| `xcode.previews_cache` | `~/Library/Developer/Xcode/UserData/Previews` | SAFE | Q | Xcode not running | Regenerated |
| `xcode.caches` | `~/Library/Caches/com.apple.dt.Xcode` | SAFE | Q | Xcode not running | Regenerated |
| `xcode.archives` | `~/Library/Developer/Xcode/Archives/*` | RISKY | A | n/a | Irreplaceable (dSYMs for crash symbolication) |
| `cocoapods.cache` | `~/Library/Caches/CocoaPods` | SAFE | N `pod cache clean --all` | none | Re-download |

### Android

| ID | Target | Risk | Method | Preconditions | Restore |
|---|---|---|---|---|---|
| `gradle.caches` | `~/.gradle/caches` (transforms, build-cache, modules) | SAFE | Q | Gradle daemons stopped, Android Studio not running | Re-download (often 2–10 GB) |
| `gradle.wrapper_dists_old` | `~/.gradle/wrapper/dists/<ver>` not referenced by any project's `gradle-wrapper.properties` | SAFE | Q | none | Re-download on demand |
| `android.avd` | `~/.android/avd/<name>.avd` idle > 60d | CAUTION | N `avdmanager delete avd -n <name>` | Emulator not running | Recreate (emulator data lost) |
| `android.avd_snapshots` | `~/.android/avd/<name>.avd/snapshots` | SAFE | Q | Emulator not running | Cold boot next time |
| `android.system_images_unused` | SDK system images not referenced by any AVD `config.ini` | CAUTION | Q (sdkmanager can't target the right SDK root; removing the package dir is the uninstall) | Android Studio not running | Re-download (1–2 GB each) |
| `android.build_tools_old` / `android.ndk_old` | Old build-tools / NDK versions not referenced by projects | CAUTION | Q | Android Studio not running | Re-download |
| `android_studio.old_versions_data` | `~/Library/{Application Support,Caches,Logs}/Google/AndroidStudio<old>` where a newer one exists | CAUTION | Q | Android Studio not running | Settings already migrated |

### Node / React Native

| ID | Target | Risk | Method | Preconditions | Restore |
|---|---|---|---|---|---|
| `node.node_modules_stale` | `node_modules` in projects idle > 90d. Idle includes the project's last commit, source change and build (Phase 4) | CAUTION (context only raises risk, so it isn't lowered for clean projects) | Q | No node process with cwd in project | `npm i` / `pnpm i` / `yarn` |
| `npm.cache` | `~/.npm/_cacache` | SAFE | N `npm cache clean --force` | none | Re-download |
| `pnpm.store` | pnpm content store (`pnpm store path`) | SAFE | N `pnpm store prune` (size shown as unknown: only unreferenced packages go) | none | Re-download unreferenced packages |
| `yarn.cache` | `yarn cache dir` (classic), per-project `.yarn/cache` for berry is excluded | SAFE | N `yarn cache clean` | none | Re-download |
| `rn.metro_cache` | `$TMPDIR/metro-*`, `$TMPDIR/haste-map-*` | SAFE | Q | Metro not running | Rebuilt |
| `rn.ios_pods_stale` | `<project>/ios/Pods`, or `Pods` next to a `Podfile`, in projects idle 90d+ (≥ 10 MB) | CAUTION; RISKY with uncommitted changes or unconfirmed git state | Q | git clean, Xcode not running | `pod install` |
| `rn.native_build_stale` | `<project>/ios/build` (next to a Podfile or Xcode project), `<project>/android/{build,app/build,.gradle}` (Gradle files present), idle 90d+ (≥ 10 MB) | SAFE; CAUTION with uncommitted changes or unconfirmed git state | Q | Gradle daemons stopped, Xcode not running | Rebuild |
| `node.next_cache_stale` | `<project>/.next` next to a `package.json`, idle 90d+ (≥ 10 MB) | SAFE; CAUTION with uncommitted changes or unconfirmed git state | Q | none | `next build` / `next dev` |
| `nvm.old_versions` | `~/.nvm/versions/node/<ver>` not default, not in any `.nvmrc` | CAUTION | Q | none | `nvm install <ver>` |

### Homebrew

| ID | Target | Risk | Method | Preconditions | Restore |
|---|---|---|---|---|---|
| `brew.cleanup` | Old versions plus download cache | SAFE | N `brew cleanup --prune=all` (dry-run: `brew cleanup -n --prune=all`) | none | Re-download |
| `brew.autoremove` | Unneeded dependency formulae | CAUTION | N `brew autoremove` (dry-run: `-n`) | none | `brew install` |

### Docker

| ID | Target | Risk | Method | Preconditions | Restore |
|---|---|---|---|---|---|
| `docker.build_cache` | BuildKit cache | SAFE | N `docker builder prune -f` | Docker reachable | Rebuild layers |
| `docker.dangling_images` | Untagged images | SAFE | N `docker image prune -f` | Docker reachable | n/a |
| `docker.unused_images` | Images unused by any container | CAUTION | N `docker image prune -a -f` | Docker reachable | Re-pull |
| `docker.stopped_containers` | Exited containers | CAUTION | N `docker container prune -f` | Docker reachable | Container state lost |
| `docker.volumes` | Named and anonymous volumes | ADVISE | A (lists volumes and sizes) | n/a | **Irreplaceable data** |
| `docker.raw_disk` | `Docker.raw` allocated vs logical size explanation | ADVISE | A (explains Docker Desktop disk limit and reclaim) | n/a | n/a |

Recipe lint forbids `--volumes` and `system prune` in any argv.

### Python

| ID | Target | Risk | Method | Preconditions | Restore |
|---|---|---|---|---|---|
| `pip.cache` | `pip cache dir` | SAFE | N `pip cache purge` | none | Re-download |
| `uv.cache` | `uv cache dir` | SAFE | N `uv cache clean` (whole cache) | none | Re-download |
| `poetry.cache` | `~/Library/Caches/pypoetry` | SAFE | Q | none | Re-download |
| `conda.pkgs` | conda package cache | SAFE | N `conda clean --all -y` (dry-run `--dry-run`) | none | Re-download |
| `python.venv_stale` | `.venv` (with `pyvenv.cfg`) beside a project marker, idle 90d+ (including the project's last activity), not in hidden/Library/protected folders | RISKY | Q | git clean, no python process in project | Recreate from requirements |

### Local AI models

| ID | Target | Risk | Method | Preconditions | Restore |
|---|---|---|---|---|---|
| `ollama.model` | Models from `ollama list`, unused > 30d (by blob mtime) | CAUTION | N `ollama rm <name>` | Ollama model not loaded (`ollama ps`) | Re-download (size shown) |
| `hf.cache_detached_revisions` | Hugging Face cache revisions not pointed to by any ref | SAFE | Q (per revision dir) | none | Re-download if needed |
| `hf.cache_repo` | Whole HF cache repos idle > 60d | CAUTION | Q | none | Re-download (size shown) |
| `lmstudio.model` | `~/.lmstudio/models/**` (or `~/.cache/lm-studio/models`) idle > 60d | CAUTION | Q | LM Studio not running | Re-download |
| `ai.duplicate_models` | Same weights (size + partial hash) present in two or more tools | ADVISE | A (which copy to keep, and why) | n/a | n/a |

### Browser caches (SAFE, `reclaim` on Enter)

| Family | Target | Risk | Method | Restore |
|---|---|---|---|---|
| Browser caches | Cache folders of Safari, Chrome, Edge, Brave, Arc and Firefox. Caches only: never profiles, history, cookies, passwords or extensions | SAFE | Q, browser not running | Pages reload and re-cache |

The exact list is in `internal/recipes/catalog/browsers.yaml`.

### Desktop app caches (SAFE, `reclaim` on Enter)

| Family | Target | Risk | Method | Restore |
|---|---|---|---|---|
| Chromium-based apps | The `Cache`, `Code Cache`, `GPUCache`, `DawnCache`, `DawnGraphiteCache` and `DawnWebGPUCache` folders under the Application Support folder of Slack, Discord, Notion, Figma, Postman, Obsidian, Claude, WhatsApp, GitHub Desktop, MongoDB Compass, Antigravity, Kiro, Trae, Wispr Flow, Logseq, Insomnia, Linear, Windsurf, Bruno, GitKraken and Todoist. Spotify's `PersistentCache` is its own CAUTION recipe (`spotify.cache`) because that folder can hold songs saved for offline listening. Microsoft Teams and Zoom need no recipe of their own: new Teams keeps its cache in its container (`app.container_caches`) and Zoom's is under `~/Library/Caches` (`user.app_caches`). Never settings, logins, messages, notes, vaults or downloads | SAFE | Q, app not running | The app rebuilds them as you use it |

The exact list is in `internal/recipes/catalog/appcaches.yaml`. The scan keeps the folders that `path` and `paths` recipes name, each sandboxed app's `Data/Library/Caches` (for `app.container_caches`) and the children of `~/Library/Caches` at any size (`recipes.KeepFunc`), so a small cache is seen; sizes under 1 MB are then hidden from the list. The Trash is kept the same way: its one-level `keep_children_of: ["~/.Trash"]` is allowed only for the quarantined-Trash recipe (`quarantinedTrash` in `load.go`), so every direct child of `~/.Trash` is seen. Items that would free under 1 MB are counted in a note; `macdoc reclaim --small` lists them. Caches directly under `~/Library/Caches` are listed by `user.app_caches`.

### Developer caches (SAFE, `reclaim` on Enter)

| Family | Target | Risk | Method | Restore |
|---|---|---|---|---|
| Developer caches | Go build cache, Cargo registry, Bun, Deno, Composer, Swift PM, node-gyp and pre-commit caches, VS Code and Cursor caches, Rustup downloads, Ruby Bundler, Terraform plugins, Dart/Flutter hosted packages, Zig and Bazel caches, Hex, opam, rbenv and pyenv download caches, cached iPhone/iPad restore files (`ios.firmware_cache`), JetBrains logs only (JetBrains caches and AI CLI caches are deliberately excluded) | SAFE | Q or native per recipe | Re-downloaded or rebuilt on demand |

The exact list is in `internal/recipes/catalog/devcaches.yaml`.

### System and general

| ID | Target | Risk | Method | Preconditions | Restore |
|---|---|---|---|---|---|
| `tm.incomplete_backups` | Interrupted Time Machine backups (`*.inProgress` under a destination's `Backups.backupdb`), found through `tmutil destinationinfo` and a folder listing | ADVISE | A (removing one needs administrator rights) | n/a | n/a |
| `tm.local_snapshots` | Local Time Machine snapshots | CAUTION | A in v1 (prints `tmutil thinlocalsnapshots` guidance; see open question) | n/a | These are local backups |
| `user.app_caches` | `~/Library/Caches/<bundle-id>` for non-Apple apps, 50 MB or more | CAUTION | Q | That app not running | Regenerated |
| `app.container_caches` | `~/Library/Containers/<bundle-id>/Data/Library/Caches` of sandboxed non-Apple apps, 50 MB or more. Only that folder, never the rest of the container (`container_caches` detector; `catalog/containers.yaml`) | CAUTION | Q | none (quit the app first) | Regenerated |
| `user.logs` | `~/Library/Logs/**` older than 30d, > 100 MB total | SAFE | Q | none | n/a |
| `user.trash` | Each item in `~/.Trash`, any size (under 1 MB with `--small`) | CAUTION | Q (moved into macdoc's quarantine first) | none | Restorable from quarantine until it expires, then gone |
| `system.ds_store` | Finder `.DS_Store` files, one item per folder directly in home (not hidden, not `Library`); never inside `.git`, `node_modules`, `.Trash`, photo or music libraries or a folder you protected. Method `sweep`: deleted at once with no quarantine copy, exact file name only (ADR 0014 §4) | CAUTION | S (sweep) | none | Finder writes them again; only that folder's view settings reset |
| `user.downloads_large_old` | `~/Downloads` installers and archives of at least 20 MB (`.dmg .pkg .mpkg .iso .xip` at any size, other archives from 100 MB) not changed in 30 days | CAUTION | Q | none | Re-download if available |
| `user.downloads_large_other` | Other large old files in `~/Downloads` (videos, documents…) | ADVISE | A | n/a | May be the only copy |
| `ios.device_backups` | MobileSync backups | ADVISE | A (Finder › device › Manage Backups) | n/a | Irreplaceable |
| `apps.leftovers` | Folders in `~/Library/{Caches,Logs,Saved Application State,HTTPStorages,WebKit}` named after a bundle ID (`com.vendor.app`, optional `.savedState`, never `com.apple.*`) that no installed app or LaunchAgent/LaunchDaemon owns, at least 1 MB, unchanged 60 days (ADR 0008) | CAUTION | Q | `no_installed_app` | Not recreated by any app; the quarantine copy is the way back |
| `apps.user_bundle` | An app bundle directly in `~/Applications` (not Apple's), size from the scan or unknown (ADR 0008) | RISKY | Q | `app_bundle_not_running` | Install it again; its settings and caches are not touched |
| `apps.system_bundle` | An app bundle directly in `/Applications` (`/Applications/<Name>.app`; not Apple's, not Mac App Store, not a symlink, owned or group-writable by the user; ADR 0011) | RISKY | Q | `app_bundle_not_running` | Install it again; settings and caches are not touched. A root-owned bundle the system refuses fails cleanly |
| `apps.support_leftovers` | Folders in `~/Library/Application Support` named by a bundle ID, no installed app or LaunchAgent owner, at least 1 MB, unchanged 180 days (ADR 0011) | RISKY | Q | `no_installed_app` | The quarantine copy is the only way back; may hold documents or settings |
| `apps.launch_agent_unload` | A user LaunchAgent whose program no longer exists: `launchctl remove <label>` | CAUTION | N | none | Nothing to start again |
| `apps.launch_agent_file` | The plist of that same background item, moved aside | CAUTION | Q | none | Nothing to start again |
| `swap.explain` | VM volume / swap files | ADVISE | A (explains swap and what reduces it) | n/a | n/a |
| `crawl.unknown_large` | Any large dir or file with no recipe | ADVISE | A (shown in `reclaim --categories`) | n/a | Unknown |

### Detector `app_leftovers` and precondition `no_installed_app`

`app_leftovers` takes two params: `dirs`, a comma-separated list of `~`-relative folders to look in, and `min_bytes`, the smallest folder size to offer (1 MB for `apps.leftovers`). A folder qualifies only if its name is a reverse-DNS bundle ID (see `recipes.LeftoverID`) and no installed app or LaunchAgent/LaunchDaemon plist owns it: the IDs are equal, or one is the other plus `.something`. If the installed-app list can't be read, the detector returns an error and offers nothing. `apps.support_leftovers` uses the same detector on `~/Library/Application Support` with a 180-day age; its allow pattern `~/Library/Application Support/*` is accepted by the lint only for a RISKY quarantine `app_leftovers` recipe, and macdoc's own folder and `MobileSync` can never match (ADR 0011). App bundles directly in `/Applications` are covered only by `apps.system_bundle` (`docs/SAFETY_MODEL.md` §3).

`no_installed_app` runs right before the move. It fails if the folder's bundle ID is owned by an installed app now, or if the list can't be read, so the item is skipped and audited.

## 2b. What `reclaim` does beyond a person's own files (ADR 0014)

`mo clean` also cleans system locations, sweeps `.DS_Store` files and runs without a prompt. `reclaim` does each under named limits, recorded in [ADR 0014](docs/adr/0014-reclaim-system-cleanup.md); the rules elsewhere are unchanged:

| What | Recipe | How it is bounded |
|---|---|---|
| `.DS_Store` files | `system.ds_store` (CAUTION, method `sweep`) | exact file name, folders directly in home only, never Library, hidden folders, `.git`, `node_modules`, media libraries or what you protected |
| System caches and logs | `system.library_caches`, `system.library_logs`, `system.var_logs` (CAUTION, method `system`) | one item below `/Library/Caches`, `/Library/Logs` or `/private/var/log`, root-owned, not a symlink, never `com.apple.*` (caches); idle 30 days |
| Orphaned LaunchDaemons and helpers | `system.launch_daemons`, `system.helper_tools` (RISKY, method `system`) | one file in `/Library/LaunchDaemons` (its program gone) or `/Library/PrivilegedHelperTools` (no installed app owns it); never `com.apple.*` |
| Interrupted Time Machine backups | `system.tm_incomplete` (RISKY, method `system`) | `tmutil delete` on one `.inProgress` folder under a destination's `Backups.backupdb` |
| No prompt | `reclaim --tier safe\|caution --yes` | only SAFE and CAUTION quarantine moves; see [docs/CLI_UX.md](docs/CLI_UX.md) |

Method `system` removals ask for the Mac password through `sudo` (only in `internal/executor/privileged.go`), need their own y/N each, are permanent (root-owned data can't be quarantined), and are never in a plan Claude proposed. The list of locations is code (`safety.CheckSystemTarget`), not recipe data; widening it needs a new ADR.

## 2a. Allowed native commands

Native argv must exactly match one entry of `nativeArgvAllowed` in `internal/recipes/load.go`, and dry-run argv one entry of `dryRunArgvAllowed` (P3-W4). Adding a new command means adding it there, with a test. `qlmanage -r cache` and `qlmanage -r` are still in the ordinary list. The `maintenance.*` recipes and the privileged-argv list were removed with `refresh` (ADR 0015).

## 3. Adding a recipe (checklist)
1. YAML with every field filled in, including `explain` and `restore`.
2. `allow` patterns are as narrow as possible. The recipe lint must pass (no protected roots, ≥ 2 components below `$HOME`, no forbidden argv flags).
3. Fixture tests: one positive, one look-alike negative, one precondition failure.
4. If native: a golden test of the tool's listing output, and dry-run argv where the tool supports it.

## `match:` (implemented)

An optional list of software a recipe covers, written `kind:identity`:

| Kind | Identity | Found by |
|---|---|---|
| `bundle:` | a bundle ID, e.g. `bundle:com.docker.docker` | the app's `Info.plist` |
| `app:` | an app name with no readable bundle ID, e.g. `app:LM Studio` | the app's folder name |
| `brew:` | a Homebrew formula or cask, e.g. `brew:ollama` | `brew list` |
| `npm:` | a global npm package, e.g. `npm:yarn` | `npm ls -g` |
| `tool:` | a program on `PATH`, e.g. `tool:pip` | the executables in the folders on `PATH` that the user owns (`internal/inventory/tools.go`) |

`macdoc retire` uses `match:` to show, for each installed program, the recipes that cover it (`covered_by`), or "advisory only" when none does. It never decides an action: a recipe is still the only source of one. `ParseMatch` in `internal/recipes/match.go` checks each entry, and a bad entry fails the catalog load.

A recipe is covered only while the program is installed. For example, the Android recipes match `tool:avdmanager` and `tool:sdkmanager`, which only show when those tools are on `PATH`.

**Migrated (36 recipes).** Npm, Homebrew, Docker, Xcode, Ollama, Gradle, the Android SDK tools, Homebrew, pip, uv, poetry, conda, CocoaPods, LM Studio and nvm now name their software. The Xcode and Docker bundle IDs, `bundle:com.apple.dt.Xcode`, `bundle:com.docker.docker`, and `bundle:com.google.android.studio`, are the publicly known values and were not checked on a Mac that has them installed. Check one with `plutil -extract CFBundleIdentifier raw <App>.app/Contents/Info.plist`.

**Not migrated, on purpose.** These recipes clean up folders that belong to a project or to macOS, not to a program, so there is no software to match: `node.node_modules_stale`, `node.next_cache_stale`, `rn.native_build_stale`, `rn.ios_pods_stale`, `python.venv_stale`, `hf.cache_repo` (its model cache belongs to no single installed tool), and the `system`, `user`, `ios` and `tm` recipes.

**Toolchain from match.** A recipe that matches known software may leave out `toolchain:`. The loader fills it in from `familyOf` in `internal/recipes/match.go`, a table that says which toolchain each known program belongs to. The 36 matched recipes no longer write their own label. The loader refuses a recipe whose named toolchain disagrees with its match, one whose match is unknown with no toolchain given, and one whose match spans two toolchains. The table is the one place the software-to-toolchain link is written. A program not listed there needs its recipe to name a toolchain itself, so discovery stays generic and only the grouping needs the table.
