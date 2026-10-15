# v1.0.0 TODO

Every **committed** v1.0.0 feature across the roadmap themes. Stretch and post-1.0 items are deliberately excluded; they stay in the theme documents.

**Units of work:** every item links to the Flow unit that delivers it; see [`units/README.md`](units/README.md).

**How to use this file:**
- Tick a box (`- [x]`) in the same PR that completes the item.
- An item counts as complete only when its code, tests, skills, CLAUDE.md / `.claude/reference/` companions and docs have all landed, as the Update Demand requires.
- Sections are in build order. Feature profiles and the guided-analysis metadata core come first, so every later surface is profile-filtered and carries purpose metadata from its first commit.

Theme documents: see the [roadmap index](README.md).

---

## 1. API surface & release pipeline

### Public Go surface ([api-and-release 00](v1.0.0-api-and-release/00-public-surface.md))
- [x] **#1** Downstream usage catalog completed (maintainer) · [U02](units/U02-public-surface.md)
- [x] **#2** Classification per package decided (public / public-narrowed / internal) · [U02](units/U02-public-surface.md)
- [x] **#3** Package moves and narrowing done; facade re-exports added · [U02](units/U02-public-surface.md)
- [x] **#4** API-compatibility check (`gorelease` / `apidiff`) in CI against the latest tag · [U02](units/U02-public-surface.md)
- [x] **#180** `io` factory: `io.NewReader` / `NewReaderFromBytes` / `NewWriter` / `NewWriterToBuffer` with typed `io.Format` constants and format knobs on typed sub-structs; `io/<fmt>` and `io/format` internal · [U02](units/U02-public-surface.md)
- [x] **#181** `Options.DisableCrosstabFusion` replaces `(*Pulse).Service()`, which is removed · [U02](units/U02-public-surface.md)
- [x] **#182** `(*Pulse).InspectBytes` / `PredictBytes` replace `descriptor.InspectFromBytes` / `PredictFromBytes`; the instance fills the extension snapshot · [U02](units/U02-public-surface.md)
- [x] **#183** `(*Pulse).CohortArtifacts(ctx, cohort) ([]string, error)` (planned as `IndexArtifacts`); the index-manifest helpers move internal · [U02](units/U02-public-surface.md)
- [x] **#184** Root-native `DateRangeSpec`, `MemberSet`, `LoadMemberSetResult` (spelling unchanged) · [U02](units/U02-public-surface.md)

#### Extension contract
- [x] **#185** Public `extend` package: aggregator, online aggregator, grouper + streaming variants, filterer builder / filter func, attribute and test interfaces, window and feature computers, factory types · [U02b](units/U02b-extension-contract.md)
- [x] **#186** `extend.Record`: a small read-only record interface (values, nulls, wide and set accessors), sized by an inventory of what built-in operators read · [U02b](units/U02b-extension-contract.md)
- [x] **#187** Registration adapts `extend` operators onto the engine; built-ins keep the concrete fast path; built-in vs adapted parity tests · [U02b](units/U02b-extension-contract.md)
- [x] **#188** `processing`, `processing/feature`, `processing/window` fully internal; any interim root aliases from U02 removed · [U02b](units/U02b-extension-contract.md)
- [x] **#189** `extension-points.md` and the `adding-*` recipes rewritten against `extend` · [U02b](units/U02b-extension-contract.md)

#### Extension hardening
- [ ] **#194** Chain predict has a production caller that passes the `ExtensionsSnapshot` (`ValidateChain` / `ValidateChainWithExtensions` reachable outside tests) · [U34](units/U34-extension-validation.md)
- [ ] **#195** An operator's own `Components()` keys probe-validated against its `ComponentSchema` at `pulse.New` · [U34](units/U34-extension-validation.md)
- [ ] **#196** `FeatureRegistration.Streamable` probe-validated at `pulse.New` · [U34](units/U34-extension-validation.md)
- [ ] **#197** Synth-distribution extension contract: an `extend` factory shape, or a documented decision that distributions are not an extension category · [U34](units/U34-extension-validation.md)
- [ ] **#216** Extension aggregators declare whether their output is a count, so `return.precision` can write it exact (today every extension aggregator rounds like a measure; built-ins are classified in `countSemanticAggregations`) · [U34](units/U34-extension-validation.md)

#### CLI shell completion
- [ ] **#208** `pulse completion {bash,zsh,fish,pwsh}` prints an installable script; every command, subcommand and flag of `buildApp()` completes · [U37](units/U37-shell-completion.md)
- [ ] **#209** Closed-vocabulary flag values complete dynamically from the library (operators, features, example profiles, I/O formats, templates, error codes, enum flags), never a CLI-side list · [U37](units/U37-shell-completion.md)
- [ ] **#210** Cohort-aware completion: `.pulse` cohorts under `PULSE_DATA_DIR` and header-only field names for field-taking flags · [U37](units/U37-shell-completion.md)
- [ ] **#211** Completion gates: every leaf and flag reachable, deterministic and quick candidates, quiet failure, nothing but candidates on stdout · [U37](units/U37-shell-completion.md)
- [ ] **#212** Per-shell install docs, `flags.md` row, and generated scripts shipped in the `make dist` archives · [U37](units/U37-shell-completion.md)

#### Predict / runtime parity (pre-existing gaps found in U03 / U05)
- [x] **#198** Predict refuses every operator × field type the runtime refuses: the "predict looser" half of the `knownTypeDivergence` ledger (`field_type_acceptance_test.go`) is emptied. Done (E2-S2 + E2-S3): every pair predict let through and the runtime refused now refuses on both sides with one code, the decimal-on-aggregator case included (`PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL`, an error on both sides); the ledger is deleted · [U35](units/U35-predict-runtime-parity.md)
- [x] **#199** The runtime refuses, rather than silently computes on, a field it cannot read: the ledger's "predict stricter, runtime wrong" half (`FEAT_POLY`, `REG_*` and `WIN_*` value windows on categorical / set / decimal / `packed_bool` / `datetime`), plus tier-1 tests on `set_*` · [U35](units/U35-predict-runtime-parity.md)
- [x] **#200** A request with more than one `Groups` entry executes every group or is refused (today only `Groups[0]` runs) · [U54](units/U54-multi-grouper.md)
- [x] **#201** Remaining silent predict / runtime gaps: crosstab cell-aggregator validity in predict, one label set for the label-collision check, label bindings on ProcessChain stages ≥ 1, windowed record rows under projection (sub-gap (a), crosstab cell / margin aggregator validity, done in U35 E2 + E3-S1, inside Compose slots too; sub-gaps (b) one label set — `descriptor.OutputLabels` on both sides — and (c) stage ≥ 1 labels — now applied — done in U35 E3-S2; (d) windowed record rows — a windows-only request's rows now carry the full record under the default projection (`NeededFields` widens that shape), done in U35 E3-S3) · [U35](units/U35-predict-runtime-parity.md)
- [x] **#207** Shard-archive cohesion compares `Nullable`: `AddShard`, `shard verify`, the `NewCohortBuilder` anchored-append pre-check and the archive reader refuse a shard whose per-field nullability differs from the canonical schema, instead of decoding it under the canonical flags · [U35](units/U35-predict-runtime-parity.md)
- [x] **#213** `Response.overlays` joins the feature gate (`gatedSlots`, `internal/descriptor/request_slots.go`): hiding the overlay features leaves `overlays` paths in the instance payload schema and in the presets. Done (U35 E5-S1): every result root's `overlays` rides its request root's gate; the Request / Response gate also lists the MATRIX_RESULT host · [U35](units/U35-predict-runtime-parity.md)
- [x] **#214** A joined slot or stage derives its `return` precision-exact set from the defaults-resolved request, as an unjoined one does (today the join path applies defaults to a service-internal clone, so `Plan.Exact` reads the un-defaulted request; `Process` included). Verified already correct (U35 E5-S1): the join clone shares the caller's `*Aggregation` pointers, so defaults reach the facade's plan; `TestReturn_PrecisionJoinedDefaultCount` gates it · [U35](units/U35-predict-runtime-parity.md)
- [x] **#215** `finalizeMergedPartial`'s empty-partial path emits the zero-n aggregation entries the serial path emits (ungrouped and grouped); today a merged run with an empty partial carries no `Aggregations` block. Done (U35 E5-S2): an empty merge is seeded with the zero-record partition state (`newPartitionPartial`, shared by both reducers), so the zero-n row, the per-slot zero-n entries and the grouped tail's zero-bucket answer (`Groupers` included) match serial · [U35](units/U35-predict-runtime-parity.md)
- [x] **#221** The Compose components veto (`composeSlotVetoes`) takes the weight-basis rule the request veto took in U18: a slot named by a Compose overlay that reads host floors only on a weighted slot (χ²-vs-ref, prop-z cell/panel, index) keeps `Components` only when that slot's crosstab cell is weighted (`descx.CrosstabCellWeightBasis`), and only the `crosstab` sub-part, not every sub-part. Pure compute saving; kept numbers unchanged. Done (U35 E5-S2): per-kind audit on `composeOverlaySlotVeto`; `OVERLAY_PROP_Z_CELL` / `_PANEL` / `OVERLAY_CHISQ_VS_REF` (targets) keep `components.crosstab` only over a weighted slot (the panel's components-reading `n_source` modes always), every other kind keeps the full veto · [U35](units/U35-predict-runtime-parity.md)
- [x] **#222** `pulse mcp` reports the effective default `return` preset (request-less default after `--return` / `mcpserve.Options.DefaultReturn` / feature profile / built-in `standard`) in its startup line and `ServeInfo` · [U20](units/U20-observability.md)
- [x] **#223** Attribute the `standard` preset's ~+10% cost over an unshaped `Process` (U18 `BenchmarkProcessDefaultReturn`; cached plan resolution is only ~0.27%): expose a shaping/apply phase in the operation hooks' phase timings and benchmark it; optimise if the apply pass dominates (U20: `shape` phase added; `Apply(standard)` ≈ 4.2 µs of the ≈ 10.3 µs standard-vs-unset delta, ~40%, under the 50% bar, so not optimised) · [U20](units/U20-observability.md)

#### Cohort facade
- [x] **#190** `CohortReader` on the facade: `Schema()`, `Len()`, `RecordAt(i)` · [U02c](units/U02c-cohort-facade.md)
- [x] **#191** `CohortBuilder` on the facade: schema + append rows, grouped (`0x02`) cohorts included · [U02c](units/U02c-cohort-facade.md)
- [x] **#192** `PredictResult.CrosstabFusable` (no-execute) with a runtime parity gate against the engine's fusion check; payload-schema golden regenerated, `format_version` stays `"1.1"` · [U02c](units/U02c-cohort-facade.md)

#### Follow-ups from U35 ([U35](units/U35-predict-runtime-parity.md))
- [ ] **#456** `ATTR_SCALE_SCORE` / `ATTR_PROFILE_SIMILARITY` are not merge-admitted: their per-run Components / warning tallies need a tally-merge hook on the shard-archive and parallel-decode reducers (~150 lines), so a Process carrying one runs the serial path (correct, slower) and a `ProcessChain` stage carrying one is refused `PULSE_CHAIN_NOT_MERGEABLE`. Perf only. Found in U35 (E5-S3, #391) · [U51](units/U51-archive-scale.md)
- [ ] **#457** Synth writer: add a dedicated test that a non-nullable field's null-bitmap bit stays 0 (the writer guards it in code; `TestCohortBuilder_NonNullableShardInNullableArchive` relies on it, nothing pins the synth path). Found in U35 (E1-S1, #207) · [U52](units/U52-synth-realism.md)
- [ ] **#458** A join `ON` key over a `u64` field above 2^53 still matches on its float64 echo (`encoding/join_keys.go` documents it): two distinct large keys can collide. Give the join key an exact `u64` path, or refuse such a key. #427 fixed the attribute keys only. Found in U35 (E1-S2) · [U34](units/U34-extension-validation.md)
- [ ] **#459** The map-decoder fallback `Record`s (stream, `shard_iter`, parallel-decode projection without a plan, lookup, `filter_to_file`) carry no exact `u64` word, so a `u64` above 2^53 reaches an extension through its float64 echo (the keyed attribute paths of #427 are unaffected). Decide: carry the word, or document the `extend.Record` float64 contract as final. Found in U35 (E1-S2) · [U34](units/U34-extension-validation.md)
- [ ] **#460** Auto labels (`Options.AutoLabels`) are not injected on `ProcessChain` stages >= 1 (explicit label bindings are applied since U35 E3-S2); a stage's rows stay un-labelled unless bound. Documented in the chain-join-facet slice. Found in U35 (E3-S2) · [U44](units/U44-strict-input.md)
- [ ] **#461** `PredictCompose` archive slots are archive-aware but read the whole archive (`Service.Open` does `afero.ReadFile`), not header-only like `Predict` / `Inspect`; resolves with #455. Found in U35 (E7-S3) · [U51](units/U51-archive-scale.md)
- [ ] **#462** Facet runtime warnings carry no details: `FacetResult.Warnings` is `[]string`, so the runtime's `PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL` rides as `"CODE: message"` while predict carries `details`. Make the facet warnings coded entries (additive wire change). Found in U35 (E5-S3) · [U44](units/U44-strict-input.md)
- [ ] **#463** Four atomic op skills sit over the soft 1200-byte budget after U35: `op-attr-reg-fitted`, `op-attr-reg-residual`, `op-attr-reg-leverage`, `op-agg-ratio`; trim in the docs audit. Found in U35 (E7-S3) · [U32](units/U32-docs-audit.md)

### Release pipeline ([api-and-release 01](v1.0.0-api-and-release/01-release-pipeline.md))
- [x] **#5** `internal/buildinfo` + `pulse.Version()`; ldflags injection; `ReadBuildInfo` fallback; `make build` uses `git describe` · [U01](units/U01-release-pipeline.md)
- [x] **#6** `pulse version` / `--version`; `mcpserve` and `gosdk` default to the real version (remove hard-coded `"1.0.0"`); manifest `pulse_version` · [U01](units/U01-release-pipeline.md)
- [x] **#7** Gate against hard-coded version literals · [U01](units/U01-release-pipeline.md)
- [x] **#8** `ci.yml` callable; `release.yml` on `v*` tags gated on CI · [U01](units/U01-release-pipeline.md)
- [x] **#9** Binaries for linux / darwin / windows × amd64 / arm64, plus checksums; uploaded to the GitHub Release (created with generated notes only when none exists; `-` tags marked pre-release) · [U01](units/U01-release-pipeline.md)

---

## 2. Feature profiles — foundation

### FP1 — Feature registry
- [x] **#10** Stable feature names and kinds (`capability`, `operator`, `io_format`, `mcp_extra`) across all registries — internal table `internal/descriptor/features.go`; operators bare, others `<kind>:<name>`; synth distributions are not features · [U04](units/U04-profiles-model.md)
- [x] **#11** `Since` version on every feature (in the internal table, not on registrations; extensions carry none), plus `TestFeaturesHaveSince` · [U04](units/U04-profiles-model.md)
- [x] **#12** Dependency graph (in `internal/descriptor/features.go`), plus `TestProfileDependenciesComplete` · [U04](units/U04-profiles-model.md)
- [x] **#13** Always-present core defined: manifest, payload schema, skills, examples, errors lookup, inspect, predict (plus open, count records, cohort artifacts) · [U04](units/U04-profiles-model.md)

### FP2 — Profile model
- [x] **#14** Feature profile file format: an exact-name allowlist, `written_with`, optional `behaviour`; `limits` / `return` refused until U19 / U17 · [U04](units/U04-profiles-model.md)
- [x] **#15** `pulse.New` validation and the config codes (`PULSE_FEATURE_PROFILE_INVALID`, `PULSE_FEATURE_PROFILE_UNKNOWN`, `PULSE_FEATURE_PROFILE_DEPENDENCY`), plus `TestProfileRejectsPatterns` · [U04](units/U04-profiles-model.md)
- [x] **#16** `Options.FeatureProfile`, `Options.FeatureProfileFile`, the `PULSE_FEATURE_PROFILE` env var (read only by `pulse mcp` / `mcpserve.NewPulse`), and `pulse mcp --feature-profile` (the CLI is otherwise unprofiled) · [U04](units/U04-profiles-model.md)

### FP3 — Instance snapshot & request path
- [x] **#17** `InstanceSnapshot` (merges the extensions snapshot with the resolved feature set) · [U05](units/U05-profiles-enforcement.md)
- [x] **#18** Hidden names resolve exactly like never-registered names at the single validation choke point, for every entry point · [U05](units/U05-profiles-enforcement.md)
- [x] **#19** Hidden request slots refused like unknown fields under strict decode · [U05](units/U05-profiles-enforcement.md)
- [x] **#20** `feature_set_digest` present on every instance, including the default · [U05](units/U05-profiles-enforcement.md)

### FP4 — Self-description
- [x] **#21** Instance-scoped manifest, payload schema (`p.PayloadSchema()`), predict and errors list · [U05](units/U05-profiles-enforcement.md)
- [x] **#22** Profile goldens for each example profile · [U05](units/U05-profiles-enforcement.md)
- [x] **#23** `TestProfileDefaultIsFull`: no profile produces output byte-identical to today · [U05](units/U05-profiles-enforcement.md)

### FP5 — Skills & ontology
- [x] **#24** Ontology graph (intents → operators → skills / examples / glossary / NotFor edges), pruned once at `pulse.New` · [U10](units/U10-skill-ontology.md)
- [x] **#25** List, search, `## See`, intents, recommendations **and exact-name get** all honour the pruned graph · [U10](units/U10-skill-ontology.md)
- [x] **#26** Progressive-disclosure rewrite of the topical skills (paired with G2) · [U10](units/U10-skill-ontology.md)
- [x] **#27** `<!-- feature: … -->` fence syntax and rendering, plus `TestSkillsCoverFeatureFences` (report-only until the rewrite is done, then failing) · [U10](units/U10-skill-ontology.md)
- [x] **#28** `TestSkillsCoverProfileGet`: every hidden skill or example is indistinguishable from a nonexistent name · [U10](units/U10-skill-ontology.md)

### FP6 — MCP
- [x] **#29** Instance-scoped registration of tools, prompts and resources · [U06](units/U06-profiles-mcp-tooling.md)
- [x] **#30** Tool input schemas carry the instance's enums only · [U06](units/U06-profiles-mcp-tooling.md)
- [x] **#31** `TestProfileInvisibilityParity` · [U06](units/U06-profiles-mcp-tooling.md)

### FP7 — Embedder tooling & export
- [x] **#32** Feature-profile tooling `init`, `check`, `diff` and `show` (leaf naming open: `pulse profile` is taken by synth data profiling) · [U06](units/U06-profiles-mcp-tooling.md)
- [x] **#33** Example feature profile files in `examples/profiles/` · [U06](units/U06-profiles-mcp-tooling.md)
- [x] **#34** `pulse docs export` / `p.ExportReference` (shares the G3 generator) · [U21](units/U21-guidance-generated-docs.md)
- [x] **#35** Embedder docs at `docs/src/library/feature-profiles.md`; `.claude/reference/feature-profiles.md` (both started in U04; U06 completes them for enforcement and tooling) · [U06](units/U06-profiles-mcp-tooling.md)

---

## 3. Guided analysis — metadata core

### G1 — Metadata core
- [x] **#36** Intent taxonomy (`descriptor/intents.go`), projected to manifest `intents[]` · [U07](units/U07-guidance-metadata.md)
- [x] **#37** `Purpose` type: plain line, intents, questions, use cases, NotFor, assumptions, level, glossary links · [U07](units/U07-guidance-metadata.md)
- [x] **#38** `Interpretation` type: per-output meaning, labelled bands, conventions, caveats; shared p-value rules · [U07](units/U07-guidance-metadata.md)
- [x] **#39** Glossary registry (about 60 terms) and the `pulse-skill://glossary` resource · [U07](units/U07-guidance-metadata.md)
- [x] **#40** Extension `Purpose` hook · [U07](units/U07-guidance-metadata.md)
- [x] **#41** Gates, starting report-only: `TestSkillsCoverAllPurposes`, `TestPurposeAlternativesResolve`, `TestPurposeQuestionsResolve`, `TestGlossaryTermsResolve`, `TestInterpretationCoversOutputs` · [U07](units/U07-guidance-metadata.md)
- [x] **#42** `TestManifestGuidanceBudget`: no guidance prose in default payloads; manifest growth stays under about 4 KB · [U07](units/U07-guidance-metadata.md)

### G2 — Back-fill (statistics reviewer signs off before the gates flip)
- [x] **#43** Tests (`TEST_*`) · [U08](units/U08-guidance-backfill-inferential.md)
- [x] **#44** Overlays (`OVERLAY_*`) · [U08](units/U08-guidance-backfill-inferential.md)
- [x] **#45** Regressions (`REG_*`) · [U08](units/U08-guidance-backfill-inferential.md)
- [x] **#46** Aggregators (`AGG_*`) · [U09](units/U09-guidance-backfill-descriptive.md)
- [x] **#47** Attributes, filterers, groupers, windows and features · [U09](units/U09-guidance-backfill-descriptive.md)
- [x] **#48** Synth distributions · [U09](units/U09-guidance-backfill-descriptive.md)
- [x] **#49** Gates flipped from report-only to failing · [U09](units/U09-guidance-backfill-descriptive.md)

---

## 4. Statistical integrity

### Weighting ([statistical-integrity 01](v1.0.0-statistical-integrity/01-weighting.md))
- [x] **#50** `Request.Weight`, per-slot `weight` (incl. `null`), `Options.DefaultWeight`, `kind` frequency / probability · [U11](units/U11-weighting-descriptive.md)
- [x] **#51** Weight validation, `n_weight_invalid`, `PULSE_WEIGHT_INVALID_ROWS` · [U11](units/U11-weighting-descriptive.md)
- [x] **#52** Weighted aggregators incl. percentiles; `AGG_WEIGHTED_MEAN` alias · [U11](units/U11-weighting-descriptive.md)
- [x] **#53** Weighted crosstab cells and margins; unweighted base via `margin_aggregations` · [U11](units/U11-weighting-descriptive.md)
- [x] **#54** Weighted share / index overlays · [U11](units/U11-weighting-descriptive.md)
- [x] **#55** Weighted tests and significance overlays with Kish `n_eff` (partly shipped in v0.39.1: `OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z` with `n_basis`) · [U12](units/U12-weighting-inferential.md)
- [x] **#56** Weighted attributes, `GROUP_QUANTILE`, regressions · [U12](units/U12-weighting-inferential.md)
- [x] **#57** Components `w_sum` / `n_eff`; manifest `weight_aware`; predict reporting; extension `WeightAware` (`n_eff`, `sum_weights_sq`, `m2_weighted`, `weighted_variance` already ship on `AGG_WEIGHTED_MEAN` as of v0.39.1; `sum_weights` vs `w_sum` naming is U11's to reconcile — shipped as `sum_weights`, no `w_sum` alias) · [U11](units/U11-weighting-descriptive.md)
- [x] **#58** SPSS weight-variable capture and suggestion · [U11](units/U11-weighting-descriptive.md)
- [x] **#59** `TestWeightUnityParity`, `TestWeightFrequencyExpansionParity`, reference fixtures; `weighting.md` skill · [U12](units/U12-weighting-inferential.md)

### Multiple comparisons ([statistical-integrity 02](v1.0.0-statistical-integrity/02-multiple-comparisons.md))
- [x] **#60** `processing/multiplicity`: Bonferroni, Holm, BH, BY · [U13](units/U13-multiplicity.md)
- [x] **#61** `multiplicity {method, family}` on Request / OverlaySpec / Test (+ ComposeOverlaySpec / ComposedRequest; `MatrixSpec` → [U28](units/U28-matrix-overlays.md)); `Options.DefaultMultiplicity` (shipped `none`; correction is opt-in) · [U13](units/U13-multiplicity.md)
- [x] **#62** Families `layer` / `row` / `column` / `request` / `compose` (`matrix` → [U28](units/U28-matrix-overlays.md)), incl. across Compose slots · [U13](units/U13-multiplicity.md)
- [x] **#63** Additive `p_adjusted` / `significant_adjusted` / `multiplicity` outputs · [U13](units/U13-multiplicity.md)
- [x] **#64** Advisory + Explain hooks; glossary terms · [U13](units/U13-multiplicity.md)
- [x] **#65** Reference-value, identity and family-boundary gates; `multiplicity-correction.md` skill · [U13](units/U13-multiplicity.md)

### Reference oracles (found in U08; [review record](reviews/U08-statistics-review.md))
- [ ] **#202** Per-output R oracle for every `TEST_*` family at tight relative tolerance: Shapiro–Wilk (Shapiro–Francia W′ + p), Brown–Forsythe, Tukey q / `p_adj`, KS p, Kendall τ-b with ties, Mann–Kendall (`TEST_TREND`) p, Pearson CI, and every family's p-value (row and post twins) · [U36](units/U36-reference-oracles.md)
- [ ] **#203** `REG_OLS` (incl. ridge / lasso / elastic net) and `REG_GLM` (binomial, poisson, gamma) coefficients, SEs and p-values, and the `REG_BAYES_LINEAR` posterior, pinned to an external reference · [U36](units/U36-reference-oracles.md)
- [ ] **#204** De-circularised overlay oracles: `OVERLAY_CHISQ_VS_POP`, `OVERLAY_CHISQ_VS_REF`, `OVERLAY_KS_VS_POP` and `OVERLAY_PAIRWISE_PROBIT_T` checked against an independent R computation · [U36](units/U36-reference-oracles.md)
- [ ] **#205** Runtime bugs found in U08: infinite `TEST_FISHER_EXACT` OR and Pearson / Spearman `details.t` break `--json`; `TEST_BROWN_FORSYTHE` F = 0, p = 0 at zero within-group spread; Shapiro–Francia p uncalibrated for n < 5 (the Winitzki inverse-erf CI critical z, ~4.7e-4 too small, was fixed by [U12](units/U12-weighting-inferential.md) E5-S1) · [U36](units/U36-reference-oracles.md)

---

## 5. Time zones ([time-zones 00](v1.0.0-time-zones/00-design.md))

- [x] **#66** Step 0: `internal/temporal`; migrate every open-coded epoch-day site; `TestNoZoneMathOutsideTemporal` · [U03](units/U03-temporal-foundation.md)
- [x] **#67** `Zone` type, embedded tzdata, transition-table fast path · [U03](units/U03-temporal-foundation.md)
- [x] **#68** `Options.DefaultTimeZone`, `Request.TimeZone`, per-slot `tz`; `date`-field rejection · [U03](units/U03-temporal-foundation.md)
- [x] **#69** Zone-aware `GROUP_DATE`, `GROUP_DATE_RANGES`, `FILTER_DATE_RANGES`, `ATTR_DATE_PART`, `FEAT_DATE_FEATURES`, `OVERLAY_YOY`, range tables, `week_start` · [U14](units/U14-zone-aware-operators.md)
- [x] **#70** Import `--source-tz` with `--dst-policy` · [U14](units/U14-zone-aware-operators.md)
- [x] **#71** Zone-aware output rendering; predict and manifest reporting (tzdata version) · [U14](units/U14-zone-aware-operators.md)
- [x] **#72** `TestUTCZoneIsIdentity`, `TestDSTBoundaries`, `TestDateFieldRejectsTZ`; `time-zones.md` skill · [U14](units/U14-zone-aware-operators.md)

---

## 6. Vector & matrix — foundation

### E1 — Linear-algebra core & co-moment accumulator
- [x] **#73** `linalg/` with Cholesky, SymEigen, SVD, QR, tolerances, sign convention and ordering; import-boundary gate · [U15](units/U15-linalg-core.md)
- [x] **#74** Synth Cholesky migrated onto `linalg` (fidelity goldens byte-identical) · [U15](units/U15-linalg-core.md)
- [x] **#75** Regression solve and inverse migrated onto `linalg` · [U15](units/U15-linalg-core.md)
- [x] **#76** Weighted co-moment accumulator (listwise and pairwise) with exact merge; property tests · [U15](units/U15-linalg-core.md)
- [x] **#77** Deterministic merge tree under `ShardWorkers` / `DecodeWorkers` · [U15](units/U15-linalg-core.md)

### E2 — Virtual vectors & matrix result
- [x] **#78** `Request.Vectors`: resolution, hashing, projection, predict echo · [U16](units/U16-matrix-result.md)
- [x] **#79** `Request.Matrices` / `Response.Matrices`, `MatrixResult`, and the symmetric/upper-triangle `MatrixPayload` encoding · [U16](units/U16-matrix-result.md)
- [x] **#80** Matrix components floor; manifest `Matrix` capability block; predict shape and cost · [U16](units/U16-matrix-result.md)
- [x] **#81** Payload-schema golden regenerated; `.claude/reference/matrix-and-vectors.md` · [U16](units/U16-matrix-result.md)
- [x] **#82** `MaxMatrixDim`, `precision` and `top_pairs` controls · [U16](units/U16-matrix-result.md) (`top_pairs` only; `precision` → [U17](units/U17-response-shaping-core.md), landed there; `MaxMatrixDim` → [U19](units/U19-resource-limits.md), landed there)
- [x] **#83** `MAT_COVARIANCE` · [U16](units/U16-matrix-result.md)
- [x] **#84** `MAT_CORRELATION` (Pearson), with parity against `TEST_PEARSON_R` · [U16](units/U16-matrix-result.md)

---

## 7. Guided analysis — docs, API & MCP

### G3 — Generated docs & skill sections
- [x] **#85** `internal/docgen`, `make docs` integration, `TestDocsGeneratedCurrent` · [U21](units/U21-guidance-generated-docs.md)
- [x] **#86** Generated operator catalog pages · [U21](units/U21-guidance-generated-docs.md)
- [x] **#87** Generated glossary page · [U21](units/U21-guidance-generated-docs.md)
- [x] **#88** "Reading your results" pages (tests, regressions, matrices, overlays, components) · [U21](units/U21-guidance-generated-docs.md)
- [x] **#89** Rendered skill sections `## Use when` / `## Reading the output`; `skill-pack.md` updated, plus `TestSkillPurposeSectionsCurrent` · [U21](units/U21-guidance-generated-docs.md)

#### Follow-ups from U21
- [ ] **#235** Correct the stale hand-written skill claims against the engine: `op-agg-ci-*` `t_critical` wording, `ATTR_REG_LEVERAGE` range, `op-win-*` `order_by` types, `WIN_LAG` offset, `WIN_LAG` / `WIN_LEAD` float64 output, `GROUP_RANGE` / `ROUNDED` `interval` default · [U38](units/U38-skill-sync.md)
- [x] **#397** In a plain (non-sweep) compose slot, an unknown grouper key such as `"label"` on `GROUP_RANGE` is silently ignored, while a sweep body's strict decode refuses it. A plain request should refuse unknown grouper keys the same way, or the two paths should be unified. Found in U42 (E4-S1) while converting the MMM fixture (`docs/roadmap/fixtures/mmm-harbor-pine/req/10`–`11` used to carry `"label"`). · [U44](units/U44-strict-input.md)
- [ ] **#236** Trim the `op-*` bodies over the 1,200-character budget (about 111), then flip `TestSkillTokenBudget` for `op-*` from soft to hard · [U38](units/U38-skill-sync.md)
- [ ] **#237** `pulse skills` / `pulse examples` and the toolmeta "embedded library" wording vs embedder additions; synth examples lack `_meta` for the `op-synth-*` `## See` tags · [U38](units/U38-skill-sync.md)
- [ ] **#238** Hidden-capability prose residue the slot-token filter does not reach: the matrices-hidden `pulse_examples_search` Filters sentence and `pulse_manifest` Carries-per-operator sentence are over-dropped (splitting them moves the default manifest golden), topical and tool skill bodies are not slot-token scrubbed by `Discovery` (12 sentences fenced in 6 topical skills, one reworded), and a weighting-hidden instance still lists the weighted aggregator keys on the Components page · [U32](units/U32-docs-audit.md)
- [ ] **#239** The Analysis Guide adds 303 pages: the mdBook search index warns at about 12 MB, and the `docs` CI job (PR-time `mdbook build`) is not a required check. Decide whether to trim the search index and whether to require the job · [U32](units/U32-docs-audit.md)

### G4 — Recommend, Explain, advisories
- [x] **#90** `pulse.Recommend` with bound (cohort) and unbound (cohort-free) modes; `pulse recommend`; `pulse_recommend`; `tool-recommend.md` · [U22](units/U22-recommend-explain.md)
- [x] **#91** `pulse.Explain` request mode; terse by default, `detail: "full"` on request · [U22](units/U22-recommend-explain.md)
- [x] **#92** `pulse.Explain` response mode; `pulse explain`; `pulse_explain`; `tool-explain.md` · [U22](units/U22-recommend-explain.md)
- [x] **#93** Predict `advisories`, with codes and fixups · [U22](units/U22-recommend-explain.md)
- [x] **#94** Explain goldens per operator family; Recommend goldens per intent · [U22](units/U22-recommend-explain.md)

#### Follow-ups from U22
- [x] **#246** Compose and Facet predict get a direct facade, CLI and MCP entry (today their advisories reach callers only through Explain and bound Recommend; `PredictOptions.SidecarLoader` is wired from the Explain facade only) · [U23](units/U23-guidance-mcp.md)
- [x] **#247** The MCP core reflector (`internal/mcp/schema.go`) rendered `json.RawMessage` as a byte array (operator `params`, `pulse_recommend`'s `recommendations[].request`); it now renders any-JSON · [U23](units/U23-guidance-mcp.md)
- [x] **#248** Bound Recommend reads a shard archive header-only: the archive path of `descx.Predict` does `io.ReadAll`, so a bound Recommend on an archive reads every shard. Done (U35 E7-S3): the facade predicts / inspects through the open handle and `descriptor.Predict` / `Inspect` read an archive by random access (`io.ReaderAt`): the zip central directory, the canonical schema entry and each shard's header; `PeekShardRecordCount` counts a stored shard from its central-directory size, never reading its records; an anchor reads its one entry; `PredictFacet` / `PredictChain` now judge an archive (they read its zip bytes as a `.pulse` header and refused `ENCODING_INVALID`); the memory estimate still charges `ArchiveBytes` (`TestArchivePredict_ReadsHeadersOnly`, `TestPredictRoots_ArchiveMatchesSingleFile`) · [U35](units/U35-predict-runtime-parity.md)
- [x] **#249** Recommend drafts every serving operator: `AGG_RATIO` (the planner omitted the `field` the wire requires — predict and runtime both refuse its absence, the operator only ignores its value), `AGG_WEIGHTED_MEAN` (no weight binding), `ATTR_REG_*` (required params the manifest does not declare); see `unmappedOnFixture` in `internal/guide/bind_test.go`. Done (U35 E7-S3): `AGG_RATIO` drafts its numerator as `field` (wire rule kept; description corrected); `ATTR_REG_FITTED` / `RESIDUAL` / `LEVERAGE` declare top-level `target` / `predictors` (+ penalty knobs) and draft like a regression; `AGG_WEIGHTED_MEAN` binds `weight_field` to the cohort's suggested weight, else drafts with a `weight_field` need (dropped when an instance default weight resolves); `unmappedOnFixture` keeps only the two intentional `compare_groups` rows. (`FEAT_BUCKETIZE` already drafted with a `boundaries` need.) · [U35](units/U35-predict-runtime-parity.md)

### G5 — MCP guidance layer
- [x] **#95** One MCP prompt per intent (extending `pulse-bootstrap` / `pulse-author-request`), plus the prompt gate · [U23](units/U23-guidance-mcp.md)
- [x] **#96** `pulse_examples_search {intent}` and question search; synonym table · [U23](units/U23-guidance-mcp.md)
- [x] **#97** `pulse_skills_list {intent}` · [U23](units/U23-guidance-mcp.md)
- [x] **#98** Intent-scoped manifest (`pulse_manifest {intent}`) · [U23](units/U23-guidance-mcp.md)
- [x] **#99** MCP tool descriptions lead with when to call the tool · [U23](units/U23-guidance-mcp.md)

#### Follow-ups from U23
- [ ] **#250** Fuzzy (typo-tolerant) search over examples and skills: a final `matchTier` after the literal and `KnownAs`/Sound tiers (the seam in `internal/examples/search.go`), edit distance scaled by length, ranked below exact and synonym hits · [U31](units/U31-guidance-guides.md)
- [ ] **#251** Align the story-example metadata spelling: #102 says `_meta.intent`, examples carry `_meta.intents` (a list); pick one and make the item text, the harness and the docs agree · [U31](units/U31-guidance-guides.md)
- [ ] **#252** `pulse examples search` has no `--intent` flag (the MCP tool and the facade `ExamplesSearchWith` take one); add it and a `docs/src/cli/flags.md` row · [U31](units/U31-guidance-guides.md)
- [x] **#253** Compose predict does not judge slot operators by name: an unknown or hidden operator in a Compose slot predicts valid, so the Explain doc's "predicts each slot" claim is inaccurate until per-slot predict runs. Done (U35 E3-S1): every Compose slot runs the Process-predict request body (`validateRequestBody`), located `details.request`; a hidden or unknown slot operator is refused as `Predict` refuses it · [U35](units/U35-predict-runtime-parity.md)
- [x] **#254** Chain predict does not validate stage label bindings. Done (U35 E3-S2): predict-chain runs the request body on every stage (labels, operators, accepted types, stage overlays), located `details.stage`; the runtime applies a stage ≥ 1 binding instead of dropping it · [U35](units/U35-predict-runtime-parity.md)
- [x] **#255** `PredictCompose` / `PredictFacet` / `PredictChain` and `pulse api predict-compose` / `-facet` / `-chain` ignore Strict and EchoRequest. Done (U35 E3-S4): Strict promotes every root warning at the root envelope (slot / stage-0 `PULSE_VECTOR_METRIC_UNSUITED` newly reported, located); EchoRequest echoes the runtime's normalized form (Compose: effective batch, slots defaults-resolved); `--strict` / `--echo-request` on the three leaves · [U35](units/U35-predict-runtime-parity.md)
- [x] **#256** Bare MCP `pulse_predict` returns no `errors` / `warnings`, so `valid: false` carries no reason. Done (U35 E3-S4): additive top-level `errors` / `warnings` on the bare arm; `PredictResult.Errors` / `Warnings` (`json:"-"`) set by `Pulse.Predict` · [U35](units/U35-predict-runtime-parity.md)
- [ ] **#257** `pulse_predict` description keeps the generic "send exactly one alternative root alone" sentence on profiles hiding compose, facet and process_chain (the per-root sentences are scrubbed) · [U32](units/U32-docs-audit.md)
- [x] **#258** `MAT_RELIABILITY` (Cronbach's alpha) needs `Purpose.KnownAs` aliases ("cronbach's alpha") when the operator lands, so the U23 synonym tier finds it · [U24](units/U24-matrix-operators.md)
- [x] **#259** The engine accepts `weight` on a facet overlay and silently ignores it (facets are never weighted); refuse it. Done (E5-S3): a set `weight` on a `FacetRequest.Overlays` spec is `PULSE_WEIGHT_UNSUPPORTED` at predict and runtime (`overlayhost.FacetSpecRefusal`); `null` / absent run · [U35](units/U35-predict-runtime-parity.md)
- [x] **#260** Bound `joins` field-name enums in the MCP input schemas cover only the left cohort; right-side and joined field names fall outside them (the description says so, predict resolves them); widen the enums from the join target's schema Done in U35 E6-S3: `pulse_inspect` takes `joins: [{right, as}]`; bind-on-inspect opens each target through the instance FS and `mcp.BindForInstanceWithJoins` widens the request-carrying tools' field enums with the joined names and binds `right_field` (`TestBind_JoinTarget*`, `TestRegister_BindOnInspectJoinTargets`). · [U35](units/U35-predict-runtime-parity.md)

### G6 — Guides
- [ ] **#100** "What can Pulse answer?" landing page · [U31](units/U31-guidance-guides.md)
- [ ] **#101** Question guides, one per intent: describe, compare groups, relationships, drivers, change over time, composition, benchmark, distribution, segment, measure construct, flows, data quality · [U31](units/U31-guidance-guides.md)
- [ ] **#102** Story examples: `_meta.intent` / `question` / `interpretation`, at least one per intent · [U31](units/U31-guidance-guides.md)
- [ ] **#103** Vector & matrix concept primers: correlation matrix, PCA, distance vs similarity, perceptual maps · [U31](units/U31-guidance-guides.md)

### G5 — Operator additions after the plan ([U39](units/U39-attr-code-in.md))
- [x] **#240** `ATTR_CODE_IN`: a row-local 0/1 attribute for categorical and unsigned-integer codes, so a top-box share of the whole base is one weighted mean on the fused crosstab path · [U39](units/U39-attr-code-in.md)
- [x] **#241** Predict `PULSE_ATTR_CODE_NOT_IN_DICTIONARY` (warning, error under `--strict`) and runtime-refusal parity · [U39](units/U39-attr-code-in.md)
- [x] **#242** Atomic skill `op-attr-code-in`, top-box example, manifest / purpose / feature rows · [U39](units/U39-attr-code-in.md)
- [x] **#243** End-to-end bit-exact acceptance suite (fused vs buffered vs hand-computed) · [U39](units/U39-attr-code-in.md)

#### Follow-ups from U39
- [ ] **#244** A general `target` / `predictors` refusal on every non-regression attribute (only `ATTR_CODE_IN` refuses them today) · [U39](units/U39-attr-code-in.md)
- [ ] **#245** Optional: move the `ATTR_CODE_IN` parse and accept rules into a neutral shared package instead of the predict copy pinned by `TestCodeIn_PredictRefusalsMatchRuntime` · [U39](units/U39-attr-code-in.md)

### Compose parameter sweeps — added after the plan ([U40](units/U40-compose-sweep.md); inspired by the [MMM worked example](fixtures/mmm-harbor-pine/README.md))
- [x] **#272** `ComposedRequest.Sweep`: one request body + named axes + a label pattern, expanded before execution into ordinary slots; `grid` (Cartesian, default) and `zip` (paired) modes; deterministic order and labels · [U40](units/U40-compose-sweep.md)
- [x] **#273** Substitution reuses the request-template engine (`$var` slot markers and `{{}}` string sugar); no second placeholder syntax · [U40](units/U40-compose-sweep.md)
- [x] **#274** Expanded slots count against the existing `Options.Limits.MaxComposeSlots` and are refused with `PULSE_LIMIT_EXCEEDED` before any slot runs; predict reports `axes`, `mode`, `expanded_count` · [U40](units/U40-compose-sweep.md)
- [x] **#275** `sweep.rank {by, order, top}` ranks finished slot responses by one scalar path; `ComposedResponse.Ranking` · [U40](units/U40-compose-sweep.md)
- [x] **#276** `PULSE_SWEEP_INVALID` and `PULSE_SWEEP_RANK_PATH` with `codeMetadata` and owners · [U40](units/U40-compose-sweep.md)
- [x] **#277** Companions: payload-schema golden, `update-demand.md` row, `features.go` capability row, `compose-requests` / `tool-compose` skills, an examples-library entry, CLAUDE.md Compose-envelope note, a `docs/src/library/` page · [U40](units/U40-compose-sweep.md)
- [x] **#278** Acceptance: the MMM fixture's 256-slot grid is reproduced by one sweep, and its ranking matches (a manual fixture run, not a Go test) · [U40](units/U40-compose-sweep.md)

#### Follow-ups from U40
- [ ] **#297** A later request references a sweep winner (`rank.top`) instead of typing the winning axis values by hand (worked-example gap 14) · [U40](units/U40-compose-sweep.md)
- [ ] **#298** Per-axis profile in the ranking output (minimum of the metric over the other axes, per axis value) for the decay identifiability reading (worked-example gap 15) · [U40](units/U40-compose-sweep.md)
- [x] **#300** A sweep rank cannot read an `omitempty` figure that is exactly 0 (e.g. `RegressionResult.residual_std_err`): the key vanishes, so the slot is excluded as `missing` instead of ranked first. Found in U40 (E2-S2) Done in U35 E6-S2: rank re-reads a missing final segment off the typed `Response` (`internal/sweep/rank_typed.go`); an `omitempty` numeric figure that is exactly 0 ranks as 0 when its applicability rule says the fit computed it (`residual_std_err` / `r2` / `adj_r2` on `REG_OLS` / `REG_BAYES_LINEAR`, `deviance` / `null_deviance` / `pseudo_r2` on `REG_GLM`); an uncomputed or unlisted figure and a nil entry stay missing. Wire `omitempty` unchanged. · [U35](units/U35-predict-runtime-parity.md)
- [x] **#299** An empty Compose (no `requests`, no `sweep`) predicts valid but the runtime refuses it with `SERVICE_VALIDATION`; make predict refuse it with the same code. Done (U35 E3-S1): same code and message as `Service.expandCompose`. Found in U40 (E1-S3) · [U35](units/U35-predict-runtime-parity.md)

### Derived cohorts — added after the plan ([U41](units/U41-derived-cohort.md))
- [x] **#279** NDJSON import matches schema fields to keys by name, not by position; mismatches are coded refusals (bug-class fix: an unsorted schema silently mislabels columns today) · [U41](units/U41-derived-cohort.md)
- [x] **#280** Materialise a request's derived columns into a new `.pulse` cohort, source schema carried over, derived columns appended as `f64` with generated descriptions · [U41](units/U41-derived-cohort.md)
- [x] **#281** Warn when a stream runs with projection and its output is destined for an import; the silent column drop is the bug · [U41](units/U41-derived-cohort.md)
- [x] **#282** Import inference for money-like columns: infer `f64`, or a `--float f64` flag, so sums do not drift under `f32` · [U41](units/U41-derived-cohort.md)
- [x] **#283** Fan-out of a window or feature list over a parameter within one request (the 23-entry adstock request becomes one declaration) · [U41](units/U41-derived-cohort.md)
- [x] **#284** `WIN_EWMA` `decay` parameter (`alpha = 1 - decay`) beside `alpha` · [U41](units/U41-derived-cohort.md)
- [x] **#285** Acceptance: materialise the example's feature cohort natively and fit on it, with no hand-written schema · [U41](units/U41-derived-cohort.md)

#### Follow-ups from U41
- [x] **#372** Bit-packed reads agree on legacy cohorts: export (`internal/io/export.go`) reads the whole byte of a `u4` / `packed_bool` field while the engine `RecordReader` family and the SPSS writer read the stored `BitPosition`, so a legacy cohort with a nonzero stored `BitPosition` exports different values than it processes. **Required resolution: proper reads — every reader (engine, export, SPSS, lookup) decodes legacy cohorts correctly and identically; no re-import.** Found in U41 (E1-S5) · [U35](units/U35-predict-runtime-parity.md)
- [ ] **#373** Legacy-offset cohort compatibility: shard cohesion and `rewriteShardDicts` refuse a cohort whose stored `ByteOffset` / `BitPosition` differ from the canonical layout (zero-offset explicit-schema cohorts from before U41 E1-S5, schema-file cohorts with a bit-packed field first from before E1-S3). **Required resolution: full cohort compatibility — such cohorts join and interoperate with new cohorts and shard archives without re-import (compare the type-derived layout, never stored offsets).** Found in U41 (E1-S5) · [U51](units/U51-archive-scale.md)
- [ ] **#374** `LateKeyRefuser` on the public `io` facade, so an embedder's keyed reader can arm the late-key refusal (`PULSE_IMPORT_SCHEMA_KEY_LATE`) like the built-in NDJSON and JSON-array readers. Found in U41 (E1-S1) · [U34](units/U34-extension-validation.md)
- [ ] **#375** `pulse import auto` (managed import) takes `--float`, `--schema` and `--ignore-extra` like `import <fmt>`; today they reach the managed import only through `ImportSpec` and MCP `pulse_import`. Found in U41 (E1-S2) · [U44](units/U44-strict-input.md)
- [ ] **#376** `session-bootstrap.md` routing hint for `--float f32` (skipped in U41: the skill is at its design budget). Found in U41 (E1-S2) · [U38](units/U38-skill-sync.md)
- [ ] **#377** Recommend drafts a materialise step: an intent route (e.g. a derived column a later model reads) that proposes `pulse_materialize` before the step-2 request. Found in U41 (E2-S2) · [U31](units/U31-guidance-guides.md)
- [x] **#378** A compose `sweep` body inside a stored request template: the renderer consumes the sweep's `{{axis}}` placeholders; extend the `for_each` scope rule in `internal/template/for_each.go` to `sweep.axes`. Found in U41 (E3-S4) Done in U35 E6-S2: an object's `sweep.axes` names scope over that sweep's `request`, `overlays` and `label` (never its `axes` / `mode` / `rank`), reusing the literal-name and `PULSE_TEMPLATE_AXIS_COLLISION` checks; fixture `internal/template/testdata/channel-sweep.json`. · [U35](units/U35-predict-runtime-parity.md)
- [x] **#379** Cohort-bound MCP schemas refuse a cohort field whose name contains a `{{ident}}` token on an item without `for_each` (the placeholder `else` branch); admit literal field names that match the pattern. Found in U41 (E3-S4) Done in U35 E6-S3: the `else` mirror is `not {pattern, not {enum}}`, so a literal enum member like `{{x}}` validates without `for_each` (`TestBind_LiteralPlaceholderFieldName`). · [U35](units/U35-predict-runtime-parity.md)
- [ ] **#380** A `for_each` item inside a compose `sweep` body (lexical scoping of item axes under sweep axes); refused for v1 with `nested_in_sweep`, can grow without a break. Found in U41 (interview) · [U40](units/U40-compose-sweep.md)

### Fit scoring — added after the plan ([U42](units/U42-fit-scoring.md))
- [x] **#286** `ATTR_REG_PREDICT`: apply a named fit to rows, in-request or from a stored model; no coefficient is ever retyped · [U42](units/U42-fit-scoring.md)
- [x] **#287** Error aggregators over a target and a prediction (`AGG_MAPE`, `AGG_RMSE`), weight-aware, with a train/holdout split by index · [U42](units/U42-fit-scoring.md)
- [x] **#288** Interval bounds pass-through: contribution and scenario calculations read the fit's `ci_lower` / `ci_upper` · [U42](units/U42-fit-scoring.md)
- [x] **#289** Acceptance: the example's holdout MAPE, contribution table with intervals and scenario slots run without any copied coefficient · [U42](units/U42-fit-scoring.md)

#### Follow-ups from U42
- [ ] **#390** Predict builds the aggregator capability table once instead of per call (`aggregatorComponentSchemaIndex` → `aggregatorCapabilities` → `withWeightAware`); the off-path predict allocation pin grew from 236 to 251 in U42. Found in U42 (E2-S1) · [U51](units/U51-archive-scale.md)
- [x] **#391** `internal/mergegate`'s built-in row-local list includes `ATTR_CODE_IN` and the `ATTR_SET_*` attributes (pre-existing omission). Found in U42 (E1-S2). Done (E5-S3): `ATTR_CODE_IN`, `ATTR_SET_HAS`, `ATTR_SET_POPCOUNT` join the list; `ATTR_SCALE_SCORE` / `ATTR_PROFILE_SIMILARITY` stay off (per-run Components / warning tallies the reducers do not fold) · [U35](units/U35-predict-runtime-parity.md)
- [ ] **#392** `pulse api compose --stream` ends with an optional terminal `{"fits":…}` NDJSON line, like the sweep's `ranking` line; today a stream carries no fit result (documented). Found in U42 (E1-S1) · [U40](units/U40-compose-sweep.md)
- [x] **#393** `FilterToFileWithRequest` and `SampleWithRequest` load `Request.models`; today a `coef()` there is refused as `PULSE_MODEL_UNKNOWN` (loud, never a wrong number). Found in U42 (E3-S2). Done in U35 E6-S1: `FilterToFileRequest.Models` loads models for the predicate's `coef()` (gated by `capability:models`), each file's content SHA-256 joining the dedup predicate hash. The `SampleWithRequest` half is moot: `SampleRequest` has no filter surface (Cohort, N, Labels), so nothing there can call `coef()`. · [U35](units/U35-predict-runtime-parity.md)
- [x] **#394** A direct test of model binding on the parallel-decode path (`parallel_reduce.go` → `BuildFiltersWithModels`), which needs an mmap'd OS file; the shard path is tested. Found in U42 (E2-S3). Done in U35 E6-S1: `TestCoefFilter_ParallelDecodeMatchesSerial` (`fit_bound_parallel_test.go`) runs a `coef()` `FILTER_EXPRESSION` through the parallel-decode reducer at 1/2/4 workers against the serial run. · [U35](units/U35-predict-runtime-parity.md)
- [ ] **#395** A grouper over explicit numeric ranges (e.g. 0–90 and 91–103), so a train/holdout split that is not a multiple of the `GROUP_RANGE` interval is one slot instead of a filter or two slots. Found in U42 (E2-S1) · [U49](units/U49-model-workbench.md)
- [ ] **#396** `tool-compose.md` back under its soft budget, and it mentions `fits` and `models` (today it is over budget and names neither). Found in U42 (E1-S1) · [U38](units/U38-skill-sync.md)

### Post-aggregation ratios — added after the plan ([U43](units/U43-post-aggregation-ratios.md))
- [x] **#290** A ratio output across aggregators (`AGG_RATIO`, or a post-aggregation formula slot) for ROI and marginal ROI; a zero or null denominator is `null` · [U43](units/U43-post-aggregation-ratios.md). Delivered by [U43](units/U43-post-aggregation-ratios.md) (E2-S1): no new operator; the existing `AGG_RATIO` already accepts attribute labels, now pinned by tests with an ROI recipe in its skill; no post-aggregation formula slot
- [x] **#291** Aggregate-derived scalars inside `ATTR_FORMULA` (a cohort-total reference), with the two-pass cost visible in predict streamability · [U43](units/U43-post-aggregation-ratios.md). Delivered by [U43](units/U43-post-aggregation-ratios.md) (E1): `total(expr)`, post-filter, cohort-wide, unweighted; predict reports the extra pass as `PULSE_ADVISORY_EXTRA_PASS`
- [x] **#292** Acceptance: the example's ROI, marginal ROI and scenario scale factors come out of Pulse with no hand-computed number · [U43](units/U43-post-aggregation-ratios.md). Delivered by [U43](units/U43-post-aggregation-ratios.md) (E2-S2): fixture `req/04`, `req/10` and `req/11` compute them natively

#### Follow-ups from U43
- [ ] **#398** A weight-aware `total()`: `ATTR_FORMULA` is not weight-aware today, so `total(expr)` is always the unweighted cohort sum. Found in U43 (E1) · [U47](units/U47-survey-tables.md)
- [ ] **#399** Merge `total()` partial sums across partitions so shard and parallel-decode runs stay parallel; today a two-pass `total()` request falls back to the sequential path there. Found in U43 (E1) · [U51](units/U51-archive-scale.md)
- [x] **#400** Predict-compose does not run per-slot `Request.Overlays` validation: a slot-level `OVERLAY_FORMULA` on a non-crosstab host is refused at runtime (`PULSE_OVERLAY_KIND_UNKNOWN`) but predict-compose is silent (pre-existing predict/runtime gap) (the runtime code is now `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`). Done (U35 E3-S1): slot overlays go through `ValidateOverlays`, located `details.request`. Found in U43 (E2-S2) · [U35](units/U35-predict-runtime-parity.md)

### Self-sufficiency for v1 — added after the plan, from the [showcase examples](https://github.com/frankbardon/pulse-casebook/tree/main/showcase) (U44–U52)
#### Strict input ([U44](units/U44-strict-input.md))
- [x] **#301** Unknown keys are refused at every request root and nested spec (sort entries, slot `params`, grouper, overlay and test params, Compose / Chain / Facet roots) in predict and process, with a did-you-mean suggestion; the CLI path matches the MCP strict decode (`PULSE_REQUEST_UNKNOWN_FIELD`). Repros: `"filters"`, `sort[].direction`, `GROUP_DATE params.unit` · [U44](units/U44-strict-input.md)
- [x] **#302** Synth specs decode strictly: unknown spec keys and distribution params are refused (`exponential {rate}` today silently uses `lambda: 1`); `correlations[]` accepts `rho` as the skill documents (or the skill is corrected), never a silent zero · [U44](units/U44-strict-input.md)
- [x] **#303** Every CLI leaf exits non-zero when the envelope carries a fatal error, under `--json` too, as `docs/src/cli` documents; the envelope itself is unchanged · [U44](units/U44-strict-input.md)
- [ ] **#304** Imports never drop rows silently: a partial import puts a coded warning with the dropped-row count and the first failure into `warnings` (e.g. a blank cell on a non-nullable `set_*` column), and `--strict` makes it an error · [U44](units/U44-strict-input.md)
- [ ] **#305** Requested knobs are honoured and echoed: `bootstrap_iters` is used as given (`<= 0` means the default 1,000, not a floor), and `RegressionResult` reports replicates requested and succeeded · [U44](units/U44-strict-input.md)
- [ ] **#306** Label bindings apply to crosstab axis keys and pairwise-overlay pair keys, or the request draws a coded `PULSE_LABEL_NOT_APPLIED` warning; never accepted-and-ignored · [U44](units/U44-strict-input.md)
- [ ] **#307** Overlay-layer warnings follow the `{code, message, details}` contract (today `Code` / `Message` / `Details`) and runtime reason strings carry no predecessor prefix (`ORBIT_STATS_SKIP_POOLED_SE_INVALID`); `TestNoOrbitPrefix*` covers runtime strings. Fix the shipped examples that relied on ignored input: `overlays/40_crosstab_pairwise_prop_z.json` (empty layer) and `crosstab/11_date_grouper_axis.json` (`unit`) · [U44](units/U44-strict-input.md) — *partial (E1-S4): reason strings renamed `PULSE_STATS_SKIP_*`, `TestNoOrbitPrefix*` scan every non-test Go string literal, `40_crosstab_pairwise_prop_z.json` normalized (non-empty layer, asserted) and `11_date_grouper_axis.json` fixed (E1-S2); the `{code, message, details}` json tags remain*
- [ ] **#308** Path and flag hygiene: a relative `Options.DataDir` is absolutised at `New` (or refused there), `api lookup -i` overrides a request file's cohort as its help states, and `cohort inspect` accepts `-i` like every other leaf · [U44](units/U44-strict-input.md)
- [ ] **#309** Warnings that matter at run time are not predict-only: the process envelope repeats `PULSE_FEAT_TARGET_LEAKAGE_RISK`, and `pulse explain` carries predict's warnings · [U44](units/U44-strict-input.md)
- [ ] **#310** Output consistency: `cohort inspect` shows nullability and `decimal128` precision/scale; `sample` / `lookup` rows print `date` / `datetime` as calendar strings; Compose responses echo each slot's `label`; import / export / shard `--json` data uses snake_case keys and type names (wire decision below) · [U44](units/U44-strict-input.md)
- [ ] **#311** Acceptance: a table test misspells a key in every slot family and gets a coded refusal; all six showcase examples run under `--strict` with nothing ignored, and their scripts drop the `.errors | length` checks in favour of exit status · [U44](units/U44-strict-input.md) — *partial (E1-S4): in-pulse table test `TestStrictInput_EveryFamilyAndRootRefusesAMisspelledKey` (every derived slot family × every root, CLI predict + process and MCP predict + runtime, synth spec, every template target); the showcase-example run under `--strict` and the script changes remain (casebook follow-up)*

#### Binary flags and decimals ([U45](units/U45-binary-and-decimal.md))
- [ ] **#312** `packed_bool` is accepted as a two-level category wherever one is: `TEST_PROP_Z`, `TEST_CHISQ` (rows or cols), `FEAT_TRAIN_TEST_SPLIT` `stratify`, and every `split_by`; skills and manifest `accepts_types` match the runtime · [U45](units/U45-binary-and-decimal.md)
- [x] **#313** Type-refusal codes say what they mean (a "must be categorical" refusal is not `PULSE_TEST_FIELD_NOT_NUMERIC`), messages do not repeat words ("field field"), and predict and runtime raise the same code for each refusal (feeds [U35](units/U35-predict-runtime-parity.md) #198; delivered by U35 E2-S3) · [U35](units/U35-predict-runtime-parity.md)
- [ ] **#314** `decimal128` coverage: `AGG_NULL_COUNT` reads the bitmap and needs no decimal math; `AGG_COUNT` returns an integer; a sum over an all-null group is `null`; `ATTR_FORMULA` can divide and compare decimals (decimal-aware operators or a working `float()`), and predict refuses what process would · [U45](units/U45-binary-and-decimal.md)
- [ ] **#315** Formula diagnostics: a compile failure carries expr-lang's reason in the message, and predict compiles every formula so the failure surfaces before the run · [U45](units/U45-binary-and-decimal.md)
- [ ] **#316** `ATTR_FORMULA` may return null, and the formula environment gains `exp`, `log`, `log1p`, `sqrt` and calendar helpers (`weekday`, `month_index`, `date_trunc`) evaluated through `internal/temporal` · [U45](units/U45-binary-and-decimal.md)
- [ ] **#317** Acceptance: the credit example drops its `outcome` twin and the `2.718… **` idiom; the A/B example stores binary metrics as `packed_bool`; the privacy example runs `TEST_CHISQ` on `churned` directly; the retail retention flags use a calendar helper instead of twelve hard-coded epoch-day ranges · [U45](units/U45-binary-and-decimal.md)

#### Grouped analysis ([U46](units/U46-grouped-analysis.md))
- [x] **#318** Composite group keys: `groups` with N ≥ 2 forms the cartesian key product in streaming and buffered grouped Process, with grouped Components and predict cardinality; delivers the "execute" half of #200 · [U54](units/U54-multi-grouper.md)
- [x] **#421** Every reducer and consumer honours composite keys: parallel decode, shard archive, Compose slots, ProcessChain stage typing, `sort`, `return` shaping, windows / post-tests partitioning, grouped matrices and `EstimateGroups` / `max_groups` (product of all groupers' cardinalities) · [U54](units/U54-multi-grouper.md)
- [x] **#422** Regression gate: 2 and 3 groupers (category × category, range × category, date × category; empty-combination and null-key cases) checked against hand-computed counts in every execution mode, plus a predict/runtime parity row; `grouper-design.md` and the `op-group-*` skills describe exactly what runs · [U54](units/U54-multi-grouper.md)
- [x] **#319** Series overlays take a `field` naming the aggregation they score (today always `aggregations[0]`, undocumented), and a Compose series host with several aggregations keys on group columns only; `predict-compose` refuses what the runtime would · [U46](units/U46-grouped-analysis.md)
- [x] **#320** `partition_by` on series overlays (rolling z-score and index, delta and index vs prior, share of total) over the leading group keys, mirroring windows. Interim (U54 follow-up): over two or more `groups` the prior, rolling, baseline and sibling kinds are refused `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE` (`details.groupers`) on predict and runtime, as `OVERLAY_YOY` is (#429); #320 lifts that refusal. Share of total is order-independent (each row over the grand total of every composite row) and is not refused — decide whether a partitioned share (each row over its outer bucket's total) is still wanted here. · [U46](units/U46-grouped-analysis.md)
- [x] **#321** Grouped tests: `tests[]` run once per group when `groups` is set (or via an explicit `within`), with every group's p-value in one multiplicity family; until then predict warns when tests and groups coexist · [U46](units/U46-grouped-analysis.md)
- [x] **#322** The series overlay payload matches its skill (`entries[i].value` present, or the skill corrected) · [U46](units/U46-grouped-analysis.md)
- [x] **#323** Acceptance (rewritten as agreed): the brand tracker's five per-KPI wave-delta slots become one request; the A/B segment readout's 12 slots become four grouped requests with one correction family; the multi-time-zone warehouse x day screen is one partitioned request (its per-warehouse local-day boundary needs per-row time zones, [U50](units/U50-local-time.md)); the retail share-of-total needs no aggregation reorder; the privacy real-vs-twin comparison is filed as #433. The showcases live in `pulse-casebook` (PR #2) [U46](units/U46-grouped-analysis.md)
- [x] **#428** Crosstab refits a nested data-dependent grouper per outer bucket: `Processor.PartitionByAxis` calls `GROUP_QUANTILE`'s `Group` on each outer bucket's records (only `GROUP_KMEANS` is fitted once over the whole set via `fitWholeSetAxes`), so a crosstab axis `[region, quartile]` bins quartiles within each region while plain `groups` (U54) fit them once over the filtered set. Decide one rule (or expose both, e.g. a `within` knob) and align crosstab, fused crosstab and plain groups. Found in U54 (E1-S1, confirmed E2-S2) · [U46](units/U46-grouped-analysis.md)
- [x] **#429** `OVERLAY_YOY` over two or more `groups`: the series host is the composite rows, not one date axis, so YoY is refused (`PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`, `details.groupers`) on predict and runtime. Give it per-partition semantics (prior-year lookup on the `GROUP_DATE` position within each combination of the other keys, sharing #320's `partition_by`) and lift the refusal. Deferred from U54 (E2-S2) · [U46](units/U46-grouped-analysis.md)
- [x] **#430** Chain stage typing names a grouped stage's key column `g.Field`, not `vectors.GroupKeyField` (`chainPredictedOutputFields` / `synthChainSchema` in `internal/descriptor/chain.go`, `ChainOutputSchema` in `internal/processing/chain.go`); only `GROUP_KMEANS` is affected and chains refuse it today, so nothing is wrong yet; derive the key column from the shared helper before chains accept it. Found in U54 (E2-S3) · [U46](units/U46-grouped-analysis.md)

#### Follow-ups from U54 (series-overlay refusal)
- [x] **#431** `OVERLAY_INDEX_VS_ROLLING_MEAN` / `OVERLAY_ZSCORE_VS_ROLLING` with `params.window: 1` answer NaN from the second present point on: `rollingCarrier`'s eviction divides by `count - 1 == 0` (`internal/processing/overlay_index_vs_rolling_mean.go`), while predict accepts window 1 (`validateOverlayRollingWindowParam` checks only `> 0`). Fix the window-1 eviction (or refuse window 1 at predict and runtime alike). Found in the U54 series-overlay refusal follow-up — FIXED (E4-S2): window-1 eviction resets to the empty sample (window 1 of the index = `OVERLAY_INDEX_VS_PRIOR`); `OVERLAY_ZSCORE_VS_ROLLING` refuses `window < 2` on both sides (`overlayhost.RollingWindowFloorRefusal`) · [U35](units/U35-predict-runtime-parity.md)
- [x] **#432** `OVERLAY_FORMULA` on a grouped Process (SERIES) host passes predict — the SERIES namespace `value` / `total` / `prior` / `baseline` validates — but the runtime refuses it `PULSE_OVERLAY_KIND_UNKNOWN`: `seriesOverlayHandlers` has no FORMULA entry. Either wire a SERIES FORMULA handler or refuse it at predict; a wired handler's `prior` is a lag-1 read, so it needs the multi-grouper refusal (`refuseCompositeSeriesHost`) the prior / rolling kinds carry. The Compose-predict side is #400. Found in the U54 series-overlay refusal follow-up — BUILT (E4-S2): the refusal half was already consistent (U30 `ScalarSpecRefusal` / shape refusal); the SERIES handler now runs (`processing.applyFormulaSeriesWith`, shared gate `formulaident.RequestSpecRefusal`); `refuseCompositeSeriesHost` no longer exists (U54), so a composite host runs FORMULA per partition like the prior / rolling kinds · [U35](units/U35-predict-runtime-parity.md)

#### Follow-ups from U46 (grouped analysis)
- [ ] **#433** One request comparing a real cohort against its synthetic twin (the privacy-safe-sharing showcase's 14 slots): `groups` cannot span two cohorts, so the comparison still needs a slot per cohort side · unowned
- [ ] **#434** Per-bucket `Components` under `fit: "within"` still describe the whole-set fit of a data-dependent grouper, not each bucket's refit (U46 E4-S1 / E4-S5 decision) · unowned
- [ ] **#435** `skills/op-test-trend.md` is about 30% over the `op-*` soft body budget; bring it within before #161 flips `TestSkillTokenBudget` to hard-failing. Found in U46 (E3-S2) · [U32](units/U32-docs-audit.md)
- [x] **#436** predict-compose / predict-chain report an unknown overlay `field` on both compared stages as two errors where the runtime raises one; align the count. Done (U35 E3-S2): one error on both sides — compose stops at the first slot (reference, then targets), chain at the first stage (target, then ref). Found in U46 (E1-S4) · [U35](units/U35-predict-runtime-parity.md)
- [x] **#437** `MaxEstimatedMemory` ignores the per-group state a grouped tier-1 test holds (tests x groups), though `MaxGroups` grades the product; decide whether the memory estimate should count it. Found in U46 (E3-S1). Done (U35 E7-S2): `MemoryInputs.TestInstances` / `TestLevels` / `TestValues` — one state per test per bucket, its split / contingency levels, and every value a non-streamable (rank-based) test keeps (records x fan-out x columns); calibrated grouped `TEST_T` 1.46x, grouped `TEST_KRUSKAL_WALLIS` 1.48x · [U35](units/U35-predict-runtime-parity.md)
- [ ] **#438** Process SERIES `OVERLAY_FORMULA` is refused (`PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`, runs on Compose only); wiring the series / scalar binders on a grouped Process host, and the partition semantics of `prior` / `total` over two or more groups, needs an owner decision. Found in U46 (E4-S15) · unowned
- [x] **#439** An ungrouped (SCALAR) Process host: predict accepts overlay kinds (e.g. `OVERLAY_FORMULA`) the runtime silently returns no layer for; refuse identically on both sides. Found in U46 (E4-S15); done in U30 E7-S5 (`overlayhost.ScalarSpecRefusal`, every Process kind refused on both sides) · [U35](units/U35-predict-runtime-parity.md)
- [x] **#440** Request-host MATRIX `OVERLAY_FORMULA` with an unknown identifier: predict `PULSE_OVERLAY_FORMULA_INVALID_IDENT` vs runtime `PULSE_OVERLAY_FORMULA_PARSE_ERROR`; reuse `internal/formulaident` on the Request host. Found in U46 (E4-S15) · [U35](units/U35-predict-runtime-parity.md)
- [ ] **#441** Compose `OVERLAY_FORMULA` over SERIES slots scores `Aggregations[0]`; it takes no `params.field` to pick the measure. Found in U46 (E4-S15) · unowned
- [x] **#442** A Compose overlay spec with no targets passes predict-compose but the runtime refuses `PULSE_OVERLAY_TARGET_UNKNOWN` (every single-target kind); refuse on predict. Found in U46 (E4-S15); done in U30 E7-S4: `ComposeSlotRefusals` refuses an empty `targets` on predict-compose and the runtime alike · [U35](units/U35-predict-runtime-parity.md)
- [ ] **#443** Compose two-sample T/Z handlers (`OVERLAY_T_VS_REF` / `_Z_VS_REF` / `_T_CELL` / `_Z_CELL`) score only `Targets[0]`; extra targets are silently ignored. Decide whether to refuse multi-target specs. Found in U46 (E4-S16) · unowned
- [ ] **#444** The Compose T/Z series arm treats a scored `AGG_VARIANCE` / `AGG_STDDEV` value as the side's mean; consider refusing non-mean scalars. Found in U46 (E4-S16) · unowned
- [ ] **#445** Compose crosstab slots and whole-chain crosstab stages over `AGG_MODE`-over-categorical cells still score dictionary codes; needs a per-kind value-vs-moment audit (`OVERLAY_T_CELL` / `_Z_CELL` read Welford triples). Found in U46 (E4-S17) · unowned
- [ ] **#446** Non-overlay use of a code column: `AGG_SUM` / `AGG_AVERAGE` etc. over a categorical field (stage 0), or over a forwarded code column (a later chain stage), compute over dictionary indexes; owner decision whether to refuse numeric aggregators over code columns. Found in U46 (E4-S17) · unowned
- [ ] **#447** Extension MATRIX overlay kinds are not judged by the categorical-code cell rule: an extension kind declares no reads-cell-value metadata (`overlayhost.ReadsCellValue` covers built-ins only). Found in U46 (E4-S17) · unowned
- [x] **#448** Moment pairwise kinds (`OVERLAY_PAIRWISE_WELCH_T` / `_TWO_MEANS_Z` / `_WEIGHTED_TWO_MEANS_Z`) over an `AGG_MODE` cell are refused only by the runtime cell-shape rule; predict may be silent. Found in U46 (E4-S17) · [U35](units/U35-predict-runtime-parity.md)

#### Survey tables ([U47](units/U47-survey-tables.md))
- [ ] **#324** Weighted pairwise proportion tests on a normalised crosstab use the column base: n comes from the column margin's effective n (or is derived from the normalise denominator automatically); a reference-oracle case pins it · [U47](units/U47-survey-tables.md)
- [ ] **#325** `GROUP_SET_PER_ELEMENT` axis margins follow the cell-admission rule (a row whose set is null reaches no cell and no margin), or the rule is stated and an opt-in provided; column % is "of those asked" · [U47](units/U47-survey-tables.md)
- [ ] **#326** Banner tables: a crosstab takes several independent column groups side by side (Total | Gender | Age | Region) and several measures as stub rows, in one request; a Total column needs no constant field · [U47](units/U47-survey-tables.md)
- [ ] **#327** Significance letters: a presentation option on pairwise overlays that assigns column letters and marks, per cell, which columns it is significantly above, at a chosen alpha and correction · [U47](units/U47-survey-tables.md)
- [ ] **#328** A tested wave-over-wave change: an inferential prior-period overlay on grouped Process (or a crosstab with no column axis), with p-values in the multiplicity family · [U47](units/U47-survey-tables.md)
- [ ] **#329** Weighting: rim (raking) and cell weighting to marginal targets, producing a weight column and an efficiency report (Kish n_eff, design effect, weight range), from the library, CLI and MCP · [U47](units/U47-survey-tables.md)
- [ ] **#330** SPSS export carries value labels from label tables and categorical dictionaries; `overlay-system.md` matches the runtime on weighted inferential overlays · [U47](units/U47-survey-tables.md)
- [ ] **#331** Acceptance: the brand tracker's banner becomes one request per table with letters; its weights are computed by Pulse from census targets instead of inside the synth spec; its SPSS hand-off carries value labels · [U47](units/U47-survey-tables.md)

#### Experiment readout ([U48](units/U48-experiment-readout.md))
- [ ] **#332** Goodness-of-fit chi-square against supplied expected proportions (`TEST_CHISQ` `params.expected` or a dedicated GOF test) and a one-sample `TEST_PROP_Z` (`p0`), so a sample-ratio check is one test · [U48](units/U48-experiment-readout.md)
- [ ] **#333** `params.reference` on two-group tests (`TEST_PROP_Z`, `TEST_WELCH`, `TEST_T`, `TEST_MANN_WHITNEY_U`, `TEST_Z_TWO_SAMPLE`) so `diff` and its interval read treatment − reference · [U48](units/U48-experiment-readout.md)
- [ ] **#334** Regression results carry coefficient confidence intervals at the request alpha, and regression p-values can join a `multiplicity` family · [U48](units/U48-experiment-readout.md)
- [ ] **#335** Covariate-adjusted comparisons in one call: a `covariate` on the two-group mean and proportion tests (CUPED), with the adjusted estimate, interval and variance reduction reported · [U48](units/U48-experiment-readout.md)
- [ ] **#336** Predict accuracy for test-only and regression-only requests (streamability and size), and `predict-compose` reports per-slot streamability, p-value counts and the cross-slot multiplicity advisory · [U48](units/U48-experiment-readout.md)
- [ ] **#337** Polish: `TEST_CHISQ` labels ordered consistently with the other tests; the Mann-Whitney ties warning says the test mostly compares the share of tied values · [U48](units/U48-experiment-readout.md)
- [ ] **#338** Acceptance: the A/B readout drops the design cohort and facet SRM workaround, the two-step CUPED and every sign flip · [U48](units/U48-experiment-readout.md)

#### Model workbench ([U49](units/U49-model-workbench.md))
- [ ] **#339** `REG_GLM` fits robustly: per-row starting values (`mustart`) and step-halving on deviance increase; a distinct coded error for divergence or separation instead of `RANK_DEFICIENT`; an oracle case on the showcase's `default ~ collections_flag` against R · [U49](units/U49-model-workbench.md)
- [ ] **#340** A failing regression slot reports its own error without discarding the request's other results (decision below) · [U49](units/U49-model-workbench.md)
- [ ] **#341** Penalised GLM (`l2`, and `l1` / `elasticnet`) for the binomial and Poisson families · [U49](units/U49-model-workbench.md)
- [ ] **#342** Discrimination metrics: a weight-aware AUC / Gini aggregator over a score and a binary outcome, and a share-of-partition window for cumulative capture curves · [U49](units/U49-model-workbench.md)
- [ ] **#343** A grouped (k-fold or delete-d) jackknife, so resampled standard errors are practical at tens of thousands of rows · [U49](units/U49-model-workbench.md)
- [ ] **#344** Leakage-safe encoding in one parameter: `FEAT_TARGET_ENCODE` `fit_on` (category means from the training rows, applied to all) or out-of-fold encoding · [U49](units/U49-model-workbench.md)
- [x] **#345** `ATTR_REG_PREDICT` (U42) scores GLM families on the response scale and can fit on a subset (training rows) while scoring all rows · [U49](units/U49-model-workbench.md). Delivered by [U42](units/U42-fit-scoring.md) (E1-S2): `scale: response` (the default) applies the inverse link, and a compose `fits` entry's own filter picks the training rows while a slot scores every row
- [ ] **#346** `GROUP_QUANTILE` and `GROUP_RANGE` keys sort in bucket order (D01…D10, numeric ranges), not lexicographically · [U49](units/U49-model-workbench.md)
- [ ] **#347** Acceptance: the credit example demonstrates leakage with the binary `collections_flag`, reports AUC, uses train-only target encoding, and scores the holdout with no transcribed coefficient · [U49](units/U49-model-workbench.md)

#### Local time ([U50](units/U50-local-time.md))
- [ ] **#348** Per-row zones: zone-capable slots accept `tz: {"field": "<categorical of IANA names>"}`, and import accepts a per-row source zone column; refusals for an unknown zone name are coded per row · [U50](units/U50-local-time.md)
- [ ] **#349** Calendar parts: `ATTR_DATE_PART` `weekday` (and ISO weekday number), a `GROUP_DATE` `hour_of_day` component, and `day_of_week` keys in Monday→Sunday order that honour `include` · [U50](units/U50-local-time.md)
- [ ] **#350** `GROUP_DATE_RANGES` returns ranges in supplied order in grouped Process and crosstab, as documented · [U50](units/U50-local-time.md)
- [ ] **#351** Rolling overlays take `min_periods` (default: the window), so the z-score and index siblings warm up alike · [U50](units/U50-local-time.md)
- [ ] **#352** Docs: what `--dst-policy earlier` / `later` do to a nonexistent local time (earlier lands on the later wall clock), and that `TEST_TREND` orders string keys lexicographically · [U50](units/U50-local-time.md)
- [ ] **#353** Acceptance: the multi-time-zone example's six-slot Compose views become single requests, including one warehouse × local-hour matrix, and the anomaly screen excludes local Saturdays in Pulse · [U50](units/U50-local-time.md)
- [ ] **#452** `ATTR_PREVIOUS_STATE`: a per-subject previous-state attribute. For each record it gives the state the same subject (a key field) held at its previous time point (ordered by a date / datetime field, read in the row's local time), or null for the subject's first record. A long panel then feeds a from-state × to-state crosstab (and `OVERLAY_MARKOV` / `OVERLAY_SYMMETRY` / `OVERLAY_RETENTION`) without pre-pivoting to wide `state_t1` / `state_t2` columns. It would be the fourth `flows` declarer. Handed on from U30 (E7-S2, #449) · [U50](units/U50-local-time.md)

#### Archive scale ([U51](units/U51-archive-scale.md))
- [ ] **#354** Archives open through `io.ReaderAt` and the zip central directory: `inspect`, `predict`, `CountRecords`, `shard list` and anchor reads touch only headers and the central directory (O(shards)), as CLAUDE.md promises · [U51](units/U51-archive-scale.md)
- [ ] **#355** `shard add` and `shard create` stream entries (copy existing entries by section, append, rewrite the central directory) so memory and time are O(new shard), keeping atomic replace and crash recovery · [U51](units/U51-archive-scale.md)
- [ ] **#356** The shard-parallel arm reads shards by section and uses projected decode; worker scaling is measured and documented · [U51](units/U51-archive-scale.md)
- [ ] **#357** Serial and shard-parallel results for `AGG_SUM` and the other mergeable aggregators are byte-equal as documented (ordered or compensated reduction), or the documented promise is corrected · [U51](units/U51-archive-scale.md)
- [ ] **#358** High-cardinality grouping memory: a per-group overhead budget, and shard workers merge into one group map instead of each holding a full copy · [U51](units/U51-archive-scale.md)
- [ ] **#359** Point lookup over archives (per-shard sidecars or an archive-level index) and a `cohort filter` option that flattens an archive into one single-file cohort · [U51](units/U51-archive-scale.md)
- [ ] **#360** CLI and predict parity: `--shard-workers` / `--decode-workers` flags; `predict-chain` accepts archives; the streaming skill states that grouped output arrives at the end · [U51](units/U51-archive-scale.md)
- [ ] **#361** Acceptance: the retail example runs at 50M rows on 4 vCPU / 16 GB without `GOMEMLIMIT`; archive header operations finish in under a second; customer lookups work on the archive; the customer chains run on every customer, not a 1-in-10 panel; the bench program is replaced by CLI flags · [U51](units/U51-archive-scale.md)
- [ ] **#455** Process runs open a shard archive by section (`io.ReaderAt` + the zip central directory, as #354 does for header operations) instead of `afero.ReadFile` of the whole archive in `Service.Open`, so a run's peak heap no longer holds the archive bytes; then drop the archive byte-length term predict's `MaxEstimatedMemory` charges since U30 E8-S3 / E8-S4 (`archiveTerms`, anchor-opened cohorts included) and re-take the `ShardWorkers` sweep in `docs/internals/BENCHMARKS_BASELINE.md`. Owner decision 2026-10-14 (U30 E8-S3 fixed only the accounting) · [U51](units/U51-archive-scale.md)

#### Synth realism ([U52](units/U52-synth-realism.md))
- [ ] **#362** A synth `datetime` type with zone-aware generation, sharing `internal/temporal` · [U52](units/U52-synth-realism.md)
- [ ] **#363** Time-dependent effects: trends and seasonality over a date field and windowed effects (a planted incident between two dates), as distributions or rule functions · [U52](units/U52-synth-realism.md)
- [ ] **#364** Seeded random draws inside rules (`uniform()`, `bernoulli(p)`) so an outcome drawn from a model needs no helper columns, plus helper fields that are not written to the output · [U52](units/U52-synth-realism.md)
- [ ] **#365** Spec idioms and warnings: a declared, rule-assigned categorical needs no sampler; the `owns_nulls` warning fires in the right direction · [U52](units/U52-synth-realism.md)
- [ ] **#366** Profile capture: numeric predictors in `--fit-models`; conditional-pair arbitration by association strength (Cramér's V / adjusted η²) or author choice; dependencies between options of one set field · [U52](units/U52-synth-realism.md)
- [ ] **#367** Fidelity report: a source-versus-synthetic utility section (per-level target rates for captured categoricals, or a refit of a supplied model on both), effect-size thresholds beside p-values for marginals, scoring for `packed_bool` / `u4` / `date` / `set_*` marginals, and a note when a rule causes a flag · [U52](units/U52-synth-realism.md)
- [ ] **#368** Sharing workflow: `--synthetic-only` output from the profile path, `constraints[]` accepted on the profile path, and disclosure metrics (exact-match rate, distance to closest record) beside the fidelity report; differential privacy is a decision below · [U52](units/U52-synth-realism.md)
- [ ] **#369** Docs: join keys never match on null (`join-design.md`), and synth rules bind `set_*` as a map while query formulas bind a list, cross-linked · [U52](units/U52-synth-realism.md)
- [ ] **#370** Acceptance: the multi-time-zone and credit datasets regenerate from synth specs alone with no helper columns; the privacy twin keeps the source's churn drivers (pseudo-R² gap closed materially, TV-without-broadband at zero) and ships with a disclosure report · [U52](units/U52-synth-realism.md)
- [x] **#450** Vector synthesis: generate a `vec_f32` / `vec_f64` field (dim, kind and scale range honoured; per-element marginals plus the inter-element correlation captured from a profile), and drop the `PULSE_SYNTH_VECTOR_UNSUPPORTED` refusal on `from-schema`, `from-profile`, `profile create` and augment. Found in U29 (E3-S4) · [U52](units/U52-synth-realism.md) (landed in [U29](units/U29-vector-field-types.md); the refusal code was removed, never released)


### Custom regressions — added after the plan ([U53](units/U53-custom-regressions.md); design in [api-and-release 04](v1.0.0-api-and-release/04-model-extensions.md))
- [ ] **#381** `Extensions.Regressions` and public `extend` regression interfaces: a streaming form (row update, merge, finalize) and a buffered form (fit over all rows, may iterate), declared at registration the way `Streamable()` is; names are `REG_<NAMESPACE>_<NAME>`, and reserved namespaces and built-in collisions are refused at `pulse.New` · [U53](units/U53-custom-regressions.md)
- [ ] **#382** Wire: `RegressionSpec.Params` (raw JSON, decoded strictly against the registration's declared params schema) and an operator outputs map on `RegressionResult` keyed by a declared schema in the manifest; payload-schema golden regenerated; additive, `format_version` stays `"1.1"` · [U53](units/U53-custom-regressions.md)
- [ ] **#383** Parity: custom regressions reach predict (params validation, streamability), manifest, payload schema, MCP, feature profiles (`Since`, dependency edges), guided analysis (`Purpose` / `Interpretation` hook), weighting (weight-aware or a coded refusal) and multiplicity (p-values join a family); the `TestExtensions_*` parity suites cover the new category · [U53](units/U53-custom-regressions.md)
- [ ] **#384** Composition: a registration may supply a row scorer so `ATTR_REG_FITTED`, `ATTR_REG_RESIDUAL`, `ATTR_REG_PREDICT` (U42) and compose-sweep ranking work on custom fits; the `resample` and `selection` modifiers work with buffered custom regressions that can refit, and are refused with a coded error otherwise · [U53](units/U53-custom-regressions.md)
- [ ] **#385** A post-fit extension slot for computations over fitted results (response-curve evaluation, budget allocation under constraints), or a recorded decision to ship it in v1.x (it is additive either way) · [U53](units/U53-custom-regressions.md)
- [ ] **#386** `REG_BAYES_LINEAR` `prior_precision` accepts a per-coefficient vector (intercept first, as `prior_mu`), the scalar form unchanged; `REG_OLS` takes non-negativity bounds on a named predictor subset; oracle cases against R for both · [U53](units/U53-custom-regressions.md)
- [ ] **#387** Media-response primitives: Weibull adstock (CDF and PDF kernels, normalised, beside the existing geometric `WIN_EWMA`) as a window option or `WIN_ADSTOCK`, and a `hill(x, half_saturation, shape)` expression function; null handling matches the other windows · [U53](units/U53-custom-regressions.md)
- [ ] **#388** Recipe and reference extension: `docs/src/internals/adding-regression.md`, and a test-only buffered extension (a Hill nonlinear least-squares fit through `gonum/optimize`) proving iterative fits work through the public contract · [U53](units/U53-custom-regressions.md)
- [ ] **#389** Acceptance: the MMM fixture fits Hill saturation through a registered custom regression, with non-negative media coefficients and an experiment-centred prior on one channel, and every step stays pure Pulse plus that registration · [U53](units/U53-custom-regressions.md)

### Owner-unit backlog from the worked example (filed under existing units)
- [ ] **#293** `REG_OLS` `penalty` with an `unpenalized: [..]` predictor list, so ridge can leave a predictor subset unshrunk; add an oracle case · [U36](units/U36-reference-oracles.md)
- [ ] **#294** Window lag default `null` and post-tests that honour null (`WIN_LAG` `default: 0` biases lag-1 autocorrelation) · [U36](units/U36-reference-oracles.md)
- [x] **#295** Clustered and HAC covariance options on `REG_OLS` `Vcov` · [U25](units/U25-multivariate-tests-segmentation.md)
- [x] **#296** Summary of a matrix across groups (mean and range per cell over a grouped matrix result) · [U28](units/U28-matrix-overlays.md)
- [ ] **#371** Docs from the showcase examples: `--source-tz` accepts fixed `+HH:MM` offsets while request `time_zone` refuses them (state the asymmetry); projection's benefit is schema-dependent (the retail example measured no gain on 15 fields of 53 bytes); the brand-tracker, A/B and other showcase READMEs are re-verified after the units that simplify them land · [U32](units/U32-docs-audit.md)

---

## 8. Response shaping ([response-shaping 00](v1.0.0-response-shaping/00-design.md))

- [x] **#104** `Request.Return {preset, include, exclude, precision}`; `Options.DefaultReturn` (library default `full`) · [U17](units/U17-response-shaping-core.md)
- [x] **#105** MCP default `standard`: `gosdk.Config.DefaultReturn`, `pulse mcp --return`; MCP goldens regenerated once; release-note callout · [U18](units/U18-response-shaping-execution.md)
- [x] **#106** Path grammar over the response schema; predict-time validation; `PULSE_RETURN_PATH_UNKNOWN` · [U17](units/U17-response-shaping-core.md)
- [x] **#107** Presets `full` / `standard` / `minimal` listed in the manifest · [U17](units/U17-response-shaping-core.md)
- [x] **#108** Selection compiled into the execution plan (unrequested parts not computed) · [U18](units/U18-response-shaping-execution.md)
- [x] **#109** Float precision control; old switches documented as shorthands; `returned` marker · [U17](units/U17-response-shaping-core.md)
- [x] **#110** Predict per-section size estimates · [U18](units/U18-response-shaping-execution.md)
- [x] **#111** `TestReturnFullIsIdentity`, `TestReturnSkipsComputation`, `TestReturnPathsMatchSchema`; `response-shaping.md` skill (the skill and the identity / schema-path gates landed in [U17](units/U17-response-shaping-core.md); this unit adds `TestReturnSkipsComputation` and extends the skill) · [U18](units/U18-response-shaping-execution.md)
- [x] **#193** Per-group aggregator Components: `Components.Aggregations` figures emitted inside each group of a grouped response, for built-in and extension operators alike (wire + payload-schema change, `format_version` stays `"1.1"`) · [U17](units/U17-response-shaping-core.md)
- [x] **#217** `PULSE_RETURN_PATH_UNMATCHED` on streams: decide whether a stream surfaces it (it has no warnings slot today, so the buffered-only warning is silent there) and how · [U18](units/U18-response-shaping-execution.md)
- [x] **#218** MCP `pulse_process` streaming option writes rows through `returnshape.MarshalStreamRow`, so wire precision and the selected columns apply there too (verified for `pulse api process|compose --stream` only) · [U18](units/U18-response-shaping-execution.md)
- [x] **#219** Resolved-plan caching: an instance default re-walks the `Response` type on every `Process`; cache the plan without its request-derived `Exact` set if profiling shows it · [U18](units/U18-response-shaping-execution.md)

---

## 9. Embedder operations

### Resource limits ([embedder-operations 01](v1.0.0-embedder-operations/01-resource-limits.md))
- [x] **#112** `Options.Limits` with high defaults; validation at `pulse.New` · [U19](units/U19-resource-limits.md)
- [x] **#113** Predict-time checks + `PredictResult.LimitFindings` · [U19](units/U19-resource-limits.md)
- [x] **#114** Runtime checks (groups, crosstab cells, join build, matrix dim, compose / chain fan-out, memory estimate, timeout) · [U19](units/U19-resource-limits.md)
- [x] **#115** `PULSE_LIMIT_EXCEEDED` with tuning fixups · [U19](units/U19-resource-limits.md)
- [x] **#116** Profile-file `limits` section; manifest `limits` block; `pulse mcp --limit` · [U19](units/U19-resource-limits.md)
- [x] **#117** Defaults-never-trip, predict/runtime parity and memory-release gates; "Tuning limits" docs page · [U19](units/U19-resource-limits.md)

#### Follow-ups from U19
- [ ] **#224** `ComposeOptions.FailFast=false` returns `(nil, SERVICE_INTERNAL)` and discards the slots that succeeded; decide whether partial slots are returned before the freeze (the doc now states today's behaviour) · [U33](units/U33-v1-release.md)
- [x] **#225** The simple `pulse.Facet(path, field)` facade runs no limits pre-flight (only `FacetSchema` / the rich facet is guarded); give it the same `MaxEstimatedMemory` pre-flight. Done (U35 E7-S1): the streamed (numeric) path runs `FacetLimitRefusal` over a one-field request before the iterator opens; the categorical fast path reads no record and has none · [U35](units/U35-predict-runtime-parity.md)
- [x] **#226** Predict reports no limit findings for Compose, ProcessChain or Facet requests (no facade predict surface); slot/stage counts and facet group counts are treated as unknowable at predict. Done (U35 E7-S1): `ComposeValidationResult` / `ChainValidationResult` / `FacetValidationResult.LimitFindings`, graded like Process and located by `LimitFinding.Details` (`request` / `stage`); predict-chain refuses `MaxChainStages`; chain stages >= 1 carry no certain memory finding; only `RequestTimeout` stays unknowable (`TestLimitsPredictRuntimeParity`) · [U35](units/U35-predict-runtime-parity.md)
- [x] **#227** `mcpserve.Describe` / `ServeInfo` report the effective limits (the manifest `limits` block and the `pulse mcp` startup line already do) · [U20](units/U20-observability.md)
- [ ] **#228** `Pulse.ProcessStream` (RowIter) bounds only the run with `RequestTimeout`; draining with `Next(ctx)` runs under the caller's ctx (`ProcessStreamResult` bounds both). Keep and document, or bound the drain too · [U33](units/U33-v1-release.md)
- [x] **#229** Tighten the memory estimate: it ignores field projection (up to ~4.9x over on projected buffered runs) and assumes at most one right match per left row (a 1:N fan-out join can exceed it) · [U35](units/U35-predict-runtime-parity.md) — join half done (U35 E7-S5): predict's `possible` fan-out finding on the right key's dictionary bound + the runtime build-side backstop on the measured key multiplicity. The projection half is a U35 non-goal (an over-estimate is safe; not owned elsewhere).
- [ ] **#230** Extension aggregators declare a state-size hint so the memory estimate can bound extensions with unbounded state (today they are outside the model) · [U34](units/U34-extension-validation.md)

### Observability ([embedder-operations 02](v1.0.0-embedder-operations/02-observability.md))
- [x] **#118** `Options.Logger` (`slog`, nil = silent); context-aware; no row data · [U20](units/U20-observability.md)
- [x] **#119** `Options.Hooks`: operation start/end (context-returning), phase timings; panic-safe · [U20](units/U20-observability.md)
- [x] **#120** `Options.Metrics` interface (opt-in) with bounded labels · [U20](units/U20-observability.md)
- [x] **#121** `contrib/otelpulse` and `contrib/prompulse` as separate modules · [U20](units/U20-observability.md)
- [x] **#122** `pulse mcp` / CLI `--log-level`, `--log-format`, opt-in `--metrics-addr` · [U20](units/U20-observability.md)
- [x] **#123** Silence, no-row-data, dependency and panic gates; "Observability" docs page · [U20](units/U20-observability.md)

#### Follow-ups from U20
- [ ] **#231** Recover a panic raised inside an operation (a faulty extension operator or expr function, an engine bug) into a coded error at the facade instead of crashing the host; only hook panics are recovered today · [U34](units/U34-extension-validation.md)
- [ ] **#232** Release rehearsal: exercise the lockstep contrib tag procedure (`scripts/release-contrib.sh`, `contrib` job in `release.yml`) on `v1.0.0-rc.1`, and decide how a root dependency bump keeps `make contrib-tidy` green (Dependabot root bumps fail `make contrib` until it is run in the PR) · [U33](units/U33-v1-release.md)
- [ ] **#233** Decide the frozen shape of `mcpserve.ServeInfo`: U20 added `Limits []descriptor.LimitMeta`, which makes the struct non-comparable with `==`; keep, or carry `pulse.Limits` instead, before the API freeze · [U33](units/U33-v1-release.md)
- [ ] **#234** Decide whether the `*Pulse` methods deliberately left without an `OperationKind` (`CohortArtifacts`, `NewCohortBuilder`, `Imports`, `ResolveImport`, `ResolveCanonicalSchema`, `ApplySeriesOverlays`, `InvalidatedSidecars`, `Watch*`) stay uninstrumented in the frozen `observe` enum; adding a kind later is additive but widens metric cardinality · [U33](units/U33-v1-release.md)

---

## 10. Vector & matrix — operators

### E3 — Core matrix operators
- [x] **#124** `MAT_CORRELATION` Spearman / Kendall · [U24](units/U24-matrix-operators.md)
- [x] **#125** `MAT_PARTIAL_CORRELATION` · [U24](units/U24-matrix-operators.md)
- [x] **#126** `MAT_RELIABILITY` (α, standardized α, ω, item-total, α-if-deleted, reverse scoring) · [U24](units/U24-matrix-operators.md)
- [x] **#127** `MAT_PCA` (loadings, eigenvalues, explained variance, KMO, Bartlett) · [U24](units/U24-matrix-operators.md)
- [x] **#128** `MAT_COLLINEARITY` (VIF, tolerance, condition indices) · [U24](units/U24-matrix-operators.md)
- [x] **#129** `RegressionResult.Vcov` and `.Correlation` · [U24](units/U24-matrix-operators.md)
- [x] **#130** Topical skill `multivariate-design.md` · [U24](units/U24-matrix-operators.md)

#### Follow-ups from U24
- [x] **#261** Raw p-values and confidence intervals on `MAT_CORRELATION` `params.method` `spearman` / `kendall` (the `cor.test` analogues, exact Kendall with ties), as `auxiliary.p` / interval matrices; U24 ships the figures only · [U28](units/U28-matrix-overlays.md)
- [x] **#262** `MAT_FACTOR`: multi-factor minres factor analysis, reusing `minresOneFactor`, the `PULSE_MATRIX_HEYWOOD` / `_NOT_IDENTIFIED` / `_NOT_CONVERGED` warnings (add it as an owner) and the PSD guard · [U25](units/U25-multivariate-tests-segmentation.md)
- [x] **#263** Factor rotation (varimax, promax) for `MAT_FACTOR`, including a varimax option on `MAT_PCA` loadings (U24 ships unrotated components) · [U25](units/U25-multivariate-tests-segmentation.md)
- [x] **#264** Link the `factor` glossary entry to `MAT_FACTOR` and add it as the third `measure_construct` declarer, clearing both exemption-ledger entries (`factor`, `measure_construct`) in `guidance_exemptions_test.go` · [U25](units/U25-multivariate-tests-segmentation.md)
- [x] **#265** Partial-correlation statistic and p-value (df = n − 2 − g; the `ppcor` values already sit in `mv_partial_correlation.json`) as `auxiliary.p` beside the multiplicity core · [U28](units/U28-matrix-overlays.md)
- [ ] **#266** Matrix oracle gaps: add weighted pairwise PCA, collinearity and reliability `item_sd` cases (the unweighted pairwise PCA and collinearity cases landed in U24); U30 E5-S2 added the weighted pairwise PCA (covariance and correlation) and reliability (`item_sd`, alpha) oracle cases, so collinearity's weighted pairwise case is what remains · [U36](units/U36-reference-oracles.md)
- [x] **#267** Predict `row_buffer_bytes` is an upper bound for ordinary groupers only: a fan-out grouper (`GROUP_SET_PER_ELEMENT` and kin) copies a row into every label bucket, so the true buffer can exceed it; add the fan-out multiplicity, and confirm `limits.MatrixStateBytes` counts the buffered rank-method row store. Done (U35 E7-S2): `descriptor.FanOutMultiplicity` (set dictionary / include bound; only `GROUP_SET_PER_ELEMENT` fans out) scales `row_buffer_bytes`, the memory estimate's row work and per-value state; the row store was already counted, separately from `MatrixStateBytes` (`limits.MatrixRowBufferBytes`), and the estimate now charges it x10 (`limits.MatrixRowWorkBytes`: store growth + rank finalize — it under-estimated a six-way fan-out at 0.82x) · [U35](units/U35-predict-runtime-parity.md)
- [ ] **#268** Trim the soft-budget overruns in the `op-mat-*` atomic skills (`op-mat-correlation` body 1970 B against 1200) with the other over-budget atomic bodies · [U32](units/U32-docs-audit.md)
- [ ] **#269** A rating-battery demo cohort (Likert items on one latent trait) so `MAT_RELIABILITY` and `MAT_PCA` examples show a defined omega and real loadings; `08_reliability.json` runs on `experiment.pulse` and shows a Heywood null omega; U30 E8-S2 added `survey.pulse`, a six-item battery on TWO correlated factors (`matrices/14_rotations.json`), whose q1-q3 or q4-q6 subset is a ready one-trait battery · [U31](units/U31-guidance-guides.md)
- [x] **#270** Matrix hardening: a per-pair Σw² for probability-weighted pairwise `item_sd` in the co-moment state; done in U30 E5-S2: each pairwise covariance entry takes its own pair's Kish factor (`PairNEff`) for `MAT_RELIABILITY` / `MAT_PCA` on a covariance, and pairwise N* is the smallest pair's own Kish n_eff · [U30](units/U30-matrix-extensions-hardening.md)
- [ ] **#271** `REG_GLM` gamma: Pulse fixes dispersion at 1 for standard errors and `vcov`; R estimates it. Decide, align, and add a gamma oracle case · [U36](units/U36-reference-oracles.md)

### E4 — Multivariate tests, fitted attributes, segmentation
- [x] **#131** `TEST_HOTELLING_T2` · [U25](units/U25-multivariate-tests-segmentation.md)
- [x] **#132** `TEST_MANOVA` (Wilks, Pillai, Hotelling–Lawley, Roy) · [U25](units/U25-multivariate-tests-segmentation.md)
- [x] **#133** `TEST_BARTLETT_SPHERICITY` · [U25](units/U25-multivariate-tests-segmentation.md)
- [x] **#134** `ATTR_MAHALANOBIS` · [U25](units/U25-multivariate-tests-segmentation.md)
- [x] **#135** `ATTR_PC_SCORE` (two-pass, `fit_on`) · [U25](units/U25-multivariate-tests-segmentation.md)
- [x] **#136** `GROUP_KMEANS` (seeded k-means++, Euclidean-only, size-ordered labels) · [U25](units/U25-multivariate-tests-segmentation.md)

#### Follow-ups from U25
- [ ] **#401** Panel-robust `REG_OLS` / `REG_GLM` / `REG_BAYES_LINEAR` covariance: HAC across a panel (per-entity Newey–West or Driscoll–Kraay) and two-way clustering. `covariance.type: hac` reads one series and refuses a repeated `order_by` (`order_by_ties`), so the MMM example clusters on the week · [U36](units/U36-reference-oracles.md)
- [x] **#402** Compose co-moment fits: fit a vector battery's co-moment state once in a compose `fits` entry and let several slots (`MAT_*`, `ATTR_MAHALANOBIS`, `ATTR_PC_SCORE`, `ATTR_FACTOR_SCORE`) reuse it, as regressions already do; done in U30 E2-S1 / E2-S2: a compose `fits` entry takes a vector battery (`MAT_*` or a fitted attribute); fitted attributes and `MAT_*` read it through `params.fit`; `ComposedResponse.Fits` carries `{vector: …}` · [U30](units/U30-matrix-extensions-hardening.md)
- [x] **#403** Named populations: save a fitted vector population (mean, covariance, loadings) under a name and score other cohorts against it, instead of re-fitting with `fit_on` per request; done in U30 E2-S3 / E2-S4: a vector fit's `save` writes a `.pulse-population.json`; `Request.populations` loads one by name; `Extensions.Populations` / `PULSE_POPULATIONS_DIR` register them at `pulse.New` · [U30](units/U30-matrix-extensions-hardening.md)
- [x] **#404** Per-`Groups` statistical tests: `TEST_HOTELLING_T2`, `TEST_MANOVA`, `TEST_BOX_M`, `TEST_MARDIA`, `TEST_BARTLETT_SPHERICITY` and the fitted attributes on multi-entry `Groups`, decided with #200. Done (E5-S3): `PULSE_MATRIX_SINGULAR` / `_INSUFFICIENT_N` join `sparseTestCodes`; the five run per group (equal to the filtered run, composite keys included) and warn rather than abort on a degenerate group; fitted attributes stay cohort-wide by design · [U35](units/U35-predict-runtime-parity.md)
- [x] **#405** `MAT_FACTOR` `ml`: RMSEA confidence interval (`psych::fa` `RMSEA` lower / upper) and its oracle; E2 ships the point RMSEA, TLI, BIC and the likelihood χ² · [U36](units/U36-reference-oracles.md)
- [x] **#406** More GPA rotations for `MAT_FACTOR` / `MAT_PCA`: quartimax, geomin, oblimax, and the `GPArotation` criteria beyond varimax, promax and oblimin, on the shared rotation engine; done in U30 E6-S2: `quartimax`, `entropy`, `geomin_orthogonal` (orthogonal; `MAT_PCA` / `ATTR_PC_SCORE` too) and `geomin` (oblique; `params.epsilon`), refused oblique on PCA with `rotation_oblique_unsupported`; `oblimax` not offered (degenerate under GPFoblq on most inputs, and quartimax when orthogonal) — rationale in `.claude/reference/matrix-and-vectors/matrix-decomposition.md` (Rotation engine) · [U30](units/U30-matrix-extensions-hardening.md)
- [x] **#407** Fold `MAT_DISTANCE`'s local metric list (`euclidean`, `sqeuclidean`, `manhattan`, `chebyshev`, `cosine`, `mahalanobis`) into the shared `linalg/metric` registry once it lands; one definition per metric · [U27](units/U27-vector-metrics-aggregates.md)
- [x] **#408** One singularity rule across the multivariate stack: the tests decide singular by `gonum` `SymEigen` against `RankTolerance`, while `MAT_PCA` is singular only when `InverseSPD` fails; align them and add the borderline case to the oracle · [U30](units/U30-matrix-extensions-hardening.md)
- [x] **#409** Extension statistical tests that take a vector (`vector` / `fields`) are not validated: `ResolveTests` skips non-built-in types, so a bad member list reaches the runtime. Validate extension test vectors as built-ins' · [U34](units/U34-extension-validation.md)
- [x] **#451** Expose the per-output-column export plan (`iocore.ExportColumn` / `ColumnAwareWriter`) on the public `io` facade, so an embedder's export writer is told which field TYPES each output column under a projection, a `_label` augment or a vector expansion (today it receives the source schema and must type by column name). Found in U29 (E3-G2); done in U29 before release (`io.ColumnAwareWriter`, `io.ExportColumn`, `io.ExportColumnKind` + constants, `io.ColumnFields`) · [U34](units/U34-extension-validation.md)
- [x] **#410** A non-Heywood weighted fixture for the `ATTR_FACTOR_SCORE` Bartlett path: most weighted oracle fixtures are Heywood cases, which null every Bartlett score, so weighted Bartlett scoring is thinly pinned to `psych` · [U36](units/U36-reference-oracles.md)
- [x] **#411** `MAT_DISTANCE` `between` refuses date-family groupers (the time-zone pass does not reach `params`) and extension groupers; resolve the zone for the date family and give extension groupers a centroid hook, or document the refusal as final · [U30](units/U30-matrix-extensions-hardening.md)
- [x] **#412** `skills/statistical-testing.md` sits within 15 B of its 6000 B hard budget, so no multivariate or other test can add a line; split the multivariate tests into their own topical skill or compress it · [U32](units/U32-docs-audit.md)
- [x] **#413** Trim the soft-budget overruns in the `op-test-*` multivariate atomic skills (`op-test-hotelling-t2` 1633 B, `op-test-manova` 1435 B against 1200) with the other over-budget atomic bodies · [U32](units/U32-docs-audit.md)
- [x] **#414** Weight-reference regeneration: rerunning `gen_weight_reference.py` shifts the `REG_*` rows by ulps (only the Hotelling rows were committed), and the R oracle needs Hotelling 1.0.8 plus Hmisc with FLIBS in `Makevars` (ICSNP and MVTests fail to build on R 4.6.1); pin and document the regeneration, or commit the shifted rows · [U36](units/U36-reference-oracles.md)
- [x] **#415** `ATTR_FACTOR_SCORE`: a refusal (`PULSE_MATRIX_NOT_IDENTIFIED` / `_HEYWOOD`) drops the fit's not-converged note; carry it on the refusal warning · [U30](units/U30-matrix-extensions-hardening.md)
- [ ] **#416** Atomic skills still over the soft 1200 B body budget after U25: `op-reg-ols` (3658 B), `op-reg-glm` (3283 B) and `op-reg-bayes-linear` (3443 B), which grew with `covariance` in U25, plus `op-mat-correlation`, `op-mat-reliability`, `op-mat-collinearity` and `op-mat-covariance`; trim them with the other over-budget bodies before the budget turns hard · [U38](units/U38-skill-sync.md)

### E5 — Row-wise vector vocabulary & similarity
- [x] **#137** expr-lang vector bindings, `v[i]` and `len`; `vsum` / `vmean` / `vmin` / `vmax` / `vsd` / `vcount`; `dot` / `norm` / `dist` / `cosine`; `argmax` / `argmin` · [U26](units/U26-vector-expr-functions.md)
- [x] **#138** Scalar maths functions `sqrt`, `log`, `exp`, `abs`, `pow` · [U26](units/U26-vector-expr-functions.md)
- [x] **#139** Centering functions `vcenter`, `vzscore`, `vnormalize` · [U26](units/U26-vector-expr-functions.md)
- [x] **#140** Set similarity `jaccard`, `dice`, `hamming`, `overlap` on `set_*` fields · [U26](units/U26-vector-expr-functions.md)
- [x] **#141** Shared metric registry `linalg/metric` · [U27](units/U27-vector-metrics-aggregates.md)
- [x] **#142** Vector `kind` (`measure` / `scale` / `composition` / `binary`) with metric defaults and `PULSE_VECTOR_METRIC_UNSUITED`; also owns `PULSE_ADVISORY_COSINE_ON_SCALE` (moved from U22) · [U27](units/U27-vector-metrics-aggregates.md)
- [x] **#143** `ATTR_SCALE_SCORE` · [U27](units/U27-vector-metrics-aggregates.md)
- [x] **#144** `AGG_VEC_MEAN`, `AGG_VEC_SUM` (array value, `expand` option) · [U27](units/U27-vector-metrics-aggregates.md)

#### Follow-ups from U26
- [x] **#417** Reference vectors from a `LookupTable`: let expression and profile similarity compare a row's vector or set against a stored reference profile (deferred from U26) · [U27](units/U27-vector-metrics-aggregates.md)
- [x] **#418** Overlay-formula callee parity: request-host `OVERLAY_FORMULA` predict reports an unknown callee (neither built-in nor embedder) as `PULSE_OVERLAY_FORMULA_INVALID_IDENT` while the runtime gives a parse error; compose-host predict checks no unknown callee beyond the vector built-ins. Found in U26 (E2-S2) · [U35](units/U35-predict-runtime-parity.md)
- [x] **#419** Static `PROCESSING_VECTOR_LENGTH_MISMATCH` at predict for `dot` / `dist` / `cosine` over named vectors of known, differing lengths (runtime already refuses). Found in U26 (E2-S2) · [U35](units/U35-predict-runtime-parity.md)

#### Follow-ups from U27
- [x] **#420** Sort, overlays and ratios over a vector aggregation's expanded element columns (`params.expand`, `<label>[<element label>]`): today they are presentation only — a `sort` key, a window / post-test input, and the value every overlay host scores (request, Compose, whole-chain — each judges only the scored aggregation since U46 E4-S8 / E4-S10) refuse them with `PROCESSING_CONFIG` `array_valued`, and a stage carrying one may not feed a chain. Lift the refusal per consumer with an explicit element-column contract (which column a SERIES overlay reads, a chain stage typing them `f64`). Deferred from U27 (E2-S3); done in U30 E4-S1..S4: sort keys, windows, post-tests, every overlay host, chain stages (as `f64`) and crosstab `params.element` cells read element columns; quoted return paths; ratios reach an element column only through a chain stage (E4-S1) · [U30](units/U30-matrix-extensions-hardening.md)
- [x] **#423** Group-centroid references: `ATTR_PROFILE_SIMILARITY` / `ATTR_VEC_DISTANCE` compare a row against the `AGG_VEC_MEAN` of a named filter or group (a two-pass fit), not only a literal or a registered `ReferenceVectors` profile. Deferred from U27 (E3); done in U30 E3-S1: `reference: {centroid: {by | filter}}`, a weighted per-member mean folded in a pre-pass; `{population: name}` for a saved population · [U30](units/U30-matrix-extensions-hardening.md)
- [x] **#424** Point-lookup-record references: compare rows against a single stored record ("rows like customer #4411") via the point-lookup index. Deferred from U27 (E3); done in U30 E3-S2: `reference: {record: {keys}}` through the cohort's sidecar index · [U30](units/U30-matrix-extensions-hardening.md)
- [x] **#425** `AGG_VEC_SD`: the per-element standard deviation of a vector, an array-valued aggregator beside `AGG_VEC_MEAN` / `AGG_VEC_SUM` (mergeable via the co-moment). Deferred from U27 (E2); done in U30 E5-S1 · [U30](units/U30-matrix-extensions-hardening.md)
- [x] **#426** Expose the registered-but-unexposed `chi_square` metric through a consumer (crosstab-profile overlays; it takes its column masses explicitly). Deferred from U27 (E1); done in U30 E5-S3: `OVERLAY_CORRESPONDENCE` parts `row_distance` / `column_distance`; `metric.ConsumerCorrespondence` · [U30](units/U30-matrix-extensions-hardening.md)
- [x] **#427** A `u64` `key_field` value above 2^53 is matched via its float64 value (records carry numbers as float64; the same limit as `ATTR_CODE_IN`), so two distinct large keys can collide; give record access an exact `u64` path. Deferred from U27 (E3-S4); no unit owns exact `u64` record access, so it sits with predict / runtime parity · [U35](units/U35-predict-runtime-parity.md)

### E6 — Matrix operations on results (overlays)
- [x] **#145** MATRIX_RESULT overlay host and the `Ref.Matrix` reference family · [U28](units/U28-matrix-overlays.md)
- [x] **#146** `OVERLAY_STD_RESIDUAL` · [U28](units/U28-matrix-overlays.md)
- [x] **#147** `OVERLAY_CORRESPONDENCE` · [U28](units/U28-matrix-overlays.md)
- [x] **#148** `OVERLAY_MARKOV` · [U28](units/U28-matrix-overlays.md)
- [x] **#149** `OVERLAY_RAKE` · [U28](units/U28-matrix-overlays.md)
- [x] **#150** `MatrixSpec.multiplicity` → `p_adjusted` auxiliary matrix via the shared correction core (replaces the dropped `OVERLAY_CORR_PVALUE`) · [U28](units/U28-matrix-overlays.md)

#### Follow-ups from U28
- [x] **#449** The `flows` intent needs ≥3 declaring operators: `OVERLAY_MARKOV` is its only declarer, and no other built-in's Purpose answers a from-state to to-state question (`WIN_LAG` / `WIN_LEAD` are numeric and run on result rows, so they cannot pair a record's earlier state with its current one; `OVERLAY_SHARE_OF_ROW` reads a from-by-to table as one-step switching shares but is a general composition overlay). Ship flows operators (a paired-change test such as McNemar / Bowker symmetry on a from-by-to table, a per-subject previous-state attribute, retention / churn rates) and drop the `intentDeclarerExemptions` entry. Deferred from U28 (E4-G2); done in U30 E7-S1 / E7-S2: `OVERLAY_SYMMETRY` and `OVERLAY_RETENTION` join `OVERLAY_MARKOV` as `flows` declarers and the exemption is gone; the per-subject previous-state attribute is #452 (U50) · [U30](units/U30-matrix-extensions-hardening.md)

#### Follow-ups from U30
- [ ] **#454** `OVERLAY_SYMMETRY`: an exact binomial McNemar test on a 2 × 2 (`params.exact`, R `binom.test(n_12, n_12 + n_21)`; many texts prefer it when n_12 + n_21 < 25, where the chi-square tail only warns `PULSE_OVERLAY_EXPECTED_LOW` today) and the discordant odds ratio n_12 / n_21 as an effect size (`details.effect_size.*`, with its interval), both pinned to an R oracle. Owner decision 2026-10-14 (U30 E7-S1); open item 2 of the [U30 flows review](reviews/U30-flows-review.md) · [U36](units/U36-reference-oracles.md)

### E7 — Native vector field types
- [x] **#151** `vec_f32` / `vec_f64` encoding and the `VECTORS` schema extension section (tag 2); `ReadVector` accessor · [U29](units/U29-vector-field-types.md)
- [x] **#152** Compatibility test: older cohorts unchanged; older binaries refuse loudly · [U29](units/U29-vector-field-types.md)
- [x] **#153** Import `--vector` folding (CSV, NDJSON, Arrow, Parquet) · [U29](units/U29-vector-field-types.md)
- [x] **#154** Export expansion; shard cohesion; parent-group membership · [U29](units/U29-vector-field-types.md)
- [x] **#155** `type-vec-f32.md`, `type-vec-f64.md`, `byte-layout.md`, CLAUDE.md byte-layout invariants (20 → 22 types) · [U29](units/U29-vector-field-types.md)

### E10 — Extensions & hardening (committed part)
- [x] **#156** `MatrixOpRegistration` and the `MAT` naming-policy namespace; probe validation; done in U30 E1-S1..S4: `extend` matrix contract + `pulse.MatrixOpRegistration`, `MAT_<NAMESPACE>_<NAME>` naming, run-probe at `pulse.New`, built-in parity on manifest / predict / MCP / runtime, bitwise worker invariance, opt-in multiplicity via `PValues` · [U30](units/U30-matrix-extensions-hardening.md)
- [x] **#157** Benchmarks: p = 50 / 256 at 1M and 10M rows; peak heap; shard scaling; done in U30 E8-S1 (and E8-S3..S5): `BenchmarkMatrixScale*` at 1M x p = 50 with committed baselines, peak heap and pred-state metrics, the `DecodeWorkers` / `ShardWorkers` sweep; the 1M x p = 256 and 10M tiers are opt-in (`make bench-scale`, no committed baseline) — `docs/internals/BENCHMARKS_BASELINE.md` · [U30](units/U30-matrix-extensions-hardening.md)
- [x] **#158** Examples-library entries for every new operator; done in U30 E8-S2: every U24-U30 operator is tagged (`TestEveryOperatorHasAnExampleTag`); new rotation criteria, vector fits, saved populations and vector element cells have examples (`matrices/14_rotations.json`, `compose/05_vector_fit_save.json`, `compose/06_population_score.json`, `crosstab/17_vector_element_cells.json`) on a new two-factor `survey` fixture · [U30](units/U30-matrix-extensions-hardening.md)

---

## 11. Documentation audit ([docs-audit 00](v1.0.0-docs-audit/00-plan.md))

- [ ] **#159** Documentation inventory and coverage matrix: every public API symbol, CLI leaf and flag, MCP tool / prompt / resource, operator, field type, error code, env var, `Options` field and request/response slot, mapped to where it is documented · [U32](units/U32-docs-audit.md)
- [ ] **#160** Automated checks in CI: link checker, runnable-snippet test, CLI help ↔ `flags.md` parity, GoDoc `Example*` functions for the public facade, removed-name scan · [U32](units/U32-docs-audit.md)
- [ ] **#161** `TestSkillTokenBudget` flipped from soft to hard-failing, with every skill within budget (known overrun: `session-bootstrap.md`, now 5,999 of 6,000 bytes after U23's `intent` routing, so the next addition must displace text) · [U32](units/U32-docs-audit.md)
- [ ] **#162** Accuracy and currency pass: mdBook site, `README.md` / `CONTRIBUTING.md` / `SECURITY.md` / `STABILITY.md`, `CLAUDE.md` and `.claude/reference/` · [U32](units/U32-docs-audit.md)
- [ ] **#163** Accuracy and currency pass: skills (atomic and topical), examples library, MCP tool / prompt / resource descriptions, error messages and fixups, manifest descriptions, `Purpose` / `Interpretation` / glossary · [U32](units/U32-docs-audit.md)
- [ ] **#164** Getting Started rewritten for v1 (install from GitHub Releases → first cohort → first analysis → first MCP session) and a single embedder guide (profiles, limits, observability, response shaping) · [U32](units/U32-docs-audit.md)
- [ ] **#165** Terminology made consistent with the glossary; pre-1.0 and removed names purged · [U32](units/U32-docs-audit.md)
- [ ] **#166** Fresh-reader review and agent task evaluation over MCP (~20 tasks, kept as a regression set); every failure fixed · [U32](units/U32-docs-audit.md)
- [ ] **#167** Findings log closed (fixed or deferred with reason and issue) and maintainer sign-off recorded · [U32](units/U32-docs-audit.md)
- [ ] **#453** `sites` change-site tool gaps found by the U30 story agents (each one cost a hand grep): (1) no operator-param index beyond enums — a new param FORM such as `params.reference` `{centroid | population | record}` surfaces only through `comparisonCapability`; (2) metric-registry consumers are listed only in part (`metricCapabilities`, `assembleManifest`'s consumer-name map) when an operator starts consuming a `linalg/metric` metric; (3) false `category-norm site missing` warnings for an operator that legitimately has no entry (`MAT_RELIABILITY` in `linalg.md`, `predict.md`, `predict_return_sizes.go`); (4) `new-op` misses hand lists that name an operator SUBSET (`vectors.VectorAggregationTypes`, refusal prose naming two of three vector aggregators, `examples/profiles/read-only-analyst.json`); (5) oracle attribution is imprecise when one fixture or generator serves several operators (`gen_rotation.R`, `mv_rotation.json`, the weighted reference generator's case tables); (6) whole-repo runners and floors are never listed as sites: `limits_goldens_test.go` (every-example runner) for an examples change, `format_version_gate_test.go` (ReadSchema call-site floor) for a read-path change. Recipe: `docs/src/internals/finding-change-sites.md`. Found in U30 (E9-S4 annotation, E3–E7 story hand-backs) · [U32](units/U32-docs-audit.md)

---

## 12. Release v1.0.0

- [ ] **#168** `STABILITY.md` published at the repo root, with the final public package list ([api-and-release 02](v1.0.0-api-and-release/02-stability-policy.md)) · [U33](units/U33-v1-release.md)
- [ ] **#169** Release candidate tag (`v1.0.0-rc.1`) built through the release pipeline and exercised by the downstream library · [U33](units/U33-v1-release.md)
- [ ] **#170** `v1.0.0` tagged · [U33](units/U33-v1-release.md)
- [ ] **#220** Decide the root alias for `types.ReturnedMarker` (`pulse.ReturnedMarker`) before the API freeze; adding it changes the public-API golden and `STABILITY.md` · [U33](units/U33-v1-release.md)
- [ ] **#206** Human statistics sign-off (release-blocking): a named statistics reviewer works through the [U08 review record](reviews/U08-statistics-review.md) and its open items (the U08 E3 / E4 sections and the U09 section) the [U12 weighted-inference review](reviews/U12-weighting-review.md) (the `n_eff` semantics and every lifted weighted formula) and the [U24 matrix-operators review](reviews/U24-matrix-operators-review.md) (findings MR-01 to MR-08 and the open owner calls) and the [U25 multivariate review](reviews/U25-multivariate-review.md) (the open owner calls), signs off the U08 inferential AND the U09 descriptive guidance AND the U12 weighted inference AND the U24 matrix and U25 multivariate guidance, and owns the statistical-review CODEOWNERS entries · [U33](units/U33-v1-release.md)

---

## 13. Cross-cutting (applies throughout; tick when verified for the whole release)

- [ ] **#171** Every new operator in every theme is weight-aware (or explicitly refuses a weight) and multiplicity-aware where it emits p-values · [U33](units/U33-v1-release.md)
- [x] **#172** Missing-data modes documented and tested; PSD refusal / `repair: "nearest"` · [U16](units/U16-matrix-result.md) (modes + `NOT_PSD` detection; `repair: "nearest"` and the refusal landed in [U24](units/U24-matrix-operators.md))
- [ ] **#173** New `PULSE_MATRIX_*` / `PULSE_VECTOR_*` / `PULSE_OVERLAY_*` / `PULSE_PROFILE_*` / `PULSE_LIMIT_*` / `PULSE_WEIGHT_*` / `PULSE_RETURN_*` / advisory codes all have `codeMetadata` + fixups · [U33](units/U33-v1-release.md)
- [ ] **#174** Every new operator has `Purpose`, `Interpretation` (if inferential), `Since`, dependency edges and an atomic skill · [U33](units/U33-v1-release.md)
- [ ] **#175** Every new gate is listed by name in CLAUDE.md "Non-Skippable CI Gates" · [U33](units/U33-v1-release.md)
- [ ] **#176** The Update Demand table has rows for: `Purpose`, `Since` / dependencies, topical-skill fences, `Request.Vectors` / `Matrices`, `Response.Matrices`, `Request.Weight` / `Multiplicity` / `TimeZone` / `Return`, `Options.Limits` / `Logger` / `Hooks` / `Metrics` · [U33](units/U33-v1-release.md)
- [ ] **#177** New env vars and CLI flags documented (CLAUDE.md "Build / Env", `flags.md`, `session-bootstrap.md`) · [U33](units/U33-v1-release.md)
- [ ] **#178** CLAUDE.md stays at or under 40,000 bytes (long form moved to `.claude/reference/`; ceiling lowered 50,000 → 40,000 in U18) · [U33](units/U33-v1-release.md)
- [ ] **#179** `format_version` remains `"1.1"` (every wire change additive) · [U33](units/U33-v1-release.md)
