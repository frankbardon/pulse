# Pulse

Pulse is a self-describing, high-performance tabular data processing engine. It ships as a Go library (`github.com/frankbardon/pulse`) and as a single CLI binary (`bin/pulse`). Every `.pulse` file carries its own schema in the header, so consumers (programs, agents, and humans) can discover what a file contains without an external catalog.

The library is the primary deliverable. The CLI is a thin adapter that exposes the same operations on the command line, and an embedded MCP server (`pulse mcp`) exposes them to LLM agents.

## Where to go from here

| If you are... | Start with |
|---|---|
| New to Pulse | [Installation](getting-started/installation.md) → [Your First Cohort](getting-started/first-cohort.md) → [CLI Tour](getting-started/cli-tour.md) |
| Driving Pulse from the shell | [Command Line Reference](cli/api-process.md) |
| Embedding Pulse in a Go program | [Library Embedding](library/overview.md) |
| Curious about the binary format | [.pulse File Format](format/header.md) |
| Hacking on Pulse itself | [Internals](internals/architecture.md) and [Contributing](contributing/setup.md) |
| Wiring Pulse into an LLM agent | [MCP Integration (Pointer)](mcp/index.md), then the in-binary skill pack |

## LLM-facing surface

LLM agents do **not** read this site. Pulse exposes a Model Context Protocol server (`pulse mcp`) and ships 19 embedded skills under `skills/` that LLMs load on demand via the `pulse_skills_list` and `pulse_skills_get` tools. The skill voice is MCP-only (tool calls, JSON payloads). This site is the human-facing counterpart — same engine, different idiom.

See [How LLMs Use Pulse](mcp/index.md) for a short pointer table.

## Source of truth

The authoritative architectural contract for Pulse lives in the repository's [CLAUDE.md](https://github.com/frankbardon/pulse/blob/main/CLAUDE.md). When this site and `CLAUDE.md` disagree, `CLAUDE.md` wins; please open an issue.

- Repository: <https://github.com/frankbardon/pulse>
- Hosted docs: <https://frankbardon.github.io/pulse/>
