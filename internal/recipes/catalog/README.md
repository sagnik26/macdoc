# Recipe catalog

One YAML file per toolchain, each a list of recipes (schema: `internal/recipes/recipe.go`,
rules: `docs/RECIPE_CATALOG.md`). Every file is validated at load time; an invalid recipe
fails `go test` and startup.
