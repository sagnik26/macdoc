## What and why

<!-- What changes, and why. For a fix, how you know it was broken. -->

## Checks

- [ ] `make check` passes (race tests, lint, import guard, forbidden-call guard, threat-model guard)
- [ ] No new delete, rename or exec call outside `internal/executor`
- [ ] Behavior or design changed: the doc that describes it is updated in this change (`docs/SAFETY_MODEL.md`, `docs/THREAT_MODEL.md`, or an ADR in `docs/adr/`)
- [ ] New recipe: it has a positive test, a look-alike negative test and a precondition-failure test (`docs/RECIPE_CATALOG.md` section 3)
- [ ] Output changed: the JSON schema and its contract tests are updated (`go test ./internal/cli -run TestSchemasFrozen -update` for additive changes only; anything else bumps `schema_version`)
- [ ] A new exported identifier has a doc comment that starts with its name

## Touches a safety-critical path?

<!-- executor, safety, planner, precond, recipes catalog, scripts, tools/forbidcheck. If yes: which guard or test would have caught a mistake here, and did you check that it fails without your change? -->
