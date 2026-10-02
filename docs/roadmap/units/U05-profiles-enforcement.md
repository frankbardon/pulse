---
id: U05
slug: profiles-enforcement
title: "Hidden features cannot run and cannot be seen by the engine or self-description"
track: Feature profiles
size: M
status: not-started
depends_on: [U04]
soft_depends_on: []
blocks: [U06, U10, U17, U19]
todo_items: [17, 18, 19, 20, 21, 22, 23]
branch: profiles-enforcement
---

# U05 — profiles-enforcement

**Outcome:** Hidden features cannot run and cannot be seen by the engine or self-description.

**Track:** Feature profiles · **Size:** M · **Depends on:** [U04](U04-profiles-model.md) · **Unblocks:** [U06](U06-profiles-mcp-tooling.md), [U10](U10-skill-ontology.md), [U17](U17-response-shaping-core.md), [U19](U19-resource-limits.md)

## Summary

Apply the feature profile (U04 naming: `pulse.FeatureProfile`, stored on the instance by `pulse.New`): an `InstanceSnapshot` drives name resolution at the single validation choke point, so hidden names behave exactly like never-registered ones. The manifest, payload schema, predict and errors list become instance-scoped. Adds `feature_set_digest` and profile goldens.

## References

**Theme documents (read before starting):**
- [feature-profiles 01 — Design & phasing](../v1.0.0-feature-profiles/01-design.md) — Core rules, P4 How each surface behaves (request handling, manifest, payload schema, errors)
- [feature-profiles 00 — Feasibility & decisions](../v1.0.0-feature-profiles/00-feasibility.md) — Hard parts 2 and 5

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [ ] **#17** (2. Feature profiles — foundation › FP3 — Instance snapshot & request path) `InstanceSnapshot` (merges the extensions snapshot with the resolved feature set)
- [ ] **#18** (2. Feature profiles — foundation › FP3 — Instance snapshot & request path) Hidden names resolve exactly like never-registered names at the single validation choke point, for every entry point
- [ ] **#19** (2. Feature profiles — foundation › FP3 — Instance snapshot & request path) Hidden request slots refused like unknown fields under strict decode
- [ ] **#20** (2. Feature profiles — foundation › FP3 — Instance snapshot & request path) `feature_set_digest` present on every instance, including the default
- [ ] **#21** (2. Feature profiles — foundation › FP4 — Self-description) Instance-scoped manifest, payload schema (`p.PayloadSchema()`), predict and errors list
- [ ] **#22** (2. Feature profiles — foundation › FP4 — Self-description) Profile goldens for each example profile
- [ ] **#23** (2. Feature profiles — foundation › FP4 — Self-description) `TestProfileDefaultIsFull`: no profile produces output byte-identical to today

## Scope

**In scope**
- `InstanceSnapshot` (extensions + features)
- One resolution step at the request-validation choke point covering Process, Compose, ProcessChain, Facet, ProcessStream, Watch, FilterToFile, template render
- Strict refusal of hidden request slots
- Instance-scoped manifest, `p.PayloadSchema()`, predict, errors list/lookup
- `feature_set_digest` on every instance
- Example-profile goldens

**Out of scope**
- Skills / examples filtering (U10)
- MCP registration (U06)

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|perf|test(profiles-enforcement/E<n>-S<m>): …`; close each epic with `milestone(profiles-enforcement/E<n>): vertical slice complete — <epic title>`.

### E1 — Hidden features never run
- S1: `InstanceSnapshot` built at `pulse.New`; threaded where `ExtensionsSnapshot` already flows
- S2: choke-point resolution: a hidden name yields the existing unknown-type error, byte-identical
- S3: hidden request slots refused as unknown fields

### E2 — Self-description shows only the instance
- S1: instance-scoped manifest + `feature_set_digest`
- S2: `p.PayloadSchema()`; `pulse schema` / `pulse://schema` serve it
- S3: instance-scoped predict and errors list; code → owning-feature map
- S4: example profiles + goldens; `TestProfileDefaultIsFull`

## Acceptance criteria

- [ ] With no profile, every existing golden (manifest, schema, skills list, examples, errors, CLI tree) is byte-identical
- [ ] For each example profile, a request using a hidden operator returns a response byte-identical to one using a never-registered name
- [ ] No hidden name appears anywhere in the instance's manifest, payload schema or errors list
- [ ] `feature_set_digest` is present on default and profiled instances alike
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- `TestProfileDefaultIsFull`
- Profile goldens under `descriptor/testdata/`

## Update Demand companions

- CLAUDE.md "Output Format Contract" (`feature_set_digest`, additive)
- `docs/src/contract/payload-schema.md` (instance-scoped schema)
- `.claude/reference/feature-profiles.md`

## Human inputs & decisions

- None.

## Notes

- `TestProfileInvisibilityParity` is finalized in U06 once MCP is covered. Start its harness here.

## Inherited from U04

U04 landed the vocabulary this unit enforces; its contract is `.claude/reference/feature-profiles.md`.

- **Inputs.** The feature table and dependency groups are internal (`internal/descriptor/features.go`: `Features()`, `LookupFeature`, `IsCoreSurface`, `MCPToolBindings`); the validated profile is the unexported `Pulse.featureProfile`. There is no public accessor yet — deciding one (or none) is this unit's call.
- **Extension operators are features.** An extension omitted from a profile becomes hidden the moment the list is applied; the embedder docs (`docs/src/library/feature-profiles.md`) must say so in the same PR.
- **Core surfaces** (never hidden): open, inspect, predict, count records, manifest, payload schema, skills, examples, errors lookup, cohort artifacts.
- **Not features:** synth distributions (gated as a whole by `capability:synth`), field types, named tables, expr functions. `TEST_X` is one feature covering both tiers; one `io_format` name gates import and export.
- **Errors list.** Codes owned only by hidden features need the code → owning-feature map; the three `PULSE_FEATURE_PROFILE_*` codes are embedder-facing config errors and stay listed.
- **`behaviour` switches already apply** (ORed into `Options`); enforcement must not double-apply them.