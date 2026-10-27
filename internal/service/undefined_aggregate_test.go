package service

import (
	"encoding/json"
	"fmt"
	"math"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// Undefined figures are null, consistently (matrix-extensions-hardening
// E4-S5). A figure that is undefined over no contributing value — a
// mean, an extreme, a spread, a quantile, a mode, a shape moment — is
// NaN in Go and null on the wire on EVERY execution arm, never 0. A
// count or a sum of nothing stays 0: the empty sum is defined.

// undefRows: g = i % 5; every g = 4 row has x, y and s null (no value
// at all), and every g = 3 row carries weight 0 (Σw = 0 under a request
// weight). The rest is parityRows' mix (nulls, empty sets, weights 0..3).
func undefRows(n int) []parityRow {
	rows := parityRows(n, freqWeight)
	for i := range rows {
		r := &rows[i]
		r.g = uint8(i % 5)
		switch r.g {
		case 4:
			r.xNull, r.yNull, r.sNull = true, true, true
		case 3:
			r.w = 0
		}
	}
	return rows
}

// undefOp is one aggregator under the sweep: undefined says the figure
// over no value is NaN (else it is want, a defined count / sum);
// weighted says the operator is weight-aware, so it also runs under a
// request weight, where a Σw = 0 group (g = 3) is undefined as well.
type undefOp struct {
	op        types.AggregationType
	field     string
	params    string
	undefined bool
	want      float64
	weighted  bool
	// zeroWeightDefined: under a weight, the g = 3 figure is still
	// defined (a sum, a count, a mode count of weight 0 is 0).
	zeroWeightDefined bool
	// weightedOnly: the operator needs a weight (AGG_WEIGHTED_MEAN).
	weightedOnly bool
}

var undefOps = []undefOp{
	{op: types.AGG_AVERAGE, field: "x", undefined: true, weighted: true},
	{op: types.AGG_WEIGHTED_MEAN, field: "x", undefined: true, weighted: true, weightedOnly: true},
	{op: types.AGG_MIN, field: "x", undefined: true},
	{op: types.AGG_MAX, field: "x", undefined: true},
	{op: types.AGG_RANGE, field: "x", undefined: true},
	{op: types.AGG_VARIANCE, field: "x", undefined: true, weighted: true},
	{op: types.AGG_STDDEV, field: "x", undefined: true, weighted: true},
	{op: types.AGG_MEDIAN, field: "x", undefined: true, weighted: true},
	{op: types.AGG_PERCENTILE, field: "x", params: `{"percentile":37.5}`, undefined: true, weighted: true},
	{op: types.AGG_MODE, field: "x", undefined: true, weighted: true},
	{op: types.AGG_SKEWNESS, field: "y", undefined: true, weighted: true},
	{op: types.AGG_KURTOSIS, field: "y", undefined: true, weighted: true},
	{op: types.AGG_ZSCORE, field: "x", undefined: true},
	{op: types.AGG_SET_CARDINALITY_AVG, field: "s", undefined: true, weighted: true},
	// Controls: a count or a sum of nothing is 0, not undefined.
	{op: types.AGG_COUNT, field: "x", want: 0, weighted: true, zeroWeightDefined: true},
	{op: types.AGG_SUM, field: "x", want: 0, weighted: true, zeroWeightDefined: true},
	{op: types.AGG_MODE_COUNT, field: "x", want: 0, weighted: true, zeroWeightDefined: true},
	{op: types.AGG_DISTINCT_COUNT, field: "x", want: 0},
	{op: types.AGG_SET_CARDINALITY_SUM, field: "s", want: 0, weighted: true, zeroWeightDefined: true},
}

// undefCell returns the Go figure and the wire figure (decoded from the
// real envelope) of label in the data row whose g equals g.
func undefCell(t *testing.T, resp *types.Response, label string, g int) (float64, any) {
	t.Helper()
	var goV float64
	found := false
	for _, row := range resp.Data {
		if fmt.Sprint(row["g"]) == fmt.Sprint(g) {
			v, ok := row[label].(float64)
			if !ok {
				t.Fatalf("g=%d %s = %v (%T), want a float64", g, label, row[label], row[label])
			}
			goV, found = v, true
		}
	}
	if !found {
		t.Fatalf("no data row with g=%d", g)
	}
	wire, ok := dataRow(t, envelopeJSON(t, resp), float64(g))[label]
	if !ok {
		t.Fatalf("g=%d %s absent on the wire, want present", g, label)
	}
	return goV, wire
}

// TestUndefinedFigures_NoValueIsNull: every aggregator over a group with
// no value answers NaN (Go) / null (wire) when its figure is undefined,
// and 0 when it is a count or a sum — on the buffered, streaming,
// grouped-streaming, two-pass, shard-parallel and parallel-decode arms,
// unweighted and (weight-aware operators) under a request weight, where
// a group whose every weight is 0 is undefined too.
func TestUndefinedFigures_NoValueIsNull(t *testing.T) {
	t.Parallel()

	store := newParityStore(t, "undef", undefRows)
	sources := []*paritySource{nil}
	for _, s := range paritySources() {
		if s.name == "request_frequency" {
			sources = append(sources, &s)
		}
	}
	for _, o := range undefOps {
		for _, src := range sources {
			if src != nil && !o.weighted || src == nil && o.weightedOnly {
				// AGG_WEIGHTED_MEAN runs weighted only.
				continue
			}
			name := string(o.op)
			if src != nil {
				name += "/weighted"
			}
			t.Run(name, func(t *testing.T) {
				ran := 0
				for _, mode := range parityModes() {
					path := store.paths[mode.cohort]
					build := func() *types.Request {
						agg := &types.Aggregation{Type: o.op, Field: o.field, Label: "f"}
						if o.params != "" {
							agg.Params = json.RawMessage(o.params)
						}
						return &types.Request{Cohort: &types.Cohort{Filename: path},
							Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
							Aggregations: []*types.Aggregation{agg}}
					}
					if src != nil {
						// A weighted request stays on its unweighted twin's
						// arm; skip an arm the twin does not reach.
						twin := build
						if o.weightedOnly {
							twin = func() *types.Request {
								r := build()
								r.Aggregations[0].Type = types.AGG_AVERAGE
								return r
							}
						}
						if runArmRequest(t, store, mode, path, twin, nil) == nil {
							continue
						}
					}
					resp := runArmRequest(t, store, mode, path, build, src)
					if resp == nil {
						continue
					}
					ran++
					groups := []int{4}
					if src != nil && !o.zeroWeightDefined {
						groups = append(groups, 3)
					}
					for _, g := range groups {
						goV, wire := undefCell(t, resp, "f", g)
						if o.undefined {
							if !math.IsNaN(goV) || wire != nil {
								t.Errorf("%s: g=%d figure = %v (wire %v), want NaN / null", mode.name, g, goV, wire)
							}
							continue
						}
						if goV != o.want || wire != o.want {
							t.Errorf("%s: g=%d figure = %v (wire %v), want %v", mode.name, g, goV, wire, o.want)
						}
					}
					// A group with values stays a number.
					if goV, wire := undefCell(t, resp, "f", 0); math.IsNaN(goV) || wire == nil {
						t.Errorf("%s: g=0 figure = %v (wire %v), want a number", mode.name, goV, wire)
					}
				}
				// A streamable operator reaches the streaming arms too (the
				// shape moments: buffered, streaming, grouped streaming); a
				// buffered-only one (median, percentile, z-score) at least
				// the buffered arm.
				if ran < 1 || o.op.Streamable() && ran < 3 {
					t.Fatalf("only %d arm(s) ran", ran)
				}
			})
		}
	}
}
