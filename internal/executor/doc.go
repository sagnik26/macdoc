// Package executor is the ONLY package allowed to delete, move, or run
// mutating commands. It is imported solely by the CLI apply/quarantine
// commands (docs/SAFETY_MODEL.md §4). Implemented in Phase 6.
package executor
