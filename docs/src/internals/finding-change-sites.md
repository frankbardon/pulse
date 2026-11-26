# Finding Change Sites

**Audience:** Pulse internals contributors (human or agent) about to
change an operator, a request/response slot or an example fixture
cohort.

A Pulse change rarely lives in one file: a new operator touches its
type constant and registry, the engine, a capability declaration, a
`features.go` row, a Purpose, an atomic skill, an example tag, the
goldens and the generated Analysis Guide. `internal/tools/sites`
prints that checklist up front — every file and declaration to edit,
the tests to rerun and the goldens to regenerate — so you read the
right files instead of grepping for them.

**Run it first**, before opening code:

```bash
make sites ARGS="op MAT_PCA"              # an existing operator
make sites ARGS="new-op overlay"          # a NEW operator of a category
make sites ARGS="slot request matrices"   # a Request / Response slot (Go or json name)
make sites ARGS="slot request my_new_slot"  # unknown name = new-slot template
make sites ARGS="slot compose sweep"      # a Compose-level slot (ComposedRequest)
make sites ARGS="fixture survey"          # an example fixture cohort (or a new stem)
make sites ARGS="categories"              # categories and their operators
# or: go run ./internal/tools/sites [--json] [--all] <mode> ...
```

Categories: `aggregator`, `attribute`, `filterer`, `grouper`, `window`,
`feature`, `test`, `regression`, `matrix`, `overlay`, `synth` (short
aliases such as `agg`, `mat`, `attr` work). A synth distribution is
named by its wire name (`sites op normal`).

## What it prints

Output is grouped by site class — wire types + registry, engine,
vectors, orchestration, weighting, multiplicity, capability
declaration, feature table, guidance, error owners, predict, MCP, CLI,
skills, examples, docs, contract prose — then:

- **Gates to rerun** — ready-to-paste `go test -count=1 ./pkg/ -run '^(…)$'`
  lines: the tests that name the operator or slot, the *category gates*
  (tests that iterate the registry function or a category-wide table, so
  they cover a new operator too) and the structural gates of each site
  class present (atomic-skill structure, `TestDocsGeneratedCurrent`, …).
- **Regenerate** — the command for every golden or generated file the
  target appears in (`go test ./descriptor/ -update`, `make docs-generate`,
  `make reference`, …). Never hand-edit those.
- **Read before editing** — the contract docs and the per-area
  `.claude/reference/update-demand/` slices for the category. This list is
  the one hand-kept part; everything else is derived.

`op` also prints the sites that never spell the operator, derived
from the ones that do:

- **Param enums** — the enum params its capability declaration
  (`internal/descriptor/capabilities_*.go`) lists, with their values,
  and every declaration using a constant that spells a value (the
  rotation dispatch, a validator, a refusal).
- **Reached without the name** — callers of a bool predicate that names
  the operator but few of its category (`ArrayValued`,
  `ComparisonFitted`), followed through further predicates
  (`IsArrayValued` → `AggregationExpands` → …), and the size estimates
  (`*Bytes`, `estimate*`, `*memory*`) declared in its files with their
  callers (`AccumulatorBytes` → `matrixMemory`).
- **R oracle fixtures** — the `internal/processing/testdata/reference/`
  JSON its tests open (a string literal that is the fixture's name or
  path), and the `scripts/reference/` scripts that write them
  (`write_golden("<stem>", …)` or `<stem>.json`; a driver such as
  `gen_reference.R` that `source()`s a writer is listed as one). In a
  shared test only the table row or map entry around the operator's
  mention counts, so a sibling's fixture stays the sibling's; the tests
  of its shared primitives count too (`binomial.json` through
  `internal/statdist`).
- **Param index** — every param its capability declares (not only the
  enums, and through helpers such as `comparisonCapability`), the
  struct fields that decode it in the files naming the operator, and
  the decoders of each form its description spells as an object key
  (`params.reference` `{"centroid": …}`, `{"population": …}`).
- **Shared primitives** — the module functions the files named for it
  call in other packages (`internal/statdist.BinomialTwoSidedP`,
  `internal/seriespartition.Partition`), with the tests of those
  packages that call them as gates.
- **Metric consumers** — for a `linalg/metric` consumer (a
  `metric.Consumer*` constant spells it): the registry's consumer
  tables, every use of the consumer API, and the callers of functions
  taking a `metric.Consumer` (`assembleManifest`). `new-op` prints the
  list for a category with a consumer; a non-consumer's `op` report
  points there.
- **Test tables** — test-file `var` tables naming it (a probe table, a
  parity case list) that need a row for a sibling or a new value.
- Under **Gates to rerun**, the benchmarks of its packages and the
  Makefile targets (other than whole-module ones) that run them.

`op` and `new-op` also print **Spec sites** — what a change to the
category's request spec struct (`types.RegressionSpec`: a new key, an
object sub-block, a coded refusal, a vocabulary or a capability) reaches
without naming any operator. The spec is the `types` struct carrying the
registry's element type that a `types.Request` field holds; the slot's
json name and the family word (`REGRESSION`) follow from it. Each kind
of site is found by its shape:

- **Refusal pipelines** — declarations calling at least three of the
  spec's checkers (funcs taking the spec and returning
  `*errors.CodedError`): `checkRegressionSpec` (predict),
  `ValidateRegression` (runtime), `AdaptRegressionFactory` (extensions).
- **Field-list derivers** — funcs from the spec to a `[]string`
  (`levelFields`, `RegressionReads`): a key naming a cohort field joins
  each.
- **Code tables** — `var` / `const` declarations listing at least three
  `*_REGRESSION_*` codes (`slotfail.RegressionCodes`, `codeMetadata`,
  `errorOwners`).
- **Vocabulary registrars** — callers of a closed vocabulary reachable
  from the spec's fields (`types.AllCovarianceTypes` in the payload
  schema's `enumValues` and the MCP schema).
- **Strict-input families** — string literals `"request.regressions"`
  or `"request.regressions.<key>"` (the CLI strict-acceptance rows).
- **Capability lists** — files naming at least two
  `capability:regression_*` features (`examples/profiles/*.json`,
  `features.go`, the feature-profile docs).

`slot request <name>` falls back to `types.ComposedRequest` when the
name is not a `types.Request` field (`sweep`, `base`, `fits`, `banner`,
`compare`, `optimize`, `post_fit`, …); `slot compose <name>` asks for
it directly, and is the only way to reach a name both roots declare
(`overlays`, `multiplicity`, `return`, `params`). `slot response <name>`
likewise falls back to `types.ComposedResponse` (`optimizations`,
`comparisons`, `fits`, `ranking`, `banner`, `sweep_trace`, `post_fit`,
…); a name both declare (`overlays`, `returned`, `warnings`) resolves to
`types.Response`.
A name declared on neither root but on structs reachable from it (an
item-level key such as `for_each`) reports every declaring field. A
Compose-level slot also prints **Compose sites**, each found by shape:

- **Strict-input family tables** — declarations listing at least five
  `"composed.<slot>[.<key>]"` literals (`strictAcceptanceRows`), with
  the Test functions that read them as gates.
- **Capability lists** — files naming at least five
  `capability:compose_*` features (example profiles, `features.go`,
  skills, the feature-profile docs).
- **Limits-parity site lists** — test declarations naming two or more
  `"compose"` / `"compose (<arm>)"` runtime sites (`parityCases`).
- **Shared derivations** — `internal/descriptor` functions the runtime
  (`internal/service`) calls with a `ComposedRequest` or one of its
  fields (`ResultCatalog`, `ResolveComposeReturn`): change predict and
  runtime together.
- **Key carriers** — skills and examples holding the slot's json key.

Every report also prints what reaches the change without naming it:

- **Whole-repo runners** (under Gates to rerun, when an example is a
  site, and always for `new-op`) — tests that run every example: a
  `Test` function that calls `internal/examples`' `All`, or lists the
  `internal/examples` tree (`Walk`, `Glob`, `ReadDir` with no named
  file), directly or through a helper in its package's tests
  (`TestLimitsDefaultsNeverTripGoldens`, `TestExamples_RunEndToEnd`).
- **Call-site floors** (`op`, `slot`) — a `Test` function that walks
  the tree, parses Go source and holds a module function's name as a
  string literal checks every call of that function
  (`TestReadSchemaCallersThreadHeaderVersion` holds `"ReadSchema"`). A
  site file calling it lists the call and the floor as a gate.
- **Shared generators** (`op`) — every hit in a fixture generator
  script (`.py` / `.R` under `testdata/` or `scripts/`) is attributed
  to its case-table entry: the row key (`("t_split", …)`), the
  `case == "ov_symmetry"` branch above it, or the first quoted key of
  the block a comment heads, prefixed with the enclosing function.

`fixture <stem>` covers an example fixture cohort
(`internal/examples/fixtures/<stem>.csv` with
`internal/examples/fixtures/schemas/<stem>.json`), existing or new.
Its sites never name an operator or a slot, so each is found by shape:

- **Registration lists** — a Go declaration (comments excluded) or a
  run of consecutive lines elsewhere naming at least three cohorts,
  each quoted, backticked or spelled `<stem>.pulse` / `.csv` / `.json`
  (or five bare stems on one line, a shell word list); a Markdown table
  row counts only its first cell. That finds `buildExampleFixtures`,
  the `fixtures/build.sh` loop, the README setup prose and cohort table,
  the generator's `Generate` and a fuzz test's cohort subset. A list
  naming at least 75% of the cohorts is **complete**; one that skips the
  cohort warns. A smaller one is a **subset**: add the cohort only if
  it belongs.
- **Readers** (existing cohort) — files naming `<stem>.pulse`,
  `<stem>.csv` or `schemas/<stem>.json`: examples, stories, tests.
- **Gates** — the fixture package's tests, the `Test` / `Fuzz`
  functions using a Go list's identifier, and the every-example
  runners; **Regenerate** names the `main` package under
  `internal/examples/fixtures/cmd/`.

`op` also warns about **category-norm sites missing**: files at least
75% of the category's operators appear in, outside Go comments, but
this one does not (a doc comment walking per-operator special cases is
no norm).

`new-op` also prints **Operator subsets**: non-test composite literals
whose elements or map keys spell at least two and under 75% of the
category (`vectors.VectorAggregationTypes`). A new operator of the
family a list selects joins it; the registry enumerators never reach
it.
`slot` warns about **per-slot walkers** (functions that reference many
of the struct's fields) that skip this slot.

In `new-op` output `<op>` stands for the operator name minus its
prefix, lower-case, joined with `_` or `-` like its neighbours
(`internal/skills/op-mat-<op>.md`, `internal/processing/matrix_<op>.go`),
`NN_` for an example number (the site note prints the directory's next
free one), and `[k/n ops]` for how many
existing operators land at a site. Every-op sites (≥ 75%) are
mandatory; the rest are conditional on what the operator does (the
`only:` list shows which operators use them — a p-value emitter lands
in the multiplicity sites, a facet overlay in the facet sites). Each
symbol carries an exemplar (`e.g. OVERLAY_T_CELL L2274`) to open and
copy the shape from.

`--all` adds what the default view folds away: history files under
`docs/roadmap/`, single-operator sites, cross-references inside other
operators' own files, and (for `op`) tests, goldens and generated
files as sites. `--json` emits the same report as JSON.

## How it stays current

Nothing in the site list is hand-maintained:

- **Operators** come from the registries by AST — the slice literal in
  `types.All*Types()` / `types.AllOverlayKinds()` /
  `synth.AllDistributions()`, resolved through the package's string
  constants.
- **An operator's sites** are the whole-token occurrences of its wire
  name, of every Go constant spelling it (`types.OverlayKindMarkov`,
  `metric.ConsumerVecDistance`) and of its atomic-skill stem, across the
  tracked tree (plus untracked, non-ignored files). Each Go hit is
  resolved to its enclosing declaration — comment-only mentions are
  flagged — and each Markdown hit to its heading. The tool's tests pin
  the file set to an independent `git grep` for several operators.
- **`new-op` templates** are the sites the category's existing
  operators share, after folding each operator's own name out of file
  names.
- **Spec sites** are read by AST from those shapes (above), never from
  a site list; the tool's tests pin each rule on a fixture tree and the
  regression sites on the real one.
- **Slot sites** are `go/types`-resolved references to the
  `types.Request` / `types.ComposedRequest` / `types.Response` /
  `types.ComposedResponse` field (selectors, promoted fields
  and composite-literal keys, the whole module type-checked against
  `go list -export` data), string literals equal to its json name,
  `Request.<Field>` mentions in prose and goldens, and update-demand
  rows naming the json name.

The hand-kept parts — the reading list per category and the structural
gate names per site class — are pinned by the tool's own tests
(`TestManualPathsExist`, `TestStaticGatesExist`), so a renamed
reference doc or gate fails `go test ./internal/tools/sites/`.

## Limits

- A site that never spells the identifier and is not reached through a
  predicate, an estimate or an enum constant — a `switch` default, a
  family-wide branch keyed on a semantic property with no predicate
  function — shows up only through the **Enumerators** section (callers
  of the registry function) or the category gates. Read the
  update-demand slice for the contract.
- Whole-repo runners and call-site floors are found by shape (above):
  an every-example runner that enumerates the library some other way
  (say through the facade's search), or a floor test that matches its
  callee by something other than a string literal, is missed.
- A floor is matched by file: a site file that calls the floor's
  callee anywhere lists it, even when the call sits in a declaration
  your change does not touch.
- Per-case attribution reads a generator script's layout (row keys,
  `case == "…"` branches, comment-headed blocks); a case table of
  another shape is reported as the enclosing function or `(top level)`.
- Predicate callers are matched by function name; a name with more than
  80 call sites is reported in the footer instead of expanded.
- Contributor recipes describe a category in general and rarely name
  one operator, so the category's `adding-*.md` recipe and
  `extension-points.md` are appended to **Read before editing** rather
  than found by name.
- A fixture-cohort list held some other way (one cohort per Go
  declaration, a list built from a glob, a table keyed by a later
  column) is not found; nor is a cohort named only as a bare word in
  prose.
- Synth distribution names are ordinary words, so they are matched only
  as `"quoted"` strings; a quoted `"normal"` in an unrelated parameter
  table is reported too.
- Slot mode type-checks the module (a few seconds; `go list -export`
  compiles anything not already in the build cache).
