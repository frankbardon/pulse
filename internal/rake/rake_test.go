package rake

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

func dict(vals ...string) *encoding.Dictionary {
	d := encoding.NewDictionary()
	for _, v := range vals {
		if _, err := d.Add(v); err != nil {
			panic(err)
		}
	}
	return d
}

// refSchema is the reference sample's schema: three categoricals, the
// wave (u8) and the design weight (f64).
func refSchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "age", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict("18-34", "35-54", "55+")},
		{Name: "gender", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict("F", "M")},
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict("NE", "MW", "S", "W")},
		{Name: "wave", Type: encoding.FieldTypeU8},
		{Name: "dw", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "flag", Type: encoding.FieldTypePackedBool},
		{Name: "score", Type: encoding.FieldTypeF32},
	}}
}

func refAt(rows []refRow) func(int64) ([]any, error) {
	return func(i int64) ([]any, error) {
		r := rows[i]
		return []any{r.age, r.gender, r.region, r.wave, r.dw, i%2 == 0, float32(1)}, nil
	}
}

func census() []Margin {
	return []Margin{
		{Field: "age", Targets: map[string]float64{"18-34": 29, "35-54": 33, "55+": 38}},
		{Field: "gender", Targets: map[string]float64{"F": 51, "M": 49}},
		{Field: "region", Targets: map[string]float64{"NE": 17, "MW": 21, "S": 38, "W": 24}},
	}
}

func baseReq() *Request {
	return &Request{Cohort: "in.pulse", Out: "out.pulse", Margins: census(), Tol: 1e-13, MaxIter: 10000}
}

func run(t *testing.T, req *Request, rows []refRow) *Solution {
	t.Helper()
	sol, err := tryRun(req, rows)
	if err != nil {
		t.Fatal(err)
	}
	return sol
}

func tryRun(req *Request, rows []refRow) (*Solution, error) {
	p, err := Compile(req, refSchema())
	if err != nil {
		return nil, err
	}
	d, err := p.Load(context.Background(), int64(len(rows)), refAt(rows))
	if err != nil {
		return nil, err
	}
	return p.Solve(context.Background(), d)
}

func relClose(a, b, tol float64) bool {
	return math.Abs(a-b) <= tol*math.Max(1, math.Max(math.Abs(a), math.Abs(b)))
}

// TestSolve_ReproducesCensusMarginals: the raked weights reproduce the
// census marginals (age 29/33/38, gender 51/49, region 17/21/38/24),
// and the observed shares differ from them before raking.
func TestSolve_ReproducesCensusMarginals(t *testing.T) {
	sol := run(t, baseReq(), refRows)
	r := sol.Report
	if !r.Converged || r.Residual > 1e-13 || r.Iterations < 2 {
		t.Fatalf("converged=%v residual=%g iterations=%d", r.Converged, r.Residual, r.Iterations)
	}
	want := map[string]float64{"18-34": .29, "35-54": .33, "55+": .38, "F": .51, "M": .49, "NE": .17, "MW": .21, "S": .38, "W": .24}
	moved := false
	for _, m := range r.Margins {
		for _, c := range m.Categories {
			w := want[c.Values[0]]
			if math.Abs(c.Achieved-w) > 1e-12 || math.Abs(c.Target-w) > 1e-15 {
				t.Errorf("%v %v: achieved %g target %g, want %g", m.Fields, c.Values, c.Achieved, c.Target, w)
			}
			if math.Abs(c.Observed-w) > 1e-3 {
				moved = true
			}
		}
	}
	if !moved {
		t.Fatal("the sample already matched the census; the test cannot tell raking from a no-op")
	}
	sum := 0.0
	for _, w := range sol.Weights {
		sum += w
	}
	if math.Abs(sum-float64(len(refRows))) > 1e-9 {
		t.Fatalf("sum of weights %g, want %d", sum, len(refRows))
	}
}

// TestSolve_MatchesSurveyRake pins every record's weight to R
// survey::rake / postStratify (gen_rake_reference.R) at 1e-9 relative.
func TestSolve_MatchesSurveyRake(t *testing.T) {
	cellReq := baseReq()
	cellReq.Margins = []Margin{{Fields: []string{"age", "gender"}}}
	for _, c := range refCellShares {
		cellReq.Margins[0].Cells = append(cellReq.Margins[0].Cells, Cell{Values: []string{c.age, c.gender}, Target: c.share})
	}
	baseW := baseReq()
	baseW.BaseWeight = "dw"
	byWave := baseReq()
	byWave.By = "wave"
	cases := map[string]*Request{"rim3": baseReq(), "rim3_base": baseW, "cell": cellReq, "by_wave": byWave}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			want := refWeights[name]
			if len(want) != len(refRows) {
				t.Fatalf("reference %s has %d weights", name, len(want))
			}
			sol := run(t, req, refRows)
			for i, w := range sol.Weights {
				if !relClose(w, want[i], 1e-9) {
					t.Fatalf("record %d: weight %.17g, survey %.17g", i, w, want[i])
				}
			}
		})
	}
}

// TestSolve_CellWeightingExact: one joint margin is post-stratification
// — exact after one sweep from population counts, every record in a cell sharing one weight
// target·n / cell count.
func TestSolve_CellWeightingExact(t *testing.T) {
	// Census COUNTS (targets_as counts): only their proportions matter.
	req := baseReq()
	req.Tol = 0
	req.Margins = []Margin{{Fields: []string{"age", "gender"}, TargetsAs: TargetsCounts}}
	for _, c := range refCellShares {
		req.Margins[0].Cells = append(req.Margins[0].Cells, Cell{Values: []string{c.age, c.gender}, Target: c.share * 331e6})
	}
	sol := run(t, req, refRows)
	if sol.Report.Iterations != 1 || !sol.Report.Converged {
		t.Fatalf("iterations=%d converged=%v", sol.Report.Iterations, sol.Report.Converged)
	}
	counts := map[[2]string]int{}
	for _, r := range refRows {
		counts[[2]string{r.age, r.gender}]++
	}
	share := map[[2]string]float64{}
	for _, c := range refCellShares {
		share[[2]string{c.age, c.gender}] = c.share
	}
	for i, r := range refRows {
		k := [2]string{r.age, r.gender}
		want := share[k] * float64(len(refRows)) / float64(counts[k])
		if !relClose(sol.Weights[i], want, 1e-14) {
			t.Fatalf("record %d (%v): %.17g, want %.17g", i, k, sol.Weights[i], want)
		}
	}
	for _, c := range sol.Report.Margins[0].Categories {
		if math.Abs(c.Achieved-c.Target) > 1e-15 {
			t.Errorf("%v achieved %g target %g", c.Values, c.Achieved, c.Target)
		}
	}
}

// TestSolve_ByRakesEachGroup: `by` rakes each wave to the same targets
// and normalises each to its own record count.
func TestSolve_ByRakesEachGroup(t *testing.T) {
	req := baseReq()
	req.By = "wave"
	sol := run(t, req, refRows)
	if len(sol.Report.Groups) != 2 || sol.Report.Groups[0].Value != "1" || sol.Report.Groups[1].Value != "2" {
		t.Fatalf("groups %+v", sol.Report.Groups)
	}
	for _, g := range sol.Report.Groups {
		if !g.Converged {
			t.Errorf("group %s not converged", g.Value)
		}
		var n int64
		sum := 0.0
		for i, r := range refRows {
			if r.wave == map[string]uint64{"1": 1, "2": 2}[g.Value] {
				n++
				sum += sol.Weights[i]
			}
		}
		if n != g.Records || math.Abs(sum-float64(n)) > 1e-9 || math.Abs(g.Efficiency.SumWeights-float64(n)) > 1e-9 {
			t.Errorf("group %s: records %d/%d, sum %g", g.Value, n, g.Records, sum)
		}
		for _, m := range g.Margins {
			for _, c := range m.Categories {
				if math.Abs(c.Achieved-c.Target) > 1e-12 {
					t.Errorf("group %s %v %v: achieved %g target %g", g.Value, m.Fields, c.Values, c.Achieved, c.Target)
				}
			}
		}
	}
	// Raking the whole cohort at once is a different fit: per-group
	// raking must not collapse to it.
	whole := run(t, baseReq(), refRows)
	same := true
	for i := range whole.Weights {
		if !relClose(whole.Weights[i], sol.Weights[i], 1e-9) {
			same = false
		}
	}
	if same {
		t.Fatal("by-wave weights equal whole-cohort weights")
	}
}

// TestSolve_Caps: caps relative to the mean weight bound every weight
// after the run; a tight cap that cannot meet the margins warns
// PULSE_RAKE_NOT_CONVERGED and still returns the last sweep.
func TestSolve_Caps(t *testing.T) {
	free := run(t, baseReq(), refRows)
	if free.Report.Efficiency.WeightMax <= 2.0 || free.Report.Efficiency.WeightMin <= 0.3 || free.Report.Efficiency.NCapped != 0 {
		t.Fatalf("uncapped max %g n_capped %d: cap below would not bind", free.Report.Efficiency.WeightMax, free.Report.Efficiency.NCapped)
	}
	req := baseReq()
	req.MaxWeight = 2.0
	req.MinWeight = 0.3
	req.Tol = 1e-9
	sol := run(t, req, refRows)
	e := sol.Report.Efficiency
	if e.WeightMax > 2.0*(1+1e-12) || e.WeightMin < 0.3*(1-1e-12) || e.NCapped == 0 {
		t.Fatalf("capped weights out of bounds: min %g max %g n_capped %d", e.WeightMin, e.WeightMax, e.NCapped)
	}
	if !sol.Report.Converged || len(sol.Warnings) != 0 {
		t.Fatalf("a feasible cap should converge: %+v %v", sol.Report, sol.Warnings)
	}
	tight := baseReq()
	tight.MaxWeight = 1.02
	tight.MaxIter = 50
	sol = run(t, tight, refRows)
	if sol.Report.Converged || sol.Report.Iterations != 50 || len(sol.Warnings) != 1 || sol.Warnings[0].Code != errors.PULSE_RAKE_NOT_CONVERGED {
		t.Fatalf("tight cap: converged=%v iterations=%d warnings=%v", sol.Report.Converged, sol.Report.Iterations, sol.Warnings)
	}
}

// TestSolve_Total scales every weight so they sum to Total.
func TestSolve_Total(t *testing.T) {
	req := baseReq()
	req.Total = 250000
	sol := run(t, req, refRows)
	free := run(t, baseReq(), refRows)
	for i := range sol.Weights {
		if !relClose(sol.Weights[i], free.Weights[i]*250000/float64(len(refRows)), 1e-12) {
			t.Fatalf("record %d: %g", i, sol.Weights[i])
		}
	}
}

// TestLoad_MissingPolicy: a null margin value is refused by default;
// missing "base" keeps the record's base factor for that margin.
func TestLoad_MissingPolicy(t *testing.T) {
	rows := append([]refRow(nil), refRows...)
	rows[3].region = ""
	rows[7].region = ""
	at := func(i int64) ([]any, error) {
		r, _ := refAt(rows)(i)
		if rows[i].region == "" {
			r[2] = nil
		}
		return r, nil
	}
	p, err := Compile(baseReq(), refSchema())
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Load(context.Background(), int64(len(rows)), at)
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_RAKE_MISSING_VALUE || ce.Details["rows"] != int64(2) || ce.Details["first_row"] != int64(3) {
		t.Fatalf("refuse: %v", err)
	}
	req := baseReq()
	req.Missing = MissingBase
	p, err = Compile(req, refSchema())
	if err != nil {
		t.Fatal(err)
	}
	d, err := p.Load(context.Background(), int64(len(rows)), at)
	if err != nil {
		t.Fatal(err)
	}
	sol, err := p.Solve(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	if sol.Report.Margins[2].NMissing != 2 || !sol.Report.Converged {
		t.Fatalf("n_missing %d converged %v", sol.Report.Margins[2].NMissing, sol.Report.Converged)
	}
	for _, c := range sol.Report.Margins[2].Categories {
		if math.Abs(c.Achieved-c.Target) > 1e-12 {
			t.Errorf("region %v achieved %g target %g", c.Values, c.Achieved, c.Target)
		}
	}
}

func TestLoad_Refusals(t *testing.T) {
	t.Run("untargeted", func(t *testing.T) {
		req := baseReq()
		req.Margins[2].Targets = map[string]float64{"NE": 20, "MW": 30, "S": 50}
		_, err := tryRun(req, refRows)
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_RAKE_UNTARGETED_CATEGORY || ce.Details["values"].([]string)[0] != "W" {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("empty cell", func(t *testing.T) {
		rows := make([]refRow, 0, len(refRows))
		for _, r := range refRows {
			if r.region != "W" {
				rows = append(rows, r)
			}
		}
		_, err := tryRun(baseReq(), rows)
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_RAKE_EMPTY_CELL || ce.Details["values"].([]string)[0] != "W" {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("empty cell zero target accepted", func(t *testing.T) {
		rows := make([]refRow, 0, len(refRows))
		for _, r := range refRows {
			if r.region != "W" {
				rows = append(rows, r)
			}
		}
		req := baseReq()
		req.Margins[2].Targets = map[string]float64{"NE": 20, "MW": 30, "S": 50, "W": 0}
		if _, err := tryRun(req, rows); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("empty cell in one group", func(t *testing.T) {
		rows := make([]refRow, 0, len(refRows))
		for _, r := range refRows {
			if !(r.wave == 2 && r.age == "18-34") {
				rows = append(rows, r)
			}
		}
		req := baseReq()
		req.By = "wave"
		_, err := tryRun(req, rows)
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_RAKE_EMPTY_CELL || ce.Details["group"] != "2" {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("base weight negative", func(t *testing.T) {
		rows := append([]refRow(nil), refRows...)
		rows[5].dw = -1
		req := baseReq()
		req.BaseWeight = "dw"
		_, err := tryRun(req, rows)
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_RAKE_INVALID || ce.Details["reason"] != "base_weight_invalid" || ce.Details["row"] != int64(5) {
			t.Fatalf("got %v", err)
		}
	})
}

func TestCompile_Refusals(t *testing.T) {
	mut := func(f func(r *Request)) *Request { r := baseReq(); f(r); return r }
	cases := []struct {
		name   string
		req    *Request
		code   errors.Code
		reason string
	}{
		{"no cohort", mut(func(r *Request) { r.Cohort = "" }), errors.PULSE_RAKE_INVALID, "cohort_missing"},
		{"no out", mut(func(r *Request) { r.Out = "" }), errors.PULSE_RAKE_INVALID, "out_missing"},
		{"out is source", mut(func(r *Request) { r.Out = r.Cohort }), errors.PULSE_RAKE_INVALID, "out_is_source"},
		{"no margins", mut(func(r *Request) { r.Margins = nil }), errors.PULSE_RAKE_INVALID, "margins_empty"},
		{"both shapes", mut(func(r *Request) { r.Margins[0].Fields = []string{"gender", "region"} }), errors.PULSE_RAKE_INVALID, "margin_shape"},
		{"joint one field", mut(func(r *Request) {
			r.Margins = []Margin{{Fields: []string{"age"}, Cells: []Cell{{Values: []string{"55+"}, Target: 1}}}}
		}), errors.PULSE_RAKE_INVALID, "margin_shape"},
		{"unknown field", mut(func(r *Request) { r.Margins[0].Field = "nope" }), errors.PULSE_RAKE_INVALID, "field_unknown"},
		{"float field", mut(func(r *Request) { r.Margins[0] = Margin{Field: "dw", Targets: map[string]float64{"1": 1}} }), errors.PULSE_RAKE_INVALID, "field_type"},
		{"repeated field", mut(func(r *Request) { r.Margins[1].Field = "age"; r.Margins[1].Targets = r.Margins[0].Targets }), errors.PULSE_RAKE_INVALID, "field_repeated"},
		{"by is margin", mut(func(r *Request) { r.By = "age" }), errors.PULSE_RAKE_INVALID, "field_repeated"},
		{"negative target", mut(func(r *Request) { r.Margins[1].Targets = map[string]float64{"F": 1.5, "M": -0.5} }), errors.PULSE_RAKE_INVALID, "targets_invalid"},
		{"zero targets", mut(func(r *Request) { r.Margins[1].Targets = map[string]float64{"F": 0, "M": 0} }), errors.PULSE_RAKE_INVALID, "targets_zero"},
		{"shares off", mut(func(r *Request) { r.Margins[1].Targets = map[string]float64{"F": 0.5, "M": 0.4} }), errors.PULSE_RAKE_INVALID, "targets_sum"},
		{"bad targets_as", mut(func(r *Request) { r.Margins[1].TargetsAs = "pct" }), errors.PULSE_RAKE_INVALID, "targets_as"},
		{"cell arity", mut(func(r *Request) {
			r.Margins = []Margin{{Fields: []string{"age", "gender"}, Cells: []Cell{{Values: []string{"55+"}, Target: 1}}}}
		}), errors.PULSE_RAKE_INVALID, "cell_shape"},
		{"cell duplicate", mut(func(r *Request) {
			r.Margins = []Margin{{Fields: []string{"age", "gender"}, Cells: []Cell{{Values: []string{"55+", "F"}, Target: .5}, {Values: []string{"55+", "F"}, Target: .5}}}}
		}), errors.PULSE_RAKE_INVALID, "cell_duplicate"},
		{"missing policy", mut(func(r *Request) { r.Missing = "drop" }), errors.PULSE_RAKE_INVALID, "missing_policy"},
		{"caps", mut(func(r *Request) { r.MaxWeight = 0.9 }), errors.PULSE_RAKE_INVALID, "caps"},
		{"min cap", mut(func(r *Request) { r.MinWeight = 1.1 }), errors.PULSE_RAKE_INVALID, "caps"},
		{"max_iter", mut(func(r *Request) { r.MaxIter = -1 }), errors.PULSE_RAKE_INVALID, "max_iter"},
		{"tol", mut(func(r *Request) { r.Tol = math.NaN() }), errors.PULSE_RAKE_INVALID, "tol"},
		{"total", mut(func(r *Request) { r.Total = -5 }), errors.PULSE_RAKE_INVALID, "total"},
		{"base unknown", mut(func(r *Request) { r.BaseWeight = "nope" }), errors.PULSE_RAKE_INVALID, "field_unknown"},
		{"base categorical", mut(func(r *Request) { r.BaseWeight = "region" }), errors.PULSE_RAKE_INVALID, "field_type"},
		{"weight field exists", mut(func(r *Request) { r.WeightField = "dw" }), errors.PULSE_RAKE_INVALID, "weight_field_exists"},
		{"weight field in use", mut(func(r *Request) { r.WeightField = "age"; r.Overwrite = true }), errors.PULSE_RAKE_INVALID, "weight_field_in_use"},
		{"unknown category", mut(func(r *Request) { r.Margins[1].Targets = map[string]float64{"F": .5, "X": .5} }), errors.PULSE_RAKE_UNKNOWN_CATEGORY, ""},
		{"unknown bool category", mut(func(r *Request) {
			r.Margins = append(r.Margins, Margin{Field: "flag", Targets: map[string]float64{"yes": 1}})
		}), errors.PULSE_RAKE_UNKNOWN_CATEGORY, ""},
		{"integer out of range", mut(func(r *Request) {
			r.Margins = append(r.Margins, Margin{Field: "wave", Targets: map[string]float64{"1": .5, "256": .5}})
		}), errors.PULSE_RAKE_UNKNOWN_CATEGORY, ""},
		{"integer non canonical", mut(func(r *Request) {
			r.Margins = append(r.Margins, Margin{Field: "wave", Targets: map[string]float64{"01": .5, "2": .5}})
		}), errors.PULSE_RAKE_UNKNOWN_CATEGORY, ""},
		{"unknown cell category", mut(func(r *Request) {
			r.Margins = []Margin{{Fields: []string{"age", "gender"}, Cells: []Cell{{Values: []string{"55+", "X"}, Target: 1}}}}
		}), errors.PULSE_RAKE_UNKNOWN_CATEGORY, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Compile(c.req, refSchema())
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != c.code || (c.reason != "" && ce.Details["reason"] != c.reason) {
				t.Fatalf("got %v (details %v), want %s %s", err, detailsOf(err), c.code, c.reason)
			}
		})
	}
	// Accepted spellings: percentages, counts, overwrite of an unused
	// column, a packed_bool and an integer margin.
	ok := []*Request{
		mut(func(r *Request) { r.Margins[1].Targets = map[string]float64{"F": .51, "M": .49} }),
		mut(func(r *Request) {
			r.Margins[1].Targets = map[string]float64{"F": 5100, "M": 4900}
			r.Margins[1].TargetsAs = TargetsCounts
		}),
		mut(func(r *Request) { r.WeightField = "dw"; r.Overwrite = true }),
		mut(func(r *Request) {
			r.Margins = append(r.Margins, Margin{Field: "flag", Targets: map[string]float64{"true": .5, "false": .5}})
		}),
		mut(func(r *Request) {
			r.Margins = append(r.Margins, Margin{Field: "wave", Targets: map[string]float64{"1": .5, "2": .5}})
		}),
	}
	for i, r := range ok {
		if _, err := Compile(r, refSchema()); err != nil {
			t.Errorf("accepted case %d: %v", i, err)
		}
	}
}

func detailsOf(err error) map[string]any {
	var ce *errors.CodedError
	if stderrors.As(err, &ce) {
		return ce.Details
	}
	return nil
}

// TestEfficiency_HandComputed pins the efficiency figures on weights
// 1, 2, 3 and the null rule for an undefined figure.
func TestEfficiency_HandComputed(t *testing.T) {
	w := []float64{1, 2, 3}
	e := efficiency(w, []int{0, 1, 2}, make([]bool, 3))
	// Σw = 6, Σw² = 14: n_eff = 36/14, deff = 3·14/36 = 7/6.
	if e.N != 3 || e.SumWeights != 6 || !relClose(e.NEff, 36.0/14, 1e-15) || !relClose(e.DeffKish, 7.0/6, 1e-15) ||
		!relClose(e.EfficiencyPct, 100*12.0/14, 1e-15) || e.WeightMin != 1 || e.WeightMax != 3 || e.WeightRangeRatio != 3 ||
		!relClose(e.WeightCV, math.Sqrt(2.0/3)/2, 1e-15) {
		t.Fatalf("%+v", e)
	}
	// deff = 1 + cv² (population CV).
	if !relClose(e.DeffKish, 1+e.WeightCV*e.WeightCV, 1e-14) {
		t.Fatalf("deff %g != 1 + cv² %g", e.DeffKish, 1+e.WeightCV*e.WeightCV)
	}
	z := efficiency([]float64{0, 2}, []int{0, 1}, make([]bool, 2))
	if !math.IsNaN(z.WeightRangeRatio) {
		t.Fatalf("range ratio with a zero weight: %g", z.WeightRangeRatio)
	}
	b, err := json.Marshal(z)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"weight_range_ratio":null`) {
		t.Fatalf("undefined figure not null: %s", b)
	}
}

// TestSolve_BitIdenticalAcrossRuns: the solver is sequential and
// order-fixed, so two runs give the same bits.
func TestSolve_BitIdenticalAcrossRuns(t *testing.T) {
	a := run(t, baseReq(), refRows)
	b := run(t, baseReq(), refRows)
	for i := range a.Weights {
		if math.Float64bits(a.Weights[i]) != math.Float64bits(b.Weights[i]) {
			t.Fatalf("record %d differs", i)
		}
	}
}

func TestPlan_Description(t *testing.T) {
	req := baseReq()
	req.By = "wave"
	req.BaseWeight = "dw"
	p, err := Compile(req, refSchema())
	if err != nil {
		t.Fatal(err)
	}
	d := p.Description()
	for _, want := range []string{"probability", "in.pulse", "age; gender; region", "wave", "dw", "per group"} {
		if !strings.Contains(d, want) {
			t.Errorf("description %q lacks %q", d, want)
		}
	}
	if p.WeightField() != DefaultWeightField || p.ReplaceIndex() != -1 {
		t.Fatalf("weight field %q replace %d", p.WeightField(), p.ReplaceIndex())
	}
}
