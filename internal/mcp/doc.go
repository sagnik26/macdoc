// Package mcp is macdoc's MCP server (`macdoc mcp`, docs/MCP_DESIGN.md).
// Claude can read the diagnosis and propose cleanup plans; it can never
// apply one, delete or move anything, pass a path, run a command or change
// settings. The only write is a plan file with origin "claude", which the
// user applies in Terminal with per-item confirmation.
//
// It must never import internal/executor or internal/cli (which imports
// the executor); scripts/imports.sh enforces this.
package mcp
