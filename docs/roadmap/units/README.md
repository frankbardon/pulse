# v1.0.0 Units of Work

The v1.0.0 roadmap broken into **32 Flow-sized units**. Each unit is one initiative: one branch (`branch:` = `slug`), one PR, and 1–3 vertical-slice epics. Every unit document carries:
- machine-readable frontmatter: `id`, `slug`, `depends_on`, `blocks`, `todo_items`, `size`, `status`;
- the outcome and scope;
- the exact TODO items it delivers, quoted verbatim with their `TODO.md` number;
- links to the theme sections to read first;
- epics and stories, acceptance criteria, gates, Update Demand companions, and the human inputs needed.

Every one of the 170 TODO items belongs to at least one unit. The generator asserts this, and each item in [`TODO.md`](../TODO.md) links back to its unit.

These files were generated once from the theme documents and `TODO.md`, and are now maintained by hand. Edit them directly, and keep a unit's `todo_items` in step with the `· [Uxx]` links in `TODO.md`.

## Conventions (match the existing Flow history)

- **Branch / scope:** the unit `slug`.
- **Story commits:** `feat(<slug>/E<n>-S<m>): <what>`, or `fix` / `perf` / `test` / `docs` as appropriate.
- **Epic close:** `milestone(<slug>/E<n>): vertical slice complete — <epic title>`.
- **Status:** update the unit's frontmatter `status` (`not-started` → `in-progress` → `done`) and tick its TODO items in the same PR.

## Definition of Done (every unit)

- [ ] `make lint` and `make test` pass.
- [ ] Update Demand satisfied: every skill, CLAUDE.md and `.claude/reference/` companion listed in the unit is updated **in the same PR**, and any `.claude/reference/` file the unit names as "load before" was loaded before starting.
- [ ] Every new gate is listed by name in CLAUDE.md "Non-Skippable CI Gates" where its prefix requires it.
- [ ] Every new error code has `codeMetadata` (Message + ≥1 Fixup).
- [ ] Every new operator has an atomic skill, manifest capability entry, example tag, `Since`, dependency edges and (once U07 has landed) `Purpose` / `Interpretation`. It is weight-aware or explicitly refuses a weight (once U11 has landed), and multiplicity-aware if it emits p-values (once U13 has landed).
- [ ] Wire changes are additive. `format_version` stays `"1.1"`. Goldens are regenerated with `-update`, never hand-edited.
- [ ] CLAUDE.md stays ≤ 50,000 bytes (long form goes to `.claude/reference/`).
- [ ] The unit's TODO items are ticked, and its `status` is `done`.

## Suggested order

The IDs are already in a valid dependency order. Units on different tracks with no dependency between them can run in parallel sessions.

| # | Unit | Track | Size | Depends on | TODO items |
|---|---|---|---|---|---|
| U01 | [release-pipeline](U01-release-pipeline.md): Every build knows its real version, and a pushed tag ships binaries | API & release | S | — | 5, 6, 7, 8, 9 |
| U02 | [public-surface](U02-public-surface.md): The public Go API is deliberate, and CI guards it | API & release | L | — | 1, 2, 3, 4 |
| U03 | [temporal-foundation](U03-temporal-foundation.md): All date math lives in one place, and requests can name a time zone | Time zones | M | — | 66, 67, 68 |
| U04 | [profiles-model](U04-profiles-model.md): Every feature has a name, and a profile file can declare an instance's feature set | Feature profiles | M | U02 | 10, 11, 12, 13, 14, 15, 16 |
| U05 | [profiles-enforcement](U05-profiles-enforcement.md): Hidden features cannot run and cannot be seen by the engine or self-description | Feature profiles | M | U04 | 17, 18, 19, 20, 21, 22, 23 |
| U06 | [profiles-mcp-tooling](U06-profiles-mcp-tooling.md): MCP servers expose only the profile, and embedders have tools to write and check profiles | Feature profiles | M | U05 | 29, 30, 31, 32, 33, 35 |
| U07 | [guidance-metadata](U07-guidance-metadata.md): Pulse can describe what each operator is for, in plain language, without bloating payloads | Guided analysis | M | U02 | 36, 37, 38, 39, 40, 41, 42 |
| U08 | [guidance-backfill-inferential](U08-guidance-backfill-inferential.md): Every test, overlay and regression explains what it is for and how to read it | Guided analysis | L | U07 | 43, 44, 45 |
| U09 | [guidance-backfill-descriptive](U09-guidance-backfill-descriptive.md): Every operator carries guidance, and the guidance gates are binding | Guided analysis | L | U08 | 46, 47, 48, 49 |
| U10 | [skill-ontology](U10-skill-ontology.md): Agents only ever see skills and examples for features the instance has | Feature profiles | L | U05, U09 | 24, 25, 26, 27, 28 |
| U11 | [weighting-descriptive](U11-weighting-descriptive.md): Weighted counts, percentages and crosstabs are correct by default when a weight is set | Statistical integrity | L | U04 | 50, 51, 52, 53, 54, 57, 58 |
| U12 | [weighting-inferential](U12-weighting-inferential.md): Significance tests and models are correct on weighted survey data | Statistical integrity | M | U11 | 55, 56, 59 |
| U13 | [multiplicity](U13-multiplicity.md): Analysts can correct for multiple comparisons in one consistent way | Statistical integrity | M | U04 | 60, 61, 62, 63, 64, 65 |
| U14 | [zone-aware-operators](U14-zone-aware-operators.md): Days, weeks and date ranges can mean local calendar days, while storage stays UTC | Time zones | M | U03 | 69, 70, 71, 72 |
| U15 | [linalg-core](U15-linalg-core.md): One trusted linear-algebra core and a mergeable co-moment accumulator, with no user-visible change | Vector & matrix | M | U02 | 73, 74, 75, 76, 77 |
| U16 | [matrix-result](U16-matrix-result.md): A first correlation matrix, end to end, through the library, CLI and MCP | Vector & matrix | L | U15, U11 | 78, 79, 80, 81, 82, 83, 84, 163 |
| U17 | [response-shaping-core](U17-response-shaping-core.md): Callers can say which parts of a response they want | Response shaping | M | U05 | 104, 106, 107, 109 |
| U18 | [response-shaping-execution](U18-response-shaping-execution.md): Unrequested work is never computed, and MCP returns lean responses by default | Response shaping | M | U17 | 105, 108, 110, 111 |
| U19 | [resource-limits](U19-resource-limits.md): Embedders can bound runaway requests, with defaults that never get in the way | Embedder operations | M | U05 | 112, 113, 114, 115, 116, 117 |
| U20 | [observability](U20-observability.md): Hosts can see what Pulse is doing, whether or not they are a server | Embedder operations | M | U02 | 118, 119, 120, 121, 122, 123 |
| U21 | [guidance-generated-docs](U21-guidance-generated-docs.md): Plain-language reference docs and skill sections generate themselves from metadata | Guided analysis | M | U09, U10 | 85, 86, 87, 88, 89, 34 |
| U22 | [recommend-explain](U22-recommend-explain.md): Developers and agents can go from a question to a valid request, and from a result to plain language | Guided analysis | L | U09, U11, U13 | 90, 91, 92, 93, 94 |
| U23 | [guidance-mcp](U23-guidance-mcp.md): MCP agents are guided from intent to result in few round-trips | Guided analysis | S | U22 | 95, 96, 97, 98, 99 |
| U24 | [matrix-operators](U24-matrix-operators.md): Analysts get the core multivariate toolkit: rank correlations, partial correlations, reliability, PCA, collinearity | Vector & matrix | L | U16 | 124, 125, 126, 127, 128, 129, 130 |
| U25 | [multivariate-tests-segmentation](U25-multivariate-tests-segmentation.md): Analysts can test whole profiles, flag unusual rows, score components and segment records | Vector & matrix | L | U24 | 131, 132, 133, 134, 135, 136 |
| U26 | [vector-expr-functions](U26-vector-expr-functions.md): Formulas and filters can work with whole vectors and sets | Vector & matrix | M | U16 | 137, 138, 139, 140 |
| U27 | [vector-metrics-aggregates](U27-vector-metrics-aggregates.md): Similarity is safe by default, and groups can be summarized as profiles | Vector & matrix | M | U26 | 141, 142, 143, 144 |
| U28 | [matrix-overlays](U28-matrix-overlays.md): Crosstabs and matrices gain residuals, perceptual maps, flow projections, raking and corrected p-values | Vector & matrix | L | U24, U13 | 145, 146, 147, 148, 149, 150 |
| U29 | [vector-field-types](U29-vector-field-types.md): Cohorts can store fixed-length numeric vectors natively | Vector & matrix | L | U16, U27 | 151, 152, 153, 154, 155 |
| U30 | [matrix-extensions-hardening](U30-matrix-extensions-hardening.md): Embedders can add their own matrix operators, and the matrix stack is proven at scale | Vector & matrix | M | U25, U28, U29 | 156, 157, 158 |
| U31 | [guidance-guides](U31-guidance-guides.md): A developer can start from a question and find the right analysis without knowing statistics | Guided analysis | M | U21, U24, U28 | 100, 101, 102, 103 |
| U32 | [v1-release](U32-v1-release.md): Pulse v1.0.0 is released with a written stability promise | API & release | S | U01, U02, U06, U10, U18, U19, U20, U23, U30, U31 | 159, 160, 161, 162, 164, 165, 166, 167, 168, 169, 170 |

## Dependency graph

```mermaid
graph TD
  U01["U01 release-pipeline"]
  U02["U02 public-surface"]
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
  U32["U32 v1-release"]
  U02 --> U04
  U04 --> U05
  U05 --> U06
  U02 --> U07
  U07 --> U08
  U08 --> U09
  U05 --> U10
  U09 --> U10
  U04 --> U11
  U11 --> U12
  U04 --> U13
  U03 --> U14
  U02 --> U15
  U15 --> U16
  U11 --> U16
  U05 --> U17
  U17 --> U18
  U05 --> U19
  U02 --> U20
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
  U06 --> U32
  U10 --> U32
  U18 --> U32
  U19 --> U32
  U20 --> U32
  U23 --> U32
  U30 --> U32
  U31 --> U32
```

## Human inputs that gate units

- **U02 public-surface:** **Blocking input:** the downstream catalog (decided: the maintainer produces it)
- **U08 guidance-backfill-inferential:** **Statistics reviewer** (decided: maintainer sources one) reviews and signs off
- **U09 guidance-backfill-descriptive:** Statistics reviewer sign-off before the flip
- **U12 weighting-inferential:** Reviewer glance at the `n_eff` semantics (same reviewer as U08)
- **U24 matrix-operators:** Statistics reviewer for Purpose/Interpretation of the new operators
- **U25 multivariate-tests-segmentation:** Statistics reviewer
- **U28 matrix-overlays:** Decide the correspondence-analysis payload shape (open question in vm6)
- **U32 v1-release:** Maintainer runs the downstream validation and tags the release

Sizes: **S** ≈ one focused session; **M** ≈ 2–3 sessions; **L** ≈ 3–5 sessions. These are relative and meant for Flow's planning, not commitments.
