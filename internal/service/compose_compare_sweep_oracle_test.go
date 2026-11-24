package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// compose_compare_sweep_oracle_test.go pins a nested-model comparison
// whose fits both bind from the sweep (`compare` over `from_sweep`, U69
// #643) to R 4.6.1: compare_sweep.json (scripts/reference/
// gen_compare_sweep.R) — a sweep over the row window id <= hi ranked by
// the full model's figure, R choosing the same winner, then anova(m0,
// m1) on lm, anova(m0, m1, test = "LRT") on a poisson glm and
// lmtest::waldtest(m0, m1, test = "Chisq") on a logit, over the winning
// window's rows.

type compareSweepCase struct {
	Name                 string      `json:"name"`
	Family               string      `json:"family"`
	Link                 string      `json:"link"`
	Test                 string      `json:"test"`
	His                  []int       `json:"his"`
	RankBy               string      `json:"rank_by"`
	RankOrder            string      `json:"rank_order"`
	WinnerSlot           int         `json:"winner_slot"`
	N                    int         `json:"n"`
	Restricted           []string    `json:"restricted"`
	Full                 []string    `json:"full"`
	Terms                []string    `json:"terms"`
	Distribution         string      `json:"distribution"`
	DFResidual           *float64    `json:"df_residual"`
	Rows                 [][]float64 `json:"rows"`
	Statistic            float64     `json:"statistic"`
	DF                   float64     `json:"df"`
	PValue               float64     `json:"p_value"`
	RestrictedDeviance   float64     `json:"restricted_deviance"`
	FullDeviance         float64     `json:"full_deviance"`
	RestrictedDFResidual float64     `json:"restricted_df_residual"`
	FullDFResidual       float64     `json:"full_df_residual"`
}

func loadCompareSweep(t *testing.T) []compareSweepCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "processing", "testdata", "reference", "compare_sweep.json"))
	if err != nil {
		t.Fatalf("read compare_sweep.json: %v (regenerate with scripts/reference/gen_compare_sweep.R)", err)
	}
	s := string(raw)
	i := strings.LastIndex(s, "\n// golden-hash: ")
	if i < 0 {
		t.Fatal("compare_sweep.json has no golden-hash footer")
	}
	body, stored := s[:i], strings.TrimSpace(s[i+len("\n// golden-hash: "):])
	if sum := sha256.Sum256([]byte(body)); hex.EncodeToString(sum[:]) != stored {
		t.Fatal("compare_sweep.json: hash mismatch (hand-edited?)")
	}
	var doc struct {
		Packages map[string]string  `json:"packages"`
		Cases    []compareSweepCase `json:"cases"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode compare_sweep.json: %v", err)
	}
	if !strings.HasPrefix(doc.Packages["R"], "R version 4.6.1") || doc.Packages["lmtest"] != "0.9.40" || len(doc.Cases) != 3 {
		t.Fatalf("compare_sweep.json: R %q, lmtest %q, %d cases", doc.Packages["R"], doc.Packages["lmtest"], len(doc.Cases))
	}
	return doc.Cases
}

const compareSweepCohort = "compare_sweep.pulse"

func (c compareSweepCase) service(t *testing.T) *Service {
	t.Helper()
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeF64, ByteOffset: 0},
		{Name: "x1", Type: encoding.FieldTypeF64, ByteOffset: 8},
		{Name: "x2", Type: encoding.FieldTypeF64, ByteOffset: 16},
		{Name: "x3", Type: encoding.FieldTypeF64, ByteOffset: 24},
		{Name: "y", Type: encoding.FieldTypeF64, ByteOffset: 32},
	}}
	rows := make([][]uint64, len(c.Rows))
	for i, r := range c.Rows {
		rows[i] = make([]uint64, len(r))
		for j, v := range r {
			rows[i][j] = math.Float64bits(v)
		}
	}
	return New(setupTestFS(t, compareSweepCohort, schema, rows))
}

// spec is the case's regression over preds, named name.
func (c compareSweepCase) spec(name string, preds []string) *types.RegressionSpec {
	s := &types.RegressionSpec{Type: types.REG_OLS, Name: name, Target: "y", Predictors: preds}
	if c.Family != "gaussian" {
		s.Type, s.Family, s.Link, s.Tol, s.MaxIters = types.REG_GLM, c.Family, c.Link, 1e-13, 200
	}
	return s
}

// batch is the case as a Compose batch: the sweep over hi (the body's
// window id ∈ [0, hi] holding "small" and "big"), ranked by big's
// figure, both fits bound from it, one comparison.
func (c compareSweepCase) batch(t *testing.T) *types.ComposedRequest {
	t.Helper()
	body, err := json.Marshal(&types.Request{
		Cohort:      &types.Cohort{Filename: compareSweepCohort},
		Filterers:   []*types.Filterer{{Type: types.FILTER_RANGE, Field: "id", Values: []string{"0", "{{hi}}"}}},
		Regressions: []*types.RegressionSpec{c.spec("small", c.Restricted), c.spec("big", c.Full)},
	})
	if err != nil {
		t.Fatal(err)
	}
	his := make([]any, len(c.His))
	for i, h := range c.His {
		his[i] = json.Number(strconv.Itoa(h))
	}
	return &types.ComposedRequest{
		Fits: []types.FitSpec{
			{Name: "m0", FromSweep: &types.FitFromSweep{Regression: "small"}},
			{Name: "m1", FromSweep: &types.FitFromSweep{Regression: "big"}},
		},
		Requests: []*types.Request{{Label: "rows", Cohort: &types.Cohort{Filename: compareSweepCohort},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "y", Label: "n"}}}},
		Sweep: &types.SweepSpec{
			Axes:    []types.SweepAxis{{Name: "hi", Values: his}},
			Label:   "hi_{{hi}}",
			Request: body,
			Rank:    &types.SweepRank{By: "regressions.big." + c.RankBy, Order: types.SweepRankOrder(c.RankOrder)},
		},
		Compare: []types.FitComparisonSpec{{Name: c.Name, Restricted: "m0", Full: "m1", Test: types.ComparisonTest(c.Test)}},
	}
}

// TestReferenceOracle_CompareSweepMatchesR: the run picks R's winning
// window, and the comparison of the winning pair — statistic, df,
// residual df, p-value, per-model deviance and residual df, n_obs,
// distribution and tested terms — agrees with R at 1e-10 relative.
func TestReferenceOracle_CompareSweepMatchesR(t *testing.T) {
	t.Parallel()
	for _, c := range loadCompareSweep(t) {
		resp, err := c.service(t).Compose(context.Background(), c.batch(t))
		if err != nil {
			t.Fatalf("%s: Compose: %v", c.Name, err)
		}
		if len(resp.Ranking) == 0 || resp.Ranking[0].Slot != c.WinnerSlot {
			t.Fatalf("%s: ranking %+v, R's winner slot %d", c.Name, resp.Ranking, c.WinnerSlot)
		}
		if len(resp.Comparisons) != 1 || resp.Comparisons[0].Error != nil {
			t.Fatalf("%s: comparisons %+v", c.Name, resp.Comparisons)
		}
		got := resp.Comparisons[0]
		if string(got.Test) != c.Test || string(got.Distribution) != c.Distribution || !slices.Equal(got.Terms, c.Terms) || got.NObs != c.N {
			t.Errorf("%s: test %s distribution %s terms %v n %d, want %s %s %v %d", c.Name, got.Test, got.Distribution, got.Terms, got.NObs, c.Test, c.Distribution, c.Terms, c.N)
		}
		check := func(what string, g, w float64) {
			if e := nestedRel(g, w); !(e <= 1e-10) {
				t.Errorf("%s: %s = %.17g, want %.17g (rel %.2g)", c.Name, what, g, w, e)
			}
		}
		check("statistic", got.Statistic, c.Statistic)
		check("df", got.DF, c.DF)
		check("p_value", got.PValue, c.PValue)
		check("restricted_deviance", got.RestrictedDeviance, c.RestrictedDeviance)
		check("full_deviance", got.FullDeviance, c.FullDeviance)
		check("restricted_df_residual", got.RestrictedDFResidual, c.RestrictedDFResidual)
		check("full_df_residual", got.FullDFResidual, c.FullDFResidual)
		if c.DFResidual != nil {
			check("df_residual", got.DFResidual, *c.DFResidual)
		} else if got.DFResidual != 0 {
			t.Errorf("%s: df_residual %g on a chi-square test", c.Name, got.DFResidual)
		}
	}
}
