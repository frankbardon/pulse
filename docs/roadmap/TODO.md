# v1.0.0 TODO

Every **committed** v1.0.0 feature across the three roadmap themes. Stretch and post-1.0 items are deliberately excluded; they stay in the theme documents.

**How to use this file:**
- Tick a box (`- [x]`) in the same PR that completes the item.
- An item counts as complete only when its code, tests, skills, CLAUDE.md / `.claude/reference/` companions and docs have all landed, as the Update Demand requires.
- Sections are in build order. Feature profiles and the guided-analysis metadata core come first, so every later surface is profile-filtered and carries purpose metadata from its first commit.

Theme documents: [vector & matrix](v1.0.0-vector-matrix/00-overview.md) · [guided analysis](v1.0.0-guided-analysis/00-overview.md) · [feature profiles](v1.0.0-feature-profiles/00-feasibility.md)

---

## 1. Feature profiles — foundation

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

## 2. Guided analysis — metadata core

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

## 3. Vector & matrix — foundation

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

## 4. Guided analysis — docs, API & MCP

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

## 5. Vector & matrix — operators

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
- [ ] `OVERLAY_CORR_PVALUE` (Bonferroni, Holm, BH, BY)

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

## 6. Cross-cutting (applies throughout; tick when verified for the whole release)

- [ ] Weighting supported on every `MAT_*`, multivariate test, vector attribute/aggregator and `GROUP_KMEANS`
- [ ] Missing-data modes documented and tested; PSD refusal / `repair: "nearest"`
- [ ] New `PULSE_MATRIX_*` / `PULSE_VECTOR_*` / `PULSE_OVERLAY_*` / `PULSE_PROFILE_*` / advisory codes all have `codeMetadata` + fixups
- [ ] Every new operator has `Purpose`, `Interpretation` (if inferential), `Since`, dependency edges and an atomic skill
- [ ] Every new gate is listed by name in CLAUDE.md "Non-Skippable CI Gates"
- [ ] The Update Demand table has rows for: `Purpose`, `Since` / dependencies, topical-skill fences, `Request.Vectors` / `Matrices`, `Response.Matrices`
- [ ] CLAUDE.md stays at or under 50,000 bytes (long form moved to `.claude/reference/`)
- [ ] `format_version` remains `"1.1"` (every wire change additive)
