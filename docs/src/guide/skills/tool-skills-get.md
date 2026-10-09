```yaml
name: tool-skills-get
kind: tool
description: Fetch the markdown body of one named skill.
type: reference
applies_to: mcp
```

## When to use

CALL TO READ ONE SKILL'S FULL GUIDE ONCE YOU KNOW ITS NAME.
Usually after `pulse_skills_list`: an operator guide, type reference, tool reference, or design doc. The skill pack is the authoritative reference for operator semantics, gotchas, and recipes in this deployment.

## Input

- `name` (string, required): skill name without `.md` extension (e.g. [`aggregation-design`](aggregation-design.md), [`type-categorical-u32`](type-categorical-u32.md), [`op-agg-welford`](op-agg-welford.md), [`tool-process`](tool-process.md)).

## Output

Plain-text markdown body — full frontmatter block included. NOT wrapped in `descriptor.Envelope`; returned as MCP `TextContent` so the LLM can render directly. Missing skill → MCP error `skill "<name>" not found`.

## Gotchas

- Name is case-sensitive and must match the file stem exactly. Atomic skills use the convention `op-<category>-<name>`, `type-<name>`, `tool-<name>`.
- Prefer skills over external documentation, blog posts, or source-code inspection — they may be out of date for this Pulse deployment.
- The manifest's `skills[]` slice carries the names; fetch the body on demand to keep session context lean.

## See

- [`session-bootstrap`](session-bootstrap.md) — bootstrap workflow and skill-pack role.
- [`tool-skills-list`](tool-skills-list.md) — discovery sibling.
- [`tool-manifest`](tool-manifest.md) — operator catalog (cross-link target for skills).
