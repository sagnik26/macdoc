# Recipes

Part of the [macdoc architecture guide](../../README.md#architecture).

## 6. Recipes

A recipe describes one thing macdoc may clean. Recipes are YAML files under
`internal/recipes/catalog/`, embedded at build time, and loaded by `recipes.Load`.
The catalog has 49 recipes across eight files (`xcode`, `android`, `node`, `docker`,
`python`, `homebrew`, `ai`, `system`).

A recipe's fields (`recipes.Recipe`):

| Field | Meaning |
|---|---|
| `id` | Stable identifier, such as `xcode.derived_data` |
| `title`, `category`, `toolchain` | What it is and where it belongs |
| `risk` | `SAFE`, `CAUTION`, `RISKY`, or `ADVISE` (advice only) |
| `method` | `quarantine` (move aside), `native` (a fixed command), or `advise` |
| `detector` | Which detector finds the candidates |
| `params` | The folder or command to look at |
| `allow` | Glob patterns the target must match |
| `keep_children_of` | Parents whose children are the real units |
| `stale_days` | How long idle before it's suggested |
| `preconditions` | Checks made just before acting (`app_not_running:Xcode`, and so on) |
| `restore` | What it costs to get back, shown to the user |
| `explain` | Plain-language explanation |

Detectors (`internal/recipes/detect_*.go`) register under a name that a recipe's
`detector:` field refers to. There are 29 of them, for example `derived_data`,
`xcode_device_support`, `simctl_runtimes`, `orphaned_sim_runtimes`, `cocoapods_cache`,
`gradle_wrapper_old`, `android_avds`, `android_system_images`, `docker_dangling`,
`venv_stale`, `ollama_models`, `brew_cleanup`, `node_modules_stale`, `project_artifact`,
`local_snapshots`, `app_caches`, `downloads_large_old`. The generic `path` and `children`
detectors cover what the specific ones don't. Some detectors run a read-only command
(for example `brew cleanup -n`, `docker system df`) and parse its output.

`keep.go` holds the rule for what a recipe must never touch, and `native.go` holds the
fixed argv for each native command. Native commands are never built from strings and
never run through a shell.

Every recipe needs three tests (positive, look-alike negative, precondition failure).
See [RECIPE_CATALOG.md](../RECIPE_CATALOG.md).
