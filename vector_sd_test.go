package pulse_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// AGG_VEC_SD end to end (U30 matrix-extensions-hardening E5-S1): the
// per-element sample sd as a crosstab element cell equals √ of
// AGG_WELFORD's sample variance of the member (both m2 / (Σw − 1),
// unweighted and under a frequency weight — read off the twin's
// components stddev), cell for cell and margin for margin; a standard deviation's margin recomputes, so predict and
// the runtime both decline the fused arm, and every arm answers the
// same bits.

// vsdRequest is the vexpr crosstab with an AGG_VEC_SD element cell (b3
// of b1..b4) or its AGG_WELFORD twin over b3.
func vsdRequest(cohort string, welford bool, weight *types.WeightSpec) *types.Request {
	cell := &types.Aggregation{Type: types.AGG_VEC_SD, Fields: []string{"b*"}, Label: "sd", Params: json.RawMessage(`{"element":"b3"}`)}
	if welford {
		cell = &types.Aggregation{Type: types.AGG_WELFORD, Field: "b3", Label: "sd"}
	}
	return &types.Request{Cohort: &types.Cohort{Filename: cohort}, Weight: weight, Crosstab: &types.CrosstabSpec{
		Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "qty"}},
		Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "b1"}},
		Cell:    cell,
		Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
	}}
}

// vsdWelfordSD flattens the twin's cell and margin components to their
// stddev — √ of the sample variance m2 / (Σw − 1) — NaN where the
// component map is absent (no row) or carries no stddev (undefined).
func vsdWelfordSD(t *testing.T, r *types.Response) []float64 {
	t.Helper()
	cc := r.Components.Crosstab
	one := func(m map[string]any) float64 {
		v, ok := m["stddev"].(float64)
		if !ok {
			return math.NaN()
		}
		return v
	}
	var out []float64
	for _, row := range cc.CellComponents {
		for _, m := range row {
			out = append(out, one(m))
		}
	}
	for _, m := range cc.RowMarginComponents {
		out = append(out, one(m))
	}
	for _, m := range cc.ColumnMarginComponents {
		out = append(out, one(m))
	}
	return append(out, one(cc.GrandTotalComponents))
}

func TestVecSD_CrosstabElementMatchesWelford(t *testing.T) {
	ctx := context.Background()
	fsys := afero.NewMemMapFs()
	writeVexprCohort(t, fsys, "v.pulse", 0, 90)
	newP := func(o pulse.Options) *pulse.Pulse {
		o.FS = fsys
		p, err := pulse.New(o)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	base := newP(pulse.Options{})
	arms := map[string]*pulse.Pulse{
		"default":           base,
		"fusion disabled":   newP(pulse.Options{DisableCrosstabFusion: true}),
		"serial":            newP(pulse.Options{DecodeWorkers: 1}),
		"buffered parallel": newP(pulse.Options{DisableCrosstabFusion: true, DecodeWorkers: 4}),
	}
	for _, w := range []*types.WeightSpec{nil, {Field: "qty", Kind: types.WeightKindFrequency}} {
		t.Run(fmt.Sprintf("weighted=%v", w != nil), func(t *testing.T) {
			req := vsdRequest("v.pulse", false, w)
			// predict ≡ runtime: a recompute margin never fuses.
			pr, err := base.Predict(ctx, req)
			if err != nil || !pr.Valid {
				t.Fatalf("predict refused the AGG_VEC_SD element crosstab: %v %+v", err, pr)
			}
			rtOK, _ := processing.CanFuseCrosstab(req, vexprSchema(), pulse.ServiceForTest(base).Extensions())
			if pr.CrosstabFusable == nil || *pr.CrosstabFusable != rtOK || rtOK {
				t.Fatalf("CrosstabFusable predict %v (reasons %v), runtime %v; want both false",
					pr.CrosstabFusable != nil && *pr.CrosstabFusable, pr.CrosstabFusionReasons, rtOK)
			}
			twin, err := base.Process(ctx, vsdRequest("v.pulse", true, w))
			if err != nil {
				t.Fatal(err)
			}
			want := vsdWelfordSD(t, twin)
			var ref []float64
			for name, p := range arms {
				resp, err := p.Process(ctx, vsdRequest("v.pulse", false, w))
				if err != nil {
					t.Fatalf("%s: %v", name, err)
				}
				got := vxCells(t, resp)
				if len(got) != len(want) {
					t.Fatalf("%s: %d figures, twin %d", name, len(got), len(want))
				}
				defined := 0
				for i := range got {
					if !vexprClose(got[i], want[i]) {
						t.Errorf("%s: figure %d = %v, √welford variance %v", name, i, got[i], want[i])
					}
					if !math.IsNaN(got[i]) {
						defined++
					}
				}
				if defined == 0 || defined == len(got) {
					t.Fatalf("%s: fixture drift — %d of %d figures defined (want some undefined, N* ≤ 1)", name, defined, len(got))
				}
				if ref == nil {
					ref = got
				} else if !vxSame(got, ref) {
					t.Errorf("%s: figures differ bitwise from another arm", name)
				}
			}
		})
	}
}
