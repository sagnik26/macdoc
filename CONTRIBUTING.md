# Contributing to macdoc

Thanks for helping. macdoc deletes files on people's Macs, so the rules below
are strict on purpose. Read `AGENTS.md` (the safety invariants) and
`docs/SAFETY_MODEL.md` before changing anything that removes, moves or runs
commands.

## Ground rules

- Only `internal/executor` may delete, move or run mutating commands. The
  `make forbidcheck` and `make importcheck` checks enforce this.
- Every cleanup comes from a recipe in `internal/recipes/catalog/*.yaml`.
- Removal always needs a person at a real terminal. Don't add `--yes` or any
  non-interactive bypass.
- Output tone is neutral: say what is known, show what restoring costs, and
  never use scare language (`docs/CLI_UX.md` §3).

## Before you open a pull request

```bash
make check      # test (race on), lint, importcheck, forbidcheck
make cover      # coverage across packages
```

- Tests live in `internal/<pkg>/tests/` as `package tests`, and use only
  exported names. Keep it that way.
- Update the docs in the same change when behaviour or design changes.
- Use small, focused commits with messages that explain why.

## Adding a recipe

Each recipe needs three tests (see `docs/RECIPE_CATALOG.md` §3):

1. a **positive** test: the recipe finds the real target;
2. a **look-alike negative** test: a similar but different folder is not touched;
3. a **precondition-failure** test: the recipe refuses when a precondition fails.

A recipe must name its target by an identifier the tool itself lists. It must
not use a shell, a path typed by the user, or `sudo`.

## Reporting security problems

Please don't open a public issue for a security problem. See `SECURITY.md`.
