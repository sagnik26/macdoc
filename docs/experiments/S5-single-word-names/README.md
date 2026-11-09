# S5: naming things with one word

**The rule** (owner, 2026-11-06): a file, type, function, method, constant or variable is named with a single word that says what it does, unless a single word is impossible because of a collision or another real problem.

Files are done (`git log --follow` shows them). This records how identifiers are renamed and what the first wave found.

## How

A text search-and-replace is unsafe for identifiers: it can rebind a call to a different variable that happens to have the new name. `main.go` here is a small renamer that uses the type checker instead. It reads a JSON list of `{pkg, name, new}` (`name` is `Name`, `Type.Method` or `Type.Field`), loads the module with its tests, and:

1. refuses a rename when the new name already exists at package level, or on the same type as a member;
2. refuses it when the new name would be visible at any place the old name is used (so a use could silently bind to something else);
3. otherwise rewrites every definition and use, in all packages and test packages, in one pass.

```sh
cd docs/experiments/S5-single-word-names && go build -o /tmp/renamer .
/tmp/renamer ../../.. wave1.json          # dry run: lists what it would refuse
/tmp/renamer ../../.. wave1.json apply    # rewrites the files
```

Afterwards: `golangci-lint fmt ./...`, a build, `make check`. The tool does not touch comments, so doc comments that start with the old name are updated separately (the `revive` rule catches the ones it misses). Updating comments by name alone is not safe either: a name shared with an identifier in another package gets its comment rewritten wrongly, which happened once and was reverted, so check the comment diff.

Left alone on purpose: struct fields are renamed only when every field has an explicit `json` or `yaml` tag, so file and wire formats do not change (checked for `audit.Record`, `quarantine.Manifest` and `config.Config`). CLI command names, MCP tool names and JSON keys are external contracts.

## Wave 1 (the small leaf packages)

`quarantine`, `sandbox`, `audit`, `fsread`, `textsafe`, `precond`, `config`, `pathfold`: 60 proposed renames, 58 applied (376 edits in 43 files, tests included). `make check` passes.

| Before | After |
|---|---|
| `audit.PlanCreated`, `ApplyStarted`, `ItemDone`, `ApplyFinished` | `audit.Planned`, `Started`, `Done`, `Finished` |
| `fsread.SmallFile`, `IsRegular`, `IsDir` | `fsread.Small`, `Regular`, `Dir` |
| `textsafe.ForModel`, `ForTerminal` | `textsafe.Model`, `Terminal` |
| `quarantine.StateHeld`, `StateMissing`, `LoadManifest` | `quarantine.Held`, `Missing`, `Load` |
| `Config.PlanTTL`, `Config.NativeTimeout` | `Config.TTL`, `Config.Timeout` |

## Wave 2 (`planner`, `safety`, `scanner`)

45 renames, 210 edits in 31 files. The collision checker refused three that would have captured an existing local variable (`commonAttrs` to `common`, `defaultWorkers` to `workers`, `defaultProgressEvery` to `every`), which is the kind of silent rebinding a text replace would have introduced. `make check` passes.

**What this wave shows about the rule.** A name often carries a verb and a noun, and a single word keeps only one of them. Some results read worse than the original:

- `buildItem` is now `item`: "`item` checks one opportunity and turns it into a plan item".
- `checkTarget` is now `target`, `selectOps` is now `choose`, `validateItems` is now `vet`, `tierRank` is now `rank`.
- `StatusOf` is now `Classify`: "Classify classifies a Verify result".
- `resolveParents` and `resolveFull` could not both be `resolve`, so they became `resolve` and `follow`, which says less about the difference.

Constants and types shorten well (`Pending`, `Expired`, `TTL`, `Normalize`, `Parse`). Functions that are verbs plus an object tend to get worse. The rule allows an exception when a single word is not possible "due to collision or any other issues"; for functions, loss of meaning is that other issue, and the sensible default is to keep the two-word name. In `store` (85 multi-word names) and `probe` (91) most would collide or lose meaning, so I did not rename them.

## Wave 3 (types, owner-approved scope: types and constants only)

11 renames applied in 7 packages (`wave3.json`): for example `platform.ExecRunner` to `Exec`, `platform.FakeRunner` to `Fake`, `store.HistoryPoint` to `Point`, `store.NodeQuery` to `Query`, `recipes.DryRunCache` to `Cache`. Four more were taken out after trying them:

- **JSON schema names.** `model.PlanItem`, `model.ScanInfo` and `executor.EntryResult` appear in `schema/*.json` as `$defs` names, so renaming them changed 11 frozen schemas. That is a contract change (it needs a `schema_version` bump), so they keep their names. The same holds for the `engine.*Result` and `*Options` families, which I did not try.
- **A gate side effect.** Renaming `executor.ItemResult` edited the signature lines of two executor functions that were already over the complexity limit, and the linter then counted them as new code. Fixing that means refactoring the executor for a rename, so it keeps its name.
- **Meaning lost.** `store.QuarantineRecord` to `Held`, `PlanStatus` to `Stage` and `ScanStatus` to `Status` were dropped as misleading.

Enum constants (`SeverityCritical`, `StepScan`, ...) keep their type prefix: without it they collide across types in the same package.

## What does not reduce to one word

The renamer refused one (`Config.AddProtect` would clash with the `Protect` field), and I took `Config.RemoveProtect` off the list to keep the pair together. I also left these alone because no honest single word exists:

- **Collision with a type:** `quarantine.ManifestName` (the type is `Manifest`), and `EntryDir`, `ManifestPath` and `PayloadPath` (names already used by a type or constant).
- **Two names that differ only by a qualifier:** `quarantine.ValidID` and `ValidName` would both become `Valid`.
- **Go convention:** error sentinels keep the `Err` prefix (`ErrBadManifest`, `ErrNotRegular`).
- **A single word that loses the meaning:** `fsread.MaxSmallFile`, `audit.MaxOutput`.

That is a small fraction in these packages (about 15 of 75 names, by my count, not a measured rate). Larger packages that share more words (`cli`, `engine`, `recipes`) are likely to have more collisions.

## Cost

Each wave is a large mechanical diff that touches tests and any document that cites the old names, and every open branch conflicts with it. My earlier estimate of about 1,300 multi-word identifiers overcounted: it also flagged acronyms such as `ID` and `TS`. The true number of declarations still to rename in the other packages has not been recounted.
