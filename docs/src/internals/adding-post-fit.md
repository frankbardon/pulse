# Adding a Post-Fit Operator (Embedders)

> **Embedding Pulse, not contributing to it.** This recipe registers a
> `POSTFIT_<NAMESPACE>_<NAME>` operator through
> `pulse.Options.Extensions.PostFits` and the public `extend` package. The
> category is extension-only: Pulse ships no built-in post-fit operator. The
> field-by-field contract is in [Extension
> Points](extension-points.md#post-fit-operator-postfits); this page walks the
> order you meet it in, with one worked operator — a marginal-ROI grid over a
> fitted media-mix model — and the embedder example that ships with it.

**Audience:** an embedder whose analysis starts where a fit ends — a
response-curve table, a marginal-return grid, a scenario table over an
optimize solution — and who wants it to run inside a Compose batch and
describe itself like a built-in: predict, manifest, MCP, Explain, feature
profiles.

A post-fit operator reads **no record**. It sees only the fits its entry names
(coefficients, standard errors, the `vcov` matrix, each channel's adstock
record, a Bayesian fit's draws under `return_draws`), the batch's slot results
(the `result()` reader), every optimize block's solution and its own params.
It runs once per `post_fit` entry, after the fits and optimize blocks and
before the nested-model comparisons.

## 1. Write the operator

`POSTFIT_ACME_MROI` reads one `REG_MMM_HILL` fit's channel coefficient, Hill
half-saturation `k:<channel>` and shape `s:<channel>`, folds in the channel's
adstock weights, and returns a grid of response and marginal return per spend
level — plus the marginal return at a reference spend read from a slot:

```go
// marginalROI is POSTFIT_ACME_MROI. For each per-period spend level in
// params.spend it reads the channel's Hill curve off one REG_MMM_HILL
// fit and returns the response and the marginal return of one more unit
// of spend. Adstock carries spend forward, so a steady spend x reaches
// the curve as a = m·x, m the sum of the channel's adstock weights (1
// without an adstock record).
func marginalROI(in extend.PostFitInput) (extend.PostFitOutput, error) {
    var p struct {
        Channel  string    `json:"channel"`
        Spend    []float64 `json:"spend"`
        RefSlot  string    `json:"reference_slot"`
        RefValue string    `json:"reference_aggregation"`
    }
    if err := json.Unmarshal(in.Params(), &p); err != nil {
        return extend.PostFitOutput{}, err
    }
    if len(in.Fits()) != 1 {
        return extend.PostFitOutput{}, fmt.Errorf("name exactly one fit, not %d", len(in.Fits()))
    }
    fit := in.Fit(in.Fits()[0])
    c := fit.Coefficients() // a copy, full precision
    beta, okB := c[p.Channel]
    k, okK := c["k:"+p.Channel]
    s, okS := c["s:"+p.Channel]
    if !okB || !okK || !okS {
        return extend.PostFitOutput{}, fmt.Errorf("fit %q has no Hill curve for %q", fit.Name(), p.Channel)
    }
    m := 1.0
    if ad, ok := fit.Adstock(p.Channel); ok {
        m = 0
        for _, w := range ad.Weights {
            m += w
        }
    }
    // curve is the response β·hill(m·x) and its slope per unit of x:
    // with r = (k / (m·x))^s, hill = 1/(1+r) and the slope is
    // β·s·r / (x·(1+r)²). float64() keeps every product unfused, so the
    // figures are the same bits on every architecture.
    curve := func(x float64) (resp, slope float64) {
        r := detmath.Pow(k/float64(m*x), s)
        return beta / (1 + r), float64(beta*float64(s*r)) / float64(x*float64((1+r)*(1+r)))
    }
    grid := &types.PostFitTable{Columns: []string{"spend", "response", "marginal_roi", "vs_half_saturation"}}
    for _, x := range p.Spend {
        if !(x > 0) {
            return extend.PostFitOutput{}, fmt.Errorf("spend level %v is not positive", x)
        }
        resp, slope := curve(x)
        side := "below"
        if float64(m*x) >= k {
            side = "above"
        }
        grid.Rows = append(grid.Rows, []any{x, resp, slope, side})
    }
    atRef := math.NaN() // null on the wire without a reference
    if p.RefSlot != "" {
        x, err := in.Result(p.RefSlot, p.RefValue)
        if err != nil {
            return extend.PostFitOutput{}, err
        }
        _, atRef = curve(x)
    }
    return extend.PostFitOutput{
        Scalars: map[string]float64{"beta": beta, "half_saturation_spend": k / m, "mroi_at_reference": atRef},
        Tables:  map[string]*types.PostFitTable{"grid": grid},
    }, nil
}
```

Rules the operator lives under:

- **Deterministic and pure.** Every accessor returns a copy, `in` is valid only
  during the call. Write cross-architecture-stable math — `linalg/detmath` for
  transcendentals and `float64()` around products that feed an add — if your
  embedders compare figures across machines.
- **Exactly the declared output.** Every declared scalar and table, nothing
  else; a table's `Columns` are its declared names in order and each cell is a
  `float64` (a float column), a `string` (a string column) or `nil`. Anything
  else is the fatal `PULSE_EXTENSION_POSTFIT_OUTPUT_MISMATCH`. A `NaN` scalar
  or cell is `null` on the wire.
- **An error fails the entry alone.** Above, a non-positive spend returns an
  error: the entry keeps `{label, type, error}` (`PULSE_POSTFIT_FAILED` for an
  uncoded error; a `*errors.CodedError` keeps its code), the batch warns
  `PULSE_SLOT_FAILED` (`slot_kind` `post_fit`) and every other result stands.
  Under `Strict` the batch fails with the entry's code. A panic is
  `PROCESSING_PANIC` (`details.op` `compose_post_fit`).

## 2. Register it

```go
// mroiRegistration declares POSTFIT_ACME_MROI: its params (checked at
// predict and at run time before the operator runs), its exact outputs
// and its guidance.
func mroiRegistration() pulse.PostFitRegistration {
    return pulse.PostFitRegistration{
        Name:        "POSTFIT_ACME_MROI",
        Description: "marginal-ROI grid over a fitted REG_MMM_HILL channel",
        Purpose: &descriptor.Purpose{
            Plain:   "Shows what one more unit of spend returns at each spend level of a fitted media-mix channel.",
            Intents: []string{"drivers"},
            Questions: []string{
                "What does one more unit of spend return at each level?",
                "Where does the channel start to saturate?",
            },
            UseCases: map[descriptor.Domain]string{descriptor.DomainOps: "Spend planning from a weekly media-mix model."},
            NotFor: []descriptor.Alternative{
                {When: "you need the spend split that maximises the total return", Use: "capability:compose_optimize"},
            },
            Level: descriptor.LevelIntermediate,
        },
        Params: []pulse.ParamMeta{
            {Name: "channel", JSONType: "string", Required: true, Description: "the Hill channel to read"},
            {Name: "spend", JSONType: "array", Required: true, Description: "per-period spend levels, each positive"},
            {Name: "reference_slot", JSONType: "string", Description: "slot whose value is the reference spend"},
            {Name: "reference_aggregation", JSONType: "string", Description: "that slot's aggregation label"},
        },
        Outputs: extend.PostFitSchema{
            Scalars: []string{"beta", "half_saturation_spend", "mroi_at_reference"},
            Tables: []extend.PostFitTableSchema{{Name: "grid", Columns: []extend.PostFitColumn{
                {Name: "spend", Kind: extend.PostFitFloat},
                {Name: "response", Kind: extend.PostFitFloat},
                {Name: "marginal_roi", Kind: extend.PostFitFloat},
                {Name: "vs_half_saturation", Kind: extend.PostFitString},
            }}},
        },
        Run: marginalROI,
    }
}
```

What each declaration buys:

- `Params` — checked at predict and at run time before the operator runs: an
  unknown key is `PULSE_REQUEST_UNKNOWN_FIELD`, a wrong type or a missing
  required key `PULSE_REQUEST_PARAM_INVALID`, both at `post_fit[i].params`.
  Undeclared (`nil`), params pass through unchecked. A post-fit param is never
  expression-capable.
- `Outputs` — the exact output contract, validated at `pulse.New` (at least
  one scalar or table, unique names, float / string columns) and listed in the
  manifest under `extensions.post_fits`.
- `Purpose` — guidance validated like the built-ins'; Explain appends its
  plain sentence to the entry's step, and its intents reach the manifest.
- `DependsOn` — feature edges. The operator's host is
  `capability:compose_post_fit` (which needs `capability:compose_fits`), so a
  [feature profile](../library/feature-profiles.md) that omits it hides it
  everywhere: a request naming it is refused as an unknown type.

## 3. Ship an example

`TestEveryOperatorHasAnExampleTag` covers the built-in operators of the
shipped library, which holds no post-fit operator. Your operator's example
ships with your registration, through `Extensions.Examples`:

```go
// newPulse registers the operator and ships its examples (an fs.FS of
// top-level .json files — your embed.FS) on the instance.
func newPulse(opts pulse.Options, examples fs.FS) (*pulse.Pulse, error) {
    opts.Extensions.PostFits = append(opts.Extensions.PostFits, mroiRegistration())
    opts.Extensions.Examples = examples
    return pulse.New(opts)
}
```

The example is a Compose body plus the `_meta` block; its `operators` tag
names the post-fit operator beside the built-ins the body uses, and its
`capabilities` declare the Compose features no structural detector sees:

```json
{
  "_meta": {
    "name": "acme-mroi-grid",
    "category": "acme",
    "description": "Marginal return of one more unit of spend on channel x at five spend levels and at the mean weekly spend, read off a media-mix fit whose decay is fitted in-model.",
    "tags": ["compose", "regression", "time-series"],
    "intents": ["drivers"],
    "capabilities": ["capability:compose_fits", "capability:compose_post_fit", "capability:regression_order"],
    "operators": ["AGG_AVERAGE", "POSTFIT_ACME_MROI", "REG_MMM_HILL"]
  },
  "requests": [
    {"label": "spend", "cohort": {"filename": "media.pulse"},
     "aggregations": [{"type": "AGG_AVERAGE", "field": "x", "label": "mean_x"}]}
  ],
  "fits": [
    {"name": "mmm", "request": {"cohort": {"filename": "media.pulse"},
      "regressions": [{"type": "REG_MMM_HILL", "target": "y", "predictors": ["x"],
        "order": {"field": "week"},
        "params": {"channels": ["x"], "adstock": "geometric", "max_lag": 6}}]}}
  ],
  "post_fit": [
    {"label": "mroi", "type": "POSTFIT_ACME_MROI", "fits": ["mmm"],
     "params": {"channel": "x", "spend": [20, 40, 60, 80, 100],
                "reference_slot": "spend", "reference_aggregation": "mean_x"}}
  ]
}
```

`pulse.New` validates it HARD against every registration (the rules:
[Extension Points, Embedder examples](extension-points.md#embedder-examples-extensionsexamples));
it is then served by `pulse_examples_search` / `pulse_examples_get` and the
instance ontology, and hidden with the operator by a feature profile.

## 4. Read the result

The entry's result lands in the response's `post_fit` root at the same index
(figures rounded; the recipe test's cohort):

```jsonc
"post_fit": [{"label": "mroi", "type": "POSTFIT_ACME_MROI",
  "scalars": {"beta": 7.99, "half_saturation_spend": 24.7, "mroi_at_reference": 0.098},
  "tables": {"grid": {"columns": ["spend", "response", "marginal_roi", "vs_half_saturation"],
    "rows": [[20, 3.16, 0.192, "below"], [40, 5.79, 0.0799, "above"], "…"]}}}]
```

The root is pinned outside the Compose `return`: always emitted whole, at full
precision. Explain describes the entry in request mode (a `post_fit[i]` step:
operator, label, fits read, params, phase) and reads each result in response
mode (a descriptive finding with `scalars.<name>` and `tables.<name>.rows`
numbers; a failed entry is `not_computable` with its code).

## 5. Test it

Pin the operator against an independent oracle, not its own formula. The
recipe's test (`internal/integration/adding_postfit_recipe_test.go`) runs the
example above with an extra slot reading the engine's `response("mmm", "x",
spend)` at each grid level and either side of it: the grid's `response` must
equal the reader and its `marginal_roi` the reader's central difference.
`PredictCompose` must accept the body, and a failing input must fail the entry
alone.

## Where to look

| Concern | Place |
|---|---|
| public registration + operator types | `extensions_postfit.go` (`PostFitRegistration`), `extend/postfit.go`, `types/postfit.go` |
| the request / schema / output rules predict and runtime share | `internal/postfit` |
| the phase (both orchestrators) | `internal/service/compose_postfit.go` |
| Explain steps and findings | `internal/guide/explain_postfit.go` |
| the agent-facing skill | `skills/compose-post-fit.md` |
| migration rows for each public symbol | `docs/roadmap/v1.0.0-api-and-release/03-embedder-migration.md` |
