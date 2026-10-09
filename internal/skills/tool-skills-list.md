---
name: tool-skills-list
kind: tool
description: List the skill pack — domain guides and atomic operator/type/tool refs, deployment-added skills included.
type: reference
applies_to: mcp
---

## When to use

Discover available skills before authoring a request when you need domain guidance for a less common operator, or as part of session bootstrap to enumerate the skill catalog. Knowing the kind of question, pass `intent`. Pair with `pulse_skills_get` to fetch a specific skill's markdown body.

## Input

`intent` (optional): an ID from the `intents` skill (`compare_groups`). Given, only that intent's skills, ranked: `intents`, design skills covering its operators, then their `op-*` skills basic to advanced. Unknown: `PULSE_RECOMMEND_INTENT_UNKNOWN`, `details.valid` lists the accepted IDs.

## Output

`{skills: [...]}`, each entry `name`, `description`, `type`, `applies_to`, `kind` (`operator` | `tool` | `type` | `design` | `reference`), `category`, `operator` (atomic), `covers` (design), `examples_tags`. Sorted by name, or ranked when `intent` is set.

## Gotchas

- The skill pack is the authoritative reference for HOW to use operators (params, gotchas, recipes) — prefer it over external documentation, blog posts, or source-code inspection, which may be out of date for this Pulse deployment.
- A deployment may add skills for its own operators (`op-*`) and its own guides (`ext-*`); they list, read and route exactly like the built-in ones.
- Atomic skills (filename prefix `op-`, `type-`, `tool-`) are intentionally short (≤2000 chars body). Cross-link via the `## See` section to the topical design skill.
- `applies_to` only carries valid CLI leaves (`process`<!-- feature: capability:process_chain -->, `process-chain`<!-- /feature -->, `compose`, `sample`, `facet`, `inspect`, `predict`, `manifest`, `mcp`). Invalid entries fail `TestSkillsManifestConsistent`.

## See

- `session-bootstrap` — skill-pack role in the manifest bootstrap flow.
- `tool-skills-get` — fetch one skill body.
- `tool-manifest` — companion bootstrap catalog.
