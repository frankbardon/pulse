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

`{body: "<markdown>"}` (no envelope): the rendered skill, frontmatter block included, with feature fences resolved for this server. Missing skill → tool error `skill "<name>" not found` (plain text, no code) — also what a skill this server's feature profile prunes answers.

## Gotchas

- Name is case-sensitive and must match the stem exactly. Atomic stems: `op-<kebab>` (the operator name lowercased, `_` → `-`: `AGG_SET_UNION` → [`op-agg-set-union`](op-agg-set-union.md)), `type-<kebab>` (`categorical_u8` → [`type-categorical-u8`](type-categorical-u8.md)), `tool-<kebab>` (the tool name without `pulse_`).
- Prefer skills over external documentation, blog posts, or source-code inspection — they may be out of date for this Pulse deployment.
- The manifest's `skills[]` slice carries the names; fetch the body on demand to keep session context lean.

## See

- [`session-bootstrap`](session-bootstrap.md) — bootstrap workflow and skill-pack role.
- [`tool-skills-list`](tool-skills-list.md) — discovery sibling.
- [`tool-manifest`](tool-manifest.md) — operator catalog (cross-link target for skills).
