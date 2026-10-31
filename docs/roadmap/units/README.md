# v1.0.0 Units of Work

The v1.0.0 roadmap broken into **57 Flow-sized units**. Each unit is one initiative: one branch (`branch:` = `slug`), one PR, and 1–3 vertical-slice epics. Every unit document carries:
- machine-readable frontmatter: `id`, `slug`, `depends_on`, `blocks`, `todo_items`, `size`, `status`;
- the outcome and scope;
- the exact TODO items it delivers, quoted verbatim with their `TODO.md` number;
- links to the theme sections to read first;
- epics and stories, acceptance criteria, gates, Update Demand companions, and the human inputs needed.

Every one of the 212 TODO items belongs to at least one unit. The generator asserts this, and each item in [`TODO.md`](../TODO.md) links back to its unit.

These files were generated once from the theme documents and `TODO.md`, and are now maintained by hand. Edit them directly, and keep a unit's `todo_items` in step with the `· [Uxx]` links in `TODO.md`.

## Conventions (match the existing Flow history)

- **Branch / scope:** the unit `slug`.
- **Story commits:** `feat(<slug>/E<n>-S<m>): <what>`, or `fix` / `perf` / `test` / `docs` as appropriate.
- **Epic close:** `milestone(<slug>/E<n>): vertical slice complete — <epic title>`.
- **Release intent:** *none — rolls into v1.0.0*, unless the unit is a deliberate pre-release checkpoint. See [Branching & release strategy](../README.md#branching--release-strategy).
- **Status:** update the unit's frontmatter `status` (`not-started` → `in-progress` → `done`) and tick its TODO items in the same PR.

## Definition of Done (every unit)

- [ ] `make lint` and `make test` pass.
- [ ] Update Demand satisfied: every skill, CLAUDE.md and `.claude/reference/` companion listed in the unit is updated **in the same PR**, and any `.claude/reference/` file the unit names as "load before" was loaded before starting.
- [ ] Every new gate is listed by name in CLAUDE.md "Non-Skippable CI Gates" where its prefix requires it.
- [ ] Every new error code has `codeMetadata` (Message + ≥1 Fixup).
- [ ] Every new operator has an atomic skill, manifest capability entry, example tag, `Since`, dependency edges and (once U07 has landed) `Purpose` / `Interpretation`. It is weight-aware or explicitly refuses a weight (once U11 has landed), and multiplicity-aware if it emits p-values (once U13 has landed).
- [ ] Wire changes are additive. `format_version` stays `"1.1"`. Goldens are regenerated with `-update`, never hand-edited.
- [ ] CLAUDE.md stays ≤ 40,000 bytes (long form goes to `.claude/reference/`).
- [ ] The unit's TODO items are ticked, and its `status` is `done`.

## Suggested order

The IDs are in a valid dependency order, except the units appended later: U35 lands before U32, and U36 (from U08's findings) lands after U08. U37 (shell completion) has no hard dependency and can land any time before U32. U38 (skill sync, appended after U21) lands after U21 and before U32. U39 (code-in attribute, appended after U38) is done and has no dependencies. U40 (compose sweep, added after U39) is done and lands before U32; U41 (derived cohort), U42 (fit scoring) and U43 (post-aggregation ratios) are done; U41, U42 and U43 follow it in a chain (derived cohort, fit scoring, post-aggregation ratios) and ship in v1.0.0 (owner decision 2026-10-10). U44–U52 (from the [showcase examples](https://github.com/frankbardon/pulse-casebook/tree/main/showcase)) also ship in v1.0.0 and land before U32; they have no hard dependencies on each other, so they can run in parallel sessions, with the soft dependencies in each unit's frontmatter. U53 (custom regressions, appended after U52) ships in v1.0.0 (owner decision 2026-10-10) and lands before U32; it has no hard dependency, with U34, U36 and U42 soft. U57 (transformed predictors, from U53's backlog item #490) follows U53, ships in v1.0.0 and lands before U32. U58 (expression maths, lags and params, appended after U57) follows U53, owns #509–#520, ships in v1.0.0 and lands before U32. U59 (SEM extensions, appended after U58) has no hard dependency (U24, U40 and U42 soft), owns #528–#538, ships in v1.0.0 and lands before U32. U60 (MMM estimation, appended after U59) has no hard dependency (U40, U53, U57, U58 and U59 soft), owns #539–#547 and the narrowed #489 (its follow-ups #548–#560 are filed under their owner units), ships in v1.0.0 and lands before U32; U61 (cross-architecture determinism, created at U60's close-out) owns #554, has U58 and U60 soft, and lands before U32; U62 (Bayesian MMM, appended after U61) has no hard dependency (U60 soft), owns the narrowed #552 and its follow-ups #561–#568, ships in v1. U63 (MMM follow-ups, appended after U62) has no hard dependency (U40, U53, U57, U60 and U62 soft), owns #549, #297 and the follow-ups #570–#581 (filed under their owner units), ships in v1.0.0 and lands before U32. U56 (operation panic recovery, split out of U34 on 2026-10-19) owns #231 and follows U20; it ships in v1.0.0 and lands before U32. U55 (previous state, appended after U50 on 2026-10-18) owns #452 and follows U50; it ships in v1.0.0 and lands before U32. U54 (multi-grouper, appended after U53 on 2026-10-11) runs NEXT, immediately after U27 (owner decision): plain `groups` has only ever executed its first grouper, silently; it takes #200 from U35 and #318 from U46, and U46 now depends on it. U09 is done and no longer waits on U36 (soft dependency). Units on different tracks with no dependency between them can run in parallel sessions.

| # | Unit | Track | Size | Depends on | TODO items |
|---|---|---|---|---|---|
| U01 | [release-pipeline](U01-release-pipeline.md): Every build knows its real version, and a pushed tag ships binaries | API & release | S | — | 5, 6, 7, 8, 9 |
| U02 | [public-surface](U02-public-surface.md): The public Go API is deliberate, and CI guards it | API & release | L | — | 1, 2, 3, 4, 180, 181, 182, 183, 184 |
| U02b | [extension-contract](U02b-extension-contract.md): Embedders author custom operators against a public contract, and the engine is fully internal | API & release | L | U02 | 185, 186, 187, 188, 189 |
| U02c | [cohort-facade](U02c-cohort-facade.md): Embedders read and write cohorts record by record, and predict says whether a crosstab fuses | API & release | M | U02 | 190, 191, 192 |
| U03 | [temporal-foundation](U03-temporal-foundation.md): All date math lives in one place, and requests can name a time zone | Time zones | M | — | 66, 67, 68 |
| U04 | [profiles-model](U04-profiles-model.md): Every feature has a name, and a profile file can declare an instance's feature set | Feature profiles | M | U02, U02b | 10, 11, 12, 13, 14, 15, 16 |
| U05 | [profiles-enforcement](U05-profiles-enforcement.md): Hidden features cannot run and cannot be seen by the engine or self-description | Feature profiles | M | U04 | 17, 18, 19, 20, 21, 22, 23 |
| U06 | [profiles-mcp-tooling](U06-profiles-mcp-tooling.md): MCP servers expose only the profile, and embedders have tools to write and check profiles | Feature profiles | M | U05 | 29, 30, 31, 32, 33, 35 |
| U07 | [guidance-metadata](U07-guidance-metadata.md): Pulse can describe what each operator is for, in plain language, without bloating payloads | Guided analysis | L | U02, U02b | 36, 37, 38, 39, 40, 41, 42 |
| U08 | [guidance-backfill-inferential](U08-guidance-backfill-inferential.md): Every test, overlay and regression explains what it is for and how to read it | Guided analysis | L | U07 | 43, 44, 45 |
| U09 | [guidance-backfill-descriptive](U09-guidance-backfill-descriptive.md): Every operator carries guidance, and the guidance gates are binding | Guided analysis | L | U08 (U36 soft) | 46, 47, 48, 49 |
| U10 | [skill-ontology](U10-skill-ontology.md): Agents only ever see skills and examples for features the instance has | Feature profiles | L | U05, U09 | 24, 25, 26, 27, 28 |
| U11 | [weighting-descriptive](U11-weighting-descriptive.md): Weighted counts, percentages and crosstabs are correct by default when a weight is set | Statistical integrity | L | U04 | 50, 51, 52, 53, 54, 57, 58 |
| U12 | [weighting-inferential](U12-weighting-inferential.md): Significance tests and models are correct on weighted survey data | Statistical integrity | L | U11 | 55, 56, 59 |
| U13 | [multiplicity](U13-multiplicity.md): Analysts can correct for multiple comparisons in one consistent way | Statistical integrity | M | U04 | 60, 61, 62, 63, 64, 65 |
| U14 | [zone-aware-operators](U14-zone-aware-operators.md): Days, weeks and date ranges can mean local calendar days, while storage stays UTC | Time zones | M | U03 | 69, 70, 71, 72 |
| U15 | [linalg-core](U15-linalg-core.md): One trusted linear-algebra core and a mergeable co-moment accumulator, with no user-visible change | Vector & matrix | M | U02, U02b | 73, 74, 75, 76, 77 |
| U16 | [matrix-result](U16-matrix-result.md): A first correlation matrix, end to end, through the library, CLI and MCP | Vector & matrix | L | U15, U11 | 78, 79, 80, 81, 82, 83, 84, 172 |
| U17 | [response-shaping-core](U17-response-shaping-core.md): Callers can say which parts of a response they want | Response shaping | M | U05 | 82, 104, 106, 107, 109, 193 |
| U18 | [response-shaping-execution](U18-response-shaping-execution.md): Unrequested work is never computed, and MCP returns lean responses by default | Response shaping | M | U17 | 105, 108, 110, 111, 217, 218, 219 |
| U19 | [resource-limits](U19-resource-limits.md): Embedders can bound runaway requests, with defaults that never get in the way | Embedder operations | M | U05 | 82, 112, 113, 114, 115, 116, 117 |
| U20 | [observability](U20-observability.md): Hosts can see what Pulse is doing, whether or not they are a server | Embedder operations | M | U02, U02b | 118, 119, 120, 121, 122, 123, 222, 223, 227 |
| U21 | [guidance-generated-docs](U21-guidance-generated-docs.md): Plain-language reference docs and skill sections generate themselves from metadata | Guided analysis | M | U09, U10 | 85, 86, 87, 88, 89, 34 |
| U22 | [recommend-explain](U22-recommend-explain.md): Developers and agents can go from a question to a valid request, and from a result to plain language | Guided analysis | L | U09, U11, U13 | 90, 91, 92, 93, 94 |
| U23 | [guidance-mcp](U23-guidance-mcp.md): MCP agents are guided from intent to result in few round-trips | Guided analysis | M | U22 | 95, 96, 97, 98, 99, 246, 247 |
| U24 | [matrix-operators](U24-matrix-operators.md): Analysts get the core multivariate toolkit: rank correlations, partial correlations, reliability, PCA, collinearity | Vector & matrix | L | U16 | 124, 125, 126, 127, 128, 129, 130, 172, 258 |
| U25 | [multivariate-tests-segmentation](U25-multivariate-tests-segmentation.md): Analysts can test whole profiles, flag unusual rows, score components and segment records | Vector & matrix | L | U24 | 131, 132, 133, 134, 135, 136, 262, 263, 264, 295 |
| U26 | [vector-expr-functions](U26-vector-expr-functions.md): Formulas and filters can work with whole vectors and sets | Vector & matrix | M | U16 | 137, 138, 139, 140 |
| U27 | [vector-metrics-aggregates](U27-vector-metrics-aggregates.md): Similarity is safe by default, and groups can be summarized as profiles | Vector & matrix | L | U26 | 141, 142, 143, 144, 407, 417 |
| U28 | [matrix-overlays](U28-matrix-overlays.md): Crosstabs and matrices gain residuals, perceptual maps, flow projections, raking and corrected p-values | Vector & matrix | L | U24, U13 | 145, 146, 147, 148, 149, 150, 261, 265, 296 |
| U29 | [vector-field-types](U29-vector-field-types.md): Cohorts can store fixed-length numeric vectors natively | Vector & matrix | L | U16, U27 | 151, 152, 153, 154, 155 |
| U30 | [matrix-extensions-hardening](U30-matrix-extensions-hardening.md): Embedders can add their own matrix operators, and the matrix stack is proven at scale | Vector & matrix | XL | U25, U28, U29 | 156, 157, 158, 270, 402, 403, 406, 408, 411, 415, 420, 423, 424, 425, 426, 449 |
| U31 | [guidance-guides](U31-guidance-guides.md): A developer can start from a question and find the right analysis without knowing statistics | Guided analysis | M | U21, U24, U28 | 100, 101, 102, 103, 250, 251, 252, 269, 377 |
| U32 | [docs-audit](U32-docs-audit.md): Pulse goes live with the most helpful, current and comprehensive documentation we can produce | API & release | L | U01, U02, U02b, U02c, U34, U35, U06, U10, U18, U19, U20, U23, U30, U31, U37, U38, U40 | 159, 160, 161, 162, 163, 164, 165, 166, 167, 238, 239, 257, 268, 371, 412, 413, 435, 453, 463, 469, 472, 473, 474, 478, 504, 505 |
| U33 | [v1-release](U33-v1-release.md): Pulse v1.0.0 is released with a written stability promise | API & release | S | U32 | 168, 169, 170, 171, 173, 174, 175, 176, 177, 178, 179, 206, 220, 232, 233, 234, 506 |
| U34 | [extension-validation](U34-extension-validation.md): Extension registrations are validated as strictly as built-ins, and chain predict knows them | API & release | M | U02b | 194, 195, 196, 197, 216, 230, 374, 409, 451, 459 |
| U35 | [predict-runtime-parity](U35-predict-runtime-parity.md): Predict and runtime agree on every built-in, and the runtime never answers with a wrong number | API & release | XL | U02c (soft) | 198, 199, 201, 207, 213, 214, 215, 221, 225, 226, 229, 248, 249, 253, 254, 255, 256, 259, 260, 267, 299, 300, 313, 372, 378, 379, 391, 393, 394, 400, 404, 418, 419, 427, 431, 432, 436, 437, 439, 440, 442, 448, 475, 476 |
| U36 | [reference-oracles](U36-reference-oracles.md): Every inferential output is pinned to an external reference, and none answers with a wrong or unencodable number | Statistical integrity | L | U08 | 202, 203, 204, 205, 266, 271, 294, 405, 410, 414, 477 |
| U37 | [shell-completion](U37-shell-completion.md): The pulse CLI completes commands, flags and values natively in the terminal | API & release | M | — (U06 soft) | 208, 209, 210, 211, 212 |
| U38 | [skill-sync](U38-skill-sync.md): Hand-written skills say exactly what the engine does, and every skill fits its budget | Guided analysis | M | U21 | 235, 236, 237, 376, 396, 416 |
| U39 | [code-in-attribute](U39-attr-code-in.md): A top-box share of the whole base is one attribute and one weighted mean on the fused crosstab path | Guided analysis | S | — | 240, 241, 242, 243, 244, 245 |
| U40 | [compose-sweep](U40-compose-sweep.md): A parameter grid is one compose request: Pulse expands it, runs every combination and ranks the results | API & release | M | — | 272, 273, 274, 275, 276, 277, 278, 297, 298, 380, 392 |
| U41 | [derived-cohort](U41-derived-cohort.md): A request's derived columns become a new cohort with the schema carried over, and the import that follows cannot mislabel a column | API & release | L | U40 | 279, 280, 281, 282, 283, 284, 285 |
| U42 | [fit-scoring](U42-fit-scoring.md): A fitted model is applied to rows by name, its error is an aggregator, and its interval bounds pass through | API & release | L | U41 | 286, 287, 288, 289 |
| U43 | [post-aggregation-ratios](U43-post-aggregation-ratios.md): A ratio of two aggregates and a scalar derived from an aggregate are native, so ROI and a scenario scale factor need no hand arithmetic | API & release | M | U42 | 290, 291, 292 |
| U44 | [strict-input](U44-strict-input.md): Pulse never silently ignores what it is given: a misspelled key, a dropped row or a failed run is always visible | API & release | M | — (U35 soft) | 301, 302, 303, 304, 305, 306, 307, 308, 309, 310, 311, 375, 397, 460, 462 |
| U45 | [binary-and-decimal](U45-binary-and-decimal.md): A 0/1 flag and an exact decimal work wherever a category or a number does, and a formula can say what it means | API & release | M | — (U35 soft) | 312, 313, 314, 315, 316, 317 |
| U46 | [grouped-analysis](U46-grouped-analysis.md): Grouped analysis is one request: composite keys, overlays that pick their measure and partition, and tests per group | API & release | L | U54 (U35 soft) | 319, 320, 321, 322, 323, 428, 429, 430 |
| U47 | [survey-tables](U47-survey-tables.md): A weighted banner table with significance letters is one request, and Pulse can compute the weights | Statistical integrity | L | — (U46, U11, U12 soft) | 324, 325, 326, 327, 328, 329, 330, 331, 398, 471 |
| U48 | [experiment-readout](U48-experiment-readout.md): An experiment readout is one request: sample-ratio check, a named reference arm, an adjusted effect with its interval, all in one correction family | Statistical integrity | M | — (U43, U46 soft) | 332, 333, 334, 335, 336, 337, 338 |
| U49 | [model-workbench](U49-model-workbench.md): Models fit reliably and are validated with standard metrics, and leakage-safe encoding is one parameter | Statistical integrity | L | — (U42 soft) | 339, 340, 341, 342, 343, 344, 345, 346, 347, 395 |
| U50 | [local-time](U50-local-time.md): Every row is read in its own local time, and calendar parts sort and filter the way a calendar does | Time zones | M | — (U46 soft) | 348, 349, 350, 351, 352, 353, 467, 468, 470 |
| U51 | [archive-scale](U51-archive-scale.md): A 50-million-row shard archive runs on a 16 GB machine: header-only operations read headers, and appending costs only the new shard | Embedder operations | L | — (U19 soft) | 354, 355, 356, 357, 358, 359, 360, 361, 373, 390, 399, 455, 456, 458, 461, 491, 492, 493, 494, 495, 496, 498, 499, 500, 501, 502, 503 |
| U52 | [synth-realism](U52-synth-realism.md): Pulse can synthesize time series, outcomes drawn from a model, and privacy-checked twins without outside tooling | API & release | L | — (U50 soft) | 362, 363, 364, 365, 366, 367, 368, 369, 370, 450, 457 |
| U53 | [custom-regressions](U53-custom-regressions.md): Embedders register their own regression models, treated exactly like built-ins, and the core gains the model options media-mix work needs | API & release | L | — (U34, U36, U42 soft) | 381, 382, 383, 384, 385, 386, 387, 388, 389, 293 |
| U54 | [multi-grouper](U54-multi-grouper.md): Two or more groupers give one row per key combination, in every execution path | API & release | M | — (U27 soft) | 200, 318, 421, 422 |
| U55 | [previous-state](U55-previous-state.md): A long panel feeds a from-state × to-state crosstab: a per-subject previous-state attribute | Time zones | M | U50 | 452 |
| U56 | [operation-panic-recovery](U56-operation-panic-recovery.md): A panic raised inside an operation becomes a coded error at the facade instead of crashing the host | Embedder operations | S | U20 (U34 soft) | 231 |
| U57 | [transformed-predictors](U57-transformed-predictors.md): A regression reads a window label of its own request, so a media-mix model fits, scores a holdout and sweeps its decay in one request | API & release | L | U53 (U40, U42 soft) | 490 |
| U58 | [expr-math-lags-params](U58-expr-math-lags-params.md): Query and synth expressions share a deterministic maths set, synth rows read earlier rows, and a request hands expressions named parameters | API & release | L | U53 (U40, U52, U57 soft) | 509–520 |
| U59 | [sem-extensions](U59-sem-extensions.md): MAT_SEM compares latent means across groups under invariance constraints, puts intervals on derived effects, scores latents per row and fits ordinal ratings | Vector & matrix | L | — (U24, U40, U42 soft) | 528–538 |
| U60 | [mmm-estimation](U60-mmm-estimation.md): A media-mix analyst jointly fits response curves, searches adstock decays with a seeded sweep and reallocates budget with a constrained optimizer | Vector & matrix | L | — (U40, U53, U57, U58, U59 soft) | 489 (narrowed), 539–547 |
| U61 | [cross-arch-determinism](U61-cross-arch-determinism.md): Every numeric result path gives identical bits on arm64 and amd64, and a repo-wide gate keeps it that way | API & release | L | — (U58, U60 soft) | 554, 569, 591 |
| U62 | [bayesian-mmm](U62-bayesian-mmm.md): A media-mix practitioner fits a Bayesian model and gets return-on-spend intervals that include adstock uncertainty | Vector & matrix | L | — (U60 soft) | 552 (narrowed), 561–568 |
| U63 | [mmm-followups](U63-mmm-followups.md): A media-mix run is one Compose call on the stock CLI, and a request aggregates window-derived columns | Vector & matrix | L | — (U40, U53, U57, U60, U62 soft) | 549, 297, 570–581 |

## Dependency graph

```mermaid
graph TD
  U01["U01 release-pipeline"]
  U02["U02 public-surface"]
  U02b["U02b extension-contract"]
  U02c["U02c cohort-facade"]
  U03["U03 temporal-foundation"]
  U04["U04 profiles-model"]
  U05["U05 profiles-enforcement"]
  U06["U06 profiles-mcp-tooling"]
  U07["U07 guidance-metadata"]
  U08["U08 guidance-backfill-inferential"]
  U09["U09 guidance-backfill-descriptive"]
  U10["U10 skill-ontology"]
  U11["U11 weighting-descriptive"]
  U12["U12 weighting-inferential"]
  U13["U13 multiplicity"]
  U14["U14 zone-aware-operators"]
  U15["U15 linalg-core"]
  U16["U16 matrix-result"]
  U17["U17 response-shaping-core"]
  U18["U18 response-shaping-execution"]
  U19["U19 resource-limits"]
  U20["U20 observability"]
  U21["U21 guidance-generated-docs"]
  U22["U22 recommend-explain"]
  U23["U23 guidance-mcp"]
  U24["U24 matrix-operators"]
  U25["U25 multivariate-tests-segmentation"]
  U26["U26 vector-expr-functions"]
  U27["U27 vector-metrics-aggregates"]
  U28["U28 matrix-overlays"]
  U29["U29 vector-field-types"]
  U30["U30 matrix-extensions-hardening"]
  U31["U31 guidance-guides"]
  U32["U32 docs-audit"]
  U33["U33 v1-release"]
  U34["U34 extension-validation"]
  U35["U35 predict-runtime-parity"]
  U36["U36 reference-oracles"]
  U37["U37 shell-completion"]
  U38["U38 skill-sync"]
  U39["U39 code-in-attribute"]
  U40["U40 compose-sweep"]
  U41["U41 derived-cohort"]
  U42["U42 fit-scoring"]
  U43["U43 post-aggregation-ratios"]
  U44["U44 strict-input"]
  U45["U45 binary-and-decimal"]
  U46["U46 grouped-analysis"]
  U47["U47 survey-tables"]
  U48["U48 experiment-readout"]
  U49["U49 model-workbench"]
  U50["U50 local-time"]
  U51["U51 archive-scale"]
  U52["U52 synth-realism"]
  U53["U53 custom-regressions"]
  U54["U54 multi-grouper"]
  U55["U55 previous-state"]
  U56["U56 operation-panic-recovery"]
  U57["U57 transformed-predictors"]
  U58["U58 expr-math-lags-params"]
  U59["U59 sem-extensions"]
  U60["U60 mmm-estimation"]
  U61["U61 cross-arch-determinism"]
  U62["U62 bayesian-mmm"]
  U63["U63 mmm-followups"]
  U02 --> U02b
  U02 --> U02c
  U02 --> U04
  U02b --> U04
  U04 --> U05
  U05 --> U06
  U02 --> U07
  U02b --> U07
  U07 --> U08
  U08 --> U09
  U05 --> U10
  U09 --> U10
  U04 --> U11
  U11 --> U12
  U04 --> U13
  U03 --> U14
  U02 --> U15
  U02b --> U15
  U15 --> U16
  U11 --> U16
  U05 --> U17
  U17 --> U18
  U05 --> U19
  U02 --> U20
  U02b --> U20
  U09 --> U21
  U10 --> U21
  U09 --> U22
  U11 --> U22
  U13 --> U22
  U22 --> U23
  U16 --> U24
  U24 --> U25
  U16 --> U26
  U26 --> U27
  U24 --> U28
  U13 --> U28
  U16 --> U29
  U27 --> U29
  U25 --> U30
  U28 --> U30
  U29 --> U30
  U21 --> U31
  U24 --> U31
  U28 --> U31
  U01 --> U32
  U02 --> U32
  U02b --> U32
  U02c --> U32
  U06 --> U32
  U10 --> U32
  U18 --> U32
  U19 --> U32
  U20 --> U32
  U23 --> U32
  U30 --> U32
  U31 --> U32
  U32 --> U33
  U02b --> U34
  U34 --> U32
  U02c -.-> U35
  U35 --> U32
  U08 --> U36
  U36 -.-> U09
  U37 --> U32
  U06 -.-> U37
  U21 --> U38
  U38 --> U32
  U40 --> U32
  U41 --> U32
  U42 --> U32
  U43 --> U32
  U40 --> U41
  U41 --> U42
  U42 --> U43
  U44 --> U32
  U45 --> U32
  U46 --> U32
  U47 --> U32
  U48 --> U32
  U49 --> U32
  U50 --> U32
  U51 --> U32
  U52 --> U32
  U53 --> U32
  U54 --> U32
  U55 --> U32
  U56 --> U32
  U57 --> U32
  U58 --> U32
  U59 --> U32
  U60 --> U32
  U61 --> U32
  U62 --> U32
  U63 --> U32
  U53 --> U57
  U53 --> U58
  U20 --> U56
  U50 --> U55
  U54 --> U46
  U35 -.-> U44
  U35 -.-> U45
  U35 -.-> U46
  U46 -.-> U47
  U11 -.-> U47
  U12 -.-> U47
  U43 -.-> U48
  U46 -.-> U48
  U42 -.-> U49
  U46 -.-> U50
  U19 -.-> U51
  U50 -.-> U52
  U34 -.-> U53
  U34 -.-> U56
  U36 -.-> U53
  U42 -.-> U53
  U40 -.-> U57
  U42 -.-> U57
  U40 -.-> U58
  U52 -.-> U58
  U57 -.-> U58
  U24 -.-> U59
  U40 -.-> U59
  U42 -.-> U59
  U40 -.-> U60
  U53 -.-> U60
  U57 -.-> U60
  U58 -.-> U60
  U59 -.-> U60
  U58 -.-> U61
  U60 -.-> U61
  U60 -.-> U62
  U60 -.-> U63
  U62 -.-> U63
```

## Human inputs that gate units

- **U02 public-surface:** none blocking — the downstream catalog is delivered (#1) and the classification decided (#2); whether U02 or U02b moves `processing` is the implementer's call
- **U08 guidance-backfill-inferential:** done without a human reviewer — an automated review (deterministic gates + an advisory LLM panel) is recorded in [`reviews/U08-statistics-review.md`](../reviews/U08-statistics-review.md); human sign-off moved to U33
- **U09 guidance-backfill-descriptive:** done without a human reviewer — an automated review is the U09 section of [`reviews/U08-statistics-review.md`](../reviews/U08-statistics-review.md); human sign-off moved to U33 (#206)
- **U12 weighting-inferential:** done without a human reviewer — an automated review of the `n_eff` semantics and every lifted weighted formula is recorded in [`reviews/U12-weighting-review.md`](../reviews/U12-weighting-review.md); the human glance moved to U33 (#206)
- **U24 matrix-operators:** Statistics reviewer for Purpose/Interpretation of the new operators
- **U25 multivariate-tests-segmentation:** done without a human reviewer — an automated review (deterministic R-oracle gates plus an advisory LLM panel on Purpose/Interpretation and the formula choices) is recorded in [`reviews/U25-multivariate-review.md`](../reviews/U25-multivariate-review.md); human sign-off moved to U33 (#206)
- **U28 matrix-overlays:** Decide the correspondence-analysis payload shape (open question in vm6)
- **U31 guidance-guides:** done without a human reader — the plain-language review ran as an automated critical non-statistician read in E3-S2 (findings in that commit body); `FEAT_POLY` gets no materialise plan (owner decision); the follow-ups #504 / #505 went to U32
- **U38 skill-sync:** done. Decided by the owner: topical `## See` sections build stem and tag edges like atomic ones; `## Components` optional for agg / group / filter / mat; recommend drafts keep asking for `interval` (`draftAsksParams`); `GROUP_DATE` refuses a non-date-family field (behaviour break). Tiers frozen at the measured provisional values; the non-positive `interval` question went to U33 (#506)
- **U32 docs-audit:** Maintainer sign-off; a fresh reader (developer without a statistics background) for the review
- **U35 predict-runtime-parity:** multi-entry `Groups` — execute every group or refuse (#200)
- **U36 reference-oracles:** Shapiro–Francia at n < 5 (refuse or exact method); how an infinite statistic is encoded
- **U37 shell-completion:** whether completion respects a feature profile; library helper vs internal CLI glue; where field-taking flags find their cohort
- **U51 archive-scale:** done. Decided in the interview: per-shard lookup sidecars plus a coverage manifest (design C), binned reproducible summation, a streaming rewrite for archive mutations. Decided by the owner: more than 65535 shards refused, add builds before it publishes, a `u64` / float join key refused, no grouped fan-out routing rule; the retention-shaped chain's 2.3× RSS (target 1.5×) accepted, fix filed as #499. The E5-S2 50M run met every per-step target, but without `GOMEMLIMIT` the retention chain peaks at a 17.3–23.1 GiB footprint, above #361's 16 GB (14.8 GiB with `GOMEMLIMIT=14GiB`); the owner accepted the miss, #361 stays open, and the remaining work is #501–#503.
- **U53 custom-regressions:** done. Decided in the unit: the post-fit slot ships in v1.x (#385 → #489), Weibull adstock is the new window `WIN_ADSTOCK` (#387), the outputs map is free-form under a declared schema (#382). Open for the owner in the effort PR: the extension multiplicity family and `p_values` basis, `conf_level` refused on a custom model, the `WIN_ADSTOCK` parameter choices, the in-test Levenberg–Marquardt versus `gonum/optimize`, `ParamMeta.Example`
- **U61 cross-arch-determinism:** done. Decided by the owner: the regression SPD path is an FMA-free port of gonum's amd64 order (amd64 0 ulp, arm64 converges); PCA / `MAT_FACTOR` / `TEST_MANOVA` pinned at 1e-12 and `MAT_SEM` early-stop fits a documented residual; `AutoHACLags` fixed in the unit; proof is the committed fingerprint golden plus CI's linux/amd64 run, never Docker. Follow-ups #588–#590 went to U33, U59 and U49; #591 was fixed in the unit
- **U33 v1-release:** Maintainer runs the downstream validation and tags the release; **a named statistics reviewer signs off the U08 review record (#206, release-blocking)** and lands the statistical-review CODEOWNERS entries

Sizes: **S** ≈ one focused session; **M** ≈ 2–3 sessions; **L** ≈ 3–5 sessions. These are relative and meant for Flow's planning, not commitments.
