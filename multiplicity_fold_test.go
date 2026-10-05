package pulse_test

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/internal/processing/multiplicity"
	"github.com/frankbardon/pulse/types"
)

// fixedPTest is an extension row test that reports a constant p.
type fixedPTest struct{ p float64 }

func (fixedPTest) UpdateRow(extend.Record) error { return nil }
func (f fixedPTest) Finalize() (*types.TestResult, error) {
	return &types.TestResult{Type: "TEST_ACME_FIXED_P", PValue: f.p, Alpha: 0.05, RejectNull: f.p < 0.05}, nil
}

// TestMultiplicity_ExtensionTestJoinsFamily: an embedder-registered
// TEST_* joins the request family exactly like a built-in — counted in
// m, adjusted with its peers, raw figures untouched.
func TestMultiplicity_ExtensionTestJoinsFamily(t *testing.T) {
	_, fs, cohort := acceptanceCohort(t)
	p, err := pulse.New(pulse.Options{FS: fs, Extensions: pulse.Extensions{
		Tests: []pulse.TestRegistration{{
			Name:       "TEST_ACME_FIXED_P",
			Tier:       pulse.TestTierRow,
			Streamable: true,
			RowFactory: func(*types.Test, *encoding.Schema) (extend.RowTest, error) { return fixedPTest{p: 0.02}, nil },
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	mk := func(m *types.Multiplicity) *types.Request {
		return &types.Request{
			Cohort:       &types.Cohort{Filename: cohort},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Label: "n"}},
			Tests: []*types.Test{
				{Type: types.TEST_T, Field: "x", SplitBy: "g", Label: "builtin"},
				{Type: "TEST_ACME_FIXED_P", Field: "x", Label: "ext"},
			},
			Multiplicity: m,
		}
	}
	ctx := context.Background()
	base, err := p.Process(ctx, mk(nil))
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.Process(ctx, mk(&types.Multiplicity{Method: types.MultiplicityMethodBonferroni}))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Tests) != 2 {
		t.Fatalf("got %d test results, want 2", len(got.Tests))
	}
	ps := []float64{base.Tests[0].PValue, base.Tests[1].PValue}
	want, _ := multiplicity.Adjust(multiplicity.MethodBonferroni, ps)
	for i, r := range got.Tests {
		if r.PValue != ps[i] || r.RejectNull != base.Tests[i].RejectNull {
			t.Errorf("test %d raw figures changed", i)
		}
		if r.PAdjusted == nil || r.Multiplicity == nil {
			t.Fatalf("test %d (%s) not corrected", i, r.Type)
		}
		if math.Abs(*r.PAdjusted-want[i]) > 1e-12 || r.Multiplicity.M != 2 {
			t.Errorf("test %d (%s): p_adjusted %v m %d, want %v m 2", i, r.Type, *r.PAdjusted, r.Multiplicity.M, want[i])
		}
	}
	if *got.Tests[1].PAdjusted != 0.04 || !*got.Tests[1].SignificantAdjusted {
		t.Errorf("extension test: p_adjusted %v significant %v, want 0.04 true", *got.Tests[1].PAdjusted, *got.Tests[1].SignificantAdjusted)
	}
}

// TestMultiplicityNoneIsIdentity: no block, and every way of naming
// `none` (request, test, overlay, instance default), answer
// byte-identically on the wire — no adjusted figure, no echo, no
// parallel adjusted matrix — and leave the request's canonical hash
// where it was. It runs over a tests request and over a crosstab
// carrying inferential overlays.
func TestMultiplicityNoneIsIdentity(t *testing.T) {
	_, fs, cohort := acceptanceCohort(t)
	none := &types.Multiplicity{Method: types.MultiplicityMethodNone}
	shapes := map[string]func() *types.Request{
		"tests": func() *types.Request {
			return &types.Request{
				Cohort:       &types.Cohort{Filename: cohort},
				Aggregations: []*types.Aggregation{{Type: types.AGG_AVERAGE, Field: "x", Label: "m"}},
				Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
				Tests: []*types.Test{
					{Type: types.TEST_T, Field: "x", SplitBy: "g", Label: "t"},
					{Type: types.TEST_PEARSON_R, Field: "x", Field2: "y", Label: "r"},
				},
			}
		},
		"overlays": func() *types.Request {
			return &types.Request{
				Cohort: &types.Cohort{Filename: cohort},
				Crosstab: &types.CrosstabSpec{
					Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
					Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "subj"}},
					Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "x", Label: "n"},
					Shape:   types.CrosstabShapeMatrix,
				},
				Overlays: []types.OverlaySpec{
					{Name: "m", Kind: types.OverlayKindChiSqMatrix, Scope: types.OverlayScopeMatrix},
					{Name: "f", Kind: types.OverlayKindFisherExactCell, Scope: types.OverlayScopeCell},
				},
			}
		},
	}
	plain := multPulse(t, fs, nil)
	run := func(p *pulse.Pulse, req *types.Request) []byte {
		t.Helper()
		before := req.Hash()
		resp, err := p.Process(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		if after := req.Hash(); after != before {
			t.Errorf("Process moved the request's canonical hash: %s -> %s", before, after)
		}
		b, err := json.Marshal(resp)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	for shape, mk := range shapes {
		t.Run(shape, func(t *testing.T) {
			baseline := run(plain, mk())
			if strings.Contains(string(baseline), "p_adjusted") || strings.Contains(string(baseline), "multiplicity") {
				t.Fatalf("baseline carries multiplicity output: %s", baseline)
			}
			if shape == "overlays" && !strings.Contains(string(baseline), `"p_value"`) {
				t.Fatalf("overlay baseline carries no inferential layer: %s", baseline)
			}
			variants := map[string]func() ([]byte, string){
				"request none": func() ([]byte, string) {
					r := mk()
					r.Multiplicity = none
					return run(plain, r), r.Hash()
				},
				"slot none": func() ([]byte, string) {
					r := mk()
					for _, tt := range r.Tests {
						tt.Multiplicity = none
					}
					for i := range r.Overlays {
						r.Overlays[i].Multiplicity = none
					}
					return run(plain, r), r.Hash()
				},
				"instance default none": func() ([]byte, string) {
					r := mk()
					return run(multPulse(t, fs, none), r), r.Hash()
				},
			}
			absentHash := mk().Hash()
			for name, v := range variants {
				t.Run(name, func(t *testing.T) {
					got, hash := v()
					if !bytes.Equal(got, baseline) {
						t.Errorf("response differs from the no-block baseline:\n got %s\nwant %s", got, baseline)
					}
					if name == "instance default none" && hash != absentHash {
						t.Errorf("an instance default moved the request hash: %s vs %s", hash, absentHash)
					}
				})
			}
		})
	}
}
