---
id: U10
slug: skill-ontology
title: "Agents only ever see skills and examples for features the instance has"
track: Feature profiles
size: L
status: done
depends_on: [U05, U09]
soft_depends_on: []
blocks: [U21, U32]
todo_items: [24, 25, 26, 27, 28]
branch: skill-ontology
---

# U10 — skill-ontology

**Outcome:** Agents only ever see skills and examples for features the instance has.

**Track:** Feature profiles · **Size:** L · **Depends on:** [U05](U05-profiles-enforcement.md), [U09](U09-guidance-backfill-descriptive.md) · **Unblocks:** [U21](U21-guidance-generated-docs.md), [U32](U32-docs-audit.md)

## Summary

Build the ontology graph (intents → operators → skills / examples / glossary / NotFor edges) and prune it per instance. List, search, `## See`, recommendations and exact-name get all honour it. Rewrite topical skills for progressive disclosure, moving comparisons into Purpose metadata, with `feature` fences as the backstop.

## References

**Theme documents (read before starting):**
- [feature-profiles 01 — Design & phasing](../v1.0.0-feature-profiles/01-design.md) — P4a Skills: a shrinking ontology with progressive disclosure
- [feature-profiles 00 — Feasibility & decisions](../v1.0.0-feature-profiles/00-feasibility.md) — Decision 7

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [x] **#24** (2. Feature profiles — foundation › FP5 — Skills & ontology) Ontology graph (intents → operators → skills / examples / glossary / NotFor edges), pruned once at `pulse.New`
- [x] **#25** (2. Feature profiles — foundation › FP5 — Skills & ontology) List, search, `## See`, intents, recommendations **and exact-name get** all honour the pruned graph
- [x] **#26** (2. Feature profiles — foundation › FP5 — Skills & ontology) Progressive-disclosure rewrite of the topical skills (paired with G2)
- [x] **#27** (2. Feature profiles — foundation › FP5 — Skills & ontology) `<!-- feature: … -->` fence syntax and rendering, plus `TestSkillsCoverFeatureFences` (report-only until the rewrite is done, then failing)
- [x] **#28** (2. Feature profiles — foundation › FP5 — Skills & ontology) `TestSkillsCoverProfileGet`: every hidden skill or example is indistinguishable from a nonexistent name

## Scope

**In scope**
- Ontology graph + pruning at `pulse.New`
- Instance-scoped `skills.List/Get`, `examples.Search/Get`, `pulse-skill://`
- Progressive-disclosure rewrite of ~25 topical skills
- Fence syntax + rendering; `TestSkillsCoverFeatureFences` (report-only → failing)
- `TestSkillsCoverProfileGet`

**Out of scope**
- Generated docs (U21)

## Epics & stories

> As shipped the unit ran as five epics (E1 graph and pruned discovery, E2 fences, E3 topical rewrite, E4 split of the long-form skills and binding gates, E5 embedder skills and examples), not the E1/E2 outline below, which is kept as the original plan.

Each epic is a vertical slice. Commit with `feat|fix|perf|test(skill-ontology/E<n>-S<m>): …`; close each epic with `milestone(skill-ontology/E<n>): vertical slice complete — <epic title>`.

### E1 — Discovery walks a pruned ontology
- S1: ontology graph from frontmatter, `_meta.operators` and Purpose edges
- S2: instance-scoped list/search/get for skills and examples; `## See` stripping
- S3: `TestSkillsCoverProfileGet`

### E2 — Topical skills disclose progressively
- S1: fence syntax + render; gate report-only
- S2: rewrite topical skills batch 1 (statistical-testing, overlay-system, crosstab-guide, aggregation-design…)
- S3: rewrite batch 2 (rest); flip the fence gate to failing

## Acceptance criteria

- [x] For every example profile, a hidden skill or example is indistinguishable from a nonexistent name via list, search **and** exact-name get
- [x] No served topical skill contains a hidden operator name
- [x] Topical skills stay within the 6000-char design budget (hard in `TestSkillTokenBudget` since E4-S3)
- [x] The default (no-profile) skill set renders byte-identical, apart from the intentional rewrites
- [x] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- `TestSkillsCoverFeatureFences`
- `TestSkillsCoverProfileGet`
- `TestSkillsCoverAllCrossReferences` still green after the rewrite

## Update Demand companions

- `.claude/reference/skill-pack.md` (fence syntax, progressive-disclosure rule)
- CLAUDE.md gate list (both new `TestSkillsCover*` gates)
- `update-demand.md` row: topical-skill operator mention → fence

## Human inputs & decisions

- None.

## Notes

- Pair the rewrite with Purpose metadata from U08/U09: comparisons move into `NotFor`.
- E1-S3 implementation decisions: an intent is pruned only when it has ≥1 operator server and every server is hidden (mirrors the glossary-term rule), so the tooling intents no operator serves (`flows`, `lookup`, `measure_construct`) never vanish just because a profile is set — a literal "no enabled operator serves it" would have dropped them on every profiled instance. Table node Name is `<label|range|lookup>/<table name>` (ID `table:label/region`) so a label and a range table of one name stay distinct.

## Shipped: deviations and decisions

- **Public ontology view.** `p.Ontology()` returns a fresh deep copy of the pruned instance graph (`descriptor.Ontology`, `OntologyNode`, `OntologyEdge` and the `OntologyNodeKind` / `OntologyEdgeKind` constants), and `p.Skills()` / `p.Skill(name)` are the facade that MCP, the manifest and the embedder all read (`SkillMetadata` is a root alias of `skills.Metadata`). The plan only had an internal graph.
- **Edge kinds shipped:** `serves_intent`, `routes_to`, `documented_by`, `exemplified_by`, `uses_term`, `not_for`, `requires_capability`. **`follow_up` and `Purpose.FollowUps` are deferred to U22**, which owns Recommend/Explain.
- **`requires:` frontmatter** on a skill (AND semantics) is an edge to a feature; a skill whose required feature is hidden is pruned. This is distinct from the flattened `DependsOn` any-of edges on operators.
- **`_meta.capabilities`** on an example declares capability edges for examples with no operator; structural detectors add the rest, and an example goes with any hidden target.
- **Eager prune.** The graph and the `Discovery` view are built once in `NewInstanceSnapshot`, at `pulse.New`, not lazily.
- **Intent-prune rule.** An intent is pruned only when it has at least one serving operator AND every server is hidden, so tooling intents with no operator server (`flows`, `lookup`, `measure_construct`) never vanish.
- **Fences replaced `ProseScrub` for bodies.** Every served body renders by `<!-- feature: NAME -->` fences plus `## See` by edge; `ProseScrub` now renders only skill metadata. The report-only switches planned for the fence gate were all deleted rather than flipped; every body leak fails `TestSkillsCoverFeatureFences`, `TestSkillsCoverProfileGet` and `TestProfileInvisibilityParity`.
- **Skill pack split.** Long skills were split (new entry-plus-children stems for synth, spss, cohort and session families, plus `expression-language` and `crosstab-margin-aggregations`); `kind: design` budget is HARD at 6000 on the rendered body.
- **Embedder surface.** `Extensions.Skills` and `Extensions.Examples` (validated at `pulse.New`, four new `PULSE_EXTENSION_{SKILL,EXAMPLE}_{INVALID,COLLISION}` codes), served and pruned like built-ins; embedder skill budgets are hard.
- **`pulse_skills_list` takes no arguments**, so the PRD's `pulse_skills_list {intent}` routing does not exist; topical skills route through manifest entries' `intents` and `pulse_skills_get intents`.
- **Fence-coverage guard** exempts an operator's transitive HARD dependencies (a profile cannot hide a hard dependency while keeping the operator).

## Handed on

| Item | Owner |
|---|---|
| `FEAT_LOG` / `FEAT_SQRT` / `FEAT_BUCKETIZE` on `decimal128`: predict refuses, the atomic skills say accepted via f64, and the runtime has no gate | U35 (predict/runtime parity) and U36 (runtime defects) |
| `ComposeOptions.FailFast` / `pulse.ComposeParallel` doc comments say the default is true while the zero value is false | U36 |
| `ValidateCompose` / `ValidateChain` have no public, CLI or MCP caller | new: compose / chain predict (roadmap) |
| `pulse_skills_list` has no intent filter, though the PRD routed through `pulse_skills_list {intent}` | U22 / U23 (search by intent) |
| About fifty atomic skill bodies are over the still-soft atomic budget (`go test ./internal/skills -run TestSkillTokenBudget -v`); embedder skills are hard | [U38](U38-skill-sync.md) |
| `op-synth-*` `## See` `tags=[synth]` matches no example (the synth fixtures lack `_meta`) | [U38](U38-skill-sync.md) |
| toolmeta `DescExamplesSearch` / `DescExamplesGet` say "embedded library"; CLI `pulse examples` / `pulse skills` list only the embedded library, not embedder additions | [U38](U38-skill-sync.md) (U21 delivered the instance-scoped export, which covers `Extensions.Skills`; the CLI listing and toolmeta wording remain) |
| `follow_up` edge and `Purpose.FollowUps` | U22 |

## Inherited from U05

The manifest `skills` list and examples counts / tags are NOT scoped by U05 (`manifestScrubSkip` leaves them alone), so a profiled instance's manifest still lists hidden atomic-skill stems. Per-fixture manifest goldens include the skill list, so a skill edit moves them (`docs/src/internals/regenerating-goldens.md`).

## Inherited from U06

U06 pulled a minimal per-instance prune forward (`internal/descriptor/discovery.go`, `inst.Discovery()`); its header comment says where new rules go (`skillPruneRules` / `examplePruneRules`, or rendering inside `Skill`). Open items it handed here:

- ~~**Topical bodies are served unrendered** even where they name a hidden operator — the single exemption in `TestProfileInvisibilityParity`. This is #27 / #28; remove the exemption when the fences land.~~ — done (E4-S3): exemption deleted, topical leaks fail in `TestSkillsCoverProfileGet`.
- ~~**The line-wise atomic-skill scrub can break structure.** Atomic bodies go through `ProseScrub` line by line, so a dropped line can cut a Markdown table row or a code-fence line and leave a malformed block.~~ — done (E2-S1): every served body (atomic and topical) renders by feature fences plus `## See` by edge; `ProseScrub` renders only skill metadata. Every body mention is fenced and every served body is swept with no exemption (E4-S3: `TestSkillsCoverFeatureFences` binding, `invisibilityExemptSkill` deleted).
- **Manifest `skills[].description` bypasses the scrub.** `internal/descriptor/manifest.go` (`sortedSkills`) copies `s.Description` straight from `skills.List()`, filtering only by `SkillVisible`, instead of routing through `Discovery.renderMetadata`, so a visible skill's description can still name a hidden operator in the manifest while `pulse_skills_list` scrubs it.
- ~~**Overlay examples with an empty `_meta.operators`** are pruned only by whole-token match on their body / description~~ — done (E1-S2 / E1-S3): overlay kinds are `exemplified_by` edges and the prose-token rules are deleted; examples prune off the graph.
- ~~**Capability-keyed example pruning** covers only facet, `crosstab`, `joins` and compose roots~~ — done (E1-S2 / E1-S3): `_meta.capabilities` + structural detectors are `requires_capability` edges, and an example goes with any hidden target.
