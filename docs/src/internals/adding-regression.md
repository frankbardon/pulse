# Adding a Regression Model (Embedders)

> **Embedding Pulse, not contributing to it.** This recipe registers a
> `REG_<NAMESPACE>_<NAME>` model through `pulse.Options.Extensions.Regressions`
> and the public `extend` package. Built-in regressions (`REG_OLS`, `REG_GLM`,
> `REG_BAYES_LINEAR`) live in `internal/processing/` and embedders cannot
> import it. The full field-by-field contract is in
> [Extension Points](extension-points.md#regression-model-regressions); this page walks
> the order you meet it in.

**Audience:** an embedder who needs a fit the built-ins do not cover — a
nonlinear saturation curve, a pooled model, a robust estimator — and wants
it to behave like a built-in everywhere: predict, manifest, MCP, feature
profiles, multiplicity, `resample` / `selection`, `ATTR_REG_PREDICT`, saved
models and compose sweeps.

## 1. Pick the form

| You need | Declare | Interface |
|---|---|---|
| one pass over rows, mergeable state (sums, co-moments) | `Streamable: true` | `extend.StreamingRegression`: `UpdateRow`, `Merge`, `Finalize` |
| every row at once, iteration (Newton, Levenberg–Marquardt, EM) | `Streamable: false` | `extend.BufferedRegression`: `Fit(rows)` |

The declaration decides the execution path in predict and at runtime, and
`pulse.New` probes the factory against it
(`PULSE_EXTENSION_STREAMABLE_MISMATCH` when they disagree). The engine owns
row admission — listwise deletion on `target` and every predictor, and on a
weighted `WeightAware` slot only rows with a valid positive weight — so the
model never sees a null predictor.

## 2. A streaming model

A through-the-origin slope: it keeps two sums, so `Merge` is addition.

```go
type slope struct{ y, x string; sxy, sxx, syy float64; n int }

func (m *slope) UpdateRow(rec extend.Record) error {
    y, _ := rec.NumericValue(m.y)
    x, _ := rec.NumericValue(m.x)
    m.sxy += float64(x * y)
    m.sxx += float64(x * x)
    m.syy += float64(y * y)
    m.n++
    return nil
}

func (m *slope) Merge(other extend.StreamingRegression) error {
    o := other.(*slope) // built by the same factory from the same spec
    m.sxy, m.sxx, m.syy, m.n = m.sxy+o.sxy, m.sxx+o.sxx, m.syy+o.syy, m.n+o.n
    return nil
}

func (m *slope) Finalize() (extend.RegressionOutput, error) {
    if m.n < 2 || m.sxx == 0 {
        return extend.RegressionOutput{}, errors.NewCodedError(
            errors.PROCESSING_REGRESSION_INSUFFICIENT_DATA, "REG_ACME_SLOPE needs two rows with x != 0")
    }
    b := m.sxy / m.sxx
    sse := m.syy - float64(b*m.sxy)
    se := math.Sqrt(sse / float64(m.n-1) / m.sxx)
    return extend.RegressionOutput{
        Coefficients: []extend.RegressionCoefficient{{Name: m.x, Estimate: b, StdErr: extend.Figure(se)}},
        Fit:          extend.RegressionFit{ResidualStdErr: extend.Figure(math.Sqrt(sse / float64(m.n-1)))},
        Outputs:      map[string]any{"sxx": m.sxx},
    }, nil
}

reg := pulse.RegressionRegistration{
    Name:        "REG_ACME_SLOPE",
    Description: "Slope through the origin.",
    Streamable:  true,
    Factory: func(spec *types.RegressionSpec, _ *encoding.Schema) (extend.Regression, error) {
        if len(spec.Predictors) != 1 {
            return nil, fmt.Errorf("REG_ACME_SLOPE takes exactly one predictor")
        }
        return &slope{y: spec.Target, x: spec.Predictors[0]}, nil
    },
    OutputSchema: []pulse.RegressionOutputMeta{{Key: "sxx", JSONType: "number", Description: "Σx²."}},
}
```

Keep the arithmetic FMA-free (`float64(x * y)` before an add) when a result
must match across amd64 and arm64.

## 3. A buffered model

An iterative fit sees `rows extend.Rows` (`Len`, `At(i)`), valid only for the
call, and may loop over them as often as it needs. The reference extension
used by Pulse's own tests, `REG_TESTMMM_HILL`
(`extensions_testmmm_hill_test.go`), is a joint nonlinear least-squares fit
of Hill-saturated channels plus linear controls — it is the pattern to copy
for anything iterative:

```go
type hill struct{ target string; predictors, channels []string }

func (m *hill) Fit(rows extend.Rows) (extend.RegressionOutput, error) {
    // read rows.At(i).NumericValue(name), iterate to convergence, then
    // return one coefficient per parameter and the Hill k / s as Outputs.
    // Report Fit.Iterations; return a coded PROCESSING_REGRESSION_NO_CONVERGE
    // when the iteration fails, which fails the slot alone.
}
```

A nonlinear parameter is a coefficient too, named by the model. `Outputs`
carries what has no coefficient slot: per-channel `k` and `s` objects, a
convergence flag, a pooled variance.

## 4. Declare params, outputs and what the model accepts

| Registration field | What it is for | Refused when undeclared |
|---|---|---|
| `Params []pulse.ParamMeta` | the keys of `regressions[i].params` — `JSONType`, `Required`, `Description` | any `params` key is `PULSE_REQUEST_UNKNOWN_FIELD`; with declared params a wrong-typed value or a missing `Required` one is `PULSE_REQUEST_PARAM_INVALID` (predict ≡ runtime). No declared params: values pass through and predict advises `PULSE_ADVISORY_EXTENSION_PARAMS_UNDECLARED` |
| `OutputSchema []pulse.RegressionOutputMeta` | the `Outputs` keys the model may fill (`Key`, `JSONType`, `Description`); listed in the manifest | an undeclared key is `PULSE_EXTENSION_REGRESSION_OUTPUT_MISMATCH` |
| `Accepts []encoding.FieldType` | field types for target and predictors; empty = the built-in analytics-numeric set | `PULSE_FIELD_TYPE_UNSUPPORTED` |
| `WeightAware` | the model reads `Record.Weight()` | any weight in force is `PULSE_EXTENSION_NOT_WEIGHT_AWARE` |
| `Vcov` | honours `vcov: true`; `Vcov` in the output is then required | `PROCESSING_CONFIG` `{regression, slot: "vcov"}` |
| `PValues` | the model reports coefficient p-values, so `multiplicity` can correct them and predict counts them | `p_values` is null on every coefficient (under `resample` too); a non-`none` `multiplicity` method is `PULSE_MULTIPLICITY_INVALID`; a finite `PValue` in the output is `_OUTPUT_MISMATCH` `undeclared_p_value` |
| `Penalty` | reads `penalty` / `alpha` / `l1_ratio` itself | `PROCESSING_CONFIG` `{slot: "penalty"}` |
| `CovarianceTypes` | the robust types it computes itself (`cluster`, `hac`, …) | `PROCESSING_CONFIG` `{slot: "covariance"}` |
| `SupportsSelection` | a refit over a predictor subset is meaningful; every fit reports `Fit.Deviance` | `PROCESSING_CONFIG` `{slot: "selection"}`; a fit without a deviance is `_OUTPUT_MISMATCH` `missing_deviance` |
| `Scorer` | scores rows from the fit, enabling `ATTR_REG_PREDICT` and `save` | `PULSE_MODEL_SCORE_INVALID` `no_scorer`; `save` refused |
| `FieldInputs` | fields the model reads beyond target, predictors and weight | a buffered request decodes every field |

**Declare or refuse.** Every shared `RegressionSpec` slot is either honoured
or refused with `PROCESSING_CONFIG` `{regression, slot}` — never silently
ignored. `resample` is always honoured; `conf_level`, `family`, `link`,
`max_iters`, `tol` and `prior` are always refused (carry the model's own
settings in `params`). The complete slot table is in
[Extension Points](extension-points.md#regression-model-regressions).

## 5. The scorer

A `Scorer` (`extend.RegressionScorerFactory`) builds an immutable,
concurrency-safe `extend.RowScorer` from `extend.FittedRegression` alone —
type, target, predictors, `Coefficients` and `Outputs` in their JSON form.
Because it never sees the request, a fit and the same fit reloaded from a
saved model score identically. Anything scoring needs beyond the
coefficients (a Hill `k` and `s`) must therefore be in `Outputs`.

```go
Scorer: func(fit extend.FittedRegression) (extend.RowScorer, error) {
    b := fit.Coefficients["x"]
    return slopeScorer{x: fit.Predictors[0], b: b}, nil
},

type slopeScorer struct{ x string; b float64 }
func (s slopeScorer) Columns() []string { return []string{s.x} } // each must be a predictor
func (s slopeScorer) Score(r extend.ScoreRow) (float64, bool) {
    v, ok := r.NumericValue(s.x)
    return float64(s.b * v), ok // ok=false is a null score
}
```

The scorer scores the whole model: `params.terms` and a `lower` / `upper`
bound are refused. A saved fit is a `model_format_version` 3 file; loading
it on an instance that does not register (or a profile hides) the type is
`PULSE_MODEL_REGRESSION_UNREGISTERED`.

## 6. Resample and selection

Nothing to write: `resample` (jackknife, bootstrap) refits a fresh model from
your factory per replicate with the built-in drivers and replaces
`std_errors`, `statistics` and `p_values` with the replicate figures
(`p_values` only when you declare `PValues`), so
**the factory must be re-entrant** and report the same coefficient names on
every fit (`_OUTPUT_MISMATCH` `replicate_coefficients`). `selection` needs
`SupportsSelection` and a `Fit.Deviance`. Both read the request deadline
before every refit.

## 7. The registration probe

`pulse.New` builds the factory against a synthetic schema (`probe_y` ~
`probe_x1` + `probe_x2`, plus a weight column), hands it params satisfying
your declaration (every `Required` param gets a placeholder of its
`JSONType`; a `Required` array gets the probe's two predictor names), fits
it over eight synthetic rows and checks the output. A factory may refuse an
absent `Required` param; one that refuses the placeholder fails
`pulse.New` with `PULSE_EXTENSION_FACTORY_PANIC` and `details.probe_params`.
A data-dependent refusal (`PROCESSING_REGRESSION_*`) on the eight rows is
tolerated. Output with no coefficients, an empty or repeated name, a
missing / misshapen `Vcov` or a finite `PValue` without `PValues` is
`_OUTPUT_MISMATCH`, checked again on every run.

## 8. Feature profiles, guidance and multiplicity

- A registration may carry `Purpose`, `Interpretation` and `DependsOn`
  exactly as the other categories do; the manifest lists it under
  `extensions.regressions`, `pulse schema` and the MCP `regressions[].type`
  enum include it, and a feature profile that omits it makes the name act
  never-registered.
- A custom-only profile meets the REG_* dependency edges of
  `ATTR_REG_PREDICT`, `capability:model_save` and `capability:compose_fits`
  (a `Scorer` for the first two).
- Declare `PValues: true` when the model reports p-values. Then only a
  coefficient carrying a `PValue` joins a default multiplicity family (an
  explicit `multiplicity.coefficients` list names the model's own
  coefficients), and predict counts one p-value per non-intercept predictor
  (basis `lower_bound`). Without it the model emits none: predict counts
  none, and a `multiplicity` block with any method but `none` is refused
  with `PULSE_MULTIPLICITY_INVALID` at predict and at runtime alike.

## 9. Test it

Mirror the in-repo suites, which import only public packages:

- `extensions_regression_test.go` — an OLS clone matching `REG_OLS` within
  1e-9 (unweighted, frequency, probability), byte-identical across
  `DecodeWorkers` / `ShardWorkers`.
- `extensions_regression_hill_test.go` / `_hill_mmm_test.go` — a nonlinear
  fit recovering a seeded truth, a scorer through `ATTR_REG_PREDICT`, save
  and reload, bootstrap, manifest and profile hiding.
- `TestExtensions_*` — the parity suites: predict equals runtime for every
  refusal, and the manifest, schema and MCP enum list the model.

Then run `make sites ARGS="categories"` for the surfaces an extension
category touches, and see [Extension Points](extension-points.md) for the
full error-code table.
