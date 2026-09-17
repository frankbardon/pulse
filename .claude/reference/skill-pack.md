# Skill pack — conventions, frontmatter, required sections, budgets

Relocated from CLAUDE.md (section `## Skill Pack`). CLAUDE.md keeps the always-load half inline — the two skill shapes, the stem convention, the budget headline, and the fact that there is **no `skills/index.json`: the filesystem walk is the manifest**. Everything below is the long form it points at.

**Load it before adding or restructuring a skill file**, changing frontmatter keys, changing the required `##` section set for a family, or moving a token budget — `skills/atomic_test.go` and `skills/coverage_test.go` enforce every rule below.

The pack under `skills/` is the LLM surface, embedded via `//go:embed *.md`. Two skill shapes — **atomic** (one file per registered surface) and **topical** (one file per cross-cutting design topic).

## Convention

- **Atomic skill = one operator / tool / type per file.** Stem encodes the surface:
  - `op-agg-<kebab>.md`, `op-attr-<kebab>.md`, `op-filter-<kebab>.md`, `op-group-<kebab>.md`, `op-win-<kebab>.md`, `op-feat-<kebab>.md`, `op-test-<kebab>.md`, `op-reg-<kebab>.md` / `op-reg-mod-<kebab>.md`, `op-synth-<kebab>.md`, `op-overlay-<kebab>.md` — one per registered operator constant.
  - `tool-<kebab>.md` — one per registered MCP tool (drop the `pulse_` prefix).
  - `type-<kebab>.md` — one per `FieldType`.
- **Topical skill = one cross-cutting design topic per file.** ~17 files (`aggregation-design`, `attribute-composition`, `cohort-schema-design`, `compose-requests`, `crosstab-guide`, `facet-design`, `feature-engineering`, `grouper-design`, `join-design`, `label-display`, `overlay-system`, `process-chain`, `regression-modeling`, `request-envelope`, `response-components`, `session-bootstrap`, `spss-cohorts`, `statistical-testing`, `streaming-and-watching`, `synthetic-data`, `synth-models`, `window-design`, plus the optional `financial-cohorts` example pack). Atomic skills cross-link into these for the why/how-it-composes prose; the topical files keep no per-operator detail.

## Frontmatter

Atomic:

```yaml
---
name: op-agg-count            # must match file stem
description: <one-line — what this operator does>
kind: operator                # operator | tool | type
category: AGG                 # AGG | ATTR | FILTER | GROUP | WIN | FEAT | TEST | REG | OVERLAY | SYNTH (empty for tool / type)
operator: AGG_COUNT           # full SCREAMING_SNAKE constant (empty for tool / type)
type: reference
applies_to: process, compose, predict
examples_tags: [streaming-friendly, cohort-analysis]
---
```

Topical:

```yaml
---
name: aggregation-design
description: <what the topic teaches>
kind: design
type: guide
applies_to: process, compose, predict
covers: [AGG, FILTER, aggregations, filterers]
---
```

`applies_to` entries must be valid CLI leaves (`process`, `compose`, `sample`, `facet`, `inspect`, `predict`, `manifest`) — or `mcp` on `tool-*` skills.

## Required body sections (atomic skills)

| Family | Required `##` sections |
|---|---|
| `op-*` (default) | `## Params`, `## Inputs`, `## Output`, `## Gotchas`, `## See` |
| `op-agg-*`, `op-group-*`, `op-filter-*` | the above **plus** `## Components` (v0.20.0 `Response.Components` contract — universal floor + per-operator schema must appear here) |
| `op-overlay-*` | `## Params`, `## Host shape` (replaces `## Inputs` — overlays decorate a host result), `## Output`, `## Gotchas`, `## See` |
| `type-*` | `## Bytes`, `## Range`, `## Null`, `## Dictionary`, `## See` |
| `tool-*` | `## When to use`, `## Input`, `## Output`, `## Gotchas`, `## See` |

`TestAtomicSkillHasRequiredSections` keys off the `category:` frontmatter field; stem prefix is the fallback.

## Token budget

Heuristic: `chars / 4 ≈ tokens`. Budgets are byte counts of the post-frontmatter body.

| Family | Budget (chars) | Token target |
|---|---|---|
| `op-*` | ≤1200 | ≤300 |
| `tool-*` | ≤2000 | ≤500 |
| `type-*` | ≤2000 | ≤500 |
| `kind: design` (topical) | ≤6000 | ≤1500 |

`TestSkillTokenBudget` enforces these. The current regime is transitional — the soft cap allows up to 1000% over budget so reviewers see the live state of legacy bodies without a red gate; a follow-up tightens to 30% over and flips `t.Logf` → `t.Errorf` once the offending `op-reg-*` / `op-reg-mod-*` / `op-feat-*` / `op-synth-regex` bodies have been trimmed.

## List source of truth

`skills.List()` walks the embedded `embed.FS` for `*.md` files and parses each frontmatter block. There is no `skills/index.json` — the filesystem is the manifest, and any new file with valid frontmatter is picked up automatically. Manifest visibility lands via the `skills` block emitted by `BuildManifest()`.

## Per-trigger target convention

| Trigger | Atomic skill convention |
|---|---|
| Aggregator (`AGG_*`) | `op-agg-<name>.md` |
| Attribute (`ATTR_*`) | `op-attr-<name>.md` |
| Filterer (`FILTER_*`) | `op-filter-<name>.md` |
| Grouper (`GROUP_*`) | `op-group-<name>.md` |
| Window (`WIN_*`) | `op-win-<name>.md` |
| Feature (`FEAT_*`) | `op-feat-<name>.md` |
| Statistical test (`TEST_*`) | `op-test-<name>.md` |
| Regression (`REG_*`) | `op-reg-<name>.md` / `op-reg-mod-<name>.md` |
| Synth distribution | `op-synth-<name>.md` |
| Overlay (`OVERLAY_*`) | `op-overlay-<name>.md` |
| Field type | `type-<name>.md` |
| MCP tool | `tool-<name>.md` (strip the `pulse_` prefix) |

Cross-cutting topics that are not operator-keyed route to the matching topical skill: `Response.Components` shape → `skills/response-components.md` (the canonical Components contract topical, paired with the v0.20.0 per-operator `## Components` requirement above); request slot map / smart defaults → `request-envelope`; streaming / `StreamResult` / `Watch` / `FilterToFileWithRequest` / request hashing → `streaming-and-watching`; error codes → `errors/fixup_metadata.go` via `pulse_errors_lookup`; extension surface → `docs/src/internals/extension-points.md`.

## Registered counts

Counts surfaced at runtime via `pulse_manifest` (`commands`, `components.{aggregators,attributes,filterers,groupers,windows,features}`, `tests`, `post_tests`, `synth_distributions`, `regressions`, `mcp_tools`). Never hardcode these in docs — the manifest is the single source of truth and the per-category coverage gates (`TestSkillsCoverAll*`, `TestOperatorHasAtomicSkill`) reject drift.

## Adding a skill

1. Create the file at the conventional stem (`op-<category>-<kebab>.md`, `tool-<kebab>.md`, `type-<kebab>.md`, or a new topical name).
2. Write the required frontmatter for the matching shape (atomic or topical) and the required `##` section set for that family.
3. Stay under budget — atomic op ≤1200 chars body, tool/type ≤2000, topical ≤6000.
4. Run `go test ./skills/... -count=1`. The filesystem walk picks the new file up; no count bump or index entry is needed.

