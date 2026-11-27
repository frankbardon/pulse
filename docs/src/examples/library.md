# Request Example Library

Pulse ships a searchable, embedded library of runnable request JSON files
spanning every operator category. They are checked into the repo
under `internal/examples/`, mounted into the binary at compile time via `//go:embed`,
and surfaced through three peer access paths:

| Access path | Best for |
|---|---|
| `pulse_examples_search` / `pulse_examples_get` (MCP tools) | LLM agents authoring requests against a running Pulse server |
| `pulse examples search` / `pulse examples show` (CLI) | Developers exploring at a shell |
| `pulse.ExamplesSearch` / `pulse.ExampleGet` (Go API) | Embedders building higher-level UIs |

## What the library contains

Every example is a complete `types.Request` JSON body — the same shape you
hand to `pulse_process`. Each file is annotated with a structured `_meta`
block describing the example. Every request JSON decodes strictly — an
unknown key is refused with `PULSE_REQUEST_UNKNOWN_FIELD` — but a root
`_meta` block is tolerated by the CLI and MCP request decoders, so the
file remains runnable verbatim. (A rendered request template does not
tolerate it.)

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
`pulse_process` / `pulse_predict`. The served body (`pulse_examples_get`,
`pulse examples show`, `Pulse.ExampleGet`) also drops the files'
repo-relative `"data_dir": ".data"` (and the `.data/` prefix of a facet
population's `cohort` path), so each cohort filename resolves against the
server's own data directory. The checked-in files keep it, so they run
verbatim from the repo root.

A **story** example also declares `_meta.question` (the plain-language
question it answers) and `_meta.interpretation` (a qualitative reading of
its result); both ride search and get results. Its request sets
`return.precision`, and its response is pinned by a committed golden
(`internal/examples/README.md`, "Story examples").

## Searching the library

Four filters, all optional and combined with AND:

| Filter | Behaviour |
|---|---|
| `query` | Plain words. One word is a case-insensitive substring of the name, description or operators; several words must each match a whole word of the name, description, operators, intents or tags. Operator aliases and intent phrasings match too, then near-miss spellings when nothing matches literally |
| `intent` | An intent-taxonomy ID the example must declare (`pulse skills show intents` lists them); an unknown one is `PULSE_RECOMMEND_INTENT_UNKNOWN` |
| `tags` | An example must carry **every** requested tag |
| `category` | Exact match against the example's directory; the manifest's `example_categories` lists them |

### CLI

```sh
pulse examples search --query welch                       # find Welch-related examples
pulse examples search --tag time-series --tag tier-2-test # AND tag filter
pulse examples search --intent compare_groups            # examples serving one intent
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

Tags are curated in `internal/examples.CanonicalTags` and validated by a
CI gate (`TestExamples_TagsFromTaxonomy`); the table below is held to that
list by `TestDocsExampleTagTableMatchesCanonicalTags`.

| Dimension | Tags |
|---|---|
| Domain / use case | `time-series`, `cohort-analysis`, `experiment-analysis`, `correlation-analysis`, `comparison`, `before-after`, `top-n`, `distribution-shape`, `cross-tabulation`, `proportion-analysis`, `trend-detection`, `outlier-detection`, `cardinality-analysis`, `data-quality`, `financial`, `feature-engineering` |
| Statistical method | `hypothesis-test`, `t-test`, `parametric`, `nonparametric`, `paired`, `one-sample`, `two-sample`, `k-sample`, `repeated-measures`, `post-hoc`, `normality-test`, `homogeneity-test`, `exact-test` |
| Regression / modeling | `regression`, `ecological`, `ols`, `glm`, `logistic`, `bayesian`, `ordinal`, `regularization`, `ridge`, `lasso`, `elasticnet`, `polynomial`, `resampling`, `jackknife`, `selection`, `stepwise`, `multinomial` |
| Pipeline machinery | `tier-1-test`, `tier-2-test`, `composed`, `pre-filter`, `feature-pipeline`, `window-operator`, `streaming-friendly`, `buffered-pipeline` |
| Matrices and multivariate tests | `matrix`, `covariance`, `multivariate` |
| Risk / edge | `leakage-risk`, `small-sample` |
| Discovery / facet | `facet` |
| Cohort shape | `sharded`, `anchor` |
| Result decoration | `overlay`, `byte-equal-test`, `compose`, `optimize`, `crosstab`, `welch`, `welford-triple`, `z` |

The category (directory name) is **not** repeated in the tags — `_meta.category`
carries that.

## Adding a new example

Write the request under `internal/examples/<category>/` against one of the
fixture cohorts (`cohort.data_dir = ".data"`) and give it a `_meta` block:
`name` (snake_case, unique), `category` (the directory), `tags` from the
taxonomy above, `operators` (every operator type in the body, alphabetized),
`intents` and a one-sentence `description`. Every key and its rule is in
[`internal/examples/README.md`](https://github.com/frankbardon/pulse/blob/main/internal/examples/README.md)
("The `_meta` block"). Then run
`go test -count=1 ./internal/examples/... ./internal/descriptor/...`; the
`TestExamples_*` gates (`TestExamples_DecodeStrictly`,
`TestExamples_OperatorsMatchBody`, `TestExamples_IntentsFromTaxonomy`, …)
name any key that is wrong.
