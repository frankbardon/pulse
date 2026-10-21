package pulse_test

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/reprosum"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// AGG_RATIO's numerator_field / denominator_field name any column a
// record carries at aggregation time — schema fields AND derived labels
// (attribute, feature). This is the ROI recipe: a contribution column
// derived per row, divided by a spend field. Fixture: the parity cohort
// (extensions_parity_test.go) — score = r%50 + 0.5, null when r%7 == 3;
// qty = r%13; region = r%4.

func ratioLabelPulse(t *testing.T) (*pulse.Pulse, afero.Fs) {
	t.Helper()
	fsys := afero.NewMemMapFs()
	writeParityCohort(t, fsys, "rl.pulse", paritySchema(t), 0, paritySmallRows)
	p, err := pulse.New(pulse.Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	return p, fsys
}

func ratioLabelAgg(label, num, den string) *types.Aggregation {
	params, _ := json.Marshal(map[string]string{"numerator_field": num, "denominator_field": den})
	return &types.Aggregation{Type: types.AGG_RATIO, Field: "qty", Label: label, Params: params}
}

// ratioLabelRef is the binned (internal/reprosum) Σnum / Σden over fixture rows whose
// region passes keepRegion, skipping rows with a null score (the
// denominator) — the reference the ratio must equal bit for bit.
func ratioLabelRef(keepRegion func(int) bool, num func(r int) float64) float64 {
	var n, d reprosum.Sum
	for r := 0; r < paritySmallRows; r++ {
		if r%7 == 3 || !keepRegion(r%4) {
			continue
		}
		n.Add(num(r))
		d.Add(float64(r%50) + 0.5)
	}
	return n.Float64() / d.Float64()
}

// TestAggRatio_DerivedLabelNumerator: a numerator naming an attribute
// label (ATTR_FORMULA) or a feature output (FEAT_SQRT) over a schema
// denominator equals Σnum/Σden exactly, ungrouped and per group row;
// predict accepts the same request without error.
func TestAggRatio_DerivedLabelNumerator(t *testing.T) {
	p, fsys := ratioLabelPulse(t)
	ctx := context.Background()
	regions := []string{"north", "south", "east", "west"}
	cases := []struct {
		name string
		mk   func(*types.Request)
		num  func(r int) float64
	}{
		{"attr_formula", func(req *types.Request) {
			req.Attributes = []*types.Attribute{{Type: types.ATTR_FORMULA, Expression: "qty * 3 + 1", Label: "contrib"}}
		}, func(r int) float64 { return float64(r%13)*3 + 1 }},
		{"feat_sqrt", func(req *types.Request) {
			req.Features = []*types.Feature{{Type: types.FEAT_SQRT, Field: "qty", Label: "contrib"}}
		}, func(r int) float64 { return math.Sqrt(float64(r % 13)) }},
	}
	for _, tc := range cases {
		for _, grouped := range []bool{false, true} {
			name := tc.name + "/ungrouped"
			if grouped {
				name = tc.name + "/grouped"
			}
			t.Run(name, func(t *testing.T) {
				req := &types.Request{Cohort: &types.Cohort{Filename: "rl.pulse"},
					Aggregations: []*types.Aggregation{ratioLabelAgg("roi", "contrib", "score")}}
				tc.mk(req)
				if grouped {
					req.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}}
				}
				if env := predictEnvelope(t, p, fsys, "rl.pulse", req); len(env.Errors) != 0 {
					t.Fatalf("predict refused a derived-label numerator: %+v", env.Errors)
				}
				resp, err := p.Process(ctx, req)
				if err != nil {
					t.Fatalf("Process: %v", err)
				}
				if !grouped {
					ftBits(t, "roi", resp.Data[0]["roi"], ratioLabelRef(func(int) bool { return true }, tc.num))
					return
				}
				if len(resp.Data) != len(regions) {
					t.Fatalf("%d group rows, want %d", len(resp.Data), len(regions))
				}
				for _, row := range resp.Data {
					reg := row["region"].(string)
					idx := -1
					for i, r := range regions {
						if r == reg {
							idx = i
						}
					}
					want := ratioLabelRef(func(g int) bool { return g == idx }, tc.num)
					ftBits(t, "roi "+reg, row["roi"], want)
				}
			})
		}
	}
}

// TestAggRatio_TwoPassAttributeNumerator: a two-pass attribute label
// (ATTR_NORMALIZED) as numerator equals the ratio of the two sums the
// same response reports, bit for bit.
func TestAggRatio_TwoPassAttributeNumerator(t *testing.T) {
	p, _ := ratioLabelPulse(t)
	req := &types.Request{Cohort: &types.Cohort{Filename: "rl.pulse"},
		Filterers:  []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "score != nil"}},
		Attributes: []*types.Attribute{{Type: types.ATTR_NORMALIZED, Field: "qty", Label: "norm"}},
		Aggregations: []*types.Aggregation{
			ratioLabelAgg("r", "norm", "score"),
			{Type: types.AGG_SUM, Field: "norm", Label: "sn"},
			{Type: types.AGG_SUM, Field: "score", Label: "sd"},
		}}
	resp, err := p.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	row := resp.Data[0]
	sn, _ := row["sn"].(float64)
	if sn == 0 {
		t.Fatalf("Σnorm = %v: the case proves nothing", row["sn"])
	}
	ftBits(t, "r", row["r"], sn/row["sd"].(float64))
}

// TestAggRatio_DerivedLabelZeroDenominator: a denominator summing to 0
// — a derived label (qty * 0) or a schema field filtered to zeros — is
// NaN in Go and null on the wire (scalar and components.ratio), never
// an error.
func TestAggRatio_DerivedLabelZeroDenominator(t *testing.T) {
	p, _ := ratioLabelPulse(t)
	cases := map[string]*types.Request{
		"derived_zero_den": {Cohort: &types.Cohort{Filename: "rl.pulse"},
			Attributes: []*types.Attribute{
				{Type: types.ATTR_FORMULA, Expression: "qty * 3 + 1", Label: "contrib"},
				{Type: types.ATTR_FORMULA, Expression: "qty * 0", Label: "spend"},
			},
			Aggregations: []*types.Aggregation{ratioLabelAgg("roi", "contrib", "spend")}},
		"schema_den_filtered_to_zero": {Cohort: &types.Cohort{Filename: "rl.pulse"},
			Filterers:    []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "qty == 0"}},
			Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Expression: "qty * 3 + 1", Label: "contrib"}},
			Aggregations: []*types.Aggregation{ratioLabelAgg("roi", "contrib", "qty")}},
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			resp, err := p.Process(context.Background(), req)
			if err != nil {
				t.Fatalf("Process: %v", err)
			}
			if v, ok := resp.Data[0]["roi"].(float64); !ok || !math.IsNaN(v) {
				t.Fatalf("roi = %v (%T), want NaN", resp.Data[0]["roi"], resp.Data[0]["roi"])
			}
			raw, err := json.Marshal(descriptor.NewEnvelope(resp))
			if err != nil {
				t.Fatalf("marshal envelope: %v", err)
			}
			var env struct {
				Data struct {
					Data       []map[string]any `json:"data"`
					Components struct {
						Aggregations []struct {
							Operator map[string]any `json:"operator"`
						} `json:"aggregations"`
					} `json:"components"`
				} `json:"data"`
			}
			if err := json.Unmarshal(raw, &env); err != nil {
				t.Fatalf("unmarshal: %v\n%s", err, raw)
			}
			if v, ok := env.Data.Data[0]["roi"]; !ok || v != nil {
				t.Fatalf("wire roi = %v (present %v), want null:\n%s", v, ok, raw)
			}
			aggs := env.Data.Components.Aggregations
			if len(aggs) != 1 {
				t.Fatalf("components.aggregations = %d entries:\n%s", len(aggs), raw)
			}
			if v, ok := aggs[0].Operator["ratio"]; !ok || v != nil {
				t.Fatalf("components ratio = %v (present %v), want null", v, ok)
			}
			if aggs[0].Operator["denominator"] != 0.0 {
				t.Fatalf("components denominator = %v, want 0", aggs[0].Operator["denominator"])
			}
		})
	}
}

// TestAggRatio_UnknownLabelRefusedLikePredict: a numerator or
// denominator naming neither a schema field nor a derived label is
// SERVICE_VALIDATION with the same message and details from predict and
// runtime.
func TestAggRatio_UnknownLabelRefusedLikePredict(t *testing.T) {
	p, fsys := ratioLabelPulse(t)
	attrs := []*types.Attribute{{Type: types.ATTR_FORMULA, Expression: "qty * 3 + 1", Label: "contrib"}}
	cases := map[string]struct{ num, den, msg string }{
		"numerator":   {"nope", "score", "aggregation AGG_RATIO: params.numerator_field references unknown field nope"},
		"denominator": {"contrib", "nope", "aggregation AGG_RATIO: params.denominator_field references unknown field nope"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			mk := func() *types.Request {
				return &types.Request{Cohort: &types.Cohort{Filename: "rl.pulse"}, Attributes: attrs,
					Aggregations: []*types.Aggregation{ratioLabelAgg("roi", tc.num, tc.den)}}
			}
			_, rerr := p.Process(context.Background(), mk())
			ce := requireCode(t, rerr, errors.SERVICE_VALIDATION)
			if ce.Message != tc.msg {
				t.Fatalf("runtime message = %q, want %q", ce.Message, tc.msg)
			}
			sameEntry(t, predictEnvelope(t, p, fsys, "rl.pulse", mk()), rerr)
		})
	}
}
