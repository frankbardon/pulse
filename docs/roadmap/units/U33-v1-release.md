---
id: U33
slug: v1-release
title: "Pulse v1.0.0 is released with a written stability promise"
track: API & release
size: XL
status: not-started
depends_on: [U32, U32b, U32c]
soft_depends_on: [all other units]
blocks: []
todo_items: [168, 169, 170, 171, 173, 174, 175, 176, 177, 178, 179, 206, 220, 224, 228, 232, 233, 234, 311, 464, 469, 497, 506, 507, 508, 526, 588, 589, 590, 591, 592, 593, 660, 661, 662, 663, 664, 665, 684, 723, 735, 736, 738, 739, 740, 741, 742, 743, 744, 745, 746, 747, 748, 749, 756, 767, 769, 770, 771, 772, 773, 774, 776, 777, 778, 779, 780, 781, 782, 783, 784, 785, 786, 787, 788, 789, 790, 791, 792, 793, 794, 795, 796, 797, 798, 802, 803]
branch: v1-release
---

# U33 — v1-release

**Outcome:** Pulse v1.0.0 is released with a written stability promise.

**Track:** API & release · **Size:** XL · **Depends on:** [U32](U32-docs-audit.md), [U32b](U32b-docs-content.md), [U32c](U32c-docs-validation.md) · **Soft:** all other units · **Unblocks:** —

## Summary

Publish `STABILITY.md` with the final public package list, cut `v1.0.0-rc.1` through the release pipeline and validate it against the downstream library, run the cross-cutting verification sweep, then tag `v1.0.0`.

## References

**Theme documents (read before starting):**
- [api-and-release 02 — Stability policy draft](../v1.0.0-api-and-release/02-stability-policy.md) — STABILITY.md draft
- [api-and-release 01 — Release pipeline & versioning](../v1.0.0-api-and-release/01-release-pipeline.md) — Release workflow

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [ ] **#168** (12. Release v1.0.0) `STABILITY.md` published at the repo root, with the final public package list ([api-and-release 02](../v1.0.0-api-and-release/02-stability-policy.md))
- [ ] **#169** (12. Release v1.0.0) Release candidate tag (`v1.0.0-rc.1`) built through the release pipeline and exercised by the downstream library
- [ ] **#170** (12. Release v1.0.0) `v1.0.0` tagged
- [ ] **#220** (12. Release v1.0.0) Decide the root alias for `types.ReturnedMarker` (`pulse.ReturnedMarker`) before the API freeze
- [ ] **#660** (1. API surface & release pipeline) Export the range-table parser (`pulse.ParseRangeTableFile`, the `PULSE_RANGE_TABLES_DIR` loader's own parser — `parseRangeTableFile` in `range_loader.go`, unexported today) so an external tool that mirrors the range-table file format can drop its copy, as `pulse.ParseLookupTableFile` already allows for lookup tables. Additive (API golden, migration doc); decide before the freeze whether it ships in v1.0.0 or a later minor. Found in lookup-tables-loader (E3-S3)
- [ ] **#756** (1. API surface & release pipeline) The label-, lookup-, range-table and populations directory loaders (`label_loader.go`, `lookup_loader.go`, `range_loader.go`, `population_loader.go`) read `PULSE_*_DIR` / `Options.*Dir` through `os` (`os.ReadDir` / `os.ReadFile` / `os.Stat`), so an embedder on an in-memory `Options.FS` cannot use them and their tests write host temp dirs. Thread the same Fs the template store uses (`templateFs`: `Options.FS` when set, else `afero.NewOsFs()`), migrate their tests to `afero.NewMemMapFs()`, and update `.claude/reference/byte-layout/table-directories.md` and `docs/src/library/options.md`. Found in docs-audit (E3-S4)
- [ ] **#767** (1. API surface & release pipeline) `make sites` has no mode for an I/O-path change: add an `io` mode in `internal/tools/sites` that finds a reader / importer change's companions by shape (format readers, `internal/io` import / convert / widen, byte-layout and io-import slices, import skills, migration doc), with a falsified fixture test and a recipe line. Found in docs-content (E4-S1)
- [ ] **#769** (7. Guided analysis — docs, API & MCP) `pulse cohort inspect` text mode prints no `Zone field:` line for a cohort's default zone field (`--json` carries `zone_field`)
- [ ] **#770** (1. API surface & release pipeline) `pulse completion` does not complete the COHORT positional of `pulse cohort zone set` / `clear` (missing from the cohort-positional table)
- [x] **#771** (12. Release v1.0.0) Register the emitted-but-unregistered codes `CLI_ERROR` and `PULSE_SHARD_AGGREGATE_DRIFT`, and gate every emitted `DOMAIN_*` literal against the registry
- [x] **#772** (11. Documentation audit) Decide whether `ComposeOptions.FailFast` should default to true in the library (the CLI already fails fast); a change needs an explicit opt-out and a migration-doc row. **Done (pre-v1 E1-S1): replaced by `ComposeOptions.CollectErrors`; the zero value fails fast; `--no-fail-fast` sets `CollectErrors`.**
- [x] **#773** (11. Documentation audit) `WatchDir` cannot watch every file in a directory (an empty `Suffix` means `.pulse`); add a way to
- [ ] **#774** (11. Documentation audit) `make sites` has no field-type, error-code or MCP-tool target; add those modes with falsified fixture tests and recipe lines
- [ ] **#776** (11. Documentation audit) Decide whether the `PULSE_TEMPLATES_DIR` loader skips Pulse sidecars like the label, lookup and range-table loaders
- [ ] **#777** (11. Documentation audit) The `standard` return preset's regression key list drops keys added since; decide membership, align the list, regenerate the manifest golden
- [ ] **#778** (11. Documentation audit) Pass an extension filterer's params to `FieldInputs` on the runtime and predict sides, with a parity test
- [ ] **#779** (11. Documentation audit) `AGG_RATIO` requires and type-checks a `field` it never reads; align manifest, refusal and skill or stop requiring it
- [ ] **#780** (11. Documentation audit) Predict ≡ runtime on a formula arity fault (`to_day()`)
- [ ] **#781** (11. Documentation audit) Predict runs the operator config checks the runtime refuses on (eight operator families + overlay baseline position), with parity tests
- [ ] **#782** (11. Documentation audit) `WIN_FORMULA` accepts the wire spelling of a Welford cell (`w.mean`)
- [ ] **#783** (11. Documentation audit) Decide the manifest `tests[].requires` spelling (Go names vs wire keys)
- [ ] **#784** (11. Documentation audit) Correct or enforce `FEAT_*` `emits_type`
- [ ] **#785** (11. Documentation audit) `pulse_facet` renders distinct values per field type
- [ ] **#786** (11. Documentation audit) Inferred `set_*` rungs promote at import instead of dropping rows
- [ ] **#787** (11. Documentation audit) `REG_MMM_BAYES` diagnostics in a named Compose fit name the fit and count its parameters right
- [ ] **#788** (11. Documentation audit) Decide whether `ATTR_DATE_PART` hour / weekday keys sort numerically
- [ ] **#789** (11. Documentation audit) A facet overlay's `ref.population.cohort` resolves through `data_dir` / `PULSE_DATA_DIR`
- [ ] **#790** (11. Documentation audit) `WIN_SHARE` / `WIN_LIFT` / `WIN_FORMULA` read a `decimal128` aggregation instead of a silent null
- [ ] **#791** (11. Documentation audit) `OVERLAY_CHISQ_VS_REF` gets a homogeneity mode or a sample-reference warning
- [ ] **#792** (11. Documentation audit) A CLI route embeds overlay layers in an export
- [ ] **#793** (11. Documentation audit) A crosstab `AGG_COUNT` cell with no `field` is refused instead of returning zeros
- [ ] **#794** (11. Documentation audit) The manifest's overlay `ref_kinds` lists wire arm names
- [ ] **#795** (11. Documentation audit) Predict's `defaults_applied[].path` uses wire keys
- [ ] **#796** (11. Documentation audit) Registered codes are emitted or reserved, by a gate
- [ ] **#797** (11. Documentation audit) `Fixup.Path` pointers use wire keys
- [ ] **#798** (11. Documentation audit) The SPSS cardinality-warning threshold is public or kept internal by decision
- [x] **#802** (11. Documentation audit) The simulate intent gets an MCP synth tool, or stays CLI / library-only by decision **Resolved 2026-11-27 by owner decision: no MCP synth tool; simulate stays CLI / library-only.**
- [ ] **#803** (11. Documentation audit) Example search gains a plain-word synonym tier
- [ ] **#661** `STABILITY.md` package list rebuilt from the public-API golden (`internal/apigolden/testdata/public_api.txt`), not the U02 draft: adds `linalg`, `linalg/detmath`, `linalg/metric`, `optimize`, `mcmc`, `observe`, `observe/otel`, `observe/prom`. MCMC, MMM, SEM and `optimize` are core and frozen; there is no experimental tier (owner decision 2026-11-02). Evidence: `v1.0.0-api-and-release/02-stability-policy.md` "Covered" table · [U33](U33-v1-release.md)
- [ ] **#662** Manifest `deprecated: true` field, promised by the `STABILITY.md` draft's Deprecation section but unbuilt (no `deprecated` manifest field and no `// Deprecated:` GoDoc outside `internal/` to back it): build it, or cut the promise from the policy. Evidence: `v1.0.0-api-and-release/02-stability-policy.md` "Deprecation" · [U33](U33-v1-release.md)
- [x] **#663** Remove the deprecated public leftovers before the freeze: `ProjectBufferedFields` (`pulse.go:466`, no-op), the synth `Bernoulli` slot (`internal/synth/spec.go:288,357`, use `DistBernoulli`), and the always-nil `Steps` fields (`descriptor/recommend.go:74`, `internal/descriptor/schema.go:624`) **Done (pre-v1 E1-S3): removed, with the `REG_MMM_BAYES` `params.time` alias.** · [U33](U33-v1-release.md)
- [ ] **#664** One Go version support policy: README says Go 1.22+ (`README.md:26`), `docs/src/contributing/setup.md:21` says 1.24+, `go.mod` says 1.26.1 and CI tests only 1.26. Pick the policy, fix both pages and add a "Supported Go versions" row to `STABILITY.md` · [U33](U33-v1-release.md)
- [ ] **#665** A v1.0.0 release-notes assembly step: compile the release notes and an upgrade guide from the alpha.0–alpha.37 rows of `v1.0.0-api-and-release/03-embedder-migration.md` (there is no CHANGELOG; `release.yml` uses `--generate-notes`) · [U33](U33-v1-release.md)
- [ ] **#311** Acceptance: a table test misspells a key in every slot family and gets a coded refusal; all six showcase examples run under `--strict` with nothing ignored, and their scripts drop the `.errors | length` checks in favour of exit status · [U33](U33-v1-release.md) — *partial (E1-S4): in-pulse table test `TestStrictInput_EveryFamilyAndRootRefusesAMisspelledKey` (every derived slot family × every root, CLI predict + process and MCP predict + runtime, synth spec, every template target); the showcase-example run under `--strict` and the script changes remain, filed as #464 under U33*
- [ ] **#464** Casebook conversion for U44: the six showcase scripts drop their `.errors | length` checks for exit status and run under `--strict`; the brand tracker's crosstab label workaround goes (labels now reach axis and overlay keys); README and REPRODUCE.md expected numbers refreshed. Remainder of #311. Found in U44 (E1-S4, E2-S4) · [U33](U33-v1-release.md)
- [ ] **#469** Convert the `pulse-casebook` credit / A-B / privacy / retail examples to the U45 idioms (no `outcome` twin or `2.718… **`, binary metrics as `packed_bool`, direct `TEST_CHISQ` on the flag, `month_index` retention flags) and refresh each README and `REPRODUCE.md`. The in-pulse proof shipped in U45 (#317); the casebook repository is separate. Found in U45 (E4-S3) · [U33](U33-v1-release.md)
- [x] **#206** (12. Release v1.0.0) Human statistics sign-off (release-blocking): a named statistics reviewer works through the [U08 review record](../reviews/U08-statistics-review.md) and its open items (the U08 E3 / E4 sections and the U09 section) the [U12 weighted-inference review](../reviews/U12-weighting-review.md) (the `n_eff` semantics and every lifted weighted formula) and the [U24 matrix-operators review](../reviews/U24-matrix-operators-review.md) (findings MR-01 to MR-08 and the open owner calls) and the [U25 multivariate review](../reviews/U25-multivariate-review.md) (the open owner calls) and the [U30 flows review](../reviews/U30-flows-review.md) (its open items), signs off the U08 inferential AND the U09 descriptive guidance AND the U12 weighted inference AND the U24 matrix and U25 multivariate guidance, and owns the statistical-review CODEOWNERS entries Signed off 2026-10-31 by the owner (@frankbardon).
- [ ] **#171** (13. Cross-cutting (applies throughout; tick when verified for the whole release)) Every new operator in every theme is weight-aware (or explicitly refuses a weight) and multiplicity-aware where it emits p-values
- [ ] **#173** (13. Cross-cutting (applies throughout; tick when verified for the whole release)) New `PULSE_MATRIX_*` / `PULSE_VECTOR_*` / `PULSE_OVERLAY_*` / `PULSE_PROFILE_*` / `PULSE_LIMIT_*` / `PULSE_WEIGHT_*` / `PULSE_RETURN_*` / advisory codes all have `codeMetadata` + fixups
- [ ] **#174** (13. Cross-cutting (applies throughout; tick when verified for the whole release)) Every new operator has `Purpose`, `Interpretation` (if inferential), `Since`, dependency edges and an atomic skill
- [ ] **#175** (13. Cross-cutting (applies throughout; tick when verified for the whole release)) Every new gate is listed by name in CLAUDE.md "Non-Skippable CI Gates"
- [ ] **#176** (13. Cross-cutting (applies throughout; tick when verified for the whole release)) The Update Demand table has rows for: `Purpose`, `Since` / dependencies, topical-skill fences, `Request.Vectors` / `Matrices`, `Response.Matrices`, `Request.Weight` / `Multiplicity` / `TimeZone` / `Return`, `Options.Limits` / `Logger` / `Hooks` / `Metrics`
- [ ] **#177** (13. Cross-cutting (applies throughout; tick when verified for the whole release)) New env vars and CLI flags documented (CLAUDE.md "Build / Env", `flags.md`, `session-bootstrap.md`)
- [ ] **#178** (13. Cross-cutting (applies throughout; tick when verified for the whole release)) CLAUDE.md stays at or under 40,000 bytes (long form moved to `.claude/reference/`)
- [ ] **#179** (13. Cross-cutting (applies throughout; tick when verified for the whole release)) `format_version` remains `"1.1"` (every wire change additive)

## Scope

**In scope**
- STABILITY.md, with its package list rebuilt from the public-API golden (#661): `linalg`, `linalg/detmath`, `linalg/metric`, `optimize`, `mcmc`, `observe`, `observe/otel` and `observe/prom` join the draft's list. **No experimental tier**: MCMC, MMM, SEM and `optimize` are core and frozen (owner decision 2026-11-02)
- The manifest `deprecated` field (#662, build it or cut the promise from the policy), removal of the deprecated public leftovers before the freeze (#663), one Go version support policy with a "Supported Go versions" row (#664), and a v1.0.0 release-notes and upgrade-guide assembly step from the alpha.0–alpha.37 migration rows (#665)
- Casebook acceptance runs: the showcase scripts under `--strict` with exit-status checks (#311, #464) and the credit / A-B / privacy / retail conversions to the U45 idioms (#469) — the casebook repository is separate, the runs gate the rc
- Human statistics sign-off of the U08 review record, covering U08 inferential and U09 descriptive guidance, and of the U12 weighted-inference review (#206) — release-blocking
- rc tag + downstream validation
- Cross-cutting verification (TODO §13)
- v1.0.0 tag

**Out of scope**
- New features
- Documentation work (U32, U32b, U32c)

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|perf|test(v1-release/E<n>-S<m>): …`; close each epic with `milestone(v1-release/E<n>): vertical slice complete — <epic title>`.

### E1 — The promise is written
- S1: `STABILITY.md` at the root with the U02 package list

### E2 — Verified and released
- S1: cross-cutting sweep (§13 items)
- S1b: statistics sign-off (#206) — reviewer works the U08 review record's open items (U08 and U09 sections) and the U12 review's open items, records the sign-off in each record, lands CODEOWNERS
- S2: `v1.0.0-rc.1`; downstream library validation
- S3: `v1.0.0`

## Acceptance criteria

- [ ] Every TODO item is ticked, or explicitly moved to post-1.0 in the theme docs
- [ ] The rc builds through the release pipeline, and the downstream library passes its own tests against it
- [ ] The API-compat check is active against `v1.0.0`
- [ ] A named statistics reviewer's sign-off is recorded in `docs/roadmap/reviews/U08-statistics-review.md` and `docs/roadmap/reviews/U12-weighting-review.md` and `docs/roadmap/reviews/U24-matrix-operators-review.md`, every open item there (U08, U09, U12 and U24) resolved or deferred with a reason, and `.github/CODEOWNERS` routes the guidance registries to the reviewer
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- All non-skippable gates green; API-compat job enabled
- Before tagging any release: `gh api repos/frankbardon/pulse/private-vulnerability-reporting` returns `enabled: true`, since `SECURITY.md`'s only reporting channel is GitHub private vulnerability reporting; if it is `false`, the owner enables it in the repository settings before the tag
- Before tagging the rc and `v1.0.0`: rerun the agent evaluation on the release build ([Agent Evaluation](../../src/contributing/agent-eval.md)) and score it with `go run ./internal/tools/evalscore internal/eval/runs/<date>-r<N>`; every task passes

## Update Demand companions

- README links to STABILITY.md
- Release notes mention the MCP default `standard` (from U18)
- Release notes tell users to rebuild a sidecar index built on a pre-1970 `datetime` key before U03 (`pulse index build`); `date` keys are unaffected (`.claude/reference/byte-layout.md`, temporal word sign)

## Human inputs & decisions

- Maintainer runs the downstream validation and tags the release
- **Statistics reviewer** (maintainer sources one) signs off U08's inferential and U09's descriptive guidance and U12's weighted inference (#206); no `rc` is tagged before it

## Notes

- TODO item 172 (missing-data modes) is verified in U16. It is listed in the sweep for completeness.

## Inherited from U03 / U04

- **Pre-1970 `datetime` index rebuild** (U03, PR #301). This is the release-note line under Update Demand companions. The fix changed how such a key is encoded, so an older index on one keys on a rounded float echo.
- **Review `mcpserve.Describe` / `ServeInfo` before the freeze** (U04, PR #304). They exist only to get the profile name into `pulse mcp`'s startup line without a `*Pulse` accessor (`mcpserve/describe.go`). U05 then added `p.FeatureProfile()`. Keep them, or remove them in favour of the accessor, before `STABILITY.md` freezes `mcpserve`.
- **After the `v1.0.0` tag: relax `TestFeaturesHaveSince`.** It requires every built-in `Since` to equal `BuiltinFeatureSince` (`"1.0.0"`, `internal/descriptor/features.go`). The first feature added after the tag must relax the check to "parseable and ≤ the next release". Contract: `.claude/reference/feature-profiles.md`. This is post-release work, recorded here so it is not lost.

## Inherited from U08

U08 shipped Purpose + Interpretation for every `TEST_*`, `OVERLAY_*` and `REG_*` without a human reviewer. Its review was automated: binding deterministic gates (convention registry, prose lint, R oracle) plus a two-pass advisory LLM panel whose findings were all triaged and applied. The record is [`reviews/U08-statistics-review.md`](../reviews/U08-statistics-review.md). Sign-off (#206) starts there:

- **Panel independence.** Two passes of one model family; treat "no finding" as weak evidence. Prioritise what the gates cannot check: `NotFor` routing and assumption completeness.
- **Convention choices.** `very small` as the label below Cohen's small; the Chinn (2000) odds-ratio conversion; Cohen 1992 f² → R² for `REG_OLS` `r2`; rank-biserial, d_z, RM partial η², rank ε², Cramér's V, ρ and τ left unbanded.
- **Decisions the record leaves open.** `OVERLAY_PAIRWISE_PROBIT_T` is anti-conservative by design (keep inferential-flagged, warn, or hide behind a feature profile); `OVERLAY_CHISQ_VS_POP` compared only the listed categories (E4-5, fixed in vibe-2026-10-31: a population category with no subset rows now enters at observed 0); the Shiffler (1988) citation for the ZSCORE_VS_TOTAL bound was not fetched; penalised-OLS p-values are plug-in approximations.
- **`OVERLAY_INDEX_VS_MARGIN` declared scopes.** The skill lists `row` / `column` scopes and a SERIES output; the capability declares only `cell` / MATRIX. Not verified against the scope gate in U08 — resolve which is right before sign-off.
- **Numeric correctness is U36's**, not the reviewer's: the runtime bugs the record lists (infinite Fisher OR / correlation `t`, Brown–Forsythe at zero spread, Shapiro n < 5; the inverse-erf CI was fixed by U12) and the per-output oracles land in [U36](U36-reference-oracles.md) before this unit.
- **CODEOWNERS.** Point the reviewer's entries at the landed registries: `internal/descriptor/{purposes,purposes_*,interpretations,interpretations_*,conventions,glossary,intents}.go`, `internal/descriptor/testdata/conventions.json`, the effect-size code in `internal/processing/test_*.go`, `internal/statdist/` and `scripts/reference/`. `.github/CODEOWNERS` carries only the default `*` owner today.

## Inherited from U09

U09 gave every descriptive built-in (aggregators, attributes, windows, features, filters, groupers, synth distributions) a Purpose, and every needs-reading one a `value` / `value.*` Interpretation. Its review used the same pattern as U08: binding gates plus an advisory LLM panel (scope A in two passes, scope B in one combined pass). The maintainer accepted all 64 findings and applied them. The record is the **U09 section of the same file**, [`reviews/U08-statistics-review.md`](../reviews/U08-statistics-review.md). #206 signs off both units there:

- **Open items.** Work the "Open items for the U33 human reviewer (U09)" list. They are runtime behaviours the guidance describes honestly; [U36](U36-reference-oracles.md) owned few of them. Status at the stats-review re-check:
  - **Fixed:** 0 for empty groups (U09-2, [U30](U30-matrix-extensions-hardening.md)); text-order group rows (U09-3, [U49](U49-model-workbench.md)); 0 / 50 for missing attribute inputs (U09-7, [U50](U50-local-time.md)); numeric groupers accepting category fields (U09-10, [U35](U35-predict-runtime-parity.md)); no external oracle for the descriptive estimators (U09-12, [U11](U11-weighting-descriptive.md) / [U12](U12-weighting-inferential.md)).
  - **Partly fixed:** the split-blind target encoder (U09-8, [U49](U49-model-workbench.md)). `params.fit_on` and out-of-fold encoding are opt-in; the default stays leaky and warns `PULSE_FEAT_TARGET_LEAKAGE_RISK`. Sign off the opt-in default, or change it.
  - **Resolved at sign-off (2026-10-31, kept as is):** truthy filters on category labels (U09-1, #588), `AGG_ZSCORE`'s shape (U09-4, #589), normal-not-t CI bounds (U09-5, #590), whether `ATTR_PERCENTILE` ties share a mid-rank (U09-6, #591; the tie ORDER became deterministic, scan order, in vibe-2026-10-31), and synth `u4` wraparound (U09-9, #592).
- **Unbanded shape readings.** Skewness, kurtosis and the descriptive z / T scores carry no bands (fixture `excluded`). Confirm that is right.
- **CODEOWNERS** covers the descriptive registries too (`purposes_*.go` and `interpretations_*.go` already match the globs above).

## Inherited from U12

U12 made the significance tests, regressions, CI bounds, scores, quantile buckets and inferential overlays weight-aware on one rule (N* = Σw or Kish n_eff; the frequency formula on w*). It ran without a human reviewer: binding gates (unity byte parity, exact frequency expansion against stock R, probability scale invariance, external references) plus a one-pass advisory LLM statistician panel. The record is [`reviews/U12-weighting-review.md`](../reviews/U12-weighting-review.md). #206 signs it off there:

- **Open items.** Work its "Open items for the U33 human reviewer (U12)" list. The engine findings that headed it are FIXED: WS-01 (one-way ANOVA F under between-group probability-weight variation, `a18ca0bd`) and the undefined-figure cases at tiny n_eff (WS-05 `216b6903`, WS-12 `e171e014`); the docs story applied the documentation findings. What remains:
  - **Reviewer decisions:** WS-04 and WS-03 / WS-02.
  - **WS-13, fixed in vibe-2026-10-31:** `TEST_PROP_Z` now warns `PULSE_TEST_EXPECTED_COUNT_TOO_LOW` when a scaled expected count falls below 5, as `TEST_CHISQ` does.
  - **WS-10, fixed in vibe-2026-10-31 (#593):** `TEST_KS`'s asymptotic branch (Σw₁·Σw₂ ≥ 10000; row test and `two_sample_post`) now warns `PULSE_TEST_TIES_DOMINATE` on the rank tests' ≥ 50% rule, weighted or unweighted; the tie-conditional exact branch stays silent.
- **The method itself.** No external software implements Kish-w* inference, so the probability arm is verified for arithmetic and scale invariance only. Sign off (or not) the framing in `.claude/reference/weighting.md` (Weighted inference): unequal-weighting correction only, weights assumed unrelated to the outcome, not design-based, χ² first-order Kish not Rao-Scott.
- **CODEOWNERS.** Add `internal/weighting/`, the weighted paths of `internal/processing/test_*.go` / `regression/` / `overlay*.go`, and `internal/service/testdata/weight_reference/` to the reviewer's entries.

## Inherited from U24

U24 shipped the multivariate matrix operators without a human reviewer: an external R oracle (psych, car, ppcor, perturb, corpcor, Matrix) pins every output at the tolerance in the record, plus a one-pass advisory LLM panel on the new guidance text.

- **Open items.** Work the "Open items for the U33 human reviewer (U24)" list in [`reviews/U24-matrix-operators-review.md`](../reviews/U24-matrix-operators-review.md): findings MR-01 to MR-08, the omega form and Heywood handling. The owner calls (a) to (f) were decided 2026-10-10 and are recorded there.
- **CODEOWNERS.** Add `internal/processing/matrix_*.go` and the `mv_*.json` goldens under `internal/processing/testdata/reference/` to the reviewer's entries.

## Inherited from U17

- **`pulse.ReturnedMarker` root alias** (#220). `Response.Returned` is a `*types.ReturnedMarker`, reachable through `types`; the root facade aliases most result types but not this one. Decide before the freeze: adding it changes the public-API golden (`TestPublicAPIGolden`, regenerate via `-update`) and `STABILITY.md`; leaving it out is fine if embedders reach the type through `types` (record the decision in the migration doc either way).

## Inherited from U19

- **`FailFast=false` discards successful slots** (#224). `ComposeParallel` with `FailFast=false` returns `(nil, SERVICE_INTERNAL)` with `failed_indices` / `first_error` details, dropping every slot that succeeded. U19 corrected `docs/src/library/parallel-compose.md`, which claimed the successful slots populate the response. Decide before the freeze whether to return the partial `ComposedResponse` (a behaviour change for `STABILITY.md` and the migration doc) or keep the all-or-nothing contract and say so in `STABILITY.md`. *Resolved by pre-v1 E1-S1:* kept all-or-nothing (`(nil, err)` in both modes, no partial slots), stated in the `ComposeOptions` / `ComposeParallel` GoDoc, `docs/src/library/parallel-compose.md`, `docs/src/cli/api-compose.md` and the migration guide.
- **`RequestTimeout` and the RowIter drain** (#228). U19 bounds `ProcessStreamResult` across run and drain (a stalled consumer trips it), but `Pulse.ProcessStream` returns a `RowIter` whose `Next(ctx)` drain runs under the caller's ctx only. Decide the frozen contract: keep (document it in `STABILITY.md` and the Tuning limits page) or carry the inner deadline into the iterator.

## Inherited from U20

- **Lockstep contrib release rehearsal** (#232). *Resolved by observability-adapters:* the adapters moved into the root module as `observe/otel` / `observe/prom`, so there are no contrib tags, no `scripts/release-contrib.sh` and no `contrib` release job left to rehearse; nothing for the release runbook.
- **`ServeInfo` comparability** (#233). `mcpserve.ServeInfo` gained `Limits []descriptor.LimitMeta`, so it is no longer `==`-comparable (an apidiff advisory). Keep the slice or switch to `pulse.Limits` before the freeze; record the choice in the migration doc and `STABILITY.md`.
- **Uninstrumented methods** (#234). `CohortArtifacts`, `NewCohortBuilder`, `Imports`, `ResolveImport`, `ResolveCanonicalSchema`, `ApplySeriesOverlays`, `InvalidatedSidecars` and `Watch*` have no `OperationKind` (`uninstrumentedMethods` in `observability_test.go`). Confirm the list before the `observe` enums freeze.

## Inherited from U38

Filed from [U38 handed on](U38-skill-sync.md#handed-on); tick in [`TODO.md`](../TODO.md) when delivered.

- [ ] **#506** — **A non-positive `interval` is silently 1.** `GROUP_RANGE` / `GROUP_ROUNDED` treat an explicit `interval ≤ 0` as 1 at predict and at runtime. U38 corrected the manifest to say so (`interval` optional, default 1) and left the behaviour alone (out of its scope). Decide before the freeze: refuse an explicit `interval ≤ 0` (a behaviour change for `STABILITY.md` and the migration doc, with a predict ≡ runtime test) or keep the coercion and state it in `STABILITY.md`.

## Inherited from U37

Filed from [U37 handed on](U37-shell-completion.md#handed-on); tick in [`TODO.md`](../TODO.md) when delivered.

- [ ] **#507** — **Lookup multiplicity and label mode have no schema enum.** `LookupRequest.multiplicity` and `LabelBinding.mode` are bare strings in the payload schema; add the enums from `types.AllLookupMultiplicities()` / `types.AllLabelModes()` (additive, golden regenerated) before the freeze.
- [x] **#508** — **Completion candidates containing `:` are split by the shell scripts.** Escape per shell in `writeCandidates` or document it in `completion.md`. *Done in U77 (E1-S2): escaped candidates, bash / fish scripts patched, documented in `completion.md`.*

## Inherited from U77

Filed from [U77 handed on](U77-ci-platform.md#handed-on); tick in [`TODO.md`](../TODO.md) when delivered.

- [ ] **#684** — **Path messages quote with `%q`.** On Windows every backslash of a path in an error or warning message reads doubled (`"C:\\Users\\…"`). Sweep the ~137 engine sites to one path-quoting helper (Unix output unchanged) and repoint the four tests that match `strconv.Quote(path)`.

## Inherited from U75

Filed from [U75 handed on](U75-zones-labels-2.md#handed-on); tick in [`TODO.md`](../TODO.md) when delivered.

- [ ] **#735** — **Black-box proof of the grouped-cohort `0x02` filter precompute's per-row zone.** The precompute that reads a per-row zone on a grouped (`0x02`) cohort is covered by a unit test only; add an integration case beside the per-row oracle (`internal/integration/time_zone_field*_test.go`) over a grouped cohort. Found in U50 (E1-S2), carried by #481.

## Inherited from U74

Filed from [U74 handed-on](U74-memory-bounds.md#handed-on); tick in [`TODO.md`](../TODO.md) when delivered.

- [ ] **#723** — **Compact `AGG_DISTINCT_COUNT` value sets.** One `map[float64]struct{}` per group bucket held ~1.4 GB on the 50M retention chain, now the largest single holder. #361 passes without it (anon peak 13.1–15.0 GiB in the 16 GiB box), so this widens a 1.0–2.9 GiB margin; counts must stay exact and worker-invariant, and the memory estimate's per-aggregator bytes move with it.

## Handed on from U76

Filed from [U76](U76-synth-fidelity-2.md#handed-on); tick in [`TODO.md`](../TODO.md) when delivered.

- [x] **#736** — **A fidelity `shape` for a `date` field.** A date twin's month volume is regenerated from `month_weights` but the report does not measure it; make `synth.TimeShapeFidelity.HourTVD` optional (`omitempty` pointer) so a date field can report `weekday_tvd` / `month_tvd` alone. Public-API golden moves; do it before the v1 freeze. *Done in U76 (E6-S4): `HourTVD` is a `*float64` (`omitempty`); a `date` entry carries `weekday_tvd` / `month_tvd` on the UTC clock, datetime reports byte-identical.*
- [x] **#738** — **A request form for `pulse sample`.** The CLI takes only `--input`, so sampling a cohort union is library / MCP only; add a `--request` JSON form (as `facet` / `lookup` accept `request.union`) or record the limit as permanent in `docs/src/cli/flags.md`. *Done in U76 (E6-S6): `pulse api sample --request FILE|-` takes a whole `SampleRequest` (a `union` included) through the strict decoder; `--count` / `--labels` layer on top, `--input` beside it is a usage error (exit 2).*

## Carried follow-ups (filed 2026-11-24)

Recorded only in shipped efforts' notes and PR bodies; checked against the code and filed under this unit. Full text in [`TODO.md`](../TODO.md) §15; tick there when delivered.

- [ ] **#739** — **Split `PULSE_TEMPLATE_TARGET_MISMATCH` out of `_TARGET_UNKNOWN`** before the freeze.
- [ ] **#740** — **One drift rule for sidecars.** The SPSS sidecar refuses on size / mtime drift; the point-lookup index escalates to a fingerprint check.
- [ ] **#741** — **Pairwise extension admission** is exact-match on `{sum, distinct_count}`; confirm, widen or document.
- [ ] **#742** — **`ApplyOverlaysWithExtensions` bypass**; confirm every caller gets the same checks.
- [ ] **#743** — **`n_within_depth` silent no-op** on crosstab kinds that do not read it; refuse where it has no effect.
- [ ] **#744** — **Audit the `ReadMemStats` memory gates** in `internal/service` tests.
- [ ] **#745** — **Model-workbench owner calls:** `fit_on` vs a profile-hidden `FILTER_EXPRESSION`, `ForbidsField` shape, `AGG_AUC` deviations.
- [ ] **#746** — **Flakes:** `TestRequestTimeout_*` on Windows, `TestHMC_ContextCancel`.
- [ ] **#747** — **Residual-correlation fidelity for a staircase endpoint.**
- [ ] **#748** — **Indirect gRPC dependency** via the OTLP exporter; accept or drop before the freeze.
- [ ] **#749** — **Confirm closed:** categorical-dictionary-index regression predictors, set-field MCP enum narrowing.

## Handed on from U32

Found in docs-audit (E3-S4); full text in [`TODO.md`](../TODO.md); tick there when delivered.

- [ ] **#756** — **The label, lookup, range-table and populations directory loaders read through `os`**, so an embedder on an in-memory `Options.FS` cannot use them; thread the template store's Fs.

## Handed on from U32b

Found in docs-content (E4-S1, E5-S1, E5-S3, E5-S5, E5-S6, E5-S7b, E5-G1, E6-S5, E7-S1, E7-S4, E7-S5, E8-S1, E8-S2, E8-G1); full text in [`TODO.md`](../TODO.md); tick there when delivered. #769 and #770 belong to the zone and completion areas of U50 and U37, both done, so this open unit carries them.

- [ ] **#767** — **`make sites` has no mode for an I/O-path change**; add an `io` mode that finds a reader / importer change's companions by shape, with a falsified fixture test.
- [ ] **#769** — **`cohort inspect` text mode omits the default zone field**; add a `Zone field:` line.
- [ ] **#770** — **No cohort completion for `cohort zone set|clear`**; add both leaves to the cohort-positional table.
- [x] **#771** — **`CLI_ERROR` and `PULSE_SHARD_AGGREGATE_DRIFT` are emitted but unregistered**; register them and gate emitted code literals against the registry. *Done in U32b (E8-S4): both registered; `TestEmittedCodeLiteralsRegistered` gates emitted literals.*
- [x] **#772** — **`ComposeOptions.FailFast` is false in the library but on in the CLI**; decide the default (an opt-out is needed to flip it). *Resolved by pre-v1 E1-S1:* `CollectErrors` is the opt-out; the zero value fails fast.
- [x] **#773** — **`WatchDir` cannot watch every file**: an empty `Suffix` means `.pulse`. *Done in pre-v1 E1-S2:* `WatchOptions.AllFiles`.
- [ ] **#774** — **`make sites` has no field-type, error-code or MCP-tool target**; add them (sibling of #767).
- [ ] **#776** — **The templates directory does not skip Pulse sidecars**; align with the other loaders or document why not.
- [ ] **#777** — **The `standard` preset's regression keys are stale**; decide membership and regenerate the manifest golden.
- [ ] **#778** — **Extension filterers get no params in `FieldInputs`**; pass them on both sides with a parity test.
- [ ] **#779** — **`AGG_RATIO` requires a `field` it never reads**; align manifest, refusal and skill, or stop requiring it.
- [ ] **#780** — **Formula arity fault: predict and runtime report different codes**; align with a parity test.
- [ ] **#781** — **Predict misses config refusals the run makes**; run the checks at predict with parity tests.
- [ ] **#782** — **`WIN_FORMULA` needs Go casing for a Welford cell**; accept `w.mean`.
- [ ] **#783** — **`tests[].requires` uses Go names**; owner decision on the wire spelling.
- [ ] **#784** — **`FEAT_*` `emits_type` is wrong and unread**; correct or enforce it.
- [ ] **#785** — **`pulse_facet` mis-renders date, datetime, decimal and big u64 values**; render per type.
- [ ] **#786** — **Inferred set rungs drop rows instead of promoting**; extend the ladder.
- [ ] **#787** — **Bayesian MMM diagnostics in a named fit name `""` and miscount parameters**; fall back to the fit name, match the list.
- [ ] **#788** — **Date-part hour / weekday keys sort as strings**; decide numeric order.
- [ ] **#789** — **Facet overlay population cohort ignores `data_dir`**; resolve it as the host cohort.
- [ ] **#790** — **Share / lift / formula windows read a decimal sum as null, silently**; convert or refuse.
- [ ] **#791** — **`OVERLAY_CHISQ_VS_REF` overstates two-sample evidence**; add a homogeneity mode or a warning.
- [ ] **#792** — **No CLI route embeds overlays in an export**; add one or document the library route.
- [ ] **#793** — **A crosstab count cell with no `field` returns zeros silently**; refuse it as the aggregations path does.
- [ ] **#794** — **Overlay `ref_kinds` are Go names**; emit the wire arms.
- [ ] **#795** — **Predict's `defaults_applied` paths are Go names**; emit wire keys.
- [ ] **#796** — **Two registered codes are never emitted**; retire or reserve them, gate it.
- [ ] **#797** — **`Fixup.Path` spellings are mixed**; use wire keys.
- [ ] **#798** — **SPSS cardinality threshold is internal-only**; expose it or decide.

## Handed on from U32c

Found in docs-validation (E1-S2, E2-S2); full text in [`TODO.md`](../TODO.md); tick there when delivered. The synth units (U52, U76) and the example-search units (U23, U31) are done, so this open unit carries them. The consolidated record of the documentation work, with every U32a, U32b and U32c finding and its disposition, is [`reviews/U32-docs-review.md`](../reviews/U32-docs-review.md); its owner sign-off line is filled by the owner there.

- [x] **#802** — **No MCP tool runs synth**, so an MCP-only agent can author a spec but not run it; add a synth tool or keep simulate CLI / library-only by decision. **Resolved 2026-11-27 by owner decision: no MCP synth tool; simulate stays CLI / library-only.**
- [ ] **#803** — **Example search has no plain-word synonym tier** ("null" does not find "missing"); add a small word-synonym table to the synonym tier.
