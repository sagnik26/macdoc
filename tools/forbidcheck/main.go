// Command forbidcheck fails if any non-test Go file outside internal/executor
// deletes, renames, truncates or runs commands (AGENTS.md invariants 2 and 8).
// The logic lives in tools/forbidcheck/check; this file only wires the command.
//
// Usage: go run ./tools/forbidcheck [-allow file] [./...|./dir/...|./dir]
package main

import (
	"os"

	"macdoc/tools/forbidcheck/check"
)

func main() {
	os.Exit(check.Run(os.Args[1:], ".", os.Stdout, os.Stderr))
}
