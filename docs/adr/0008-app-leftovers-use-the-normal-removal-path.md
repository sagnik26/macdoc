# 0008: Removing an app's leftover data uses the normal removal path

**Status:** Accepted (owner decision, 2026-11-14) for leftovers going through the normal path. Implemented: the `apps.leftovers` recipe and `no_installed_app` precondition, the `apps.user_bundle` recipe (apps in `~/Applications`, `app_bundle_not_running`), and `apps.launch_agent_unload` / `apps.launch_agent_file` for background items whose program is gone (`launchctl remove <label>`, since a native `{id}` can't hold the `/` that `bootout` needs). Bundles in `/Applications` and Application Support leftovers were decided later in ADR 0011. **Recorded in:** `internal/recipes/catalog/apps.yaml`, `internal/recipes/leftovers.go`, `internal/precond`, `docs/RECIPE_CATALOG.md`.

## Context

When an app is dragged to the Trash, its caches, logs, saved state and web storage stay in `~/Library`. ADR 0007 made installed software visible but advisory, and left removal of what an uninstalled app leaves behind for its own decision. The owner asked for macdoc to clean these up without a second, looser way to delete things.

## Decision

1. Leftover data is removed by a recipe like any other (ADR 0002, AGENTS rule 4). `apps.leftovers` is a CAUTION recipe with the `quarantine` method, so it uses the normal sealed plan, terminal approval, per-item confirmation, audit log and restore path (ADR 0001, ADR 0003, ADR 0004).
2. The detector `app_leftovers` offers only folders in `~/Library/Caches`, `Logs`, `Saved Application State`, `HTTPStorages` and `WebKit` that are named after a reverse-DNS bundle identifier (`com.vendor.app`, with an optional `.savedState` suffix, never `com.apple.*`), are at least 1 MB, and have not changed for 60 days.
3. A folder is offered only when no installed app and no LaunchAgent or LaunchDaemon plist has that identifier (equal, or one is the other plus `.something`).
4. If the list of installed apps can't be read, nothing is offered. An unknown list is never treated as an empty one.
5. The precondition `no_installed_app` checks the same thing again right before the move, so an app installed after the plan was made stops the item (AGENTS rule 5).

## Consequences

- No new removal surface: the executor, the protected-path rules and the audit log are unchanged. `/Applications` stays a protected root except for the one exception in ADR 0011.
- Matching is by bundle identifier only, never by a similar name. An app kept somewhere macdoc doesn't look (an external disk, another user) can look uninstalled, so the recipe is CAUTION and the explanation says so.
- The data can't be recreated by the app, so the restore note says the quarantine copy is the only way back until it is deleted (ADR 0004).
- Application Support was first left out because it holds documents and settings; ADR 0011 later added it as a RISKY recipe.

## Not decided here

Nothing is open here. App bundles in `/Applications` and Application Support leftovers were decided in ADR 0011.
