# Request Example Library

Pulse ships a searchable, embedded catalogue of runnable request JSON files
spanning every operator category. They are checked into the repo
under `examples/`, mounted into the binary at compile time via `//go:embed`,
and surfaced through three peer access paths:

| Access path | Best for |
|---|---|
| `pulse_examples_search` / `pulse_examples_get` (MCP tools) | LLM agents authoring requests against a running Pulse server |
| `pulse examples search` / `pulse examples show` (CLI) | Developers exploring at a shell |
| `pulse.ExamplesSearch` / `pulse.ExampleGet` (Go API) | Embedders building higher-level UIs |

## What the library contains

Every example is a complete `types.Request` JSON body — the same shape you
hand to `pulse_process`. Each file is annotated with a structured `_meta`
block describing the example. Pulse's JSON unmarshaller ignores unknown
fields by default, so the `_meta` block is invisible at execution time;
the file remains runnable verbatim.

```json
{
  "_meta": {
    "name": "t_test_one_sample",
    "category": "tests",
    "tags": ["hypothesis-test", "t-test", "tier-1-test", "parametric", "one-sample", "streaming-friendly"],
    "operators": ["AGG_AVERAGE", "AGG_COUNT", "TEST_T"],
    "description": "One-sample t-test comparing revenue mean against the hypothesized mu=100."
  },
  "cohort": {...},
  ...
}
```

Fetching via `pulse_examples_get` returns the request body with the `_meta`
block already stripped, so you can pass it straight to
`pulse_process` / `pulse_predict`.

## Searching the library

Three filter dimensions, all optional and combined with AND:

| Filter | Behaviour |
|---|---|
| `query` | Case-insensitive substring across the example's name, description, and operator list |
| `tags` | An example must carry **every** requested tag |
| `category` | Exact match against the example's directory (`aggregations`, `attributes`, `features`, `filterers`, `groupers`, `regression`, `tests`, `windows`) |

### CLI

```sh
pulse examples search --query welch                       # find Welch-related examples
pulse examples search --tag time-series --tag tier-2-test # AND tag filter
pulse examples search --category tests --json             # JSON envelope
pulse examples show t_test_one_sample                     # print runnable JSON
pulse examples show t_test_one_sample --json              # full record (with _meta)
```

### MCP

```jsonc
// arguments to pulse_examples_search
{"query": "welch"}
{"tags": ["time-series", "tier-2-test"]}
{"category": "features"}
```

### Go API

```go
p, _ := pulse.New(pulse.Options{DataDir: "/data"})

// Search:
hits := p.ExamplesSearch("welch", []string{"experiment-analysis"}, "")
for _, h := range hits {
    fmt.Println(h.Name, "—", h.Description)
}

// Fetch and run:
ex, ok := p.ExampleGet("t_test_one_sample")
if ok {
    var req pulse.Request
    _ = json.Unmarshal(ex.Body, &req)
    resp, _ := p.Process(ctx, &req)
    _ = resp
}
```

## Tag taxonomy

Tags are curated and validated by a CI gate (`TestExamples_TagsFromTaxonomy`).
The taxonomy spans four dimensions:

| Dimension | Tags |
|---|---|
| Domain / use case | `time-series`, `cohort-analysis`, `experiment-analysis`, `correlation-analysis`, `comparison`, `before-after`, `top-n`, `distribution-shape`, `cross-tabulation`, `proportion-analysis`, `trend-detection`, `outlier-detection`, `cardinality-analysis`, `data-quality`, `geo-analysis`, `financial`, `feature-engineering` |
| Statistical method | `hypothesis-test`, `t-test`, `parametric`, `nonparametric`, `paired`, `one-sample`, `two-sample`, `k-sample`, `repeated-measures`, `post-hoc`, `normality-test`, `homogeneity-test`, `exact-test` |
| Regression / modeling | `regression`, `ecological` |
| Pipeline machinery | `tier-1-test`, `tier-2-test`, `composed`, `pre-filter`, `feature-pipeline`, `window-operator`, `streaming-friendly`, `buffered-pipeline` |
| Risk / edge | `leakage-safe`, `leakage-risk`, `small-sample` |

The category (directory name) is **not** repeated in the tags — `_meta.category`
carries that.

## Adding a new example

1. Write the request JSON under `examples/<category>/`. Use existing files as
   shape templates. Keep `cohort.data_dir = ".data"` and reference one of the
   fixture cohorts.
2. Add a `_meta` block at the top of the file:
   - `name` — kebab-case-with-underscores, unique across the whole library.
   - `category` — must match the parent directory.
   - `tags` — pick 3-6 from the taxonomy above.
   - `operators` — the list of `AGG_* / ATTR_* / FILTER_* / GROUP_* / WIN_* / FEAT_* / TEST_*` types appearing in the body, alphabetized and deduped.
   - `description` — one-sentence, present-tense summary.
3. Re-run `go test ./examples/... ./descriptor/...` to confirm the new file passes:
   - `TestExamples_AllParseAsRequest`
   - `TestExamples_UniqueNames`
   - `TestExamples_TagsFromTaxonomy`
   - `TestExamples_OperatorsMatchBody`
   - `TestExamples_CategoryMatchesDirectory`
   - `TestManifestExamplesPopulated`
4. The annotation tool at `cmd/annotate-examples/` is idempotent and may be
   re-used; updating its in-source `annotations` slice and re-running will
   rewrite the file's `_meta` block in canonical form.
