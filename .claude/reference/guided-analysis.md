# Guided analysis — intents, Purpose, Interpretation, glossary

The guided-analysis metadata model (roadmap U07) lets Pulse say what each operator is for, and how to read what it returns, in words a non-statistician recognises. **All of it is DECLARATION, never execution:** nothing in the engine reads it to run a request, and guidance prose is PULLED on demand — never pushed into a default payload (roadmap principle 6). CLAUDE.md "Guided analysis" carries the always-load half; this file is the contract.

Load it before changing any of: `descriptor/guidance.go`, `internal/descriptor/{intents,glossary,purposes,purpose_validate,interpretations,interpretation_validate,guidance_prose,guidance_skills,conventions}.go` (plus the per-category `purposes_*.go` / `interpretations_*.go` files and `testdata/conventions.json`), the prose lint (`guidance_lint_test.go`), `internal/skills/virtual.go`, root `guidance.go` / `extensions_guidance.go`, a `details.effect_size.*` key, an overlay kind's `Inferential` flag, a shared distribution primitive (`internal/statdist`, `internal/processing/test_stat.go` / `test_studentized.go`, the normal helpers) or the R reference goldens.

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

**Glossary limits** (`internal/descriptor/glossary.go`): unique kebab-case IDs; `Short` ≤200 runes and `WhyCare` ≤300; every `SeeAlso` resolves; every `Jargon` term has ≥1 `Form`; no `Form` claimed by two terms. The set started as a ~60-term starter; `TestGlossary_Size` holds it to a loose 55..150 (`glossaryMinTerms` / `glossaryMaxTerms`, never an exact count) so backfills can add the terms their prose links. It includes a term for every emitted effect-size key (`TestGlossary_EffectSizeKeysHaveTerms`, key in kebab case — `cramers_v` → `cramers-v`). `TestGlossary_OrphanReport` is binding: every term is linked by some built-in Purpose or carries an owner-tagged exemption (Gates).

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
| agg / attribute / window / feature | `value` — the primary result of a single-column operator (a number, a label, a list) |
| agg / attribute / window / feature, multi-column | `value.*` — read once per operator, `Means` names the columns: `AGG_WELFORD` ({mean, variance, n}), `AGG_SET_FREQUENCY` (label → count), `FEAT_POLY`, `FEAT_ONE_HOT`, `FEAT_DATE_FEATURES` (`multiColumnOutputs`) |

The `value` paths are `FieldStatic`: the shape is declared per operator in `internal/descriptor/interpretation_reading.go` from its real output, never probed. The wrong shape (`value` on a multi-column operator, `value.*` on a single-column one) and per-column keys (`value.mean`) are refused, as is any `value` path on a filterer, grouper, test, regression or overlay (`TestValueOutputPath`, `TestMultiColumnOutputs`).

**Needs reading vs self-reading.** Every descriptive built-in (aggregator, attribute, filterer, grouper, window, feature, synth distribution) is in exactly one of `needsReadingOperators` / `selfReadingOperators` (same file). Rule: an operator NEEDS READING when a non-statistician cannot read the number from its name — the result is standardised, scale-free, model-based, transformed, or a rank / relative figure; otherwise it is SELF-READING. Filterers, groupers and distributions are listed by name, never category-wide, so a newly registered operator is in neither list and fails `TestDescriptiveReadingLists` until classified. A needs-reading operator must carry an Interpretation on its `value` / `value.*` (coverage gate below). Extend the lists, not the tests.

**Static validation** (`ValidateInterpretations(name, ins, resolver)` with `BuiltinOutputResolver`): a path anchored to a struct tag, `ComponentSchema` key or declared overlay shape is `FieldStatic`; a path into a map-valued slot (`details.*`, `summary.parameters.*`) is `FieldDeferred` — it passes statically and the runtime probe must prove it. The per-regression-type applicability table `regressionOutputs` refuses `r2` on GLM, `p_values` on Bayes, `pseudo_r2` off GLM. Content rules: `Means` or a known `Shared`; `Bands` only with a `Convention` (bands are a labelled convention, never authoritative), each labelled, ascending, non-overlapping, only the first open below and the last open above; `Sign` keys `+` / `-`. Rules: `field`, `field_unknown`, `means`, `convention`, `bands`, `shared`, `sign`. **Built-in bands come only from the convention registry** (`internal/descriptor/conventions.go`: `conventionBands(id)` / `conventionCitation(id)`), held equal to the independently sourced `testdata/conventions.json` by `TestConventionRegistryMatchesFixture` and bound per Interpretation by `TestBuiltinBandsCiteRegisteredConvention` (`update-demand.md`); extension bands stay free-text conventions. The built-in registry is assembled per category (`mergeInterpretations`; `statTestInterpretations` in `interpretations_stattests.go` covers all 21 `TEST_*` families, `overlayInterpretations` in `interpretations_overlays.go` every `Inferential` overlay kind plus the four descriptive `OVERLAY_ZSCORE_*` kinds, `regressionInterpretations` in `interpretations_regressions.go` every `REG_*` type, reading exactly its `regressionOutputs` row, `descriptiveInterpretations` in `interpretations_descriptive.go` every needs-reading descriptive operator's `value` / `value.*`, unbanded) — a later category adds its own file and map, never a `_test.go`-suffixed name. A banded `statistic` (not an effect-size key) binds through the fixture's `statistic_bindings` (`TEST_PEARSON_R` → `pearson_r`, `TEST_FISHER_EXACT` → `odds_ratio`, `REG_OLS` `r2` → `r_squared`, Cohen's f² benchmarks of Cohen 1992 Table 1 row 8 converted via R² = f²/(1+f²)); an effect size deliberately left unbanded (Cramér's V, rank-biserial, paired `cohens_d` = d_z, repeated-measures `partial_eta_squared`, rank `epsilon_squared`, Spearman's and Kendall's `statistic`) is listed under the fixture's `excluded` with its reason, as are `adj_r2` (both linear types), the Bayesian posterior-mean `r2` and the GLM `pseudo_r2`, the four descriptive `OVERLAY_ZSCORE_*` readings (a spread of values, not a standard error, divides them, so neither a z-test critical value nor any published convention bands them), and the `AGG_SKEWNESS` / `AGG_KURTOSIS` / `AGG_ZSCORE` `value` (no graded shape convention met the fixture bar; Bulmer's skewness rule of thumb is only secondary-sourced).

**Prose lint** (`TestGuidanceProseLint`, `internal/descriptor/guidance_lint_test.go`, binding, built-ins only): every built-in Purpose, Interpretation and shared rule set is held to an ASA / APA JARS / SAMPL-derived rubric, each violation reported as `(operator, field, rule ID, offending span)`. Text rules over every prose string: `ASA-PROOF` (proves / confirms / establishes near hypothesis or effect), `ASA-PNULL` (p as the probability the null is true, "due to chance"), `ASA-NODIFF` ("no difference / effect / relationship" as a reading), `ASA-IMPORTANT` ("significant" coupled with important / meaningful / large, unless a negation sits between — a contrast, not a coupling). Registry rules: `PV-SHARED` (every p-value field — leaf `p_value` / `p_values`, plus the slot quirks in `pValueFieldsByOperator` — cites `Shared: "p-value"` with no bespoke `Means`), `ES-CONV` (every `details.effect_size.*` carries registry bands + citation, or a "no sourced bands"-style rationale caveat), `CORR-CAUSAL` (correlation `statistic`, and every `REG_*` `coefficients.*`, carries a causation caveat), `ASSUME-INDEP` (every `TEST_*` and `REG_*` Purpose's `Assumptions` names independence, or the pairing for a paired test), `MULTI-COMP` (`TEST_TUKEY_HSD`, `OVERLAY_PAIRWISE_*`, `*_CELL`, `OVERLAY_PROP_Z_PANEL`), `NORM-POWER` (`TEST_SHAPIRO_WILK`, `TEST_KS`, `OVERLAY_KS_VS_POP`: low power at small n, trivial departures flagged at huge n; the KS pair adds the estimated-parameters / Lilliefors caveat). Content requirements may sit anywhere in the operator's own Purpose or Interpretations; the shared rule sets never satisfy them. Operator-scoped rules apply only once the operator declares guidance, and scopes are data tables (`lintScope`, `contentRules`) — a backfill extends coverage by declaring guidance, not by editing the lint. Exemptions go in `guidanceLintAllowlist`, keyed `(operator, field, rule)` with a mandatory justification; an unjustified or stale entry fails. Today's entries are the shared p-value rule set's own null statement and its absence-of-evidence caveat.

**Runtime probes** (binding, two-way complete against `DeclaredInterpretationFields()`):
- Tests — `internal/processing/interpretation_runtime_test.go`: `TestInterpretationFieldsHoldAtRuntime` (every deferred `details.*` field present in the Go `res.Details` of ≥1 fixture on every registered tier), `TestEffectSizeKeysHoldAtRuntime` (per family and tier the union of emitted `details.effect_size` keys EQUALS `EffectSizeKeysByTest()`, finite values), `TestTestProbeFixturesCoverRegistries`. Probes check Go values, not JSON presence (`omitempty` hides a zero `df`). Conditional key sets need their own fixture (TEST_CHISQ 2×2 for `phi`, TEST_T one- and two-sample).
- Regressions — `internal/processing/regression/interpretation_runtime_test.go` `TestRegressionOutputsHoldAtRuntime`: regression paths are static, so the probe holds the `regressionOutputs` applicability row itself (read through `RegressionOutputKeys`) to what each engine emits on its plain fit path — every listed key from ≥1 probe of the type, and no fit-output key the row omits. A new `REG_*` type needs a `regressionProbes` entry.
- Overlays — `internal/service/interpretation_runtime_test.go` `TestOverlayInterpretationFieldsHoldAtRuntime`. **Recipe for an overlay Interpretation on a `summary.parameters.<k>` path:** add an entry to `overlayInterpretationProbes` keyed by the `OVERLAY_*` kind — `{fields: []string{"summary.parameters.<k>"}, run: func(t) types.OverlayLayer { … }}` — where `run` drives a real host fixture and returns the layer (`chiSqVsPopFacetLayer` is the template; `crosstabOverlayLayer`, `chiSqVsRefComposeLayer` and `ksVsPopFacetLayer` cover the crosstab, Compose and numeric-facet hosts). The declaration fails until its probe lands. On a SERIES kind (`OVERLAY_CHISQ_ROW` / `_COL`) `summary.<key>` names each series entry's summary, and the probe resolves a parameters key there, requiring it on every entry. Overlay slots holding a p-value under a path that does not say `p_value` (the `cells.value` of every per-cell and pairwise kind, plus the slot quirks) are listed in the lint's `pValueFieldsByOperator`.
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

**Two tiers over ONE validator per type, BOTH binding.** VALIDITY fails on any broken rule. COVERAGE fails on any gap that is neither covered nor listed in the exemption ledger (no coverage gate is report-only any more); each gate still `t.Log`s its full gap list.

- `TestSkillsCoverAllPurposes` (CLAUDE.md-listed) — validity limits over `builtinPurposes`; no Purpose key names an unregistered surface; coverage: every built-in (10 categories incl. post-tests, overlay kinds, distributions) declares a Purpose.
- `TestPurposeAlternativesResolve` — every `NotFor.Use` resolves (bare → a registered built-in via `PurposeSurfaces`, `<kind>:<name>` → a `features.go` row) and is not the operator itself.
- `TestPurposeQuestionsResolve` — intent IDs exist; coverage: every intent is declared by ≥3 built-in Purposes AND tagged by ≥1 example's `_meta.intents` (two gap kinds, two tables).
- `TestGlossaryTermsResolve` — glossary well-formedness + the `purpose` subtest (glossary links + jargon rule).
- `TestInterpretationCoversOutputs` — Interpretation validity; coverage: every test family, regression and `Inferential` overlay kind reads `statistic` / `p_value` / its declared effect sizes (or the per-shape slot), and every needs-reading descriptive operator its `value` / `value.*`. `TestDescriptiveReadingLists` (+ `_Falsifiers`) — every descriptive built-in in exactly one reading list.
- `TestGuidanceProseLint` — the binding prose lint (Interpretation section above); `TestGlossary_Size` (loose 55..150); `TestGlossary_OrphanReport` — coverage: every glossary term is linked by some built-in Purpose.

**Exemption ledger** — `internal/descriptor/guidance_exemptions_test.go`, the ONE place a coverage gap may be excused (test-only data; production code never reads the roadmap). Modelled on `guidanceLintAllowlist`, each entry is `{Key, Why, Owner}`: `Why` is mandatory, `Owner` is the roadmap unit that will close the gap (`ownerPermanent` for a gap closed by design). `applyExemptions` fails an entry that is unjustified, ownerless, owned by a malformed or unknown unit, listed twice, or STALE — the gap is now covered (delete the entry with the change that closes it), or the owner's `docs/roadmap/units/<U>-*.md` frontmatter says `status: done` (a permanent entry never goes stale on status). The status reader is injected (`unitStatusReader`; `roadmapUnitStatusIn(dir)` over a fixture directory in tests).

| Table | Gate | Key | Owners (none is U09-owned) |
|---|---|---|---|
| `purposeExemptions` | `TestSkillsCoverAllPurposes` | operator / family / kind name | (empty: every built-in declares a Purpose) |
| `interpretationExemptions` | `TestInterpretationCoversOutputs` | `<operator>:<field>` | (empty: every inferential output and needs-reading primary result is read) |
| `intentDeclarerExemptions` | `TestPurposeQuestionsResolve` | intent ID | `lookup` permanent; `flows` → U28; `measure_construct` → U24 |
| `intentExampleExemptions` | `TestPurposeQuestionsResolve` | intent ID | `lookup` and `simulate` permanent (synth specs are a separate surface, not library examples); `flows` → U28; `measure_construct` → U24 |
| `exampleIntentExemptions` | `TestExamples_EveryExampleHasIntent` | example category directory | (empty: every directory is tagged) |
| `glossaryOrphanExemptions` | `TestGlossary_OrphanReport` | term ID | eigenvalue / loading / principal-component / reliability → U24; centroid / distance → U25; similarity → U27; raking / stochastic-matrix / steady-state → U28; pairwise-deletion → U24 (every built-in correlation is a two-field test, so pairwise and listwise deletion coincide until the `MAT_CORRELATION` matrix) |

The permanent keys are exactly `lookup` (both intent tables: a non-analytic intent served by `pulse_lookup`, never an operator) and `simulate` in `intentExampleExemptions`. `TestGuidanceExemptions_Ledger` pins that set — a new permanent exemption is a deliberate edit of that test. `TestGuidanceExemptions_NoU09Owner` binds that no table holds a U09-owned entry (negative arm over an injected ledger); `TestGuidanceExemptions_TablesComplete` scans the ledger file so every declared table is in `exemptionTables()`, the set both ledger-wide tests read. `TestGuidanceExemptions_Apply` / `_RoadmapStatusReader` / `_GatesBite` falsify every ledger rule and prove each coverage gate fails on an unexempted gap against the real ledger.
- `TestExamples_IntentsFromTaxonomy` — example `_meta.intents` values are intent IDs (binding).
- `TestExamples_EveryExampleHasIntent` — every library example carries ≥1 `_meta.intents` tag (binding), by the QUESTION the example answers, most relevant first. The per-directory ledger `exampleIntentExemptions` (key = directory; stale once the directory is fully tagged) is empty now that every directory is tagged. `internal/examples/synth/` specs are not library examples (no `_meta`, not embedded), so `simulate` is a permanent `intentExampleExemptions` entry.
- `TestExamples_IntentPurposeConsistency` — REPORT-ONLY (logs, never fails on a mismatch): each intent an example is tagged with should appear in the Purpose intents of at least one operator it exercises (`_meta.operators` plus the `OVERLAY_*` kinds in its body). A mismatch is not a bug — a COUNT crosstab answers `composition` though `AGG_COUNT` says `describe`; the tag is the example's question. It fails only if it stops resolving Purposes.
- The runtime probes above; `TestVirtualSkillStemsNotEmbedded`; `TestGuidanceSkills_*` (surface parity).

**Budget gate** — `TestManifestGuidanceBudget` (`internal/descriptor/guidance_prose_test.go`) + `TestManifestGuidanceBudget_DefaultResults` (root): the compact full-registry manifest's guidance bytes stay under TWO binding caps — `manifestGuidancePerEntryCap` (64 B) on each entry's `intents` key + value, sized for ID-only arrays (longest today 50 B, two IDs), and `manifestGuidanceFixedCap` (1280 B) on what does not scale with the registry: the top-level `intents[]` taxonomy, the overlay `inferential` flags and the two virtual-skill `skills[]` entries (930 B today). **Why two caps, not a total (U09 decision):** the budget exists to ban prose, not to ration identifiers; the former 4096-byte total scaled with registry size — U09's ~94 new `intents` arrays projected to ~6.3 KB — and would re-trip on every operator family (U24 / U25 / U27 / U28) while still carrying IDs only. Each cap is falsified (`TestManifestGuidanceBudget_Falsifiers`). And no declared prose string (`GuidanceProse()`, ≥ `GuidanceProseMinLen` = 16 runes) appears verbatim or JSON-escaped in the default manifest, `Response` or `PredictResult`. Registry-driven: `proseSources` feeds every guidance type (`TestGuidanceProseSourcesComplete`), so a new Purpose, term, intent or Interpretation is swept with no gate change.

Gate registration detail: `.claude/reference/update-demand.md` (Other load-bearing contract gates).

## Convention registry

`internal/descriptor/conventions.go` (`builtinConventions`) is the ONLY source of built-in `Interpretation.Bands`: an Interpretation calls `conventionBands(id)` and `conventionCitation(id)` and never writes thresholds inline. Each entry carries an ID, a citation, the `Statistics` it may band, ascending `Thresholds`, `Labels` (one more than thresholds — the lowest band below Cohen's "small" is labelled `very small`, never "negligible" or "none"), `Abs` (bands read |value|) and `SymmetricLog` (a ratio statistic whose bands apply to max(x, 1/x)).

| ID | Statistics | Thresholds | Source |
|---|---|---|---|
| `cohen1988_d` | `cohens_d`, `hedges_g`, `glass_delta`, `cohens_h` | 0.2 / 0.5 / 0.8, `Abs` | Cohen (1988) |
| `cohen1988_eta2` | `eta_squared`, `omega_squared` | 0.01 / 0.06 / 0.14 | Cohen (1988) |
| `cohen1988_r` | `pearson_r` | 0.1 / 0.3 / 0.5, `Abs` | Cohen (1988) |
| `cohen1988_w` | `cohens_w`, `phi` | 0.1 / 0.3 / 0.5 | Cohen (1988) |
| `cohen1988_or` | `odds_ratio` | 1.44 / 2.48 / 4.27, `SymmetricLog` | Cohen's d benchmarks via d = ln(OR)·√3/π (Chinn 2000) |
| `cohen1988_r2` | `r_squared` | 0.02 / 0.13 / 0.26 | Cohen's f² benchmarks via R² = f²/(1+f²) (Cohen 1992) |

**Fixture contract.** `testdata/conventions.json` is transcribed independently from the sources, never generated from the Go registry. `TestConventionRegistryMatchesFixture` holds the two equal field by field, requires an https source URL plus a page / section locator per entry, and recomputes every derived threshold (η² from f, OR from d, R² from f²). Its `statistic_bindings` map a banded non-effect-size output to a registry statistic, and its `excluded` list names every output left unbanded on purpose WITH a reason (Interpretation section above). `TestBuiltinBandsCiteRegisteredConvention` binds each banded built-in Interpretation to exactly one registered convention (matched on Bands, Convention and `Abs`) whose `Statistics` cover the banded output, and refuses bands on an `excluded` output. Adding a convention means: source it, add the fixture entry and the registry entry in one change; an output with no sourced convention stays unbanded and carries a "no sourced bands" caveat (lint rule `ES-CONV`).

## Statistical primitives and the R oracle

Every p-value, critical value and interval rests on a small set of shared distribution primitives. Their numeric correctness is pinned to R, not to hand-pasted literals.

- **Generator.** `scripts/reference/gen_reference.R`, run by `make reference` (R 4.6.1 + `jsonlite`), writes one JSON per primitive into `internal/processing/testdata/reference/` — the R and package versions, the R expression, and every case at `%.17g` — then appends the `// golden-hash:` footer processing's `TestGoldensNotHandEdited` checks. **CI never runs R**; the goldens are committed. Where stock R is less accurate than the oracle needs (`ptukey` / `qtukey`, far-tail `qt`), the script refines in R (nested quadrature, Newton-polished quantiles) and keeps R's own value beside it (`r_ptukey_upper`, `r_qt`, …). Recipe: `docs/src/internals/regenerating-goldens.md`.
- **Tests.** `TestReferenceOracle_*` (`internal/processing/reference_oracle_test.go`, `internal/statdist/reference_oracle_test.go`) cover the Student-t family, `chiSquareSurvival`, `fSurvival`, `standardNormalCDF`, `standardNormalPPF`, `kolmogorovSurvival`, `studentizedRangeSurvival` and `studentizedRangeInverse`, with p down to ~1e-300.
- **Tolerance policy.** Relative `1e-10` (an absolute floor only at the representable tail edge). Never an absolute tolerance on a p-value, and never a looser one to make a case pass: amd64 and arm64 fuse `a*b+c` differently, so values drift by ulps and a relative bound is the only portable one. **A failure means the Go primitive is wrong — fix it; regenerate only to change the grid** (script and goldens committed together).
- **Normal tails.** A two-sided normal p is `normalTwoSidedP(z)` = `erfc(|z|/√2)` and a one-sided upper tail `normalUpperTailP(z)` = Φ(−z); never `2·(1 − Φ(|z|))`, which cancels to exactly 0 past |z| ≈ 8.3. Every normal-approximation family has a `*_TinyP` test at |z| ≈ 10 against R's `2*pnorm(-|z|)` (`internal/processing/normal_tail_test.go`, `TestNormalTwoSidedP_MatchesR`).
- **`internal/statdist`.** The Student-t family (`StudentTTwoSidedP`, `StudentTCDF`, `StudentTQuantile`, `StudentTInverseTwoSided`) and the regularized incomplete beta live once, in a leaf importable by `internal/processing` and `internal/processing/regression` (stdlib + gonum only, `TestStatdistImportBoundary`). `TestStudentTFormsAgree` pins the paired forms bit for bit. Helpers keep their function boundaries on purpose (FMA bit-identity). Chi-square, F, normal, Kolmogorov and studentized range still live in `internal/processing`; moving one to `statdist` is a refactor that must stay bit-identical.
- **Not yet covered.** The oracle pins the PRIMITIVES, not each operator's assembled output. Per-output numeric oracles (Shapiro–Wilk, Brown–Forsythe, Tukey q / `p_adj`, KS p, Kendall τ-b with ties, Mann–Kendall p, Pearson CI, every `TEST_*` p at tight tolerance, `REG_OLS` / `REG_GLM` SE + p, the `REG_BAYES_LINEAR` posterior, de-circularised `OVERLAY_CHISQ_VS_POP` / `OVERLAY_KS_VS_POP`, `OVERLAY_PAIRWISE_PROBIT_T`) are U36.

## Review record

The statistics review of the U08 guidance is committed at `docs/roadmap/reviews/U08-statistics-review.md`: method (the deterministic layers above plus an advisory LLM panel), every finding with its disposition and fixing commit, the engine fixes it triggered, and the open items for the human reviewer. Human sign-off is a release-blocking U33 item; a later guidance backfill appends its own section rather than starting a new file.

## Extension hook

All eight root `*Registration` structs take an optional `Purpose *descriptor.Purpose`; `TestRegistration` also takes `Interpretation []descriptor.Interpretation`. At every `pulse.New`, AFTER naming/shape validation, the probe and the `DependsOn` check, `validateExtensionGuidance` (`extensions_guidance.go`) runs the SAME `ValidatePurpose` the built-in tier uses, resolving `NotFor.Use` against the instance (built-ins, the feature table AND other extensions), then `ValidateInterpretations` in STRUCTURE-ONLY mode (nil resolver: path syntax and content, never key existence). First invalid wins (category order, then slice index; Purpose before Interpretation) → `PULSE_EXTENSION_PURPOSE_INVALID`, details `category`, `name`, `index`, `part`, the first failing rule and every violation. Only the Purpose's sorted intent IDs project to `OperatorMeta.intents`; prose never enters the manifest (`TestExtensions_PurposeProseNeverInManifest` — per instance, so outside `GuidanceProse`). An absent Purpose projects no `intents` key and keeps the extension out of the coverage report by construction. Embedder prose: `docs/src/internals/extension-points.md`.

## Out of scope (owned later)

- U24 / U25 / U27 / U28 — the ledger's remaining owners: the `measure_construct` and `flows` intents plus the glossary terms their operators will link (Gates, exemption ledger). Each closes its own entries; a covered gap or an owner marked `status: done` makes the entry fail as stale.
- U36 also owns the runtime defects U09 documented rather than fixed (split-aware `FEAT_TARGET_ENCODE`, `AGG_ZSCORE`'s always-zero value, normal-z `AGG_CI_*`, tied `ATTR_PERCENTILE`, attributes writing 0 for missing inputs) — the Interpretations and skills describe today's behaviour.
- U10 — profile-aware skill rendering.
- U21 — rendering guidance into skills and docs (and syncing atomic skills that drifted from the registries).
- U22 — recommend / explain (first consumer of intent shapes).
- U23 — guidance over MCP.
- U33 — release-blocking human statistics sign-off of the U08 review record (`docs/roadmap/reviews/U08-statistics-review.md`), with the reviewer's CODEOWNERS entries.
- U36 — per-output numeric oracles (Statistical primitives and the R oracle, "Not yet covered").
