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

// manovaOracleStat is one statistic of an mv_manova.json case.
type manovaOracleStat struct {
	Statistic float64  `json:"statistic"`
	F         float64  `json:"f"`
	DF1       float64  `json:"df1"`
	DF2       float64  `json:"df2"`
	PValue    *float64 `json:"p_value"`
}

// manovaOracle is one mv_manova.json case (scripts/reference/
// gen_multivariate_tests.R): summary(manova(...)) for all four
// statistics, unweighted and on the frequency rep() expansion (both
// asserted in R); probability hand-computed under the one rule.
type manovaOracle struct {
	Fixture      string           `json:"fixture"`
	Fields       []string         `json:"fields"`
	Split        string           `json:"split"`
	Levels       []string         `json:"levels"`
	Weight       string           `json:"weight"`
	N            []int64          `json:"n"`
	NStar        []float64        `json:"n_star"`
	DFHypothesis float64          `json:"df_hypothesis"`
	DFResidual   float64          `json:"df_residual"`
	Eigenvalues  []float64        `json:"eigenvalues"`
	Pillai       manovaOracleStat `json:"pillai"`
	Wilks        manovaOracleStat `json:"wilks"`
	HL           manovaOracleStat `json:"hotelling_lawley"`
	Roy          manovaOracleStat `json:"roy"`
}

const manovaOracleTol = 1e-10

func manovaReq(fields []string, split string, mod func(*types.Test)) func() *types.Request {
	return func() *types.Request {
		spec := &types.Test{Type: types.TEST_MANOVA, Fields: append([]string(nil), fields...), SplitBy: split}
		if mod != nil {
			mod(spec)
		}
		return &types.Request{Tests: []*types.Test{spec}}
	}
}

// TestManova_MatchesROracle pins every mv_manova.json case — mtcars by
// cyl (p = 4, q = 2), gear (p = 3, q = 2), carb (p = 2, q = 5, two
// singleton levels) and the weighted fixture by y_bin (q = 1, where the
// four coincide) — under no weight, frequency and probability weights,
// through the real processor on both execution paths, alternating
// inline fields and a named vector. All four statistics with F, df1,
// df2 and p, the eigenvalues, both df and per-level n / N* must match.
func TestManova_MatchesROracle(t *testing.T) {
	doc := loadReference[manovaOracle](t, "mv_manova")
	if len(doc.Cases) == 0 {
		t.Fatal("mv_manova.json holds no cases")
	}
	kinds := map[string]int{}
	for ci, c := range doc.Cases {
		kinds[c.Weight]++
		t.Run(fmt.Sprintf("%s/%s/%s", c.Fixture, c.Split, c.Weight), func(t *testing.T) {
			schema, recs := hotellingFixture(t, c.Fixture, c.Split)
			res, _ := runHotelling(t, schema, recs, func() *types.Request {
				req := manovaReq(c.Fields, "grp", nil)()
				req.Weight = oracleWeight(c.Weight)
				if ci%2 == 1 {
					req.Vectors = []types.VectorSpec{{Name: "profile", Fields: c.Fields}}
					req.Tests[0].Fields, req.Tests[0].Vector = nil, "profile"
				}
				return req
			})
			if res.Type != types.TEST_MANOVA || res.Variant != "one_way" {
				t.Fatalf("type/variant = %s/%s", res.Type, res.Variant)
			}
			if !reflect.DeepEqual(res.Details["groups"], c.Levels) || !reflect.DeepEqual(res.Details["n"], c.N) {
				t.Fatalf("groups/n = %v/%v, want %v/%v", res.Details["groups"], res.Details["n"], c.Levels, c.N)
			}
			chk := func(name string, got, want float64) {
				t.Helper()
				if !hotClose(got, want, manovaOracleTol) {
					t.Errorf("%s = %.17g, R = %.17g", name, got, want)
				}
			}
			chk("df_hypothesis", res.Details["df_hypothesis"].(float64), c.DFHypothesis)
			chk("df_residual", res.Details["df_residual"].(float64), c.DFResidual)
			eig := res.Details["eigenvalues"].([]float64)
			if len(eig) != len(c.Eigenvalues) {
				t.Fatalf("eigenvalues = %v, want %v", eig, c.Eigenvalues)
			}
			for i := range eig {
				// Structural zeros (p > q) are rounding noise in R.
				if math.Abs(eig[i]-c.Eigenvalues[i]) > manovaOracleTol*math.Max(1, c.Eigenvalues[0]) {
					t.Errorf("eigenvalue[%d] = %.17g, R = %.17g", i, eig[i], c.Eigenvalues[i])
				}
			}
			for _, s := range []struct {
				key  string
				want manovaOracleStat
			}{{"pillai", c.Pillai}, {"wilks", c.Wilks}, {"hotelling_lawley", c.HL}, {"roy", c.Roy}} {
				got := res.Details[s.key].(map[string]any)
				chk(s.key+".statistic", got["statistic"].(float64), s.want.Statistic)
				chk(s.key+".f", got["f"].(float64), s.want.F)
				chk(s.key+".df1", got["df1"].(float64), s.want.DF1)
				chk(s.key+".df2", got["df2"].(float64), s.want.DF2)
				if s.want.PValue == nil {
					if !math.IsNaN(got["p_value"].(float64)) {
						t.Errorf("%s.p_value = %v, R = NA", s.key, got["p_value"])
					}
				} else {
					chk(s.key+".p_value", got["p_value"].(float64), *s.want.PValue)
				}
				_, ub := got["upper_bound"]
				if ub != (s.key == "roy") {
					t.Errorf("%s upper_bound present = %v", s.key, ub)
				}
			}
			// The headline is Pillai's.
			pil := res.Details["pillai"].(map[string]any)
			if res.Statistic != pil["statistic"] || res.PValue != pil["p_value"] || res.DF != pil["df1"] ||
				res.Details["f"] != pil["f"] || res.Details["df2"] != pil["df2"] {
				t.Errorf("headline %v/%v/%v is not Pillai's %v", res.Statistic, res.DF, res.PValue, pil)
			}
			if c.Weight == "none" {
				if res.Details["sum_weights"] != nil {
					t.Errorf("unweighted run carries sum_weights")
				}
				return
			}
			key := map[string]string{"frequency": "sum_weights", "probability": "n_eff"}[c.Weight]
			nStar := asFloats(res.Details[key])
			if len(nStar) != len(c.NStar) {
				t.Fatalf("N* = %v, want %v", nStar, c.NStar)
			}
			for i := range nStar {
				chk(fmt.Sprintf("N*[%d]", i), nStar[i], c.NStar[i])
			}
		})
	}
	if kinds["none"] == 0 || kinds["frequency"] == 0 || kinds["probability"] == 0 {
		t.Fatalf("oracle must hold every weight kind, got %v", kinds)
	}
}

// TestManova_SplitNeedsTwoLevels: a single level is
// PULSE_TEST_SPLIT_GROUPS_LT_2; three levels run.
func TestManova_SplitNeedsTwoLevels(t *testing.T) {
	schema := hotellingSchema()
	err := processErr(t, schema, hotellingRows(schema, 30, []string{"a"}, nil), manovaReq([]string{"x1", "x2"}, "g", nil)())
	if hotCode(err) != errors.PULSE_TEST_SPLIT_GROUPS_LT_2 {
		t.Fatalf("1 level: err = %v, want PULSE_TEST_SPLIT_GROUPS_LT_2", err)
	}
	schema = hotellingSchema()
	if err := processErr(t, schema, hotellingRows(schema, 30, []string{"a", "b", "c"}, nil), manovaReq([]string{"x1", "x2"}, "g", nil)()); err != nil {
		t.Fatalf("3 levels: %v", err)
	}
}

// TestManova_SingularWithinWarns: a member that is a linear combination
// of the others, or constant, leaves E singular — a
// PULSE_MATRIX_SINGULAR warning with null figures (JSON null), never an
// error.
func TestManova_SingularWithinWarns(t *testing.T) {
	for name, mod := range map[string]func(int, map[string]float64){
		"dependent": func(_ int, v map[string]float64) { v["x3"] = v["x1"] + 2*v["x2"] },
		"constant":  func(_ int, v map[string]float64) { v["x3"] = 4 },
	} {
		schema := hotellingSchema()
		recs := hotellingRows(schema, 40, []string{"a", "b", "c"}, mod)
		res, resp := runHotelling(t, schema, recs, manovaReq([]string{"x1", "x2", "x3"}, "g", nil))
		if !math.IsNaN(res.Statistic) || !math.IsNaN(res.PValue) || res.RejectNull {
			t.Fatalf("%s: statistic %v p %v reject %v", name, res.Statistic, res.PValue, res.RejectNull)
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

// TestManova_InsufficientN: n < p + k complete rows is
// PULSE_TEST_INSUFFICIENT_N; n = p + k runs.
func TestManova_InsufficientN(t *testing.T) {
	schema := hotellingSchema()
	req := manovaReq([]string{"x1", "x2", "x3"}, "g", nil)
	if err := processErr(t, schema, hotellingRows(schema, 4, []string{"a", "b"}, nil), req()); hotCode(err) != errors.PULSE_TEST_INSUFFICIENT_N {
		t.Fatalf("n = p + k - 1: err = %v", err)
	}
	schema = hotellingSchema()
	if err := processErr(t, schema, hotellingRows(schema, 5, []string{"a", "b"}, nil), req()); err != nil {
		t.Fatalf("n = p + k must run: %v", err)
	}
}

// TestManova_TwoLevelsEqualsHotelling: with two levels (q = 1) the
// four statistics share one exact F, which is the pooled two-sample
// Hotelling's T² F and p.
func TestManova_TwoLevelsEqualsHotelling(t *testing.T) {
	schema := hotellingSchema()
	recs := hotellingRows(schema, 30, []string{"a", "b"}, nil)
	man, _ := runHotelling(t, schema, recs, manovaReq([]string{"x1", "x2", "x3"}, "g", nil))
	hot, _ := runHotelling(t, schema, recs, hotellingReq(func(s *types.Test) { s.SplitBy = "g" }))
	for _, key := range []string{"pillai", "wilks", "hotelling_lawley", "roy"} {
		d := man.Details[key].(map[string]any)
		if !hotClose(d["f"].(float64), hot.Details["f"].(float64), 1e-11) || !hotClose(d["p_value"].(float64), hot.PValue, 1e-9) {
			t.Errorf("%s F/p = %v/%v, Hotelling %v/%v", key, d["f"], d["p_value"], hot.Details["f"], hot.PValue)
		}
	}
}

// TestManova_ListwiseAndFrequency: a null member drops the row and
// counts it; integer frequency weights equal the rep() expansion.
func TestManova_ListwiseAndFrequency(t *testing.T) {
	schema := hotellingSchema()
	groups := []string{"a", "b", "c"}
	clean := hotellingRows(schema, 30, groups, nil)
	withNull := append(append([]*Record(nil), clean...), NewRecord(schema, map[string]float64{"x1": 99, "x3": 1, "g": 0}))
	req := manovaReq([]string{"x1", "x2", "x3"}, "g", nil)
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
	for _, key := range []string{"pillai", "wilks", "hotelling_lawley", "roy"} {
		x, y := wf.Details[key].(map[string]any), ex.Details[key].(map[string]any)
		if !hotClose(x["statistic"].(float64), y["statistic"].(float64), 1e-11) || !hotClose(x["p_value"].(float64), y["p_value"].(float64), 1e-9) {
			t.Errorf("%s: frequency %v, expansion %v", key, x, y)
		}
	}
}

// TestManova_ResolverRefusals: split_by is required, a second member
// set and unknown params are refused (SERVICE_VALIDATION reasons).
func TestManova_ResolverRefusals(t *testing.T) {
	schema := hotellingSchema()
	recs := hotellingRows(schema, 30, []string{"a", "b"}, nil)
	for reason, mod := range map[string]func(*types.Test){
		"split_by_required":    func(s *types.Test) { s.SplitBy = "" },
		"vector2_not_accepted": func(s *types.Test) { s.Fields2 = []string{"x1", "x2"} },
		"bad_params":           func(s *types.Test) { s.Params = json.RawMessage(`{"mu":[0,0]}`) },
	} {
		err := processErr(t, schema, recs, manovaReq([]string{"x2", "x3"}, "g", mod)())
		ce, ok := err.(*errors.CodedError)
		if !ok || ce.Code != errors.SERVICE_VALIDATION || ce.Details["reason"] != reason {
			t.Errorf("%s: err = %v", reason, err)
		}
	}
}
