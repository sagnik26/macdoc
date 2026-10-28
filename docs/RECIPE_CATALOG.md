# macdoc: Recipe Catalog v1

Recipes are the **only** things macdoc can act on. Each recipe is an embedded YAML file, validated at startup.

## 1. Recipe schema (fields)

| Field | Purpose |
|---|---|
| `id` | Stable dotted ID, e.g. `xcode.derived_data` |
| `category` / `toolchain` | For `storage` and `dev` grouping |
| `detector` | Named Go detector + params (glob roots, tool listing) |
| `allow` | Path patterns the target **must** match after canonicalization |
| `risk` | Base class: SAFE / CAUTION / RISKY / ADVISE / BLOCKED |
| `method` | `native` (argv template) or `quarantine` |
| `native.argv` | Fixed argv. Placeholders only for identifiers validated against the tool's own listing |
| `native.dry_run_argv` | Tool's own dry-run if it has one (e.g. `brew cleanup -n`) |
| `preconditions` | Named checks: `app_not_running:Xcode`, `docker_reachable`, `emulator_not_running`, `gradle_daemons_stopped`, `git_clean` |
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

### System and general

| ID | Target | Risk | Method | Preconditions | Restore |
|---|---|---|---|---|---|
| `tm.local_snapshots` | Local Time Machine snapshots | CAUTION | A in v1 (prints `tmutil thinlocalsnapshots` guidance; see open question) | n/a | These are local backups |
| `user.app_caches` | `~/Library/Caches/<bundle-id>` for non-Apple apps, > 500 MB | CAUTION | Q | That app not running | Regenerated |
| `user.logs` | `~/Library/Logs/**` older than 30d, > 100 MB total | SAFE | Q | none | n/a |
| `user.trash` | `~/.Trash` | ADVISE | A ("Empty Trash in Finder") | n/a | Gone after emptying |
| `user.downloads_large_old` | `~/Downloads` files > 500 MB and older than 90d with installer/archive extensions only (`.dmg .pkg .zip .iso …`) | CAUTION | Q | none | Re-download if available |
| `user.downloads_large_other` | Other large old files in `~/Downloads` (videos, documents…) | ADVISE | A | n/a | May be the only copy |
| `ios.device_backups` | MobileSync backups | ADVISE | A (Finder › device › Manage Backups) | n/a | Irreplaceable |
| `swap.explain` | VM volume / swap files | ADVISE | A (explains swap and what reduces it) | n/a | n/a |
| `crawl.unknown_large` | Any large dir or file with no recipe | ADVISE | A (shown in `storage --large`) | n/a | Unknown |

## 2a. Allowed native commands

Native argv must exactly match one entry of `nativeArgvAllowed` in `internal/recipes/load.go`, and dry-run argv one entry of `dryRunArgvAllowed` (P3-W4). Adding a new command means adding it there, with a test.

## 3. Adding a recipe (checklist)
1. YAML with every field filled in, including `explain` and `restore`.
2. `allow` patterns are as narrow as possible. The recipe lint must pass (no protected roots, ≥ 2 components below `$HOME`, no forbidden argv flags).
3. Fixture tests: one positive, one look-alike negative, one precondition failure.
4. If native: a golden test of the tool's listing output, and dry-run argv where the tool supports it.
