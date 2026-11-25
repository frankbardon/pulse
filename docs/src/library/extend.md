# Writing Operators (`extend`)

**Audience:** embedders adding an operator Pulse does not ship.

## When to use it

Reach for `extend` when no built-in operator answers your question and a
request-level workaround (an expression, a computed attribute) would be
awkward. You implement one interface from
[`github.com/frankbardon/pulse/extend`](https://pkg.go.dev/github.com/frankbardon/pulse/extend),
wrap it in the matching registration on `pulse.Options.Extensions`, and
Pulse adapts it at `pulse.New`. From then on predict, the manifest, MCP
and the runtime treat it exactly like a built-in.

`extend` imports only the public `encoding`, `types`, `errors` and
`linalg` packages, never the engine, so an operator package stays small.

## One aggregator

```go compile
package spread

import (
	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
)

// Spread is the largest value minus the smallest.
type Spread struct{}

func (Spread) Aggregate(rows extend.Rows, field string) (float64, error) {
	lo, hi, seen := 0.0, 0.0, false
	for i := 0; i < rows.Len(); i++ {
		v, ok := rows.At(i).NumericValue(field) // false for null
		if !ok {
			continue
		}
		if !seen || v < lo {
			lo = v
		}
		if !seen || v > hi {
			hi = v
		}
		seen = true
	}
	return hi - lo, nil
}

// New builds an instance that serves AGG_ACME_SPREAD.
func New(dataDir string) (*pulse.Pulse, error) {
	return pulse.New(pulse.Options{DataDir: dataDir, Extensions: pulse.Extensions{
		Aggregators: []pulse.AggregatorRegistration{{
			Name:        "AGG_ACME_SPREAD", // <CATEGORY>_<NAMESPACE>_<NAME>
			Description: "Largest value minus the smallest.",
			Factory: func(*types.Aggregation, *encoding.Schema) (extend.Aggregator, error) {
				return Spread{}, nil
			},
		}},
	}})
}
```

A request then names `"type": "AGG_ACME_SPREAD"` like any aggregator.

## Rules that bite

- A `Record` or `Rows` is valid only during the call that received it:
  copy values out, never keep the handle (package doc, "Reuse contract").
- `NumericValue` is false for null, for set fields and for vectors; a
  categorical's `NumericValue` is its dictionary index. `date` is epoch
  days, `datetime` epoch seconds.
- Optional sibling interfaces (`OnlineAggregator`, `MergeableAggregator`,
  …) opt an operator into streaming and shard merging; without them it
  runs buffered.

## Where the detail lives

- [Extension Points](../internals/extension-points.md): every
  registration shape, naming policy, validation, component schemas,
  guidance metadata, embedder skills and examples.
- Per-category recipes: [Adding an Aggregator](../internals/adding-aggregator.md)
  and its siblings, [Adding a Regression Model](../internals/adding-regression.md),
  [Adding an Overlay Kind](../internals/adding-overlay.md),
  [Adding a Post-Fit Operator](../internals/adding-post-fit.md).
- GoDoc: [`extend`](https://pkg.go.dev/github.com/frankbardon/pulse/extend),
  [`pulse.Extensions`](https://pkg.go.dev/github.com/frankbardon/pulse#Extensions).
