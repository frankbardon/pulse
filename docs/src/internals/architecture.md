# Architecture Overview

> **Source of truth:** the canonical architectural contract is
> [`CLAUDE.md`](https://github.com/frankbardon/pulse/blob/main/CLAUDE.md) at the
> repository root. This chapter restates its design principles for human
> readers; if the two ever disagree, CLAUDE.md is authoritative.

Pulse is a high-performance, self-describing tabular data processing engine. It
ships as a Go library (`github.com/frankbardon/pulse`) and as a CLI binary
(`cmd/pulse/`). The library is the primary deliverable; the CLI is a thin
adapter over it.

## Design principles

- **Library-first.** The `pulse.go` facade (`pulse.New`, `pulse.Options`,
  `pulse.Process`, `pulse.Compose`, `pulse.Import`, `pulse.Export`,
  `pulse.Convert`, `pulse.Inspect`, `pulse.Predict`, `pulse.Sample`,
  `pulse.Facet`) is the public API. The CLI calls the library; it never
  contains business logic.
- **Self-describing.** Every `.pulse` file carries its schema in the header.
  The `descriptor/` package provides `manifest`, `predict`, and `inspect`
  operations that expose the system's capabilities and validate requests
  without executing them.
- **Skill-augmented.** The `skills/` package embeds 19 markdown skill files
  into the binary via `//go:embed`. LLM agents (and Nexus, the orchestration
  layer that consumes Pulse) can call `skills.List()` and `skills.Get(name)`
  at boot time to inject domain-specific guidance into their context.
- **Nexus relationship.** Pulse is a standalone processing engine. Nexus is
  the upstream orchestration agent that calls Pulse's library API or CLI.
  Pulse has no dependency on Nexus. Nexus discovers Pulse's capabilities via
  `pulse manifest --json` and loads skills from the embedded skill pack.

The next chapter, [Package Layout](packages.md), shows where each of these
concerns lives in the source tree.
