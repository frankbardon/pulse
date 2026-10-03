# Guided analysis — intents, Purpose, Interpretation, glossary

The guided-analysis metadata model (roadmap U07) lets Pulse say what each operator is for, and how to read what it returns, in words a non-statistician recognises. **All of it is DECLARATION, never execution:** nothing in the engine reads it to run a request, and guidance prose is PULLED on demand — never pushed into a default payload (roadmap principle 6). CLAUDE.md "Guided analysis" carries the always-load half; this file is the contract.

Load it before changing any of: `descriptor/guidance.go`, `internal/descriptor/{intents,glossary,purposes,purpose_validate,interpretations,interpretation_validate,guidance_prose,guidance_skills}.go`, `internal/skills/virtual.go`, root `guidance.go` / `extensions_guidance.go`, a `details.effect_size.*` key, or an overlay kind's `Inferential` flag.

## Data model

Public types live in `descriptor/guidance.go`; the registries and built-in declarations live in `internal/descriptor/` beside `capabilities_*.go` (and so inherit the no-execute import ban, `TestPredictNoExecutionImports`).

| Type | Fields | Notes |
|---|---|---|
| `Intent` | `ID`, `Label`, `Analytic`, `Sounds []string`, `Shapes []Shape` | one taxonomy entry |
| `Shape` | `Roles []Role` | one alternative data shape an intent applies to |
| `Role` | `Name`, `Kinds []FieldKind`, `Min`, `Max` | `Min 0` = optional; `Max` = `RoleUnbounded` (-1) for no ceiling |
| `FieldKind` | `numeric` / `categorical` / `date` / `bool` / `set` | every field type maps to exactly one (`FieldKindOf`, total over the registered types) |
| `Purpose` | `Plain`, `Intents`, `Questions`, `UseCases map[Domain]string`, `NotFor []Alternative`, `Assumptions`, `Level`, `Glossary []string` | per operator |
| `Alternative` | `When`, `Use` | `Use` = bare registered operator or `<kind>:<name>` feature spelling |
| `Domain` | `survey` / `ops` / `science` / `harness` | closed |
| `Level` | `basic` / `intermediate` / `advanced` | closed |
| `Interpretation` | `Field`, `Means`, `Bands []Band`, `Abs`, `Convention`, `Sign map[string]string`, `Caveats`, `Shared` | per output field |
| `Band` | `Min *float64`, `Max *float64`, `Label` | half-open `[Min, Max)`; nil = open end |
| `Term` | `ID`, `Short`, `WhyCare`, `SeeAlso`, `Jargon`, `Forms` | one glossary entry |

**Purpose limits** (`internal/descriptor/purpose_validate.go`): `Plain` non-empty and ≤ `PurposePlainMax` (140) characters; ≥1 intent; ≥ `PurposeQuestionsMin` (2) non-empty `Questions`; ≥1 `NotFor`; ≥1 `UseCases` entry keyed by a known `Domain` with a non-empty value; `Level` one of the three. Each broken rule is a `PurposeRule` (`plain`, `intents`, `questions`, `not_for`, `use_cases`, `level`, `alternative`, `intent_unknown`, `glossary_unknown`, `jargon_unlinked`).

**Glossary limits** (`internal/descriptor/glossary.go`): unique kebab-case IDs; `Short` ≤200 runes and `WhyCare` ≤300; every `SeeAlso` resolves; every `Jargon` term has ≥1 `Form`; no `Form` claimed by two terms. The set started as a ~60-term starter; `TestGlossary_Size` holds it to a loose 55..150 (`glossaryMinTerms` / `glossaryMaxTerms`, never an exact count) so backfills can add the terms their prose links. It includes a term for every emitted effect-size key (`TestGlossary_EffectSizeKeysHaveTerms`, key in kebab case — `cramers_v` → `cramers-v`). `TestGlossary_OrphanReport` logs — never fails — every term no built-in Purpose links; flipping it to binding is U09's call.

## Intent taxonomy

`internal/descriptor/intents.go` `intentRegistry`, in declaration order. Fifteen IDs, closed, **not a feature** — a feature profile never prunes the taxonomy (`TestManifestIntents_StaticUnderProfile`).

- Analytic (`Analytic: true`): `describe`, `compare_groups`, `relationship`, `drivers`, `change_over_time`, `composition`, `benchmark`, `distribution_shape`, `segment`, `measure_construct`, `flows`, `data_quality`.
- Non-analytic — routes to tooling, not operators: `prepare`, `simulate`, `lookup`.

Every intent declares ≥1 `Shape` with validated kinds (`TestIntentRegistry_WellFormed`; the exact ID set is pinned by `TestIntentRegistry_ExactIDs`). Shapes have no runtime consumer yet — U22 (recommend) is the first.

**Manifest projection.** Top-level `intents[]` is the sorted ID strings ONLY. Each `Operator`, `TestMeta`, `RegressionMeta`, `DistributionMeta`, overlay-kind `OverlayCapability` and extension `OperatorMeta` entry carries `intents []string` (`omitempty`) — the sorted intent IDs of its Purpose. The nested `ProcessChainCapability.Overlays` list does NOT carry them. An entry without a Purpose omits the key, so guidance-free output is byte-identical. A hidden operator under a feature profile drops its intents through the existing hide path (`TestManifestIntents_EntriesAndHidePath`). `format_version` stays `"1.1"` — the slots are additive.

## Glossary and the jargon rule

A `Jargon: true` term must be LINKED (listed in `Purpose.Glossary`) wherever one of its `Forms` appears in a `Plain` sentence. Matching is case-insensitive, word-boundary, longest match wins (`jargonTermsIn`; `TestJargonTermsIn_LongestMatchWordBoundary`). The glossary is served whole — never profile-pruned.

## Virtual skills

`glossary` and `intents` are skills with no file, rendered as markdown from the registries (`RenderGlossarySkill` / `RenderIntentsSkill`, `internal/descriptor/guidance_skills.go`) and registered through `skills.RegisterVirtual` at init. Kind `reference` (`skills.KindReference`): no atomic family, no `##` set, no budget. They reach every skill surface — `pulse skills list|show`, `pulse_skills_list` / `pulse_skills_get`, the `pulse-skill://glossary` / `pulse-skill://intents` exact resources, manifest `skills[]` — and no feature profile hides them. The stems are reserved (`skills.ReservedVirtualNames`; `TestVirtualSkillStemsNotEmbedded`). Mechanics: `.claude/reference/skill-pack.md` (List source of truth). Facade: `pulse.Glossary()` / `pulse.Intents()` return deep copies in declaration order.

## Interpretation

**Paths.** `Interpretation.Field` is a normalised path — dot-separated lowercase snake_case segments, `*` only as the last segment, declared once per operator:

| Surface | Paths |
|---|---|
| tests (`types.TestResult`) | `statistic`, `p_value`, `df`, `details.<k>`, `details.effect_size.<k>` |
| regressions (`types.RegressionResult`) | the type's applicable tags (`r2`, `adj_r2`, `pseudo_r2`, …), map slots as `<key>.*` (`coefficients.*`) |
| overlays | `scalar` / `cells.value` per declared shape, `summary.<key>` (`types.OverlaySummary` tags), `summary.parameters.<k>` |
| agg / group / filter | `components.<key>` from the `ComponentSchema` plus the universal floor |

**Static validation** (`ValidateInterpretations(name, ins, resolver)` with `BuiltinOutputResolver`): a path anchored to a struct tag, `ComponentSchema` key or declared overlay shape is `FieldStatic`; a path into a map-valued slot (`details.*`, `summary.parameters.*`) is `FieldDeferred` — it passes statically and the runtime probe must prove it. The per-regression-type applicability table `regressionOutputs` refuses `r2` on GLM, `p_values` on Bayes, `pseudo_r2` off GLM. Content rules: `Means` or a known `Shared`; `Bands` only with a `Convention` (bands are a labelled convention, never authoritative), each labelled, ascending, non-overlapping, only the first open below and the last open above; `Sign` keys `+` / `-`. Rules: `field`, `field_unknown`, `means`, `convention`, `bands`, `shared`, `sign`. **Built-in bands come only from the convention registry** (`internal/descriptor/conventions.go`: `conventionBands(id)` / `conventionCitation(id)`), held equal to the independently sourced `testdata/conventions.json` by `TestConventionRegistryMatchesFixture` and bound per Interpretation by `TestBuiltinBandsCiteRegisteredConvention` (`update-demand.md`); extension bands stay free-text conventions. The built-in registry is assembled per category (`mergeInterpretations`; `statTestInterpretations` in `interpretations_stattests.go` covers all 21 `TEST_*` families) — a later category adds its own file and map, never a `_test.go`-suffixed name. A banded `statistic` (not an effect-size key) binds through the fixture's `statistic_bindings` (`TEST_PEARSON_R` → `pearson_r`, `TEST_FISHER_EXACT` → `odds_ratio`); an effect size deliberately left unbanded (Cramér's V, rank-biserial, paired `cohens_d` = d_z, repeated-measures `partial_eta_squared`, rank `epsilon_squared`, Spearman's and Kendall's `statistic`) is listed under the fixture's `excluded` with its reason.

**Prose lint** (`TestGuidanceProseLint`, `internal/descriptor/guidance_lint_test.go`, binding, built-ins only): every built-in Purpose, Interpretation and shared rule set is held to an ASA / APA JARS / SAMPL-derived rubric, each violation reported as `(operator, field, rule ID, offending span)`. Text rules over every prose string: `ASA-PROOF` (proves / confirms / establishes near hypothesis or effect), `ASA-PNULL` (p as the probability the null is true, "due to chance"), `ASA-NODIFF` ("no difference / effect / relationship" as a reading), `ASA-IMPORTANT` ("significant" coupled with important / meaningful / large, unless a negation sits between — a contrast, not a coupling). Registry rules: `PV-SHARED` (every p-value field — leaf `p_value` / `p_values`, plus the slot quirks in `pValueFieldsByOperator` — cites `Shared: "p-value"` with no bespoke `Means`), `ES-CONV` (every `details.effect_size.*` carries registry bands + citation, or a "no sourced bands"-style rationale caveat), `CORR-CAUSAL` (correlation `statistic` carries a causation caveat), `ASSUME-INDEP` (every `TEST_*` Purpose's `Assumptions` names independence, or the pairing for a paired test), `MULTI-COMP` (`TEST_TUKEY_HSD`, `OVERLAY_PAIRWISE_*`, `*_CELL`, `OVERLAY_PROP_Z_PANEL`), `NORM-POWER` (`TEST_SHAPIRO_WILK`, `TEST_KS`, `OVERLAY_KS_VS_POP`: low power at small n, trivial departures flagged at huge n; the KS pair adds the estimated-parameters / Lilliefors caveat). Content requirements may sit anywhere in the operator's own Purpose or Interpretations; the shared rule sets never satisfy them. Operator-scoped rules apply only once the operator declares guidance, and scopes are data tables (`lintScope`, `contentRules`) — a backfill extends coverage by declaring guidance, not by editing the lint. Exemptions go in `guidanceLintAllowlist`, keyed `(operator, field, rule)` with a mandatory justification; an unjustified or stale entry fails. Today's entries are the shared p-value rule set's own null statement and its absence-of-evidence caveat.

**Runtime probes** (binding, two-way complete against `DeclaredInterpretationFields()`):
- Tests — `internal/processing/interpretation_runtime_test.go`: `TestInterpretationFieldsHoldAtRuntime` (every deferred `details.*` field present in the Go `res.Details` of ≥1 fixture on every registered tier), `TestEffectSizeKeysHoldAtRuntime` (per family and tier the union of emitted `details.effect_size` keys EQUALS `EffectSizeKeysByTest()`, finite values), `TestTestProbeFixturesCoverRegistries`. Probes check Go values, not JSON presence (`omitempty` hides a zero `df`). Conditional key sets need their own fixture (TEST_CHISQ 2×2 for `phi`, TEST_T one- and two-sample).
- Overlays — `internal/service/interpretation_runtime_test.go` `TestOverlayInterpretationFieldsHoldAtRuntime`. **Recipe for the first overlay Interpretation on a `summary.parameters.<k>` path:** add an entry to `overlayInterpretationProbes` keyed by the `OVERLAY_*` kind — `{fields: []string{"summary.parameters.<k>"}, run: func(t) types.OverlayLayer { … }}` — where `run` drives a real host fixture and returns the layer (`chiSqVsPopFacetLayer` is the template). The table starts empty, so the declaration fails until its probe lands.
- A deferred field outside the `test` / `overlay` categories fails until a harness exists for it.

**Overlay `Inferential`.** `OverlayCapability.Inferential` (manifest `overlays[].inferential`, `omitempty`) is DECLARED per kind in `capabilities_overlay.go`, never inferred from names (`TestOverlayCapabilities_InferentialFlag` pins the set). Known slot quirks an Interpretation must follow: `T_VS_REF` / `Z_VS_REF` carry the p-value in `summary.statistic`; `CHISQ_VS_REF` carries p in `scalar`.

## Shared p-value rules

`sharedInterpretations["p-value"]` (`SharedPValue`), cited by `Shared: "p-value"` from whichever field holds the p (`p_value`, `summary.p_value`, `scalar`, `cells.value`). It says: below alpha (0.05 unless the request sets another) is "significant"; significant is not important — read the effect size; not significant is not "no difference"; more than one test means multiple-comparison caution (`TestSharedPValueRules`).

## Effect-size keys

Nested under `details.effect_size` on both tiers (post-test twins emit identical keys). The declared set is `testEffectSizeKeys` (`EffectSizeKeysByTest()`). **Undefined ⇒ the key is OMITTED, never NaN or 0.** Additive: `Details` is an open map, so `format_version` stays `"1.1"`.

| Test | Key | Formula / convention |
|---|---|---|
| `TEST_T` one-sample | `cohens_d` | (mean − mu) / sd |
| `TEST_T` two-sample, `TEST_WELCH`, `TEST_PAIRED_T`, `TEST_Z_TWO_SAMPLE` | `cohens_d` | pre-existing |
| `TEST_ANOVA_F` | `eta_squared` (pre-existing), `omega_squared` | ω² = (SS_b − df_b·MS_w)/(SS_t + MS_w), clamped ≥ 0 |
| `TEST_ANOVA_WELCH` | `omega_squared` | df_b(F*−1)/(df_b(F*−1)+N), clamped ≥ 0 (Lakens 2013) |
| `TEST_ANOVA_RM` | `partial_eta_squared` | SS_treatment / (SS_treatment + SS_error) |
| `TEST_CHISQ` | `cramers_v`, `phi` (2×2 only) | V = √(χ²/(n·(min(r,c)−1))); φ = √(χ²/n) |
| `TEST_PROP_Z` | `cohens_h` | 2·asin√p₁ − 2·asin√p₂ (sign of `diff`) |
| `TEST_KRUSKAL_WALLIS` | `epsilon_squared` | H·(n+1)/(n²−1) (Tomczak & Tomczak 2014); omitted when every value ties |
| `TEST_MANN_WHITNEY_U` | `rank_biserial` | (U_A − U_B)/(n_A·n_B), Kerby 2014; > 0 ⇒ `groups[0]` larger (sign of `z`) |
| `TEST_WILCOXON_SR` | `rank_biserial` | (W⁺ − W⁻)/(W⁺ + W⁻) over non-zero differences; > 0 ⇒ Field exceeds Field2 |

A new key joins `testEffectSizeKeys`, the glossary `effectSizeKeys` list, a glossary term and the atomic skill `## Output` in the same change. `TEST_FISHER`'s `details.odds_ratio` is not under `effect_size`.

**Caveats.** Wilcoxon zero differences are DROPPED before ranking (R's `wilcox.test(d[d != 0])` convention, not Pratt), and `details.n` counts non-zero pairs only — the references in `test_effect_size_rank_test.go` are generated that way. Pearson `ci_low` / `ci_high` collapse to `r` when n < 4 or |r| = 1 (no Fisher-z interval exists). Stat outputs can drift by an FMA ulp on amd64 vs arm64 — reference tests use tolerances.

## Gates

**Two tiers over ONE validator per type.** VALIDITY is binding; COVERAGE is report-only (`t.Log` of the full missing list grouped by category — never fails until U09 flips it).

- `TestSkillsCoverAllPurposes` (CLAUDE.md-listed) — validity limits over `builtinPurposes`; no Purpose key names an unregistered surface; coverage log of every built-in lacking a Purpose (10 categories incl. post-tests, overlay kinds, distributions).
- `TestPurposeAlternativesResolve` — every `NotFor.Use` resolves (bare → a registered built-in via `PurposeSurfaces`, `<kind>:<name>` → a `features.go` row) and is not the operator itself.
- `TestPurposeQuestionsResolve` — intent IDs exist; coverage log of intents with <3 declaring operators or no `_meta.intents`-tagged example.
- `TestGlossaryTermsResolve` — glossary well-formedness + the `purpose` subtest (glossary links + jargon rule).
- `TestInterpretationCoversOutputs` — Interpretation validity; coverage log of every test family, regression and `Inferential` overlay kind lacking `statistic` / `p_value` / its declared effect sizes.
- `TestGuidanceProseLint` — the binding prose lint (Interpretation section above); `TestGlossary_Size` (loose 55..150) and the report-only `TestGlossary_OrphanReport`.
- `TestExamples_IntentsFromTaxonomy` — optional example `_meta.intents` values are intent IDs (binding).
- The runtime probes above; `TestVirtualSkillStemsNotEmbedded`; `TestGuidanceSkills_*` (surface parity).

**Budget gate** — `TestManifestGuidanceBudget` (`internal/descriptor/guidance_prose_test.go`) + `TestManifestGuidanceBudget_DefaultResults` (root): the compact full-registry manifest spends ≤4096 bytes on guidance (every `intents` key at any depth, the overlay `inferential` flags and the two virtual-skill `skills[]` entries), and no declared prose string (`GuidanceProse()`, ≥ `GuidanceProseMinLen` = 16 runes) appears verbatim or JSON-escaped in the default manifest, `Response` or `PredictResult`. Registry-driven: `proseSources` feeds every guidance type (`TestGuidanceProseSourcesComplete`), so a new Purpose, term, intent or Interpretation is swept with no gate change.

Gate registration detail: `.claude/reference/update-demand.md` (Other load-bearing contract gates).

## Extension hook

All eight root `*Registration` structs take an optional `Purpose *descriptor.Purpose`; `TestRegistration` also takes `Interpretation []descriptor.Interpretation`. At every `pulse.New`, AFTER naming/shape validation, the probe and the `DependsOn` check, `validateExtensionGuidance` (`extensions_guidance.go`) runs the SAME `ValidatePurpose` the built-in tier uses, resolving `NotFor.Use` against the instance (built-ins, the feature table AND other extensions), then `ValidateInterpretations` in STRUCTURE-ONLY mode (nil resolver: path syntax and content, never key existence). First invalid wins (category order, then slice index; Purpose before Interpretation) → `PULSE_EXTENSION_PURPOSE_INVALID`, details `category`, `name`, `index`, `part`, the first failing rule and every violation. Only the Purpose's sorted intent IDs project to `OperatorMeta.intents`; prose never enters the manifest (`TestExtensions_PurposeProseNeverInManifest` — per instance, so outside `GuidanceProse`). An absent Purpose projects no `intents` key and keeps the extension out of the coverage report by construction. Embedder prose: `docs/src/internals/extension-points.md`.

## Out of scope (owned later)

- U08 — Purpose + Interpretation for every `TEST_*`, `OVERLAY_*`, `REG_*`, plus the statistics-reviewer pass (incl. the U07 exemplars `AGG_AVERAGE`, `TEST_ANOVA_F`, `TEST_PEARSON_R` and the glossary).
- U09 — descriptive operators; flips the coverage tier to binding.
- U10 — profile-aware skill rendering.
- U21 — rendering guidance into skills and docs.
- U22 — recommend / explain (first consumer of intent shapes).
- U23 — guidance over MCP.
