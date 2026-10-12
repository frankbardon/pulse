package processing

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// mardiaOracle is one mv_mardia.json case (scripts/reference/
// gen_multivariate_tests.R): the closed form of MVN::mvn(mvnTest =
// "mardia") in base R, asserted against psych::mardia unweighted and on
// the frequency rep() expansion.
type mardiaOracle struct {
	Fixture        string   `json:"fixture"`
	Fields         []string `json:"fields"`
	Rows           *int     `json:"rows"`
	Weight         string   `json:"weight"`
	N              int64    `json:"n"`
	NStar          float64  `json:"n_star"`
	SmallSample    bool     `json:"small_sample"`
	Skewness       float64  `json:"skewness"`
	SkewnessChiSq  float64  `json:"skewness_chisq"`
	SkewnessDF     float64  `json:"skewness_df"`
	SkewnessPValue float64  `json:"skewness_p_value"`
	Kurtosis       float64  `json:"kurtosis"`
	KurtosisZ      float64  `json:"kurtosis_z"`
	KurtosisPValue float64  `json:"kurtosis_p_value"`
}

// mardiaOracleTol: R forms b1p from the n × n D matrix through LAPACK's
// solve, Pulse from the whitened third-moment tensor through the
// reference Cholesky; on these fixtures they agree to ~1e-13 relative.
const mardiaOracleTol = 1e-10

// runMardia runs one request through the processor ungrouped and
// grouped, asserts both took the BUFFERED path (TEST_MARDIA never
// streams) and answered byte-identically, and returns the result.
func runMardia(t *testing.T, schema *encoding.Schema, recs []*Record, build func() *types.Request) *types.TestResult {
	t.Helper()
	var out [2][]byte
	var res *types.TestResult
	for i, grouped := range []bool{false, true} {
		req := build()
		gf := groupField(schema)
		req.Aggregations = []*types.Aggregation{{Type: types.AGG_COUNT, Field: gf, Label: "n", Weight: types.NullSlotWeight()}}
		if CanStreamRequest(req, schema) {
			t.Fatalf("grouped=%v: CanStreamRequest = true with TEST_MARDIA", grouped)
		}
		// The second arm drives the buffered entry directly (a grouped
		// request now runs the test once per group).
		p := NewProcessor(schema)
		var resp *types.Response
		var err error
		if grouped {
			resp, err = processBuffered(p, req, recs)
		} else {
			resp, err = p.Process(context.Background(), req, NewSliceIterator(recs))
		}
		if err != nil {
			t.Fatalf("grouped=%v: %v", grouped, err)
		}
		if p.LastPath() != PathBuffered {
			t.Fatalf("grouped=%v ran %v, want buffered", grouped, p.LastPath())
		}
		if len(resp.Tests) != 1 || resp.Tests[0] == nil {
			t.Fatalf("grouped=%v: tests = %+v", grouped, resp.Tests)
		}
		out[i], _ = json.Marshal(resp.Tests)
		if i == 0 {
			res = resp.Tests[0]
		}
	}
	if !bytes.Equal(out[0], out[1]) {
		t.Fatalf("ungrouped and grouped differ:\n%s\n%s", out[0], out[1])
	}
	return res
}

// TestMardia_MatchesROracle pins every mv_mardia.json case — attitude
// (p = 4), mtcars (p = 3, and mpg alone for p = 1), the first 12 and 6
// mtcars rows (the n < 20 small-sample correction; 6 rows stays small
// under frequency weights too) and the weighted fixture with its
// zero-weight rows — unweighted and frequency weighted, alternating the
// headline statistic and the member source (inline fields / a named
// vector): b1p, b2p, the skewness χ² / df / p, the kurtosis z / p.
func TestMardia_MatchesROracle(t *testing.T) {
	doc := loadReference[mardiaOracle](t, "mv_mardia")
	if doc.Packages["psych"] == "" {
		t.Fatal("mv_mardia.json does not record the psych package version")
	}
	small, kinds := 0, map[string]int{}
	for ci, c := range doc.Cases {
		kinds[c.Weight]++
		if c.SmallSample {
			small++
		}
		rows := "all"
		if c.Rows != nil {
			rows = fmt.Sprint(*c.Rows)
		}
		t.Run(fmt.Sprintf("%s/p%d/rows=%s/%s", c.Fixture, len(c.Fields), rows, c.Weight), func(t *testing.T) {
			schema, recs := hotellingFixture(t, c.Fixture, "")
			if c.Rows != nil {
				recs = recs[:*c.Rows]
			}
			statistic := []string{"skewness", "kurtosis"}[ci%2]
			res := runMardia(t, schema, recs, func() *types.Request {
				spec := &types.Test{Type: types.TEST_MARDIA, Params: json.RawMessage(`{"statistic":"` + statistic + `"}`)}
				req := &types.Request{Weight: oracleWeight(c.Weight), Tests: []*types.Test{spec}}
				if ci%4 >= 2 {
					req.Vectors = []types.VectorSpec{{Name: "profile", Fields: c.Fields}}
					spec.Vector = "profile"
				} else {
					spec.Fields = c.Fields
				}
				return req
			})
			if res.Type != types.TEST_MARDIA || res.Variant != statistic || len(res.Warnings) != 0 {
				t.Fatalf("type %s, variant %q, warnings %v", res.Type, res.Variant, res.Warnings)
			}
			chk := func(name string, got, want float64) {
				t.Helper()
				if !hotClose(got, want, mardiaOracleTol) {
					t.Errorf("%s = %.17g, R = %.17g", name, got, want)
				}
			}
			d := res.Details
			if d["n"] != c.N || d["small_sample"] != c.SmallSample {
				t.Fatalf("n / small_sample = %v / %v, want %d / %v", d["n"], d["small_sample"], c.N, c.SmallSample)
			}
			chk("skewness", d["skewness"].(float64), c.Skewness)
			chk("skewness_chisq", d["skewness_chisq"].(float64), c.SkewnessChiSq)
			chk("skewness_df", d["skewness_df"].(float64), c.SkewnessDF)
			chk("skewness_p_value", d["skewness_p_value"].(float64), c.SkewnessPValue)
			chk("kurtosis", d["kurtosis"].(float64), c.Kurtosis)
			chk("kurtosis_z", d["kurtosis_z"].(float64), c.KurtosisZ)
			chk("kurtosis_p_value", d["kurtosis_p_value"].(float64), c.KurtosisPValue)
			if statistic == "skewness" {
				chk("statistic", res.Statistic, c.SkewnessChiSq)
				chk("df", res.DF, c.SkewnessDF)
				chk("p_value", res.PValue, c.SkewnessPValue)
			} else {
				chk("statistic", res.Statistic, c.KurtosisZ)
				chk("p_value", res.PValue, c.KurtosisPValue)
				if res.DF != 0 {
					t.Errorf("kurtosis headline df = %v, want 0 (a z has none)", res.DF)
				}
			}
			if res.RejectNull != (res.PValue < 0.05) {
				t.Errorf("reject_null %v at p %v", res.RejectNull, res.PValue)
			}
			if c.Weight == "none" {
				if d["sum_weights"] != nil {
					t.Errorf("unweighted run carries sum_weights")
				}
				return
			}
			chk("sum_weights", d["sum_weights"].(float64), c.NStar)
		})
	}
	if small < 2 || kinds["none"] == 0 || kinds["frequency"] == 0 {
		t.Fatalf("oracle coverage: %d small-sample cases, kinds %v", small, kinds)
	}
}

// mardiaRows is a deterministic, clearly non-normal 3-member sample
// over hotellingSchema (x3 carries a quartic: a long right tail).
func mardiaRows(schema *encoding.Schema, n int, mod func(i int, v map[string]float64)) []*Record {
	return hotellingRows(schema, n, []string{"a"}, func(i int, v map[string]float64) {
		v["x3"] = math.Exp(v["x3"]) + math.Pow(float64(i%10), 4)
		if mod != nil {
			mod(i, v)
		}
	})
}

func mardiaReq(mod func(*types.Test)) func() *types.Request {
	return func() *types.Request {
		spec := &types.Test{Type: types.TEST_MARDIA, Fields: []string{"x1", "x2", "x3"}}
		if mod != nil {
			mod(spec)
		}
		return &types.Request{Tests: []*types.Test{spec}}
	}
}

// TestMardia_ListwiseAndNulls: a row with any null member is dropped
// and counted in n_dropped, and the figures equal a run on the complete
// rows alone.
func TestMardia_ListwiseAndNulls(t *testing.T) {
	schema := hotellingSchema()
	all := mardiaRows(schema, 200, func(i int, v map[string]float64) {
		if i%7 == 3 {
			delete(v, "x2")
		}
	})
	var complete []*Record
	for i, r := range mardiaRows(schema, 200, nil) {
		if i%7 != 3 {
			complete = append(complete, r)
		}
	}
	got := runMardia(t, schema, all, mardiaReq(nil))
	want := runMardia(t, schema, complete, mardiaReq(nil))
	if got.Details["n_dropped"] != int64(29) || got.Details["n"] != int64(171) {
		t.Fatalf("n / n_dropped = %v / %v, want 171 / 29", got.Details["n"], got.Details["n_dropped"])
	}
	if got.Statistic != want.Statistic || got.PValue != want.PValue || got.Details["kurtosis"] != want.Details["kurtosis"] {
		t.Fatalf("listwise run %v / %v differs from the complete rows' %v / %v", got.Statistic, got.PValue, want.Statistic, want.PValue)
	}
	if !(got.PValue < 0.05) {
		t.Errorf("a long-tailed member should read as non-normal, p = %v", got.PValue)
	}
}

// TestMardia_FrequencyIsExpansion: integer frequency weights answer like
// the physically duplicated rows run unweighted (n stays raw).
func TestMardia_FrequencyIsExpansion(t *testing.T) {
	schema := hotellingSchema()
	weighted := mardiaRows(schema, 15, nil)
	var expanded []*Record
	for i, r := range mardiaRows(schema, 15, nil) {
		for range 1 + i%3 {
			expanded = append(expanded, r)
		}
	}
	got := runMardia(t, schema, weighted, func() *types.Request {
		r := mardiaReq(nil)()
		r.Weight = &types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency}
		return r
	})
	want := runMardia(t, schema, expanded, mardiaReq(nil))
	for _, k := range []string{"skewness", "skewness_chisq", "skewness_p_value", "kurtosis", "kurtosis_z", "kurtosis_p_value"} {
		if !hotClose(got.Details[k].(float64), want.Details[k].(float64), 1e-12) {
			t.Errorf("%s: weighted %v, expanded %v", k, got.Details[k], want.Details[k])
		}
	}
	if got.Details["n"] != int64(15) || got.Details["sum_weights"] != float64(len(expanded)) {
		t.Errorf("n / sum_weights = %v / %v, want 15 / %d", got.Details["n"], got.Details["sum_weights"], len(expanded))
	}
	if got.Details["small_sample"] != false || want.Details["small_sample"] != false {
		t.Errorf("N = Σw = %d ≥ 20 is not small", len(expanded))
	}
}

// TestMardia_Singular: a constant member, an exact linear dependency or
// no more rows than members leave every figure null with
// PULSE_MATRIX_SINGULAR.
func TestMardia_Singular(t *testing.T) {
	schema := hotellingSchema()
	cases := []struct {
		name string
		recs []*Record
	}{
		{"constant member", mardiaRows(schema, 20, func(_ int, v map[string]float64) { v["x2"] = 4 })},
		{"linear dependency", mardiaRows(schema, 20, func(_ int, v map[string]float64) { v["x3"] = 2*v["x1"] - v["x2"] })},
		{"n equals p", mardiaRows(schema, 3, nil)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := runMardia(t, schema, c.recs, mardiaReq(nil))
			if len(res.Warnings) != 1 || !bytes.HasPrefix([]byte(res.Warnings[0]), []byte(errors.PULSE_MATRIX_SINGULAR)) {
				t.Fatalf("warnings %v, want one PULSE_MATRIX_SINGULAR", res.Warnings)
			}
			for _, v := range []float64{res.Statistic, res.PValue, res.Details["skewness"].(float64), res.Details["kurtosis_z"].(float64)} {
				if !math.IsNaN(v) {
					t.Errorf("figure %v, want NaN (null)", v)
				}
			}
			if res.RejectNull {
				t.Error("reject_null on a null p")
			}
		})
	}
}

// TestMardia_Refusals: too few rows is PULSE_TEST_INSUFFICIENT_N; a
// bad statistic, split_by or a bad alpha refuse at construction.
func TestMardia_Refusals(t *testing.T) {
	schema := hotellingSchema()
	rt, err := newMardiaRow(&types.Test{Type: types.TEST_MARDIA, Fields: []string{"x1", "x2"}}, schema)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range mardiaRows(schema, 2, nil) {
		mustUpdate(t, rt, r)
	}
	_, err = rt.Finalize()
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_TEST_INSUFFICIENT_N {
		t.Fatalf("2 rows: %v, want PULSE_TEST_INSUFFICIENT_N", err)
	}
	for _, c := range []struct {
		name string
		spec types.Test
		code errors.Code
	}{
		{"bad statistic", types.Test{Params: json.RawMessage(`{"statistic":"both"}`)}, errors.SERVICE_VALIDATION},
		{"split_by", types.Test{SplitBy: "g"}, errors.SERVICE_VALIDATION},
		{"alpha", types.Test{Alpha: 1.5}, errors.PULSE_TEST_INVALID_ALPHA},
		{"no members", types.Test{Fields: []string{}}, errors.PROCESSING_CONFIG},
	} {
		t.Run(c.name, func(t *testing.T) {
			spec := c.spec
			spec.Type = types.TEST_MARDIA
			if spec.Fields == nil {
				spec.Fields = []string{"x1", "x2"}
			}
			_, err := newMardiaRow(&spec, schema)
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != c.code {
				t.Fatalf("%v, want %s", err, c.code)
			}
		})
	}
}

// TestMardia_NotStreamable: the type opts out of streaming, so a
// tests-plus-aggregation request that would otherwise stream buffers.
func TestMardia_NotStreamable(t *testing.T) {
	if types.TEST_MARDIA.Streamable() {
		t.Fatal("TEST_MARDIA.Streamable() = true; it needs the fitted mean and covariance before any row's products")
	}
	schema := hotellingSchema()
	req := mardiaReq(nil)()
	req.Aggregations = []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x1"}}
	if CanStreamRequest(req, schema) {
		t.Fatal("CanStreamRequest = true with TEST_MARDIA")
	}
	req.Tests[0].Type = types.TEST_BARTLETT_SPHERICITY
	if !CanStreamRequest(req, schema) {
		t.Fatal("control: the same request with a streamable vector test should stream")
	}
}
