# v1.0.0 TODO

Every **committed** v1.0.0 feature across the roadmap themes. Stretch and post-1.0 items are deliberately excluded; they stay in the theme documents.

**How to use this file:**
- Tick a box (`- [x]`) in the same PR that completes the item.
- An item counts as complete only when its code, tests, skills, CLAUDE.md / `.claude/reference/` companions and docs have all landed, as the Update Demand requires.
- Sections are in build order. Feature profiles and the guided-analysis metadata core come first, so every later surface is profile-filtered and carries purpose metadata from its first commit.

Theme documents: see the [roadmap index](README.md).

---

## 1. API surface & release pipeline

### Public Go surface ([api-and-release 00](v1.0.0-api-and-release/00-public-surface.md))
- [ ] Downstream usage catalog completed (maintainer)
- [ ] Classification per package decided (public / public-narrowed / internal)
- [ ] Package moves and narrowing done; facade re-exports added
- [ ] API-compatibility check (`gorelease` / `apidiff`) in CI against the latest tag

### Release pipeline ([api-and-release 01](v1.0.0-api-and-release/01-release-pipeline.md))
- [ ] `internal/buildinfo` + `pulse.Version()`; ldflags injection; `ReadBuildInfo` fallback; `make build` uses `git describe`
- [ ] `pulse version` / `--version`; `mcpserve` and `gosdk` default to the real version (remove hard-coded `"1.0.0"`); manifest `pulse_version`
- [ ] Gate against hard-coded version literals
- [ ] `ci.yml` callable; `release.yml` on `v*` tags gated on CI
- [ ] Binaries for linux / darwin / windows × amd64 / arm64, plus checksums; GitHub Release with generated notes; release labels

---

## 2. Feature profiles — foundation

### FP1 — Feature registry
- [ ] Stable feature names and kinds (`capability`, `operator`, `io_format`, `mcp_extra`) across all registries
- [ ] `Since` version on every registration, plus `TestFeaturesHaveSince`
- [ ] Dependency graph in `descriptor/dependencies.go`, plus `TestProfileDependenciesComplete`
- [ ] Always-present core defined: manifest, payload schema, skills, examples, errors lookup, inspect, predict

### FP2 — Profile model
- [ ] Profile file format: an exact-name allowlist, `written_with`, optional `behaviour`
- [ ] `pulse.New` validation and the config codes (`PULSE_PROFILE_FEATURE_UNKNOWN`, `PULSE_PROFILE_DEPENDENCY`), plus `TestProfileRejectsPatterns`
- [ ] `Options.Profile`, `Options.ProfileFile`, the `PULSE_PROFILE` env var, and `pulse mcp --profile` (the CLI is otherwise unprofiled)

### FP3 — Instance snapshot & request path
- [ ] `InstanceSnapshot` (merges the extensions snapshot with the resolved feature set)
- [ ] Hidden names resolve exactly like never-registered names at the single validation choke point, for every entry point
- [ ] Hidden request slots refused like unknown fields under strict decode
- [ ] `feature_set_digest` present on every instance, including the default

### FP4 — Self-description
- [ ] Instance-scoped manifest, payload schema (`p.PayloadSchema()`), predict and errors list
- [ ] Profile goldens for each example profile
- [ ] `TestProfileDefaultIsFull`: no profile produces output byte-identical to today

### FP5 — Skills & ontology
- [ ] Ontology graph (intents → operators → skills / examples / glossary / NotFor edges), pruned once at `pulse.New`
- [ ] List, search, `## See`, intents, recommendations **and exact-name get** all honour the pruned graph
- [ ] Progressive-disclosure rewrite of the topical skills (paired with G2)
- [ ] `<!-- feature: … -->` fence syntax and rendering, plus `TestSkillsCoverFeatureFences` (report-only until the rewrite is done, then failing)
- [ ] `TestSkillsCoverProfileGet`: every hidden skill or example is indistinguishable from a nonexistent name

### FP6 — MCP
- [ ] Instance-scoped registration of tools, prompts and resources
- [ ] Tool input schemas carry the instance's enums only
- [ ] `TestProfileInvisibilityParity`

### FP7 — Embedder tooling & export
- [ ] `pulse profile init`, `check`, `diff` and `show`
- [ ] Example profile files in `examples/profiles/`
- [ ] `pulse docs export` / `p.ExportReference` (shares the G3 generator)
- [ ] Embedder docs at `docs/src/library/feature-profiles.md`; `.claude/reference/feature-profiles.md`

---

## 3. Guided analysis — metadata core

### G1 — Metadata core
- [ ] Intent taxonomy (`descriptor/intents.go`), projected to manifest `intents[]`
- [ ] `Purpose` type: plain line, intents, questions, use cases, NotFor, assumptions, level, glossary links
- [ ] `Interpretation` type: per-output meaning, labelled bands, conventions, caveats; shared p-value rules
- [ ] Glossary registry (about 60 terms) and the `pulse-skill://glossary` resource
- [ ] Extension `Purpose` hook
- [ ] Gates, starting report-only: `TestSkillsCoverAllPurposes`, `TestPurposeAlternativesResolve`, `TestPurposeQuestionsResolve`, `TestGlossaryTermsResolve`, `TestInterpretationCoversOutputs`
- [ ] `TestManifestGuidanceBudget`: no guidance prose in default payloads; manifest growth stays under about 4 KB

### G2 — Back-fill (statistics reviewer signs off before the gates flip)
- [ ] Tests (`TEST_*`)
- [ ] Overlays (`OVERLAY_*`)
- [ ] Regressions (`REG_*`)
- [ ] Aggregators (`AGG_*`)
- [ ] Attributes, filterers, groupers, windows and features
- [ ] Synth distributions
- [ ] Gates flipped from report-only to failing

---

## 4. Statistical integrity

### Weighting ([statistical-integrity 01](v1.0.0-statistical-integrity/01-weighting.md))
- [ ] `Request.Weight`, per-slot `weight` (incl. `null`), `Options.DefaultWeight`, `kind` frequency / probability
- [ ] Weight validation, `n_weight_invalid`, `PULSE_WEIGHT_INVALID_ROWS`
- [ ] Weighted aggregators incl. percentiles; `AGG_WEIGHTED_MEAN` alias
- [ ] Weighted crosstab cells and margins; unweighted base via `margin_aggregations`
- [ ] Weighted share / index overlays
- [ ] Weighted tests and significance overlays with Kish `n_eff`
- [ ] Weighted attributes, `GROUP_QUANTILE`, regressions
- [ ] Components `w_sum` / `n_eff`; manifest `weight_aware`; predict reporting; extension `WeightAware`
- [ ] SPSS weight-variable capture and suggestion
- [ ] `TestWeightUnityParity`, `TestWeightFrequencyExpansionParity`, reference fixtures; `weighting.md` skill

### Multiple comparisons ([statistical-integrity 02](v1.0.0-statistical-integrity/02-multiple-comparisons.md))
- [ ] `processing/multiplicity`: Bonferroni, Holm, BH, BY
- [ ] `multiplicity {method, family}` on Request / OverlaySpec / Test / MatrixSpec; `Options.DefaultMultiplicity` (shipped `none`; correction is opt-in)
- [ ] Families `layer` / `row` / `column` / `request` / `matrix`, incl. across Compose slots
- [ ] Additive `p_adjusted` / `significant_adjusted` / `multiplicity` outputs
- [ ] Advisory + Explain hooks; glossary terms
- [ ] Reference-value, identity and family-boundary gates; `multiple-comparisons.md` skill

---

## 5. Time zones ([time-zones 00](v1.0.0-time-zones/00-design.md))

- [ ] Step 0: `encoding/temporal`; migrate the six open-coded epoch-day sites; `TestNoZoneMathOutsideTemporal`
- [ ] `Zone` type, embedded tzdata, transition-table fast path
- [ ] `Options.DefaultTimeZone`, `Request.TimeZone`, per-slot `tz`; `date`-field rejection
- [ ] Zone-aware `GROUP_DATE`, `GROUP_DATE_RANGES`, `FILTER_DATE_RANGES`, `ATTR_DATE_PART`, `FEAT_DATE_FEATURES`, `OVERLAY_YOY`, range tables, `week_start`
- [ ] Import `--source-tz` with `--dst-policy`
- [ ] Zone-aware output rendering; predict and manifest reporting (tzdata version)
- [ ] `TestUTCZoneIsIdentity`, `TestDSTBoundaries`, `TestDateFieldRejectsTZ`; `time-zones.md` skill

---

## 6. Vector & matrix — foundation

### E1 — Linear-algebra core & co-moment accumulator
- [ ] `linalg/` with Cholesky, SymEigen, SVD, QR, tolerances, sign convention and ordering; import-boundary gate
- [ ] Synth Cholesky migrated onto `linalg` (fidelity goldens byte-identical)
- [ ] Regression solve and inverse migrated onto `linalg`
- [ ] Weighted co-moment accumulator (listwise and pairwise) with exact merge; property tests
- [ ] Deterministic merge tree under `ShardWorkers` / `DecodeWorkers`

### E2 — Virtual vectors & matrix result
- [ ] `Request.Vectors`: resolution, hashing, projection, predict echo
- [ ] `Request.Matrices` / `Response.Matrices`, `MatrixResult`, and the symmetric/upper-triangle `MatrixPayload` encoding
- [ ] Matrix components floor; manifest `Matrix` capability block; predict shape and cost
- [ ] Payload-schema golden regenerated; `.claude/reference/matrix-and-vectors.md`
- [ ] `MaxMatrixDim`, `precision` and `top_pairs` controls
- [ ] `MAT_COVARIANCE`
- [ ] `MAT_CORRELATION` (Pearson), with parity against `TEST_PEARSON_R`

---

## 7. Guided analysis — docs, API & MCP

### G3 — Generated docs & skill sections
- [ ] `internal/docgen`, `make docs` integration, `TestDocsGeneratedCurrent`
- [ ] Generated operator catalog pages
- [ ] Generated glossary page
- [ ] "Reading your results" pages (tests, regressions, matrices, overlays, components)
- [ ] Rendered skill sections `## Use when` / `## Reading the output`; `skill-pack.md` updated, plus `TestSkillPurposeSectionsCurrent`

### G4 — Recommend, Explain, advisories
- [ ] `pulse.Recommend` with bound (cohort) and unbound (cohort-free) modes; `pulse recommend`; `pulse_recommend`; `tool-recommend.md`
- [ ] `pulse.Explain` request mode; terse by default, `detail: "full"` on request
- [ ] `pulse.Explain` response mode; `pulse explain`; `pulse_explain`; `tool-explain.md`
- [ ] Predict `advisories`, with codes and fixups
- [ ] Explain goldens per operator family; Recommend goldens per intent

### G5 — MCP guidance layer
- [ ] One MCP prompt per intent (extending `pulse-bootstrap` / `pulse-author-request`), plus the prompt gate
- [ ] `pulse_examples_search {intent}` and question search; synonym table
- [ ] `pulse_skills_list {intent}`
- [ ] Intent-scoped manifest (`pulse_manifest {intent}`)
- [ ] MCP tool descriptions lead with when to call the tool

### G6 — Guides
- [ ] "What can Pulse answer?" landing page
- [ ] Question guides, one per intent: describe, compare groups, relationships, drivers, change over time, composition, benchmark, distribution, segment, measure construct, flows, data quality
- [ ] Story examples: `_meta.intent` / `question` / `interpretation`, at least one per intent
- [ ] Vector & matrix concept primers: correlation matrix, PCA, distance vs similarity, perceptual maps

---

## 8. Response shaping ([response-shaping 00](v1.0.0-response-shaping/00-design.md))

- [ ] `Request.Return {preset, include, exclude, precision}`; `Options.DefaultReturn` (library default `full`)
- [ ] MCP default `standard`: `gosdk.Config.DefaultReturn`, `pulse mcp --return`; MCP goldens regenerated once; release-note callout
- [ ] Path grammar over the response schema; predict-time validation; `PULSE_RETURN_PATH_UNKNOWN`
- [ ] Presets `full` / `standard` / `minimal` listed in the manifest
- [ ] Selection compiled into the execution plan (unrequested parts not computed)
- [ ] Float precision control; old switches documented as shorthands; `returned` marker
- [ ] Predict per-section size estimates
- [ ] `TestReturnFullIsIdentity`, `TestReturnSkipsComputation`, `TestReturnPathsMatchSchema`; `response-shaping.md` skill

---

## 9. Embedder operations

### Resource limits ([embedder-operations 01](v1.0.0-embedder-operations/01-resource-limits.md))
- [ ] `Options.Limits` with high defaults; validation at `pulse.New`
- [ ] Predict-time checks + `PredictResult.LimitFindings`
- [ ] Runtime checks (groups, crosstab cells, join build, matrix dim, compose / chain fan-out, memory estimate, timeout)
- [ ] `PULSE_LIMIT_EXCEEDED` with tuning fixups
- [ ] Profile-file `limits` section; manifest `limits` block; `pulse mcp --limit`
- [ ] Defaults-never-trip, predict/runtime parity and memory-release gates; "Tuning limits" docs page

### Observability ([embedder-operations 02](v1.0.0-embedder-operations/02-observability.md))
- [ ] `Options.Logger` (`slog`, nil = silent); context-aware; no row data
- [ ] `Options.Hooks`: operation start/end (context-returning), phase timings; panic-safe
- [ ] `Options.Metrics` interface (opt-in) with bounded labels
- [ ] `contrib/otelpulse` and `contrib/prompulse` as separate modules
- [ ] `pulse mcp` / CLI `--log-level`, `--log-format`, opt-in `--metrics-addr`
- [ ] Silence, no-row-data, dependency and panic gates; "Observability" docs page

---

## 10. Vector & matrix — operators

### E3 — Core matrix operators
- [ ] `MAT_CORRELATION` Spearman / Kendall
- [ ] `MAT_PARTIAL_CORRELATION`
- [ ] `MAT_RELIABILITY` (α, standardized α, ω, item-total, α-if-deleted, reverse scoring)
- [ ] `MAT_PCA` (loadings, eigenvalues, explained variance, KMO, Bartlett)
- [ ] `MAT_COLLINEARITY` (VIF, tolerance, condition indices)
- [ ] `RegressionResult.Vcov` and `.Correlation`
- [ ] Topical skill `multivariate-design.md`

### E4 — Multivariate tests, fitted attributes, segmentation
- [ ] `TEST_HOTELLING_T2`
- [ ] `TEST_MANOVA` (Wilks, Pillai, Hotelling–Lawley, Roy)
- [ ] `TEST_BARTLETT_SPHERICITY`
- [ ] `ATTR_MAHALANOBIS`
- [ ] `ATTR_PC_SCORE` (two-pass, `fit_on`)
- [ ] `GROUP_KMEANS` (seeded k-means++, Euclidean-only, size-ordered labels)

### E5 — Row-wise vector vocabulary & similarity
- [ ] expr-lang vector bindings, `v[i]` and `len`; `vsum` / `vmean` / `vmin` / `vmax` / `vsd` / `vcount`; `dot` / `norm` / `dist` / `cosine`; `argmax` / `argmin`
- [ ] Scalar maths functions `sqrt`, `log`, `exp`, `abs`, `pow`
- [ ] Centering functions `vcenter`, `vzscore`, `vnormalize`
- [ ] Set similarity `jaccard`, `dice`, `hamming`, `overlap` on `set_*` fields
- [ ] Shared metric registry `linalg/metric`
- [ ] Vector `kind` (`measure` / `scale` / `composition` / `binary`) with metric defaults and `PULSE_VECTOR_METRIC_UNSUITED`
- [ ] `ATTR_SCALE_SCORE`
- [ ] `AGG_VEC_MEAN`, `AGG_VEC_SUM` (array value, `expand` option)

### E6 — Matrix operations on results (overlays)
- [ ] MATRIX_RESULT overlay host and the `Ref.Matrix` reference family
- [ ] `OVERLAY_STD_RESIDUAL`
- [ ] `OVERLAY_CORRESPONDENCE`
- [ ] `OVERLAY_MARKOV`
- [ ] `OVERLAY_RAKE`
- [ ] `MatrixSpec.multiplicity` → `p_adjusted` auxiliary matrix via the shared correction core (replaces the dropped `OVERLAY_CORR_PVALUE`)

### E7 — Native vector field types
- [ ] `vec_f32` / `vec_f64` encoding and the `VECTORS` schema extension section (tag 2); `ReadVector` accessor
- [ ] Compatibility test: older cohorts unchanged; older binaries refuse loudly
- [ ] Import `--vector` folding (CSV, NDJSON, Arrow, Parquet)
- [ ] Export expansion; shard cohesion; parent-group membership
- [ ] `type-vec-f32.md`, `type-vec-f64.md`, `byte-layout.md`, CLAUDE.md byte-layout invariants (20 → 22 types)

### E10 — Extensions & hardening (committed part)
- [ ] `MatrixOpRegistration` and the `MAT` naming-policy namespace; probe validation
- [ ] Benchmarks: p = 50 / 256 at 1M and 10M rows; peak heap; shard scaling
- [ ] Examples-library entries for every new operator

---

## 11. Release v1.0.0

- [ ] `STABILITY.md` published at the repo root, with the final public package list ([api-and-release 02](v1.0.0-api-and-release/02-stability-policy.md))
- [ ] Release candidate tag (`v1.0.0-rc.1`) built through the release pipeline and exercised by the downstream library
- [ ] `v1.0.0` tagged

---

## 12. Cross-cutting (applies throughout; tick when verified for the whole release)

- [ ] Every new operator in every theme is weight-aware (or explicitly refuses a weight) and multiplicity-aware where it emits p-values
- [ ] Missing-data modes documented and tested; PSD refusal / `repair: "nearest"`
- [ ] New `PULSE_MATRIX_*` / `PULSE_VECTOR_*` / `PULSE_OVERLAY_*` / `PULSE_PROFILE_*` / `PULSE_LIMIT_*` / `PULSE_WEIGHT_*` / `PULSE_RETURN_*` / advisory codes all have `codeMetadata` + fixups
- [ ] Every new operator has `Purpose`, `Interpretation` (if inferential), `Since`, dependency edges and an atomic skill
- [ ] Every new gate is listed by name in CLAUDE.md "Non-Skippable CI Gates"
- [ ] The Update Demand table has rows for: `Purpose`, `Since` / dependencies, topical-skill fences, `Request.Vectors` / `Matrices`, `Response.Matrices`, `Request.Weight` / `Multiplicity` / `TimeZone` / `Return`, `Options.Limits` / `Logger` / `Hooks` / `Metrics`
- [ ] New env vars and CLI flags documented (CLAUDE.md "Build / Env", `flags.md`, `session-bootstrap.md`)
- [ ] CLAUDE.md stays at or under 50,000 bytes (long form moved to `.claude/reference/`)
- [ ] `format_version` remains `"1.1"` (every wire change additive)
