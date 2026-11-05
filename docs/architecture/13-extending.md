# Extending macdoc

Part of the [macdoc architecture guide](../../README.md#architecture).

## 15. Extending macdoc

- **Add a recipe.** Add a YAML entry under `internal/recipes/catalog/`, and a detector if
  the recipe needs a new kind of candidate. Write the three tests. The recipe must name
  its target by something the tool itself lists, and must not need a shell.
- **Add a detector.** Write the detector function in a `detect_<area>.go` file, register it
  there with `Register("name", fn)` (see `detect_ai.go` for the pattern), and test it against
  fixtures in `tests/testdata/`.
- **Add a CLI command.** Add a file in `internal/cli/` that builds a Cobra command, wire it
  in `root.go`, and give it a `--json` schema if it prints data. If it needs new analysis,
  put that in `engine`, not in the command.
- **Add an MCP tool.** Only read-only or propose-only tools. Update the pinned test
  and `docs/MCP_DESIGN.md`.
- **Add an executor action.** Don't, unless the change is reviewed as a safety change.
  Any new way of changing the disk belongs in `internal/executor`, behind the existing
  checks and the audit log.
