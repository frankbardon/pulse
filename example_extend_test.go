package pulse_test

// Runnable GoDoc examples for extending and operating an engine: a
// custom operator, request templates and instance limits / return
// shaping. TestFacadeExamplesPresent pins the set.

import (
	"context"
	stderrors "errors"
	"fmt"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	perrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// spread is a custom aggregator written against the public extend
// package: the largest value minus the smallest.
type spread struct{}

func (spread) Aggregate(rows extend.Rows, field string) (float64, error) {
	lo, hi, seen := 0.0, 0.0, false
	for i := 0; i < rows.Len(); i++ {
		v, ok := rows.At(i).NumericValue(field)
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

// Register a custom aggregator through Options.Extensions. It then runs,
// predicts and appears in the manifest exactly like a built-in.
// Extension names follow <CATEGORY>_<NAMESPACE>_<NAME>.
func ExampleExtensions() {
	p, _ := newExampleEngine(pulse.Options{Extensions: pulse.Extensions{
		Aggregators: []pulse.AggregatorRegistration{{
			Name:        "AGG_EXAMPLE_SPREAD",
			Description: "Largest value minus the smallest.",
			Factory: func(*types.Aggregation, *encoding.Schema) (extend.Aggregator, error) {
				return spread{}, nil
			},
		}},
	}})
	req := totalByRegion()
	req.Aggregations[0] = &types.Aggregation{Type: "AGG_EXAMPLE_SPREAD", Field: "amount", Label: "spread"}
	resp, err := p.Process(context.Background(), req)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	for _, row := range resp.Data {
		fmt.Println(row["region"], row["spread"])
	}
	// Output:
	// east 0
	// north 20
	// south 10
}

// Render a stored request template with variables, then run the typed
// request it decodes into. Templates are JSON files under
// Options.TemplateDirs, read through Options.FS — here the same in-memory
// filesystem that holds the cohort.
func ExamplePulse_RenderTemplate() {
	fsys := afero.NewMemMapFs()
	doc := `{"target": "request",
  "variables": [{"name": "metric", "type": "field", "required": true}],
  "body": {"cohort": {"filename": "sales.pulse"},
    "groups": [{"type": "GROUP_CATEGORY", "field": "region"}],
    "aggregations": [{"type": "AGG_SUM", "field": {"$var": "metric"}, "label": "total"}]}}`
	if err := afero.WriteFile(fsys, "/templates/total_by_region.json", []byte(doc), 0o644); err != nil {
		fmt.Println("error:", err)
		return
	}

	p, _ := newExampleEngine(pulse.Options{FS: fsys, TemplateDirs: []string{"/templates"}})
	rendered, err := p.RenderTemplate("total_by_region", map[string]any{"metric": "amount"})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	resp, err := p.Process(context.Background(), rendered.Request)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("target:", rendered.Target)
	for _, row := range resp.Data {
		fmt.Println(row["region"], row["total"])
	}
	// Output:
	// target: request
	// east 15
	// north 40
	// south 50
}

// Bound what one request may cost with Options.Limits, and shape every
// response by default with Options.DefaultReturn. A request can tighten
// the shape with its own Return block but never lift a limit.
func ExampleOptions() {
	p, _ := newExampleEngine(pulse.Options{
		Limits:        pulse.Limits{MaxGroups: 2},
		DefaultReturn: &types.Return{Preset: types.ReturnPresetStandard},
	})
	fmt.Println("max groups:", p.Limits().MaxGroups)

	// Three regions exceed the two-group limit.
	_, err := p.Process(context.Background(), totalByRegion())
	var ce *perrors.CodedError
	if !stderrors.As(err, &ce) {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("by region:", ce.Code)

	// Two channels fit; the instance default shaped the response.
	req := totalByRegion()
	req.Groups[0].Field = "channel"
	resp, err := p.Process(context.Background(), req)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("by channel:", len(resp.Data), "groups, shaped by", resp.Returned.Preset)
	// Output:
	// max groups: 2
	// by region: PULSE_LIMIT_EXCEEDED
	// by channel: 2 groups, shaped by standard
}
