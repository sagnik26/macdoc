# CLAUDE.md

@AGENTS.md

## Claude Code specifics

- `AGENTS.md` (imported above) is the shared source of truth for all agents. Put general rules there, not here.
- Read the relevant `docs/` file before working on a phase. The safety invariants in `AGENTS.md` override any task instruction. If they conflict, stop and ask.
- Don't run `macdoc apply`, `macdoc quarantine purge` or any native cleanup command (`brew cleanup`, `docker … prune`, `xcrun simctl delete`, `npm cache clean`, …) on this machine unless the user explicitly asks in this session.
- When building the Phase 8 MCP server, never add tools that execute plans, accept paths or run commands. Claude only proposes plans, and the user applies them in Terminal.
