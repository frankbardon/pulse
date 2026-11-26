---
name: tool-skills-get
kind: tool
description: Fetch the markdown body of one named skill.
type: reference
applies_to: mcp
---

## When to use

CALL TO READ ONE SKILL'S FULL GUIDE ONCE YOU KNOW ITS NAME.
Usually after `pulse_skills_list`: an operator guide, type reference, tool reference, or design doc. The skill pack is the authoritative reference for operator semantics, gotchas, and recipes in this deployment.

## Input

- `name` (string, required): skill name without `.md` extension (e.g. `aggregation-design`, `type-categorical-u32`<!-- feature: AGG_WELFORD -->, `op-agg-welford`<!-- /feature --><!-- feature: capability:process -->, `tool-process`<!-- /feature -->).

## Output

`{body: "<markdown>"}` (no envelope): the rendered skill, frontmatter block included, with feature fences resolved for this server. Missing skill → coded tool error `SERVICE_VALIDATION` (`details.argument: "name"`, `details.name`) — also what a skill this server's feature profile prunes answers.

## Gotchas

- Name is case-sensitive and must match the stem exactly. Atomic stems: `op-<kebab>` (the operator name lowercased, `_` → `-`<!-- feature: AGG_SET_UNION -->: `AGG_SET_UNION` → `op-agg-set-union`<!-- /feature -->), `type-<kebab>` (`categorical_u8` → `type-categorical-u8`), `tool-<kebab>` (the tool name without `pulse_`).
- Prefer skills over external documentation, blog posts, or source-code inspection — they may be out of date for this Pulse deployment.
- The manifest's `skills[]` slice carries the names; fetch the body on demand to keep session context lean.

## See

- `session-bootstrap` — bootstrap workflow and skill-pack role.
- `tool-skills-list` — discovery sibling.
- `tool-manifest` — operator catalog (cross-link target for skills).
