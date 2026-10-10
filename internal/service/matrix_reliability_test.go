package service

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// matrix_reliability_test.go is MAT_RELIABILITY end to end: the R
// oracle (psych::alpha on the raw and the weighted covariance, omega
// off psych::fa(nfactors = 1, fm = "minres")), reverse scoring with the
// declared range, and every case where omega is withheld (p = 2, a
// Heywood fit, an unrepaired non-PSD pairwise input).

// reliabilityOracle is one mv_reliability.json case.
type reliabilityOracle struct {
	Fixture        string      `json:"fixture"`
	Items          []string    `json:"items"`
	Reverse        []string    `json:"reverse"`
	ScaleMin       *float64    `json:"scale_min"`
	ScaleMax       *float64    `json:"scale_max"`
	Missing        string      `json:"missing"`
	Weight         string      `json:"weight"`
	Alpha          float64     `json:"alpha"`
	AlphaStd       float64     `json:"alpha_standardized"`
	MeanR          float64     `json:"mean_inter_item_r"`
	ItemTotalR     []float64   `json:"item_total_r"`
	AlphaIfDeleted []*float64  `json:"alpha_if_deleted"`
	ItemMean       []float64   `json:"item_mean"`
	ItemSD         []float64   `json:"item_sd"`
	InterItemR     [][]float64 `json:"inter_item_correlation"`
	OmegaModel     *float64    `json:"omega_total_model"`
	Heywood        bool        `json:"heywood"`
}

// reliabilityOracleTol pins the closed-form figures (alpha family, item
// statistics, the correlation): 1e-10 absolute + relative.
const reliabilityOracleTol = 1e-10

// reliabilityOmegaTol pins omega: its loadings come from the minres
// fit, which matches psych within 1e-5 on a proper fit (psych's L-BFGS-B
// stops early; .claude/reference/matrix-and-vectors.md, One-factor
// minres). omega is smoother than any one loading; 1e-5 holds with room.
const reliabilityOmegaTol = 1e-5

func reliabilitySpec(name string, fields []string, params map[string]any, weight string) types.MatrixSpec {
	raw, _ := json.Marshal(params)
	s := types.MatrixSpec{Name: name, Type: types.MAT_RELIABILITY, Fields: fields, Params: raw}
	switch weight {
	case "frequency":
		s.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w_freq", Kind: types.WeightKindFrequency})
	case "probability":
		s.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w_prob", Kind: types.WeightKindProbability})
	}
	return s
}

func relFloats(t *testing.T, v any) []float64 {
	t.Helper()
	f, ok := v.([]float64)
	if !ok {
		t.Fatalf("vector is %T, want []float64", v)
	}
	return f
}

// TestMatrixReliability_MatchesROracle pins every mv_reliability.json
// case: likert_ties, reversed_items (keyed with the declared 1..5 range,
// and raw), attitude, nulls (listwise and pairwise), 2-item batteries,
// the Heywood fixture; unweighted, frequency (the rep() expansion) and
// probability weights. omega is the model form (Σλ)²/((Σλ)²+Σψ) —
// omega_total_model — null at p = 2 and on a Heywood fit.
func TestMatrixReliability_MatchesROracle(t *testing.T) {
	var golden struct {
		Cases []reliabilityOracle `json:"cases"`
	}
	dir := loadMV(t, "mv_reliability.json", &golden)
	cfg := fixtureFS(t, dir)
	if len(golden.Cases) == 0 {
		t.Fatal("oracle has no cases")
	}
	near := func(where string, got, want, tol float64) {
		t.Helper()
		if !(math.Abs(got-want) <= tol*(1+math.Abs(want))) {
			t.Errorf("%s = %.17g, R = %.17g", where, got, want)
		}
	}
	for _, c := range golden.Cases {
		name := fmt.Sprintf("%s/p%d/rev%d/%s/%s", c.Fixture, len(c.Items), len(c.Reverse), c.Missing, c.Weight)
		t.Run(name, func(t *testing.T) {
			params := map[string]any{"missing": c.Missing}
			if len(c.Reverse) > 0 {
				params["reverse"] = c.Reverse
				params["scale_min"] = *c.ScaleMin
				params["scale_max"] = *c.ScaleMax
			}
			resp := processMatrices(t, cfg, &types.Request{
				Cohort:   &types.Cohort{Filename: filepath.Join(dir, c.Fixture+".pulse")},
				Matrices: []types.MatrixSpec{reliabilitySpec("rel", c.Items, params, c.Weight)},
			})
			res := resp.Matrices[0]
			if !reflect.DeepEqual(res.Primary.RowKeys, c.Items) {
				t.Fatalf("axis %v, want %v", res.Primary.RowKeys, c.Items)
			}
			for i := range c.InterItemR {
				for j := range c.InterItemR[i] {
					near(fmt.Sprintf("r[%d][%d]", i, j), res.Primary.Values[i][j], c.InterItemR[i][j], reliabilityOracleTol)
				}
			}
			near("alpha", res.Scalars["alpha"], c.Alpha, reliabilityOracleTol)
			near("alpha_standardized", res.Scalars["alpha_standardized"], c.AlphaStd, reliabilityOracleTol)
			near("mean_inter_item_r", res.Scalars["mean_inter_item_r"], c.MeanR, reliabilityOracleTol)
			for k, key := range []string{"item_total_r", "item_mean", "item_sd"} {
				want := [][]float64{c.ItemTotalR, c.ItemMean, c.ItemSD}[k]
				got := relFloats(t, res.Vectors[key])
				for i := range want {
					near(fmt.Sprintf("%s[%d]", key, i), got[i], want[i], reliabilityOracleTol)
				}
			}
			aid := relFloats(t, res.Vectors["alpha_if_deleted"])
			for i, w := range c.AlphaIfDeleted {
				if w == nil {
					if !math.IsNaN(aid[i]) {
						t.Errorf("alpha_if_deleted[%d] = %v, R gives NA", i, aid[i])
					}
					continue
				}
				near(fmt.Sprintf("alpha_if_deleted[%d]", i), aid[i], *w, reliabilityOracleTol)
			}
			omega := res.Scalars["omega"]
			switch {
			case len(c.Items) < 3:
				if !math.IsNaN(omega) || findWarning(res, errors.PULSE_MATRIX_NOT_IDENTIFIED) == nil {
					t.Errorf("p = 2: omega %v, warnings %v; want null with PULSE_MATRIX_NOT_IDENTIFIED", omega, matWarningCodes(res))
				}
			case c.Heywood:
				w := findWarning(res, errors.PULSE_MATRIX_HEYWOOD)
				if !math.IsNaN(omega) || w == nil {
					t.Fatalf("Heywood: omega %v, warnings %v; want null with PULSE_MATRIX_HEYWOOD", omega, matWarningCodes(res))
				}
				if !reflect.DeepEqual(w.Details["members"], []string{c.Items[0]}) || w.Details["output"] != "omega" {
					t.Errorf("Heywood details = %v", w.Details)
				}
			default:
				near("omega", omega, *c.OmegaModel, reliabilityOmegaTol)
				if len(res.Warnings) != 0 && c.Missing == "listwise" {
					t.Errorf("proper fit carries warnings %v", matWarningCodes(res))
				}
			}
		})
	}
}

// TestMatrixReliability_ComponentsReportFit: components.matrices carries
// the minres fit's iterations and converged beside the floor — also on a
// run that keeps components but excludes the matrices slot — and omits
// them when no fit ran (p = 2).
func TestMatrixReliability_ComponentsReportFit(t *testing.T) {
	var golden struct {
		Cases []reliabilityOracle `json:"cases"`
	}
	dir := loadMV(t, "mv_reliability.json", &golden)
	cfg := fixtureFS(t, dir)
	cohort := &types.Cohort{Filename: filepath.Join(dir, "attitude.pulse")}
	items := []string{"rating", "complaints", "privileges", "learning", "raises", "critical", "advance"}
	for _, ret := range []*types.Return{nil, {Exclude: []string{"matrices"}}} {
		resp, err := New(cfg).Process(context.Background(), &types.Request{Cohort: cohort, Return: ret,
			Matrices: []types.MatrixSpec{reliabilitySpec("rel", items, nil, ""), reliabilitySpec("two", items[:2], nil, "")}})
		if err != nil {
			t.Fatal(err)
		}
		if resp.Components == nil || len(resp.Components.Matrices) != 2 {
			t.Fatalf("return %+v: components = %+v", ret, resp.Components)
		}
		op := resp.Components.Matrices[0].Operator
		if it, _ := op["iterations"].(int); it < 1 || op["converged"] != true {
			t.Errorf("return %+v: fit components = %v, want iterations >= 1 and converged", ret, op)
		}
		if two := resp.Components.Matrices[1].Operator; two != nil {
			t.Errorf("return %+v: a 2-item battery ran no fit, yet components carry %v", ret, two)
		}
		if ret != nil && len(resp.Matrices) != 0 {
			t.Errorf("excluded matrices slot rendered %d results", len(resp.Matrices))
		}
	}
}

// TestMatrixReliability_ReverseScoring: params.reverse flips x' =
// scale_min + scale_max − x before the fold, so the keyed battery equals
// the same battery stored pre-reversed; a reverse list without a range,
// half a range or an inverted range is PROCESSING_CONFIG; a value
// outside the range is PROCESSING_CONFIG at run time (never clamped);
// reverse names outside the items and a battery of one item are
// bad_params.
func TestMatrixReliability_ReverseScoring(t *testing.T) {
	var golden struct {
		Cases []reliabilityOracle `json:"cases"`
	}
	dir := loadMV(t, "mv_reliability.json", &golden)
	cfg := fixtureFS(t, dir)
	cohort := &types.Cohort{Filename: filepath.Join(dir, "reversed_items.pulse")}
	items := []string{"r1", "r2", "r3", "r4", "r5"}
	run := func(params map[string]any, fields []string) (*types.Response, error) {
		return New(cfg).Process(context.Background(), &types.Request{Cohort: cohort,
			Matrices: []types.MatrixSpec{reliabilitySpec("rel", fields, params, "")}})
	}
	code := func(err error) (errors.Code, map[string]any) {
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) {
			return "", nil
		}
		return ce.Code, ce.Details
	}

	keyed, err := run(map[string]any{"reverse": []string{"r2", "r4"}, "scale_min": 1, "scale_max": 5}, items)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := run(nil, items)
	if err != nil {
		t.Fatal(err)
	}
	k, r := keyed.Matrices[0], raw.Matrices[0]
	if !(k.Scalars["alpha"] > 0.7) || !(r.Scalars["alpha"] < 0) {
		t.Errorf("keyed alpha %v, raw alpha %v: reversal did not key the battery", k.Scalars["alpha"], r.Scalars["alpha"])
	}
	// A reversed item's correlations flip sign exactly; its mean
	// reflects about the scale midpoint.
	for j := range items {
		if j == 1 {
			continue
		}
		want := -r.Primary.Values[1][j]
		if j == 3 {
			want = r.Primary.Values[1][j]
		}
		if math.Abs(k.Primary.Values[1][j]-want) > 1e-12 {
			t.Errorf("keyed r[r2][%s] = %v, want %v", items[j], k.Primary.Values[1][j], want)
		}
	}
	if km, rm := relFloats(t, k.Vectors["item_mean"])[1], relFloats(t, r.Vectors["item_mean"])[1]; math.Abs(km-(6-rm)) > 1e-12 {
		t.Errorf("keyed mean of r2 = %v, want 6 − %v", km, rm)
	}

	// A declared range with no reverse list only checks the values.
	if _, err := run(map[string]any{"scale_min": 1, "scale_max": 5}, items); err != nil {
		t.Errorf("range without reverse refused: %v", err)
	}

	for _, c := range []struct {
		name   string
		params map[string]any
		fields []string
		code   errors.Code
		reason string
	}{
		{"reverse without range", map[string]any{"reverse": []string{"r2"}}, items, errors.PROCESSING_CONFIG, ""},
		{"half a range", map[string]any{"reverse": []string{"r2"}, "scale_min": 1}, items, errors.PROCESSING_CONFIG, ""},
		{"inverted range", map[string]any{"reverse": []string{"r2"}, "scale_min": 5, "scale_max": 1}, items, errors.PROCESSING_CONFIG, ""},
		{"value outside range", map[string]any{"reverse": []string{"r2"}, "scale_min": 1, "scale_max": 4}, items, errors.PROCESSING_CONFIG, ""},
		{"reverse names a non-item", map[string]any{"reverse": []string{"r9"}, "scale_min": 1, "scale_max": 5}, items, errors.SERVICE_VALIDATION, "bad_params"},
		{"reverse twice", map[string]any{"reverse": []string{"r2", "r2"}, "scale_min": 1, "scale_max": 5}, items, errors.SERVICE_VALIDATION, "bad_params"},
		{"one item", nil, items[:1], errors.SERVICE_VALIDATION, "bad_params"},
		{"unknown key", map[string]any{"summary": map[string]any{"top_pairs": 1}}, items, errors.SERVICE_VALIDATION, "bad_params"},
		{"bad repair", map[string]any{"repair": "clip"}, items, errors.SERVICE_VALIDATION, "bad_params"},
	} {
		_, err := run(c.params, c.fields)
		got, details := code(err)
		if got != c.code || (c.reason != "" && details["reason"] != c.reason) {
			t.Errorf("%s: error %v, want %s %s", c.name, err, c.code, c.reason)
		}
		if c.name == "value outside range" && (!slices.Contains(items, fmt.Sprint(details["field"])) || details["value"] != 5.0) {
			t.Errorf("out-of-range details = %v", details)
		}
	}
}

// TestMatrixReliability_NotPSDNullsOmegaOnly: on an unrepaired non-PSD
// pairwise input omega is null with a PULSE_MATRIX_NOT_PSD WARNING
// (output omega) — the matrix is not refused, alpha still computes —
// and under params.repair "nearest" omega fits the nearest correlation
// matrix with the repair warning.
func TestMatrixReliability_NotPSDNullsOmegaOnly(t *testing.T) {
	var golden struct {
		Cases []struct {
			Do2Eigen bool     `json:"do2eigen"`
			Fields   []string `json:"fields"`
		} `json:"cases"`
	}
	dir := loadMV(t, "mv_near_pd.json", &golden)
	cfg := fixtureFS(t, dir)
	fields := golden.Cases[0].Fields
	cohort := &types.Cohort{Filename: filepath.Join(dir, "nonpsd_pairwise.pulse")}
	resp := processMatrices(t, cfg, &types.Request{Cohort: cohort, Matrices: []types.MatrixSpec{
		reliabilitySpec("plain", fields, map[string]any{"missing": "pairwise"}, ""),
		reliabilitySpec("repaired", fields, map[string]any{"missing": "pairwise", "repair": "nearest"}, ""),
	}})
	plain, repaired := resp.Matrices[0], resp.Matrices[1]
	w := findWarning(plain, errors.PULSE_MATRIX_NOT_PSD)
	if w == nil || w.Details["output"] != "omega" || !math.IsNaN(plain.Scalars["omega"]) {
		t.Fatalf("unrepaired: omega %v, warnings %v", plain.Scalars["omega"], matWarningCodes(plain))
	}
	if !strings.Contains(w.Message, `repair "nearest"`) {
		t.Errorf("warning message %q does not name the repair", w.Message)
	}
	if math.IsNaN(plain.Scalars["alpha"]) || plain.Scalars["alpha"] != repaired.Scalars["alpha"] {
		t.Errorf("alpha %v / %v: alpha must compute regardless of PSD and ignore the repair", plain.Scalars["alpha"], repaired.Scalars["alpha"])
	}
	rw := findWarning(repaired, errors.PULSE_MATRIX_NOT_PSD)
	if rw == nil || rw.Details["repair"] != "nearest" {
		t.Fatalf("repaired: warnings %v", matWarningCodes(repaired))
	}
	if om := repaired.Scalars["omega"]; math.IsNaN(om) && findWarning(repaired, errors.PULSE_MATRIX_HEYWOOD) == nil {
		t.Errorf("repaired omega is null with no Heywood warning (%v)", matWarningCodes(repaired))
	}
}
