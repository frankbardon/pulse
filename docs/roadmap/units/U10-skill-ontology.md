---
id: U10
slug: skill-ontology
title: "Agents only ever see skills and examples for features the instance has"
track: Feature profiles
size: L
status: not-started
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

- [ ] **#24** (2. Feature profiles — foundation › FP5 — Skills & ontology) Ontology graph (intents → operators → skills / examples / glossary / NotFor edges), pruned once at `pulse.New`
- [ ] **#25** (2. Feature profiles — foundation › FP5 — Skills & ontology) List, search, `## See`, intents, recommendations **and exact-name get** all honour the pruned graph
- [ ] **#26** (2. Feature profiles — foundation › FP5 — Skills & ontology) Progressive-disclosure rewrite of the topical skills (paired with G2)
- [ ] **#27** (2. Feature profiles — foundation › FP5 — Skills & ontology) `<!-- feature: … -->` fence syntax and rendering, plus `TestSkillsCoverFeatureFences` (report-only until the rewrite is done, then failing)
- [ ] **#28** (2. Feature profiles — foundation › FP5 — Skills & ontology) `TestSkillsCoverProfileGet`: every hidden skill or example is indistinguishable from a nonexistent name

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

- [ ] For every example profile, a hidden skill or example is indistinguishable from a nonexistent name via list, search **and** exact-name get
- [ ] No served topical skill contains a hidden operator name
- [ ] Topical skills stay within the 6000-char design budget
- [ ] The default (no-profile) skill set renders byte-identical, apart from the intentional rewrites
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

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

## Inherited from U05

The manifest `skills` list and examples counts / tags are NOT scoped by U05 (`manifestScrubSkip` leaves them alone), so a profiled instance's manifest still lists hidden atomic-skill stems. Per-fixture manifest goldens include the skill list, so a skill edit moves them (`docs/src/internals/regenerating-goldens.md`).

## Inherited from U06

U06 pulled a minimal per-instance prune forward (`internal/descriptor/discovery.go`, `inst.Discovery()`); its header comment says where new rules go (`skillPruneRules` / `examplePruneRules`, or rendering inside `Skill`). Open items it handed here:

- **Topical bodies are served unrendered** even where they name a hidden operator — the single exemption in `TestProfileInvisibilityParity`. This is #27 / #28; remove the exemption when the fences land.
- **The line-wise atomic-skill scrub can break structure.** Atomic bodies go through `ProseScrub` line by line, so a dropped line can cut a Markdown table row or a code-fence line and leave a malformed block. Fence-aware rendering (#27) should replace it for atomic bodies too.
- **Manifest `skills[].description` bypasses the scrub.** `internal/descriptor/manifest.go` (`sortedSkills`) copies `s.Description` straight from `skills.List()`, filtering only by `SkillVisible`, instead of routing through `Discovery.renderMetadata`, so a visible skill's description can still name a hidden operator in the manifest while `pulse_skills_list` scrubs it.
- ~~**Overlay examples with an empty `_meta.operators`** are pruned only by whole-token match on their body / description~~ — done (E1-S2 / E1-S3): overlay kinds are `exemplified_by` edges and the prose-token rules are deleted; examples prune off the graph.
- ~~**Capability-keyed example pruning** covers only facet, `crosstab`, `joins` and compose roots~~ — done (E1-S2 / E1-S3): `_meta.capabilities` + structural detectors are `requires_capability` edges, and an example goes with any hidden target.
