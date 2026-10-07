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
- [ ] **#198** Predict refuses every operator × field type the runtime refuses: the "predict looser" half of the `knownTypeDivergence` ledger (`field_type_acceptance_test.go`) is emptied · [U35](units/U35-predict-runtime-parity.md)
- [ ] **#199** The runtime refuses, rather than silently computes on, a field it cannot read: the ledger's "predict stricter, runtime wrong" half (`FEAT_POLY`, `REG_*` and `WIN_*` value windows on categorical / set / decimal / `packed_bool` / `datetime`), plus tier-1 tests on `set_*` · [U35](units/U35-predict-runtime-parity.md)
- [ ] **#200** A request with more than one `Groups` entry executes every group or is refused (today only `Groups[0]` runs) · [U35](units/U35-predict-runtime-parity.md)
- [ ] **#201** Remaining silent predict / runtime gaps: crosstab cell-aggregator validity in predict, one label set for the label-collision check, label bindings on ProcessChain stages ≥ 1, windowed record rows under projection · [U35](units/U35-predict-runtime-parity.md)
- [ ] **#207** Shard-archive cohesion compares `Nullable`: `AddShard`, `shard verify`, the `NewCohortBuilder` anchored-append pre-check and the archive reader refuse a shard whose per-field nullability differs from the canonical schema, instead of decoding it under the canonical flags · [U35](units/U35-predict-runtime-parity.md)
- [ ] **#213** `Response.overlays` joins the feature gate (`gatedSlots`, `internal/descriptor/request_slots.go`): hiding the overlay features leaves `overlays` paths in the instance payload schema and in the presets · [U35](units/U35-predict-runtime-parity.md)
- [ ] **#214** A joined slot or stage derives its `return` precision-exact set from the defaults-resolved request, as an unjoined one does (today the join path applies defaults to a service-internal clone, so `Plan.Exact` reads the un-defaulted request; `Process` included) · [U35](units/U35-predict-runtime-parity.md)
- [ ] **#215** `finalizeMergedPartial`'s empty-partial path emits the zero-n aggregation entries the serial path emits (ungrouped and grouped); today a merged run with an empty partial carries no `Aggregations` block · [U35](units/U35-predict-runtime-parity.md)

#### Cohort facade
- [x] **#190** `CohortReader` on the facade: `Schema()`, `Len()`, `RecordAt(i)` · [U02c](units/U02c-cohort-facade.md)
- [x] **#191** `CohortBuilder` on the facade: schema + append rows, grouped (`0x02`) cohorts included · [U02c](units/U02c-cohort-facade.md)
- [x] **#192** `PredictResult.CrosstabFusable` (no-execute) with a runtime parity gate against the engine's fusion check; payload-schema golden regenerated, `format_version` stays `"1.1"` · [U02c](units/U02c-cohort-facade.md)

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
- [ ] **#34** `pulse docs export` / `p.ExportReference` (shares the G3 generator) · [U21](units/U21-guidance-generated-docs.md)
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
- [x] **#82** `MaxMatrixDim`, `precision` and `top_pairs` controls · [U16](units/U16-matrix-result.md) (`top_pairs` only; `precision` → [U17](units/U17-response-shaping-core.md), landed there; `MaxMatrixDim` → [U19](units/U19-resource-limits.md))
- [x] **#83** `MAT_COVARIANCE` · [U16](units/U16-matrix-result.md)
- [x] **#84** `MAT_CORRELATION` (Pearson), with parity against `TEST_PEARSON_R` · [U16](units/U16-matrix-result.md)

---

## 7. Guided analysis — docs, API & MCP

### G3 — Generated docs & skill sections
- [ ] **#85** `internal/docgen`, `make docs` integration, `TestDocsGeneratedCurrent` · [U21](units/U21-guidance-generated-docs.md)
- [ ] **#86** Generated operator catalog pages · [U21](units/U21-guidance-generated-docs.md)
- [ ] **#87** Generated glossary page · [U21](units/U21-guidance-generated-docs.md)
- [ ] **#88** "Reading your results" pages (tests, regressions, matrices, overlays, components) · [U21](units/U21-guidance-generated-docs.md)
- [ ] **#89** Rendered skill sections `## Use when` / `## Reading the output`; `skill-pack.md` updated, plus `TestSkillPurposeSectionsCurrent` · [U21](units/U21-guidance-generated-docs.md)

### G4 — Recommend, Explain, advisories
- [ ] **#90** `pulse.Recommend` with bound (cohort) and unbound (cohort-free) modes; `pulse recommend`; `pulse_recommend`; `tool-recommend.md` · [U22](units/U22-recommend-explain.md)
- [ ] **#91** `pulse.Explain` request mode; terse by default, `detail: "full"` on request · [U22](units/U22-recommend-explain.md)
- [ ] **#92** `pulse.Explain` response mode; `pulse explain`; `pulse_explain`; `tool-explain.md` · [U22](units/U22-recommend-explain.md)
- [ ] **#93** Predict `advisories`, with codes and fixups · [U22](units/U22-recommend-explain.md)
- [ ] **#94** Explain goldens per operator family; Recommend goldens per intent · [U22](units/U22-recommend-explain.md)

### G5 — MCP guidance layer
- [ ] **#95** One MCP prompt per intent (extending `pulse-bootstrap` / `pulse-author-request`), plus the prompt gate · [U23](units/U23-guidance-mcp.md)
- [ ] **#96** `pulse_examples_search {intent}` and question search; synonym table · [U23](units/U23-guidance-mcp.md)
- [ ] **#97** `pulse_skills_list {intent}` · [U23](units/U23-guidance-mcp.md)
- [ ] **#98** Intent-scoped manifest (`pulse_manifest {intent}`) · [U23](units/U23-guidance-mcp.md)
- [ ] **#99** MCP tool descriptions lead with when to call the tool · [U23](units/U23-guidance-mcp.md)

### G6 — Guides
- [ ] **#100** "What can Pulse answer?" landing page · [U31](units/U31-guidance-guides.md)
- [ ] **#101** Question guides, one per intent: describe, compare groups, relationships, drivers, change over time, composition, benchmark, distribution, segment, measure construct, flows, data quality · [U31](units/U31-guidance-guides.md)
- [ ] **#102** Story examples: `_meta.intent` / `question` / `interpretation`, at least one per intent · [U31](units/U31-guidance-guides.md)
- [ ] **#103** Vector & matrix concept primers: correlation matrix, PCA, distance vs similarity, perceptual maps · [U31](units/U31-guidance-guides.md)

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
- [ ] **#112** `Options.Limits` with high defaults; validation at `pulse.New` · [U19](units/U19-resource-limits.md)
- [ ] **#113** Predict-time checks + `PredictResult.LimitFindings` · [U19](units/U19-resource-limits.md)
- [ ] **#114** Runtime checks (groups, crosstab cells, join build, matrix dim, compose / chain fan-out, memory estimate, timeout) · [U19](units/U19-resource-limits.md)
- [ ] **#115** `PULSE_LIMIT_EXCEEDED` with tuning fixups · [U19](units/U19-resource-limits.md)
- [ ] **#116** Profile-file `limits` section; manifest `limits` block; `pulse mcp --limit` · [U19](units/U19-resource-limits.md)
- [ ] **#117** Defaults-never-trip, predict/runtime parity and memory-release gates; "Tuning limits" docs page · [U19](units/U19-resource-limits.md)

### Observability ([embedder-operations 02](v1.0.0-embedder-operations/02-observability.md))
- [ ] **#118** `Options.Logger` (`slog`, nil = silent); context-aware; no row data · [U20](units/U20-observability.md)
- [ ] **#119** `Options.Hooks`: operation start/end (context-returning), phase timings; panic-safe · [U20](units/U20-observability.md)
- [ ] **#120** `Options.Metrics` interface (opt-in) with bounded labels · [U20](units/U20-observability.md)
- [ ] **#121** `contrib/otelpulse` and `contrib/prompulse` as separate modules · [U20](units/U20-observability.md)
- [ ] **#122** `pulse mcp` / CLI `--log-level`, `--log-format`, opt-in `--metrics-addr` · [U20](units/U20-observability.md)
- [ ] **#123** Silence, no-row-data, dependency and panic gates; "Observability" docs page · [U20](units/U20-observability.md)

---

## 10. Vector & matrix — operators

### E3 — Core matrix operators
- [ ] **#124** `MAT_CORRELATION` Spearman / Kendall · [U24](units/U24-matrix-operators.md)
- [ ] **#125** `MAT_PARTIAL_CORRELATION` · [U24](units/U24-matrix-operators.md)
- [ ] **#126** `MAT_RELIABILITY` (α, standardized α, ω, item-total, α-if-deleted, reverse scoring) · [U24](units/U24-matrix-operators.md)
- [ ] **#127** `MAT_PCA` (loadings, eigenvalues, explained variance, KMO, Bartlett) · [U24](units/U24-matrix-operators.md)
- [ ] **#128** `MAT_COLLINEARITY` (VIF, tolerance, condition indices) · [U24](units/U24-matrix-operators.md)
- [ ] **#129** `RegressionResult.Vcov` and `.Correlation` · [U24](units/U24-matrix-operators.md)
- [ ] **#130** Topical skill `multivariate-design.md` · [U24](units/U24-matrix-operators.md)

### E4 — Multivariate tests, fitted attributes, segmentation
- [ ] **#131** `TEST_HOTELLING_T2` · [U25](units/U25-multivariate-tests-segmentation.md)
- [ ] **#132** `TEST_MANOVA` (Wilks, Pillai, Hotelling–Lawley, Roy) · [U25](units/U25-multivariate-tests-segmentation.md)
- [ ] **#133** `TEST_BARTLETT_SPHERICITY` · [U25](units/U25-multivariate-tests-segmentation.md)
- [ ] **#134** `ATTR_MAHALANOBIS` · [U25](units/U25-multivariate-tests-segmentation.md)
- [ ] **#135** `ATTR_PC_SCORE` (two-pass, `fit_on`) · [U25](units/U25-multivariate-tests-segmentation.md)
- [ ] **#136** `GROUP_KMEANS` (seeded k-means++, Euclidean-only, size-ordered labels) · [U25](units/U25-multivariate-tests-segmentation.md)

### E5 — Row-wise vector vocabulary & similarity
- [ ] **#137** expr-lang vector bindings, `v[i]` and `len`; `vsum` / `vmean` / `vmin` / `vmax` / `vsd` / `vcount`; `dot` / `norm` / `dist` / `cosine`; `argmax` / `argmin` · [U26](units/U26-vector-expr-functions.md)
- [ ] **#138** Scalar maths functions `sqrt`, `log`, `exp`, `abs`, `pow` · [U26](units/U26-vector-expr-functions.md)
- [ ] **#139** Centering functions `vcenter`, `vzscore`, `vnormalize` · [U26](units/U26-vector-expr-functions.md)
- [ ] **#140** Set similarity `jaccard`, `dice`, `hamming`, `overlap` on `set_*` fields · [U26](units/U26-vector-expr-functions.md)
- [ ] **#141** Shared metric registry `linalg/metric` · [U27](units/U27-vector-metrics-aggregates.md)
- [ ] **#142** Vector `kind` (`measure` / `scale` / `composition` / `binary`) with metric defaults and `PULSE_VECTOR_METRIC_UNSUITED` · [U27](units/U27-vector-metrics-aggregates.md)
- [ ] **#143** `ATTR_SCALE_SCORE` · [U27](units/U27-vector-metrics-aggregates.md)
- [ ] **#144** `AGG_VEC_MEAN`, `AGG_VEC_SUM` (array value, `expand` option) · [U27](units/U27-vector-metrics-aggregates.md)

### E6 — Matrix operations on results (overlays)
- [ ] **#145** MATRIX_RESULT overlay host and the `Ref.Matrix` reference family · [U28](units/U28-matrix-overlays.md)
- [ ] **#146** `OVERLAY_STD_RESIDUAL` · [U28](units/U28-matrix-overlays.md)
- [ ] **#147** `OVERLAY_CORRESPONDENCE` · [U28](units/U28-matrix-overlays.md)
- [ ] **#148** `OVERLAY_MARKOV` · [U28](units/U28-matrix-overlays.md)
- [ ] **#149** `OVERLAY_RAKE` · [U28](units/U28-matrix-overlays.md)
- [ ] **#150** `MatrixSpec.multiplicity` → `p_adjusted` auxiliary matrix via the shared correction core (replaces the dropped `OVERLAY_CORR_PVALUE`) · [U28](units/U28-matrix-overlays.md)

### E7 — Native vector field types
- [ ] **#151** `vec_f32` / `vec_f64` encoding and the `VECTORS` schema extension section (tag 2); `ReadVector` accessor · [U29](units/U29-vector-field-types.md)
- [ ] **#152** Compatibility test: older cohorts unchanged; older binaries refuse loudly · [U29](units/U29-vector-field-types.md)
- [ ] **#153** Import `--vector` folding (CSV, NDJSON, Arrow, Parquet) · [U29](units/U29-vector-field-types.md)
- [ ] **#154** Export expansion; shard cohesion; parent-group membership · [U29](units/U29-vector-field-types.md)
- [ ] **#155** `type-vec-f32.md`, `type-vec-f64.md`, `byte-layout.md`, CLAUDE.md byte-layout invariants (20 → 22 types) · [U29](units/U29-vector-field-types.md)

### E10 — Extensions & hardening (committed part)
- [ ] **#156** `MatrixOpRegistration` and the `MAT` naming-policy namespace; probe validation · [U30](units/U30-matrix-extensions-hardening.md)
- [ ] **#157** Benchmarks: p = 50 / 256 at 1M and 10M rows; peak heap; shard scaling · [U30](units/U30-matrix-extensions-hardening.md)
- [ ] **#158** Examples-library entries for every new operator · [U30](units/U30-matrix-extensions-hardening.md)

---

## 11. Documentation audit ([docs-audit 00](v1.0.0-docs-audit/00-plan.md))

- [ ] **#159** Documentation inventory and coverage matrix: every public API symbol, CLI leaf and flag, MCP tool / prompt / resource, operator, field type, error code, env var, `Options` field and request/response slot, mapped to where it is documented · [U32](units/U32-docs-audit.md)
- [ ] **#160** Automated checks in CI: link checker, runnable-snippet test, CLI help ↔ `flags.md` parity, GoDoc `Example*` functions for the public facade, removed-name scan · [U32](units/U32-docs-audit.md)
- [ ] **#161** `TestSkillTokenBudget` flipped from soft to hard-failing, with every skill within budget (known overrun: `session-bootstrap.md`) · [U32](units/U32-docs-audit.md)
- [ ] **#162** Accuracy and currency pass: mdBook site, `README.md` / `CONTRIBUTING.md` / `SECURITY.md` / `STABILITY.md`, `CLAUDE.md` and `.claude/reference/` · [U32](units/U32-docs-audit.md)
- [ ] **#163** Accuracy and currency pass: skills (atomic and topical), examples library, MCP tool / prompt / resource descriptions, error messages and fixups, manifest descriptions, `Purpose` / `Interpretation` / glossary · [U32](units/U32-docs-audit.md)
- [ ] **#164** Getting Started rewritten for v1 (install from GitHub Releases → first cohort → first analysis → first MCP session) and a single embedder guide (profiles, limits, observability, response shaping) · [U32](units/U32-docs-audit.md)
- [ ] **#165** Terminology made consistent with the glossary; pre-1.0 and removed names purged · [U32](units/U32-docs-audit.md)
- [ ] **#166** Fresh-reader review and agent task evaluation over MCP (~20 tasks, kept as a regression set); every failure fixed · [U32](units/U32-docs-audit.md)
- [ ] **#167** Findings log closed (fixed or deferred with reason and issue) and maintainer sign-off recorded · [U32](units/U32-docs-audit.md)

---

## 12. Release v1.0.0

- [ ] **#168** `STABILITY.md` published at the repo root, with the final public package list ([api-and-release 02](v1.0.0-api-and-release/02-stability-policy.md)) · [U33](units/U33-v1-release.md)
- [ ] **#169** Release candidate tag (`v1.0.0-rc.1`) built through the release pipeline and exercised by the downstream library · [U33](units/U33-v1-release.md)
- [ ] **#170** `v1.0.0` tagged · [U33](units/U33-v1-release.md)
- [ ] **#220** Decide the root alias for `types.ReturnedMarker` (`pulse.ReturnedMarker`) before the API freeze; adding it changes the public-API golden and `STABILITY.md` · [U33](units/U33-v1-release.md)
- [ ] **#206** Human statistics sign-off (release-blocking): a named statistics reviewer works through the [U08 review record](reviews/U08-statistics-review.md) and its open items (the U08 E3 / E4 sections and the U09 section) and the [U12 weighted-inference review](reviews/U12-weighting-review.md) (the `n_eff` semantics and every lifted weighted formula), signs off the U08 inferential AND the U09 descriptive guidance AND the U12 weighted inference, and owns the statistical-review CODEOWNERS entries · [U33](units/U33-v1-release.md)

---

## 13. Cross-cutting (applies throughout; tick when verified for the whole release)

- [ ] **#171** Every new operator in every theme is weight-aware (or explicitly refuses a weight) and multiplicity-aware where it emits p-values · [U33](units/U33-v1-release.md)
- [x] **#172** Missing-data modes documented and tested; PSD refusal / `repair: "nearest"` · [U16](units/U16-matrix-result.md) (modes + `NOT_PSD` detection; `repair: "nearest"` and the refusal → [U24](units/U24-matrix-operators.md))
- [ ] **#173** New `PULSE_MATRIX_*` / `PULSE_VECTOR_*` / `PULSE_OVERLAY_*` / `PULSE_PROFILE_*` / `PULSE_LIMIT_*` / `PULSE_WEIGHT_*` / `PULSE_RETURN_*` / advisory codes all have `codeMetadata` + fixups · [U33](units/U33-v1-release.md)
- [ ] **#174** Every new operator has `Purpose`, `Interpretation` (if inferential), `Since`, dependency edges and an atomic skill · [U33](units/U33-v1-release.md)
- [ ] **#175** Every new gate is listed by name in CLAUDE.md "Non-Skippable CI Gates" · [U33](units/U33-v1-release.md)
- [ ] **#176** The Update Demand table has rows for: `Purpose`, `Since` / dependencies, topical-skill fences, `Request.Vectors` / `Matrices`, `Response.Matrices`, `Request.Weight` / `Multiplicity` / `TimeZone` / `Return`, `Options.Limits` / `Logger` / `Hooks` / `Metrics` · [U33](units/U33-v1-release.md)
- [ ] **#177** New env vars and CLI flags documented (CLAUDE.md "Build / Env", `flags.md`, `session-bootstrap.md`) · [U33](units/U33-v1-release.md)
- [ ] **#178** CLAUDE.md stays at or under 40,000 bytes (long form moved to `.claude/reference/`; ceiling lowered 50,000 → 40,000 in U18) · [U33](units/U33-v1-release.md)
- [ ] **#179** `format_version` remains `"1.1"` (every wire change additive) · [U33](units/U33-v1-release.md)
