package processing

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
)

// The R oracle and the bit-for-bit MAT_PCA parity live in
// internal/service (TestBartlettSphericity_EqualsPCABartlett), where
// rows carry merge-block positions; these are the engine-level rules.

func sphericityReq(fields []string, mod func(*types.Test)) func() *types.Request {
	return func() *types.Request {
		spec := &types.Test{Type: types.TEST_BARTLETT_SPHERICITY, Fields: append([]string(nil), fields...)}
		if mod != nil {
			mod(spec)
		}
		return &types.Request{Tests: []*types.Test{spec}}
	}
}

// TestBartlettSphericity_ClosedForm: the statistic, df and p are
// psych::cortest.bartlett's closed form on the rows' correlation, at
// N = n unweighted; the determinant rides Details.
func TestBartlettSphericity_ClosedForm(t *testing.T) {
	schema := hotellingSchema()
	recs := hotellingRows(schema, 50, []string{"a"}, nil)
	res, _ := runHotelling(t, schema, recs, sphericityReq([]string{"x1", "x2", "x3"}, nil))
	cm, _ := linalg.NewCoMoment(3, linalg.Listwise)
	for _, r := range recs {
		x := make([]float64, 3)
		for i, f := range []string{"x1", "x2", "x3"} {
			x[i], _ = r.NumericValue(f)
		}
		cm.Add(x, 1)
	}
	det := determinantSPD(cm.Corr())
	chi := -math.Log(det) * (50 - 1 - float64(2*3+5)/6)
	if res.Type != types.TEST_BARTLETT_SPHERICITY || res.DF != 3 || res.Details["n"] != int64(50) {
		t.Fatalf("type %s df %v n %v", res.Type, res.DF, res.Details["n"])
	}
	if res.Statistic != chi || res.PValue != chiSquareSurvival(chi, 3) || res.Details["determinant"] != det {
		t.Fatalf("statistic %v p %v det %v, want %v / %v / %v", res.Statistic, res.PValue, res.Details["determinant"], chi, chiSquareSurvival(chi, 3), det)
	}
	if res.RejectNull != (res.PValue < 0.05) || len(res.Warnings) != 0 {
		t.Fatalf("reject %v warnings %v", res.RejectNull, res.Warnings)
	}
}

// TestBartlettSphericity_NullsWarn: a dependent or constant member is
// PULSE_MATRIX_SINGULAR, and an inference size at or below
// 1 + (2p + 5)/6 (a probability weight concentrated on one row) is
// PULSE_MATRIX_INSUFFICIENT_N — each a warning with a null statistic
// and p (JSON null), never an error.
func TestBartlettSphericity_NullsWarn(t *testing.T) {
	cases := []struct {
		name string
		code errors.Code
		n    int
		mod  func(int, map[string]float64)
		wt   *types.WeightSpec
	}{
		{"dependent", errors.PULSE_MATRIX_SINGULAR, 40, func(_ int, v map[string]float64) { v["x3"] = v["x1"] + 2*v["x2"] }, nil},
		{"constant", errors.PULSE_MATRIX_SINGULAR, 40, func(_ int, v map[string]float64) { v["x3"] = 4 }, nil},
		{"low n_eff", errors.PULSE_MATRIX_INSUFFICIENT_N, 6, func(i int, v map[string]float64) {
			v["w"] = 1
			if i == 0 {
				v["w"] = 500
			}
		}, &types.WeightSpec{Field: "w", Kind: types.WeightKindProbability}},
	}
	for _, c := range cases {
		schema := hotellingSchema()
		recs := hotellingRows(schema, c.n, []string{"a"}, c.mod)
		res, resp := runHotelling(t, schema, recs, func() *types.Request {
			r := sphericityReq([]string{"x1", "x2", "x3"}, nil)()
			r.Weight = c.wt
			return r
		})
		if !math.IsNaN(res.Statistic) || !math.IsNaN(res.PValue) || res.RejectNull || res.DF != 3 {
			t.Fatalf("%s: statistic %v p %v df %v", c.name, res.Statistic, res.PValue, res.DF)
		}
		if len(res.Warnings) != 1 || !strings.HasPrefix(res.Warnings[0], string(c.code)+":") {
			t.Fatalf("%s: warnings = %v", c.name, res.Warnings)
		}
		raw, err := json.Marshal(resp)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(raw, []byte(`"statistic":null`)) || !bytes.Contains(raw, []byte(`"p_value":null`)) {
			t.Fatalf("%s: undefined figures not null on the wire: %s", c.name, raw)
		}
	}
}

// TestBartlettSphericity_ListwiseAndFrequency: a null member drops the
// row and counts it; integer frequency weights equal the rep()
// expansion (N* = Σw); fewer than 2 complete rows is
// PULSE_TEST_INSUFFICIENT_N.
func TestBartlettSphericity_ListwiseAndFrequency(t *testing.T) {
	schema := hotellingSchema()
	clean := hotellingRows(schema, 40, []string{"a"}, nil)
	withNull := append(append([]*Record(nil), clean...), NewRecord(schema, map[string]float64{"x1": 99, "x3": 1, "g": 0}))
	req := sphericityReq([]string{"x1", "x2", "x3"}, nil)
	a, _ := runHotelling(t, schema, clean, req)
	b, _ := runHotelling(t, schema, withNull, req)
	if b.Details["n_dropped"] != int64(1) || a.Statistic != b.Statistic {
		t.Fatalf("listwise: n_dropped %v, %v vs %v", b.Details["n_dropped"], a.Statistic, b.Statistic)
	}
	var expanded []*Record
	for i, r := range clean {
		for k := 0; k < 1+i%3; k++ {
			expanded = append(expanded, r)
		}
	}
	wf, _ := runHotelling(t, schema, clean, func() *types.Request {
		r := req()
		r.Weight = &types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency}
		return r
	})
	ex, _ := runHotelling(t, schema, expanded, req)
	if !hotClose(wf.Statistic, ex.Statistic, 1e-10) || !hotClose(wf.PValue, ex.PValue, 1e-9) {
		t.Errorf("frequency %v/%v, expansion %v/%v", wf.Statistic, wf.PValue, ex.Statistic, ex.PValue)
	}
	if wf.Details["sum_weights"] != float64(len(expanded)) {
		t.Errorf("sum_weights = %v, want %d", wf.Details["sum_weights"], len(expanded))
	}
	schema = hotellingSchema()
	if err := processErr(t, schema, hotellingRows(schema, 1, []string{"a"}, nil), req()); hotCode(err) != errors.PULSE_TEST_INSUFFICIENT_N {
		t.Fatalf("n = 1: err = %v", err)
	}
}

// TestBartlettSphericity_UnpositionedBlocks: rows no source stamped
// fold into arrival-order blocks of linalg.MergeBlockSize, combined by
// the one tree — so past one block the statistic is the tree's, not a
// single running fold's.
func TestBartlettSphericity_UnpositionedBlocks(t *testing.T) {
	schema := hotellingSchema()
	n := 2*linalg.MergeBlockSize + 77
	// Large offsets with small spreads make the fold order reach the
	// last bits, so a single running fold would differ from the tree.
	recs := hotellingRows(schema, n, []string{"a"}, func(i int, v map[string]float64) {
		v["x1"] = 1e4 + v["x1"]*1e-3
		v["x2"] = 0.5*v["x1"] + v["x2"]*1e-3
		v["x3"] = 1e6 + v["x3"]
	})
	res, _ := runHotelling(t, schema, recs, sphericityReq([]string{"x1", "x2", "x3"}, nil))
	single, _ := linalg.NewCoMoment(3, linalg.Listwise)
	blocks, _ := NewBlockCoMoments(3, linalg.Listwise)
	for i, r := range recs {
		x := make([]float64, 3)
		for j, f := range []string{"x1", "x2", "x3"} {
			x[j], _ = r.NumericValue(f)
		}
		if err := blocks.AddAt(BlockKey{Block: i / linalg.MergeBlockSize}, x, 1); err != nil {
			t.Fatal(err)
		}
		single.Add(x, 1)
	}
	if one := bartlettSphericity(single.Corr(), float64(n)); one.ChiSq == bartlettSphericity(mustTree(t, blocks).Corr(), float64(n)).ChiSq {
		t.Fatalf("fixture too tame: the single fold and the tree agree (%v), so the test cannot tell them apart", one.ChiSq)
	}
	cm, err := blocks.Tree()
	if err != nil {
		t.Fatal(err)
	}
	want := bartlettSphericity(cm.Corr(), float64(n))
	if res.Statistic != want.ChiSq || res.PValue != want.PValue {
		t.Fatalf("statistic %v p %v, tree %v / %v", res.Statistic, res.PValue, want.ChiSq, want.PValue)
	}
}

// TestBartlettSphericity_Refusals: split_by, a second member set,
// unknown params and pairwise missing are SERVICE_VALIDATION reasons; a
// single member is PROCESSING_CONFIG.
func TestBartlettSphericity_Refusals(t *testing.T) {
	schema := hotellingSchema()
	recs := hotellingRows(schema, 30, []string{"a", "b"}, nil)
	for reason, mod := range map[string]func(*types.Test){
		"split_by_not_accepted": func(s *types.Test) { s.SplitBy = "g" },
		"vector2_not_accepted":  func(s *types.Test) { s.Fields2 = []string{"x1", "x2"} },
		"bad_params":            func(s *types.Test) { s.Params = json.RawMessage(`{"mu":[0,0]}`) },
		"pairwise_unsupported":  func(s *types.Test) { s.Params = json.RawMessage(`{"missing":"pairwise"}`) },
	} {
		err := processErr(t, schema, recs, sphericityReq([]string{"x2", "x3"}, mod)())
		ce, ok := err.(*errors.CodedError)
		if !ok || ce.Code != errors.SERVICE_VALIDATION || ce.Details["reason"] != reason {
			t.Errorf("%s: err = %v", reason, err)
		}
	}
	if _, err := newBartlettSphericityRow(&types.Test{Type: types.TEST_BARTLETT_SPHERICITY, Fields: []string{"x1"}}, &encoding.Schema{}); hotCode(err) != errors.PROCESSING_CONFIG {
		t.Fatalf("one member: err = %v", err)
	}
}

func mustTree(t *testing.T, b *BlockCoMoments) *linalg.CoMoment {
	t.Helper()
	cm, err := b.Tree()
	if err != nil {
		t.Fatal(err)
	}
	return cm
}
