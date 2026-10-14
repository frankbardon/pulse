# Finding Change Sites

**Audience:** Pulse internals contributors (human or agent) about to
change an operator or a request/response slot.

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

`op` also warns about **category-norm sites missing**: files at least
75% of the category's operators appear in but this one does not.
`slot` warns about **per-slot walkers** (functions that reference many
of the struct's fields) that skip this slot.

In `new-op` output `<op>` stands for the operator name minus its
prefix, lower-case, joined with `_` or `-` like its neighbours
(`internal/skills/op-mat-<op>.md`, `internal/processing/matrix_<op>.go`),
`NN_` for the next free example number, and `[k/n ops]` for how many
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
- **Slot sites** are `go/types`-resolved references to the
  `types.Request` / `types.Response` field (selectors, promoted fields
  and composite-literal keys, the whole module type-checked against
  `go list -export` data), string literals equal to its json name,
  `Request.<Field>` mentions in prose and goldens, and update-demand
  rows naming the json name.

The hand-kept parts — the reading list per category and the structural
gate names per site class — are pinned by the tool's own tests
(`TestManualPathsExist`, `TestStaticGatesExist`), so a renamed
reference doc or gate fails `go test ./internal/tools/sites/`.

## Limits

- A site that never spells the identifier — a `switch` default, a
  family-wide branch keyed on a parameter — shows up only through the
  **Enumerators** section (callers of the registry function) or the
  category gates. Read the update-demand slice for the contract.
- Synth distribution names are ordinary words, so they are matched only
  as `"quoted"` strings; a quoted `"normal"` in an unrelated parameter
  table is reported too.
- Slot mode type-checks the module (a few seconds; `go list -export`
  compiles anything not already in the build cache).
