package processing

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// boxMOracle is one mv_box_m.json case (scripts/reference/
// gen_multivariate_tests.R): biotools::boxM's chi-square (asserted
// unweighted and on the frequency rep() expansion), Box's F
// approximation in SPSS's form, probability hand-computed under the
// one rule.
type boxMOracle struct {
	Fixture      string    `json:"fixture"`
	Fields       []string  `json:"fields"`
	Split        string    `json:"split"`
	Levels       []string  `json:"levels"`
	Weight       string    `json:"weight"`
	N            []int64   `json:"n"`
	NStar        []float64 `json:"n_star"`
	M            float64   `json:"m"`
	ChiSq        float64   `json:"chisq"`
	DF           float64   `json:"df"`
	PValue       float64   `json:"p_value"`
	F            float64   `json:"f"`
	DF2          float64   `json:"df2"`
	FPValue      float64   `json:"f_p_value"`
	FBranch      string    `json:"f_branch"`
	LogDet       []float64 `json:"log_det"`
	LogDetPooled float64   `json:"log_det_pooled"`
}

// boxMOracleTol: R takes log-determinants through LAPACK's LU, Pulse
// through the FMA-free reference Cholesky; on these fixtures they agree
// to ~1e-13 relative.
const boxMOracleTol = 1e-10

func boxMReq(fields []string, split string, mod func(*types.Test)) func() *types.Request {
	return func() *types.Request {
		spec := &types.Test{Type: types.TEST_BOX_M, Fields: append([]string(nil), fields...), SplitBy: split}
		if mod != nil {
			mod(spec)
		}
		return &types.Request{Tests: []*types.Test{spec}}
	}
}

// TestBoxM_MatchesROracle pins every mv_box_m.json case — mtcars by
// cyl (p = 3, k = 3), by am (p = 3, k = 2), mpg alone by cyl (p = 1,
// the A2 < A1² F branch) and the weighted fixture by y_bin (p = 4) —
// under no weight, frequency and probability weights, through the real
// processor on both execution paths, alternating inline fields and a
// named vector: χ², df, p, M, the F approximation and the
// log-determinants.
func TestBoxM_MatchesROracle(t *testing.T) {
	t.Parallel()

	doc := loadReference[boxMOracle](t, "mv_box_m")
	if doc.Packages["biotools"] == "" {
		t.Fatal("mv_box_m.json does not record the biotools package version")
	}
	kinds, branches := map[string]int{}, map[string]int{}
	for ci, c := range doc.Cases {
		kinds[c.Weight]++
		branches[c.FBranch]++
		t.Run(fmt.Sprintf("%s/%s/p%d/%s", c.Fixture, c.Split, len(c.Fields), c.Weight), func(t *testing.T) {
			schema, recs := hotellingFixture(t, c.Fixture, c.Split)
			res, _ := runHotelling(t, schema, recs, func() *types.Request {
				req := boxMReq(c.Fields, "grp", nil)()
				req.Weight = oracleWeight(c.Weight)
				if ci%2 == 1 {
					req.Vectors = []types.VectorSpec{{Name: "profile", Fields: c.Fields}}
					req.Tests[0].Fields, req.Tests[0].Vector = nil, "profile"
				}
				return req
			})
			if res.Type != types.TEST_BOX_M || len(res.Warnings) != 0 {
				t.Fatalf("type %s, warnings %v", res.Type, res.Warnings)
			}
			if !reflect.DeepEqual(res.Details["groups"], c.Levels) || !reflect.DeepEqual(res.Details["n"], c.N) {
				t.Fatalf("groups/n = %v/%v, want %v/%v", res.Details["groups"], res.Details["n"], c.Levels, c.N)
			}
			chk := func(name string, got, want float64) {
				t.Helper()
				if !hotClose(got, want, boxMOracleTol) {
					t.Errorf("%s = %.17g, R = %.17g", name, got, want)
				}
			}
			chk("statistic", res.Statistic, c.ChiSq)
			chk("df", res.DF, c.DF)
			chk("p_value", res.PValue, c.PValue)
			chk("m", res.Details["m"].(float64), c.M)
			chk("df1", res.Details["df1"].(float64), c.DF)
			chk("f", res.Details["f"].(float64), c.F)
			chk("df2", res.Details["df2"].(float64), c.DF2)
			chk("f_p_value", res.Details["f_p_value"].(float64), c.FPValue)
			chk("log_det_pooled", res.Details["log_det_pooled"].(float64), c.LogDetPooled)
			ld := res.Details["log_det"].([]float64)
			for i := range c.LogDet {
				chk(fmt.Sprintf("log_det[%d]", i), ld[i], c.LogDet[i])
			}
			if res.RejectNull != (res.PValue < 0.05) {
				t.Errorf("reject_null %v at p %v", res.RejectNull, res.PValue)
			}
			if c.Weight == "none" {
				if res.Details["sum_weights"] != nil {
					t.Errorf("unweighted run carries sum_weights")
				}
				return
			}
			key := map[string]string{"frequency": "sum_weights", "probability": "n_eff"}[c.Weight]
			nStar := asFloats(res.Details[key])
			for i := range c.NStar {
				chk(fmt.Sprintf("N*[%d]", i), nStar[i], c.NStar[i])
			}
		})
	}
	if kinds["none"] == 0 || kinds["frequency"] == 0 || kinds["probability"] == 0 {
		t.Fatalf("oracle must hold every weight kind, got %v", kinds)
	}
	if branches["a2_gt"] == 0 || branches["a2_lt"] == 0 {
		t.Fatalf("oracle must exercise both F branches, got %v", branches)
	}
}

// TestBoxM_SplitNeedsTwoLevels: one level is
// PULSE_TEST_SPLIT_GROUPS_LT_2; a level with one complete row is
// PULSE_TEST_INSUFFICIENT_N.
func TestBoxM_SplitNeedsTwoLevels(t *testing.T) {
	t.Parallel()

	schema := hotellingSchema()
	err := processErr(t, schema, hotellingRows(schema, 30, []string{"a"}, nil), boxMReq([]string{"x1", "x2"}, "g", nil)())
	if hotCode(err) != errors.PULSE_TEST_SPLIT_GROUPS_LT_2 {
		t.Fatalf("1 level: err = %v", err)
	}
	schema = hotellingSchema()
	recs := hotellingRows(schema, 30, []string{"a", "b"}, nil)
	recs = append(recs, NewRecord(schema, map[string]float64{"x1": 1, "x2": 2, "x3": 3, "w": 1, "g": float64(dictIDOrAdd(schema, "g", "c"))}))
	if err := processErr(t, schema, recs, boxMReq([]string{"x1", "x2"}, "g", nil)()); hotCode(err) != errors.PULSE_TEST_INSUFFICIENT_N {
		t.Fatalf("1-row level: err = %v", err)
	}
}

// TestBoxM_SingularCovarianceWarns: a level with no more rows than
// members, a member constant within every level, or a linear
// dependency leaves a covariance singular — a PULSE_MATRIX_SINGULAR
// warning with null figures (JSON null), never an error.
func TestBoxM_SingularCovarianceWarns(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		n   int
		mod func(int, map[string]float64)
	}{
		"dependent":       {40, func(_ int, v map[string]float64) { v["x3"] = v["x1"] + 2*v["x2"] }},
		"constant":        {40, func(_ int, v map[string]float64) { v["x3"] = 4 }},
		"level n = p (3)": {9, nil},
	}
	for name, c := range cases {
		schema := hotellingSchema()
		recs := hotellingRows(schema, c.n, []string{"a", "b", "c"}, c.mod)
		res, resp := runHotelling(t, schema, recs, boxMReq([]string{"x1", "x2", "x3"}, "g", nil))
		if !math.IsNaN(res.Statistic) || !math.IsNaN(res.PValue) || res.RejectNull || !math.IsNaN(res.Details["f"].(float64)) {
			t.Fatalf("%s: statistic %v p %v f %v", name, res.Statistic, res.PValue, res.Details["f"])
		}
		if len(res.Warnings) != 1 || !strings.HasPrefix(res.Warnings[0], string(errors.PULSE_MATRIX_SINGULAR)+":") {
			t.Fatalf("%s: warnings = %v", name, res.Warnings)
		}
		raw, err := json.Marshal(resp)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(raw, []byte(`"statistic":null`)) || !bytes.Contains(raw, []byte(`"p_value":null`)) {
			t.Fatalf("%s: undefined figures not null on the wire: %s", name, raw)
		}
	}
}

// TestBoxM_ListwiseAndFrequency: a null member drops the row and counts
// it; integer frequency weights equal the rep() expansion; equal
// covariances (the same rows in every level) give M = 0.
func TestBoxM_ListwiseAndFrequency(t *testing.T) {
	t.Parallel()

	schema := hotellingSchema()
	groups := []string{"a", "b", "c"}
	clean := hotellingRows(schema, 45, groups, nil)
	withNull := append(append([]*Record(nil), clean...), NewRecord(schema, map[string]float64{"x1": 99, "x3": 1, "g": 0}))
	req := boxMReq([]string{"x1", "x2", "x3"}, "g", nil)
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
	wreq := func() *types.Request {
		r := req()
		r.Weight = &types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency}
		return r
	}
	wf, _ := runHotelling(t, schema, clean, wreq)
	ex, _ := runHotelling(t, schema, expanded, req)
	for _, key := range []string{"m", "f", "df2", "f_p_value"} {
		if !hotClose(wf.Details[key].(float64), ex.Details[key].(float64), 1e-10) {
			t.Errorf("%s: frequency %v, expansion %v", key, wf.Details[key], ex.Details[key])
		}
	}
	if !hotClose(wf.Statistic, ex.Statistic, 1e-10) || !hotClose(wf.PValue, ex.PValue, 1e-9) {
		t.Errorf("frequency %v/%v, expansion %v/%v", wf.Statistic, wf.PValue, ex.Statistic, ex.PValue)
	}

	// The same 15 rows in each of three levels: identical covariances.
	schema = hotellingSchema()
	base := hotellingRows(schema, 15, []string{"a"}, nil)
	var same []*Record
	for _, g := range groups {
		id := float64(dictIDOrAdd(schema, "g", g))
		for _, r := range base {
			v := map[string]float64{"g": id}
			for _, f := range []string{"x1", "x2", "x3"} {
				v[f], _ = r.NumericValue(f)
			}
			same = append(same, NewRecord(schema, v))
		}
	}
	eq, _ := runHotelling(t, schema, same, req)
	if m := eq.Details["m"].(float64); math.Abs(m) > 1e-9 || eq.PValue < 0.999 {
		t.Fatalf("equal covariances: M = %v, p = %v", m, eq.PValue)
	}
}

// TestBoxM_ResolverRefusals: split_by is required, a second member set
// and unknown params are refused (SERVICE_VALIDATION reasons).
func TestBoxM_ResolverRefusals(t *testing.T) {
	t.Parallel()

	schema := hotellingSchema()
	recs := hotellingRows(schema, 30, []string{"a", "b"}, nil)
	for reason, mod := range map[string]func(*types.Test){
		"split_by_required":    func(s *types.Test) { s.SplitBy = "" },
		"vector2_not_accepted": func(s *types.Test) { s.Fields2 = []string{"x1", "x2"} },
		"bad_params":           func(s *types.Test) { s.Params = json.RawMessage(`{"mu":[0,0]}`) },
	} {
		err := processErr(t, schema, recs, boxMReq([]string{"x2", "x3"}, "g", mod)())
		ce, ok := err.(*errors.CodedError)
		if !ok || ce.Code != errors.SERVICE_VALIDATION || ce.Details["reason"] != reason {
			t.Errorf("%s: err = %v", reason, err)
		}
	}
}
