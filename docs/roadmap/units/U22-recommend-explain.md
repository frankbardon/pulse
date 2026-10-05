---
id: U22
slug: recommend-explain
title: "Developers and agents can go from a question to a valid request, and from a result to plain language"
track: Guided analysis
size: L
status: not-started
depends_on: [U09, U11, U13]
soft_depends_on: [U18]
blocks: [U23]
todo_items: [90, 91, 92, 93, 94]
branch: recommend-explain
---

# U22 — recommend-explain

**Outcome:** Developers and agents can go from a question to a valid request, and from a result to plain language.

**Track:** Guided analysis · **Size:** L · **Depends on:** [U09](U09-guidance-backfill-descriptive.md), [U11](U11-weighting-descriptive.md), [U13](U13-multiplicity.md) · **Soft:** [U18](U18-response-shaping-execution.md) · **Unblocks:** [U23](U23-guidance-mcp.md)

## Summary

`pulse.Recommend` (bound and cohort-free modes), `pulse.Explain` (request and response modes, terse by default), predict advisories with codes/fixups (incl. the weighting and many-tests advisories), each with CLI leaf, MCP tool, skill, and goldens.

## References

**Theme documents (read before starting):**
- [guided-analysis 03 — MCP & API](../v1.0.0-guided-analysis/03-mcp-and-api.md) — M1 Recommend, M2 Explain, M3 advisories

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [ ] **#90** (7. Guided analysis — docs, API & MCP › G4 — Recommend, Explain, advisories) `pulse.Recommend` with bound (cohort) and unbound (cohort-free) modes; `pulse recommend`; `pulse_recommend`; `tool-recommend.md`
- [ ] **#91** (7. Guided analysis — docs, API & MCP › G4 — Recommend, Explain, advisories) `pulse.Explain` request mode; terse by default, `detail: "full"` on request
- [ ] **#92** (7. Guided analysis — docs, API & MCP › G4 — Recommend, Explain, advisories) `pulse.Explain` response mode; `pulse explain`; `pulse_explain`; `tool-explain.md`
- [ ] **#93** (7. Guided analysis — docs, API & MCP › G4 — Recommend, Explain, advisories) Predict `advisories`, with codes and fixups
- [ ] **#94** (7. Guided analysis — docs, API & MCP › G4 — Recommend, Explain, advisories) Explain goldens per operator family; Recommend goldens per intent

## Scope

**In scope**
- Recommend + CLI + MCP + skill
- Explain request/response + CLI + MCP + skill
- Advisories + codes + fixups
- Goldens

**Out of scope**
- Opt-in `Response.Interpretation` (stretch)

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|perf|test(recommend-explain/E<n>-S<m>): …`; close each epic with `milestone(recommend-explain/E<n>): vertical slice complete — <epic title>`.

### E1 — Question → valid request
- S1: Recommend unbound (intent → operators, shapes, placeholder skeleton)
- S2: Recommend bound (schema matching, predict-validated drafts, ranking)
- S3: CLI/MCP/skill; goldens per intent

### E2 — Result → plain language
- S1: Explain request mode
- S2: Explain response mode (findings[], terse default, `detail: full`)
- S3: CLI/MCP/skill; goldens per family

### E3 — Predict warns about poor fit
- S1: advisories (two-group-many-groups, many-tests, categorical-as-numeric, ordinal-parametric, cosine-on-scale, weight-unused) + fixups

## Acceptance criteria

- [ ] Every bound recommendation passes predict; unbound ones are marked `bound: false`
- [ ] Explain never says "no difference" for a non-significant result; bands always name their convention
- [ ] Advisories never change execution and are suppressible per code
- [ ] Recommend and Explain respect the instance profile (no hidden operator mentioned)
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- Recommend goldens per intent; Explain goldens per family
- `TestSkillsCoverAllMCPTools` (new tools)
- `TestSkillsCoverAllCliLeaves` (new leaves)

## Update Demand companions

- `skills/tool-recommend.md`, `skills/tool-explain.md`; `mcp/toolmeta/meta.go`
- CLAUDE.md facade list (`Recommend`, `Explain`)
- `docs/src/cli/flags.md`; `errors/fixup_metadata.go`

## Consumes from U10

- Recommend walks `p.Ontology()` (`serves_intent`, `not_for`, `routes_to`, `documented_by`, `exemplified_by`, `uses_term` edges), so it is pruned per instance with no extra filtering.
- **U22 owns the `follow_up` edge and `Purpose.FollowUps`**, which U10 deliberately did not ship. Add the edge kind to `descriptor.OntologyEdgeKind`, the golden and the pruning rules together.
- `pulse_skills_list` takes no intent filter; routing by intent goes through manifest entries' `intents` unless this unit adds one.

## Inherited from U13

- **The many-tests advisory reads data, not a code.** Predict reports `p_values {total, uncorrected, basis, threshold}` (`descriptor.PValueCount`, `internal/descriptor/predict_pvalues.go`); trigger is `uncorrected >= descriptor.MultiplicityTriggerThreshold` (10). `basis` is `exact` / `dictionary` / `lower_bound` (weakest wins) — an advisory built on a `dictionary` or `lower_bound` count must say it is an estimate. `PULSE_ADVISORY_MANY_TESTS` was deliberately not added; this unit owns the code + fixup if it wants one.
- **Count the other roots.** Only Request predict counts today. `ComposeValidationResult`, `ChainValidationResult` and `FacetValidationResult` carry no `p_values`; add them when the advisory needs them (a compose-host count is cohort-free, so always `lower_bound`; v1 chain stages reach no p-site).
- Explain should name the correction actually applied: TestResult / OverlayLayer `multiplicity {method, family, alpha, m[, m_per]}` is present only when a correction ran. Skill: `skills/multiplicity-correction.md`.

## Human inputs & decisions

- None.
