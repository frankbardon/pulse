# Skill pack — conventions, frontmatter, required sections, budgets

Relocated from CLAUDE.md (section `## Skill Pack`). CLAUDE.md keeps the always-load half inline — the two skill shapes, the stem convention, the budget headline, and the fact that there is **no `skills/index.json`: the filesystem walk is the manifest**. Everything below is the long form it points at.

**Load it before adding or restructuring a skill file**, changing frontmatter keys, changing the required `##` section set for a family, or moving a token budget — `internal/skills/atomic_test.go` and `internal/skills/coverage_test.go` enforce every rule below.

The pack under `skills/` is the LLM surface, embedded via `//go:embed *.md`. Two skill shapes — **atomic** (one file per registered surface) and **topical** (one file per cross-cutting design topic).

## Convention

- **Atomic skill = one operator / tool / type per file.** Stem encodes the surface:
  - `op-agg-<kebab>.md`, `op-attr-<kebab>.md`, `op-filter-<kebab>.md`, `op-group-<kebab>.md`, `op-win-<kebab>.md`, `op-feat-<kebab>.md`, `op-test-<kebab>.md`, `op-reg-<kebab>.md` / `op-reg-mod-<kebab>.md`, `op-synth-<kebab>.md`, `op-overlay-<kebab>.md` — one per registered operator constant.
  - `tool-<kebab>.md` — one per registered MCP tool (drop the `pulse_` prefix).
  - `type-<kebab>.md` — one per `FieldType`.
- **Topical skill = one cross-cutting design topic per file.** ~17 files (`aggregation-design`, `attribute-composition`, `cohort-schema-design`, `compose-requests`, `crosstab-guide`, `crosstab-margin-aggregations`, `expression-language`, `facet-design`, `feature-engineering`, `grouper-design`, `join-design`, `label-display`, `overlay-system`, `pairwise-n-sources`, `process-chain`, `regression-modeling`, `request-envelope`, `response-components`, `session-bootstrap`, `spss-cohorts`, `statistical-testing`, `streaming-and-watching`, `synthetic-data` (entry skill for the focused `synth-conflicts`, `synth-correlations`, `synth-determinism`, `synth-fidelity-report`, `synth-marginals`, `synth-profile-capture`, `synth-set-fields`), `synth-models` (entry skill for `synth-model-draw`, `synth-model-recovery`, `synth-model-selection`, `synth-residual-correlations`, `synth-residual-recovery`, `synth-shape-fit`), `synth-structural-rules` (entry skill for `synth-rule-claims`, `synth-rule-detectors`, `synth-rule-expressions`, `synth-rule-nulls`, `synth-rule-validation`, `synth-rules-from-profile`), `window-design`, plus the optional `financial-cohorts` example pack). Atomic skills cross-link into these for the why/how-it-composes prose; the topical files keep no per-operator detail.

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
requires: [capability:crosstab]   # optional (U10)
---
```

**`requires:` (topical, optional).** The features a topical skill cannot be read without, in feature-profile spelling (operators bare, every other kind `<kind>:<name>` — `capability:crosstab`, `TEST_WELCH`, `io_format:spss`); both list forms parse into `skills.Metadata.Requires`. Semantics are AND: a feature profile hiding ANY listed feature prunes the skill; a skill with no `requires:` is never pruned for its own sake. Each entry becomes a `requires_capability` edge in the skill ontology, and an entry that is not a built-in feature is an unresolved reference that fails `TestOntology_BaseHasNoProblems` (`internal/descriptor`). List only what the skill is ABOUT — a passing mention of an operator belongs in a feature fence, not here.

`applies_to` entries must be valid CLI leaves (`process`, `compose`, `sample`, `facet`, `inspect`, `predict`, `manifest`) — or `mcp` on `tool-*` skills.

## Skill ontology (U10)

The pack, the examples, the operators (Purpose), the intent taxonomy, the glossary, the feature table and the MCP tool bindings join into one typed graph: public data types `descriptor.Ontology` / `OntologyNode` / `OntologyEdge` (closed, additive-only kind constants, no prose), built once per process by `BaseOntology()` in `internal/descriptor/ontology.go` — whose doc comment is the authoritative edge-source table. Node IDs are `<kind>:<name>` (`skill:op-agg-count`, `operator:AGG_COUNT`, `intent:describe`, `example:<_meta.name>`, `glossary_term:<id>`, `mcp_tool:pulse_facet`), except a capability node — every non-operator feature — whose ID and Name are its feature spelling verbatim (`capability:crosstab`, `io_format:csv`). Virtual skills are `skill` nodes. Skill-side edge sources: `operator:` frontmatter (→ `documented_by`), the `tool-<kebab>` stem (→ `documented_by` from `mcp_tool:pulse_<snake>`), an ATOMIC skill's `## See` section — each backticked skill stem is a `routes_to` edge and each `` `pulse_examples_search tags=[a, b]` `` links every example carrying all the tags (`exemplified_by`); other spans are prose and topical See sections emit nothing — topical `requires:` (→ `requires_capability`) and each name a topical body fences (→ `routes_to`; "Feature fences" below). Example-side edge sources (`internal/descriptor/ontology_examples.go`, E1-S2): `_meta.operators` and every body `overlays[].kind` (→ `exemplified_by` from the operator), `_meta.intents` (→ `serves_intent`), `_meta.capabilities` and the CLOSED structural detector table — `crosstab` key, root `requests` (compose), root `stages` (process_chain), `joins` key, facet category/tag — (→ `example requires_capability <capability>`), and a description naming an operator not otherwise linked (→ `example routes_to operator`, the one prose-derived edge). `TestExamples_EdgeCoverage` binds the library: every operator token in a body or description is an edge, and a declared capability never contradicts its detector. Each instance walks its own graph (`internal/descriptor/ontology_instance.go`, E1-S3): built EAGERLY in `NewInstanceSnapshot`, it adds the visible extension operators and named tables (`table:<label|range|lookup>/<name>`) and is pruned to the feature set — skill and example visibility in `Discovery` is node survival, and the virtual `intents` / `glossary` bodies render from it. Prune rules: `.claude/reference/feature-profiles.md` (Discovery prune (ontology)).

## Feature fences (U10)

A skill body wraps prose that only makes sense when a feature is offered in a **feature fence** (parser / renderer: `internal/skills/fence.go`; instance rendering: `internal/descriptor/skill_render.go`):

```markdown
<!-- feature: TEST_WELCH, capability:crosstab -->
Prose, a table row, a list item or a whole code block.
<!-- /feature -->

Inline: compare with<!-- feature: TEST_WELCH --> `TEST_WELCH`<!-- /feature -->.
```

- **Names** are feature-profile spelling — operators bare (`TEST_WELCH`), every other kind `<kind>:<name>` (`capability:crosstab`, `io_format:spss`); an operator that is no feature (a synth distribution, an extension operator) resolves by its operator node. A comma list means **AND**: the fence survives only when every name is visible. An unknown name is a validation error (`descx.ValidateSkillFences`, gated over the pack by `TestSkillFences_NamesAreFeatures`), never a silently hidden fence.
- **Block form:** opener and closer each alone on their line. Rendering removes WHOLE lines — the markers always, the lines between them when the fence is hidden — so a table, list or code fence never keeps half a line. A blank line a dropped run leaves next to another blank line collapses into it; blank runs the source already had are kept.
- **Inline form:** opener with other text on its line; it MUST close on the same line. A hidden span is cut verbatim (whitespace included — put the leading space inside the fence, as above); a line the cut leaves empty or holding only a list / heading marker is dropped. Several inline fences may share a line.
- **Errors** (`*skills.FenceError`, 1-based `Line`): nesting (any opener inside an open fence), a closer with no opener, a block never closed, an inline opener not closed on its line, an empty name. `TestSkillFences_EmbeddedPackParses` keeps the pack well formed. Markers are recognised everywhere, code blocks included.
- **Rendering.** Markers are ALWAYS stripped from a served body: `skills.Get` (CLI `pulse skills show`, the pass-through instance) is the FULL render (every fence kept); `skills.Raw` is the embedded file verbatim (renderers and the ontology builder read it). A profiled instance renders each visible body against its PRUNED graph (`Discovery.Skill`): fences first, then — atomic bodies only — `## See` by edge: an item naming a pruned skill stem, or a `pulse_examples_search tags=[…]` ref no visible example answers, is cut with its `, ` / `; ` / ` / ` separator (an item keeps its `(annotation)`; a shared ` — description` tail stays), and a line left naming nothing is dropped. `## See` stems therefore need no fences. A fence-free body renders byte-identically.
- **Graph.** Each name a TOPICAL body fences is a `skill routes_to <feature>` edge; it never prunes the skill (only `requires:` does). Atomic fences emit no edge.
- **Frontmatter `description` cannot hold a fence** — it is rendered by the metadata `ProseScrub`, so keep descriptions free of other features' names ("Fence coverage" below).

## Fence coverage (U10)

Fences are the BACKSTOP, not the design. A served skill should teach the DECISION (which question, which shape of data, which trade-off), route to an intent (`pulse_skills_get intents`) rather than enumerate operators, and leave operator-vs-operator comparisons to each operator's `Purpose.NotFor` (`.claude/reference/guided-analysis.md`) — the manifest already prunes those per instance. Only a name that must stay in prose is fenced.

`TestSkillsCoverFeatureFences` (`internal/descriptor/skill_fence_coverage_test.go`) scans every embedded skill (virtual skills excluded) and prints a per-file table sorted by violation count, with per-family totals:

```sh
go test ./internal/descriptor/ -run TestSkillsCoverFeatureFences -v
```

- **Scan list:** every operator feature (bare constant), every non-operator feature in its `<kind>:<name>` spelling (`capability:crosstab`, `io_format:spss`), and every MCP tool a FEATURE owns (`pulse_facet` → `capability:facet`; `features.go` `mcpToolBindings`). Core tools (`pulse_manifest`, `pulse_skills_get`, …) are never hidden and are not scanned. A bare word sharing a capability's name (`crosstab`, `process`, `import`) is prose, not a feature name, and is not scanned — only the kind-prefixed spelling is.
- **Whole tokens:** a run of `[A-Za-z0-9_]` optionally joined by one colon to another run — `pulse_process_chain` never counts as `pulse_process`, `AGG_SUM_X` never as `AGG_SUM`. A colon token that is no feature (`operator:AGG_SUM`, an ontology ID) is split and each half looked up. Code blocks and JSON examples are scanned like prose.
- **Fenced** means the mention disappears when the body is rendered with that ONE feature hidden — it sits in a fence whose name list includes it (a tool needs its owning feature). A fence naming a different feature does not count.
- **Guard exemption:** a name the skill is pruned with needs no fence — an atomic skill's own `operator:` plus its HARD dependencies (each feature named alone in a `DependsOn` group, transitively — a valid feature profile cannot hide `AGG_WELFORD` and keep `OVERLAY_T_CELL`, so that skill may name it bare, even in its description; an any-of host group guards nothing), `capability:synth` for a synth distribution, the feature owning a tool skill's tool, a topical skill's `requires:` targets. Read from the base ontology exactly as the prune reads it.
- **Frontmatter `description`:** cannot hold a fence, so every unguarded name there is a violation; other frontmatter keys (`operator:`, `covers:`) are not scanned.
- **Modes:** REPORT-ONLY while the const `fenceCoverageFail` is false (logs the table, passes); E4-S3 flips it and every violation fails. A malformed fence or an unknown fence name fails in BOTH modes.

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

**Virtual skills.** Two skills have no file: `glossary` and `intents`, rendered as markdown from the Go registries (`RenderGlossarySkill` / `RenderIntentsSkill` in `internal/descriptor/guidance_skills.go`) and registered at that package's init through `skills.RegisterVirtual`. `List` / `Get` merge them in, so every surface (`pulse skills list|show`, `pulse_skills_list` / `pulse_skills_get`, the `pulse-skill://` exact resources and template, manifest `skills[]`) carries them with no per-consumer code. Their kind is `reference` (`skills.KindReference`): no atomic family, no `##` set, no budget — the atomic gates walk the embedded files, never `List`. No prune rule matches them, so a feature profile never hides them (U10 owns profile-aware rendering). The stems are reserved (`skills.ReservedVirtualNames`): an embedded `glossary.md` / `intents.md` fails `TestVirtualSkillStemsNotEmbedded` and panics `RegisterVirtual`. The registries also surface as the root facade `pulse.Glossary()` / `pulse.Intents()` (deep copies). The registries' own contract (taxonomy, glossary, jargon rule): `.claude/reference/guided-analysis.md`.

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
4. Run `go test ./internal/skills/... -count=1`. The filesystem walk picks the new file up; no count bump or index entry is needed.

