---
id: U05
slug: profiles-enforcement
title: "Hidden names and slots cannot run and cannot be seen by the engine or self-description"
track: Feature profiles
size: M
status: done
depends_on: [U04]
soft_depends_on: []
blocks: [U06, U10, U17, U19]
todo_items: [17, 18, 19, 20, 21, 22, 23]
branch: profiles-enforcement
---

# U05 — profiles-enforcement

**Outcome:** Hidden names and slots cannot run and cannot be seen by the engine or self-description. Facade methods are not gated (see Landed deviations).

**Track:** Feature profiles · **Size:** M · **Depends on:** [U04](U04-profiles-model.md) · **Unblocks:** [U06](U06-profiles-mcp-tooling.md), [U10](U10-skill-ontology.md), [U17](U17-response-shaping-core.md), [U19](U19-resource-limits.md)

## Summary

Apply the feature profile (U04 naming: `pulse.FeatureProfile`, stored on the instance by `pulse.New`): an `InstanceSnapshot` drives name resolution at the single validation choke point, so hidden names behave exactly like never-registered ones. The manifest, payload schema, predict and errors list become instance-scoped. Adds `feature_set_digest` and profile goldens.

## References

**Theme documents (read before starting):**
- [feature-profiles 01 — Design & phasing](../v1.0.0-feature-profiles/01-design.md) — Core rules, P4 How each surface behaves (request handling, manifest, payload schema, errors)
- [feature-profiles 00 — Feasibility & decisions](../v1.0.0-feature-profiles/00-feasibility.md) — Hard parts 2 and 5

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [x] **#17** (2. Feature profiles — foundation › FP3 — Instance snapshot & request path) `InstanceSnapshot` (merges the extensions snapshot with the resolved feature set)
- [x] **#18** (2. Feature profiles — foundation › FP3 — Instance snapshot & request path) Hidden names resolve exactly like never-registered names at the single validation choke point, for every entry point
- [x] **#19** (2. Feature profiles — foundation › FP3 — Instance snapshot & request path) Hidden request slots refused like unknown fields under strict decode
- [x] **#20** (2. Feature profiles — foundation › FP3 — Instance snapshot & request path) `feature_set_digest` present on every instance, including the default
- [x] **#21** (2. Feature profiles — foundation › FP4 — Self-description) Instance-scoped manifest, payload schema (`p.PayloadSchema()`), predict and errors list
- [x] **#22** (2. Feature profiles — foundation › FP4 — Self-description) Profile goldens for each example profile
- [x] **#23** (2. Feature profiles — foundation › FP4 — Self-description) `TestProfileDefaultIsFull`: no profile produces output byte-identical to today

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

- [x] With no profile, every existing golden (manifest, schema, skills list, examples, errors, CLI tree) is byte-identical
- [x] For each example profile, a request using a hidden operator returns a response byte-identical to one using a never-registered name
- [x] No hidden name appears anywhere in the instance's manifest, payload schema or errors list
- [x] `feature_set_digest` is present on default and profiled instances alike
- [x] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

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

## Landed deviations

Decided in the unit interview; contract of record is `.claude/reference/feature-profiles.md`.

- **No single choke point; hide at native lookup sites.** Unknown-name errors vary per site and per execution path, and a pre-check would reorder errors. Every `Lookup*`, the regression resolver, the overlay route, predict's `opRoute` and the shared predict-and-runtime rules consult the `InstanceSnapshot`, so a hidden name reaches its own never-registered error. Parity is asserted after name substitution by a harness across entry points.
- **Facade methods are ungated.** A profile is an agent-visibility contract; the embedder owns their Go calls. The unit outcome is "hidden names and slots", not "hidden capabilities".
- **Example profiles are private fixtures** (`descriptor/testdata/profiles/`: `minimal`, `survey-crosstab`, `empty`). U06 publishes `examples/profiles/*.json`.
- **Digest.** `p.FeatureSetDigest()` = `"fs1:"` + sha256 of the sorted enabled names plus the effective behaviour switches, so a behaviour change moves it. Present in the manifest (`feature_set_digest`) and as the payload schema `$comment`. The design's "reveals nothing about whether a profile exists" is softened: a same-version observer can tell a profiled digest from the default (salting would break cross-process caching).
- **Error-owner table is internal** (`internal/descriptor/error_owners.go`), not a field on the public `errors` metadata; the `PULSE_FEATURE_PROFILE_*` codes stay listed. Fixup prose naming hidden features is stripped at render time and 19 messages were reworded to name only owners.
- **Accessors:** `p.FeatureSetDigest()`, `p.FeatureProfile() (*FeatureProfile, bool)`, `p.PayloadSchema() ([]byte, error)`; no `EnabledFeatures()`.
- **Manifest capability blocks are omittable.** `descriptor.Manifest.Facet` / `ProcessChain` / `Join` / `Crosstab` / `Export` / `Import` became pointers (a public type change); `commands` / `operations` filter through a new command-to-feature table, `mcp_tools` through the existing bindings. `skills` and the examples counts are left for U10. `extensions.label_tables` / `range_tables` are `[]` when `capability:labels` / `range_tables` is hidden (user decision: a hidden capability's named tables are listed as if none were registered).
- **`capability:filter_to_file` now hard-depends on `FILTER_EXPRESSION`** (it compiles every filterer into one filter expression).
- **`pulse schema` serves the default CLI instance** through `p.PayloadSchema()`; the `pulse://schema` MCP resource serves the served instance's `p.PayloadSchema()` too. The CLI is not profiled, so `pulse errors lookup` stays full.
- **Instance-dependent wording** of some refusals and hints (zone-capable operators, `ATTR_RANK` to `WIN_RANK` hint, pairwise advice, facet overlay kinds, `FEAT_TARGET_ENCODE` warning, suggestion reasons); a profile-free instance reads byte-identically.
- **Template render** withholds hidden slots (`template.RenderWith`) so a hidden slot fails like a never-existing key.

- **Runtime refusal text follows the instance** (pulled forward from U06). Runtime refusals that recommend other built-ins (set-field alternatives, two-sample tests' too-many-groups advice, `TEST_TUKEY_HSD`'s `TEST_ANOVA_F` pairing, pairwise `n_source` advice and admitted cell aggregators, regression `REG_OLS` / `REG_GLM` pointers, the chain gate's non-scalar note) name only what the instance offers; names behind a hard dependency edge stay literal. Unprofiled refusals are byte-identical.
- **Hidden named tables resolve as unregistered** (pulled forward from U06). With `capability:labels` hidden a label binding naming a registered table is `PULSE_LABEL_TABLE_UNKNOWN` and an `AutoLabels` default naming one is not injected; with `capability:range_tables` hidden a `table:` source is `PULSE_RANGE_TABLE_UNKNOWN`. The facade discovery methods stay ungated; `LookupTables` have no capability and always resolve.
- **Malformed-request parity** (pulled forward from U06). `TestHiddenOperatorMalformedParity` drives requests a pre-lookup rule refuses through every library entry point incl. Predict. Fixed: a hidden aggregator over a decimal128 field ran through the name-keyed decimal dispatch (now refused as never-registered), and a hidden `FILTER_EXPRESSION` under a `FacetSchema` `additive_fields` scope got the additive-field refusal (now an unknown filter type).
- **MCP schema resource and strict-decode details follow the instance** (pulled forward from U06). `pulse://schema` serves `p.PayloadSchema()` — a profile-free instance without extensions is byte-identical to `BuildPayloadSchema()`. `internal/mcp/strict.go` locates an unknown key inside a compose request or chain stage with the service's `details.request` / `details.stage` (was `request_index` / `stage_index`, plus `stage_name`), so an MCP client sees what a library caller sees.

## Handed to U06 / U10

- **U06:** MCP registration and tool enums, the `BindOnInspect` rebind, `toolmeta` prose, prompts, `TestProfileInvisibilityParity` over MCP, `SeriesOverlayRequest.Overlays` (method-level, ungated), and the decision on emptying the label and range tables.
- **U10:** manifest `skills` and examples counts and tags; skills and examples that name hidden operators.
