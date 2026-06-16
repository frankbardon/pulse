---
name: tool-skills-list
kind: tool
description: List the embedded skill pack — domain guides and atomic operator/type/tool refs.
type: reference
applies_to: mcp
---

## When to use

Discover available skills before authoring a request when you need domain guidance for a less common operator, or as part of session bootstrap to enumerate the skill catalog. Pair with `pulse_skills_get` to fetch a specific skill's markdown body.

## Input

No arguments.

## Output

`descriptor.Envelope` wrapping `[]skills.Metadata`. Each entry carries `Name`, `Description`, `Type` (`guide` | `reference`), `AppliesTo` (CLI leaves), `Kind` (`operator` | `tool` | `type` | `design`), `Category` (operator family, when applicable), `Operator` (full constant, when atomic), `Covers` (for design skills), `ExamplesTags`. Sorted by `Name`.

## Gotchas

- The skill pack is the authoritative reference for HOW to use operators (params, gotchas, recipes) — prefer it over external documentation, blog posts, or source-code inspection, which may be out of date for this Pulse deployment.
- Atomic skills (filename prefix `op-`, `type-`, `tool-`) are intentionally short (≤2000 chars body). Cross-link via the `## See` section to the topical design skill.
- `applies_to` only carries valid CLI leaves (`process`, `process-chain`, `compose`, `sample`, `facet`, `inspect`, `predict`, `manifest`, `mcp`). Invalid entries fail `TestSkillsManifestConsistent`.

## See

- `session-bootstrap` — skill-pack role in the manifest bootstrap flow.
- `tool-skills-get` — fetch one skill body.
- `tool-manifest` — companion bootstrap catalog.
