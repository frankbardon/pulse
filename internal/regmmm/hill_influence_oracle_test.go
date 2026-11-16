package regmmm_test

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/regmmm"
	"github.com/frankbardon/pulse/types"
)

// hill_influence_oracle_test.go pins leverage and influence over a bound
// REG_MMM_HILL fit (U71 E2-S6, #732) to R: mmm_hill_adstock.json's
// kernel cases (params.adstock geometric, delayed, weibull_cdf,
// weibull_pdf — the carried slot, whose kernel partials read the
// carried lags) and its `plain` case (no params.adstock) each store the
// tangent-plane figures of the polished fit — hat values, rstandard,
// rstudent, Cook's distance and DFFITS of lm(e ~ J − 1), J the analytic
// Jacobian checked against numDeriv::jacobian
// (scripts/reference/gen_mmm_hill_adstock.R). Each case runs end to end:
// a Compose fit (vcov: true) and a slot request over the fit's own rows
// whose ATTR_REG_LEVERAGE / ATTR_REG_INFLUENCE (cooks, dffits) /
// ATTR_REG_RESIDUAL (standardized, studentized) slots name it; R's
// figures ride the cohort as columns, so each slot's worst row error is
// one AGG_MAX.
//
// Tolerance: hillOracleRel (1e-6, the fit oracles'), each figure's
// error taken relative to its column's largest |value| (a near-zero
// residual's figure is a stopping-distance residue, not a scale). The
// engine's Levenberg–Marquardt x sits a stopping distance from R's
// polished point (measured 2.9e-8 on a coefficient); the figures
// inherit it — measured worst 1.2e-7, so 1e-6 is ≥ 8× the evidence,
// while a 0.1% error in the s partial fails it by 1000× and a dropped
// kernel partial by 10⁵× (falsified).

// hillInfluenceFigures are the slots each case runs and the oracle key
// each reads, by label.
var hillInfluenceFigures = []struct {
	label, key string
	typ        types.AttributeType
	params     string
}{
	{"h", "leverage", types.ATTR_REG_LEVERAGE, `{"fit":"mmm"`},
	{"cooks", "cooks", types.ATTR_REG_INFLUENCE, `{"fit":"mmm"`},
	{"dffits", "dffits", types.ATTR_REG_INFLUENCE, `{"fit":"mmm","measure":"dffits"`},
	{"std", "standardized", types.ATTR_REG_RESIDUAL, `{"fit":"mmm","kind":"standardized"`},
	{"stud", "studentized", types.ATTR_REG_RESIDUAL, `{"fit":"mmm","kind":"studentized"`},
}

// hillInfluenceCase is a case's rows, its R figures and its spec.
type hillInfluenceCase struct {
	name      string
	n         int
	columns   map[string][]float64
	influence map[string][]float64
	params    map[string]any
	adstocked bool
}

// loadHillInfluenceOracle reads every case's influence block (the
// kernel cases through loadHillAdstockOracle's hash and version checks).
func loadHillInfluenceOracle(t *testing.T) []hillInfluenceCase {
	t.Helper()
	var out []hillInfluenceCase
	for _, c := range loadHillAdstockOracle(t) {
		out = append(out, hillInfluenceCase{name: c.Name, n: c.N, columns: c.Columns, influence: c.Influence, adstocked: true,
			params: map[string]any{"channels": []string{"tv"}, "adstock": c.Kernel, "max_lag": c.MaxLag}})
	}
	raw, err := os.ReadFile(filepath.Join("testdata", "mmm_hill_adstock.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Packages map[string]string `json:"packages"`
		Plain    struct {
			Name      string               `json:"name"`
			N         int                  `json:"n"`
			Columns   map[string][]float64 `json:"columns"`
			Influence map[string][]float64 `json:"influence"`
		} `json:"plain"`
	}
	if err := json.Unmarshal(raw[:strings.LastIndex(string(raw), "\n// golden-hash: ")], &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Packages["numDeriv"] != "2016.8.1.1" || doc.Plain.N == 0 {
		t.Fatalf("mmm_hill_adstock.json records numDeriv %q and a plain case of %d rows", doc.Packages["numDeriv"], doc.Plain.N)
	}
	out = append(out, hillInfluenceCase{name: doc.Plain.Name, n: doc.Plain.N, columns: doc.Plain.Columns, influence: doc.Plain.Influence,
		params: map[string]any{"channels": []string{"tv"}}})
	for _, c := range out {
		for _, f := range hillInfluenceFigures {
			if len(c.influence[f.key]) != c.n {
				t.Fatalf("%s: influence.%s has %d rows, want %d", c.name, f.key, len(c.influence[f.key]), c.n)
			}
		}
	}
	return out
}

// hillInfluenceCohort writes the case as week (u16), y, tv, price and
// each figure's R column <label>_r (f64) to name.
func hillInfluenceCohort(t *testing.T, p *pulse.Pulse, name string, c hillInfluenceCase) {
	t.Helper()
	cols := [][]float64{c.columns["y"], c.columns["tv"], c.columns["price"]}
	names := []string{"y", "tv", "price"}
	for _, f := range hillInfluenceFigures {
		cols, names = append(cols, c.influence[f.key]), append(names, f.label+"_r")
	}
	s := encoding.Schema{Fields: []encoding.Field{{Name: "week", Type: encoding.FieldTypeU16, Description: "Hill influence oracle row order."}}}
	off := s.FieldWidth(0)
	for i, col := range names {
		s.Fields = append(s.Fields, encoding.Field{Name: col, Type: encoding.FieldTypeF64, CsvColumnIdx: i + 1, ByteOffset: off,
			Description: "Hill influence oracle column " + col + "."})
		off += s.FieldWidth(i + 1)
	}
	b, err := p.NewCohortBuilder(context.Background(), name, s, pulse.CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for r := range c.n {
		row := pulse.CohortRow{uint64(r)}
		for _, col := range cols {
			row = append(row, col[r])
		}
		if err := b.Append(row); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestReferenceOracle_MMMHillInfluence: every case's leverage, Cook's
// distance, DFFITS and standardized / studentized residuals over the
// bound fit's own rows match R's tangent-plane figures — carried
// through the in-model adstock on the kernel cases, the channel as
// given on the plain one.
func TestReferenceOracle_MMMHillInfluence(t *testing.T) {
	t.Parallel()

	p := instance(t, afero.NewMemMapFs(), nil)
	worst := 0.0
	for _, c := range loadHillInfluenceOracle(t) {
		file := "influence_" + c.name + ".pulse"
		hillInfluenceCohort(t, p, file, c)
		params, _ := json.Marshal(c.params)
		s := &types.RegressionSpec{Type: regmmm.Name, Name: "mmm", Target: "y", Predictors: []string{"tv", "price"},
			Params: params, Vcov: true}
		carry := "}"
		if c.adstocked {
			s.Order = &types.RegressionOrder{Field: "week"}
			carry = `,"order":{"field":"week"}}`
		}
		var attrs []*types.Attribute
		var aggs []*types.Aggregation
		for _, f := range hillInfluenceFigures {
			scale := 0.0
			for _, v := range c.influence[f.key] {
				scale = math.Max(scale, math.Abs(v))
			}
			attrs = append(attrs,
				&types.Attribute{Type: f.typ, Label: f.label, Params: json.RawMessage(f.params + carry)},
				&types.Attribute{Type: types.ATTR_FORMULA, Label: f.label + "_e",
					Expression: "abs(" + f.label + " - " + f.label + "_r) / " + strconv.FormatFloat(scale, 'g', -1, 64)})
			aggs = append(aggs,
				&types.Aggregation{Type: types.AGG_MAX, Field: f.label + "_e", Label: f.label},
				&types.Aggregation{Type: types.AGG_COUNT, Field: f.label, Label: f.label + "_n"})
		}
		out, err := p.Compose(context.Background(), &pulse.ComposedRequest{
			Fits: []types.FitSpec{{Name: "mmm", Request: &types.Request{Cohort: &types.Cohort{Filename: file},
				Regressions: []*types.RegressionSpec{s}}}},
			Requests: []*pulse.Request{{Label: "rows", Cohort: &types.Cohort{Filename: file}, Attributes: attrs, Aggregations: aggs}},
		})
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		row := out.Responses[0].Data[0]
		for _, f := range hillInfluenceFigures {
			if n, _ := row[f.label+"_n"].(float64); int(n) != c.n {
				t.Errorf("%s: %s scored %v rows, want %d", c.name, f.label, row[f.label+"_n"], c.n)
			}
			e, ok := row[f.label].(float64)
			if !ok || e > hillOracleRel {
				t.Errorf("%s: %s worst relative error %v, want ≤ %g", c.name, f.label, row[f.label], hillOracleRel)
			}
			worst = math.Max(worst, e)
		}
	}
	t.Logf("worst relative error across the figures: %.2g", worst)
}
