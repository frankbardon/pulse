# Architecture Overview

> **Source of truth:** the canonical architectural contract is
> [`CLAUDE.md`](https://github.com/frankbardon/pulse/blob/main/CLAUDE.md) at the
> repository root, with its long form in
> [`.claude/reference/architecture.md`](https://github.com/frankbardon/pulse/blob/main/.claude/reference/architecture.md).
> This chapter restates its design principles for human readers; if they ever
> disagree, CLAUDE.md is authoritative.

Pulse is a high-performance, self-describing tabular data processing engine. It
ships as a Go library (`github.com/frankbardon/pulse`) and as a CLI binary
(`cmd/pulse/`). The library is the primary deliverable; the CLI is a thin
adapter over it.

## Design principles

- **Library-first.** The `pulse.go` facade (`pulse.New`, `pulse.Options` and
  the methods on `*pulse.Pulse` — `Process`, `Compose`, `Import`, `Export`,
  `Convert`, `Inspect`, `InspectBytes`, `Predict`, `PredictBytes`, `Sample`,
  `Facet`, `CohortArtifacts`, …) is the public API. The CLI calls the
  library; it never contains business logic.
- **A deliberate public surface.** Only the root package and a small set of
  "noun" packages (`types`, `errors`, `encoding`, `descriptor`, `io`, `synth`,
  `mcp/gosdk`, `mcpserve`, `extend`) are importable.
  The engine, the adapters, the MCP core and every support package live under
  `internal/`. `TestPublicAPIGolden` freezes the exported shape; see
  [Package Layout](packages.md).
- **Self-describing.** Every `.pulse` file carries its schema in the header.
  The no-execute `manifest`, `predict` and `inspect` operations (implemented
  in `internal/descriptor/`, result types in the public `descriptor/`) expose
  the system's capabilities and validate requests without executing them.
- **Skill-augmented.** `internal/skills/` embeds the markdown skill pack into
  the binary via `//go:embed`. Agents load it through the MCP skill tools and
  the `pulse-skill://` resources, and discover the rest of Pulse's
  capabilities via `pulse manifest --json`.
- **Embedder-first, consumer-agnostic.** Pulse is a standalone engine with no
  dependency on any consumer. Library embedders are a first-class audience;
  harnesses discover Pulse through the manifest and the embedded skills.

The next chapter, [Package Layout](packages.md), shows where each of these
concerns lives in the source tree.
