# Agent workspaces

Each folder is both a recursively seeded template and the default working
directory for that agent. Add project-scoped configuration anywhere below the
matching source directory and rebuild; for example,
`docker/home/agents/opencode/opencode.json` becomes
`/home/fremen/agents/opencode/opencode.json`, where OpenCode discovers it from
its working directory.

Use the project-scoped path understood by each CLI:

| Agent | Paths below its Lisan agent directory |
| --- | --- |
| Codex | `AGENTS.md`, `.codex/config.toml` |
| OpenCode | `AGENTS.md`, `opencode.json`, `opencode.jsonc`, or `.opencode/` |
| Claude Code | `CLAUDE.md`, `.claude/settings.json` |
| Kimi Code | `AGENTS.md`, `.kimi-code/local.toml`, `.kimi-code/mcp.json` |
| Oh My Pi | `AGENTS.md`, `.omp/config.yml` |

Lisan copies new template files into fresh and existing Docker home volumes
without replacing files the user has already changed. Native `vm` launches use
the same recursive, preserve-existing behavior for every enabled agent. These
are project-scoped drop-ins; each CLI's user-level login and state directories
remain in their standard home locations.

Keep repositories below `/home/fremen/projects` or inside the corresponding
agent folder; both locations persist in the `arrakis-usul` Docker volume. Keep
credentials and other secrets out of these image templates and use each CLI's
login or secret-storage flow instead.
