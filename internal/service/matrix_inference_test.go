package service

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
)

// matrix_inference_test.go is params.inference end to end: the per-pair
// auxiliary.p / ci_lower / ci_upper on MAT_CORRELATION (pearson) and
// MAT_PARTIAL_CORRELATION against R (matrix_inference.json: cor.test,
// the rep() expansion, Kish n_eff, ppcor) and the ppcor p-values of
// mv_partial_correlation.json; parity with TEST_PEARSON_R; the
// inference-free byte identity; the size nulls and their warnings; the
// `return` gate.

// inferenceOracle is one matrix_inference.json case.
type inferenceOracle struct {
	Type     types.MatrixType `json:"type"`
	Method   string           `json:"method"`
	Fixture  string           `json:"fixture"`
	Members  []string         `json:"members"`
	Controls []string         `json:"controls"`
	Missing  string           `json:"missing"`
	Weight   string           `json:"weight"`
	Alpha    float64          `json:"alpha"`
	R        [][]*float64     `json:"r"`
	P        [][]*float64     `json:"p"`
	CILower  [][]*float64     `json:"ci_lower"`
	CIUpper  [][]*float64     `json:"ci_upper"`
}

// inferenceOracleTol: 1e-10 absolute + relative. R's pt / qnorm and
// LAPACK solve against Pulse's statdist and the FMA-free reference
// kernels; the fixtures agree to ~1e-13.
const inferenceOracleTol = 1e-10

func inferenceSpec(typ types.MatrixType, name string, members []string, params map[string]any, weight string) types.MatrixSpec {
	raw, _ := json.Marshal(params)
	s := partialSpec(name, members, nil, weight)
	s.Type, s.Params = typ, raw
	return s
}

func checkMatrixCells(t *testing.T, what string, got [][]float64, want [][]*float64, diagNull bool) {
	t.Helper()
	for i := range want {
		for j := range want[i] {
			g := got[i][j]
			if i == j && diagNull {
				if !math.IsNaN(g) {
					t.Errorf("%s [%d][%d] = %v, want null on the diagonal", what, i, j, g)
				}
				continue
			}
			if want[i][j] == nil {
				if !math.IsNaN(g) {
					t.Errorf("%s [%d][%d] = %v, R gives NA", what, i, j, g)
				}
				continue
			}
			if w := *want[i][j]; !(math.Abs(g-w) <= inferenceOracleTol*(1+math.Abs(w))) {
				t.Errorf("%s [%d][%d] = %.17g, R = %.17g", what, i, j, g, w)
			}
		}
	}
}

// TestMatrixInference_MatchesROracle pins every matrix_inference.json
// case: Pearson (cor.test; frequency the rep() expansion; probability
// the Kish closed form; pairwise per pair), the rank methods (cor.test
// spearman / kendall p — exact or approximate, tied or not, n below and
// above 50, pairwise, frequency the rep() expansion — and the
// Bonett–Wright / Fieller intervals) and partial correlations (control
// all / a list, listwise / pairwise, df = N* − 2 − g), at alpha 0.05,
// 0.1 and 0.01.
func TestMatrixInference_MatchesROracle(t *testing.T) {
	t.Parallel()

	var golden struct {
		Cases []inferenceOracle `json:"cases"`
	}
	dir := loadMV(t, "matrix_inference.json", &golden)
	cfg := fixtureFS(t, dir)
	if len(golden.Cases) < 20 {
		t.Fatalf("oracle has %d cases", len(golden.Cases))
	}
	for _, c := range golden.Cases {
		t.Run(fmt.Sprintf("%s/%s/%s/%s/%s/%s/alpha=%g", c.Type, c.Method, c.Fixture, strings.Join(c.Controls, "+"), c.Missing, c.Weight, c.Alpha), func(t *testing.T) {
			params := map[string]any{"missing": c.Missing, "inference": true}
			if c.Method != "" {
				params["method"] = c.Method
			}
			if c.Alpha != 0.05 {
				params["alpha"] = c.Alpha
			}
			if len(c.Controls) > 0 {
				params["control"] = c.Controls
			}
			res := processMatrices(t, cfg, &types.Request{
				Cohort:   &types.Cohort{Filename: filepath.Join(dir, c.Fixture+".pulse")},
				Matrices: []types.MatrixSpec{inferenceSpec(c.Type, "m", c.Members, params, c.Weight)},
			}).Matrices[0]
			checkMatrixCells(t, "primary", res.Primary.Values, c.R, false)
			for key, want := range map[string][][]*float64{"p": c.P, "ci_lower": c.CILower, "ci_upper": c.CIUpper} {
				aux := res.Auxiliary[key]
				if aux == nil {
					t.Fatalf("auxiliary.%s missing (auxiliary keys %v)", key, auxKeys(res))
				}
				if aux.Kind != types.MatrixKindSquareSymmetric || len(aux.RowKeys) != len(c.Members) {
					t.Fatalf("auxiliary.%s shape = %s %v", key, aux.Kind, aux.RowKeys)
				}
				checkMatrixCells(t, "auxiliary."+key, aux.Values, want, true)
			}
		})
	}
}

func auxKeys(res types.MatrixResult) []string {
	var out []string
	for k := range res.Auxiliary {
		out = append(out, k)
	}
	return out
}

// TestMatrixInference_PartialMatchesPpcorP: every mv_partial_correlation.json
// case's ppcor p-value (n = N*, min pair n pairwise) is auxiliary.p.
func TestMatrixInference_PartialMatchesPpcorP(t *testing.T) {
	t.Parallel()

	var golden struct {
		Cases []struct {
			partialOracle
			PValue [][]*float64 `json:"p_value"`
		} `json:"cases"`
	}
	dir := loadMV(t, "mv_partial_correlation.json", &golden)
	cfg := fixtureFS(t, dir)
	for _, c := range golden.Cases {
		t.Run(fmt.Sprintf("%s/%s/%s/%s", c.Fixture, strings.Join(c.Controls, "+"), c.Missing, c.Weight), func(t *testing.T) {
			params := map[string]any{"missing": c.Missing, "inference": true}
			if len(c.Controls) > 0 {
				params["control"] = c.Controls
			}
			res := processMatrices(t, cfg, &types.Request{
				Cohort:   &types.Cohort{Filename: filepath.Join(dir, c.Fixture+".pulse")},
				Matrices: []types.MatrixSpec{inferenceSpec(types.MAT_PARTIAL_CORRELATION, "m", c.Members, params, c.Weight)},
			}).Matrices[0]
			checkMatrixCells(t, "auxiliary.p", res.Auxiliary["p"].Values, c.PValue, true)
			if c.Missing == "pairwise" && findWarning(res, errors.PULSE_MATRIX_PAIRWISE_N_STAR) == nil {
				t.Error("pairwise partial inference names no PULSE_MATRIX_PAIRWISE_N_STAR")
			}
		})
	}
}

// TestMatrixInference_PearsonParity: every auxiliary cell equals
// TEST_PEARSON_R's p_value / ci_low / ci_high on the same pair (the one
// shared arithmetic, correlationInference), unweighted and under both
// weight kinds, one block and several, listwise and pairwise.
func TestMatrixInference_PearsonParity(t *testing.T) {
	t.Parallel()

	members := []string{"x1", "x2", "x3"}
	for _, n := range []int{40, 3*4096 + 17} {
		for _, missing := range []string{"listwise", "pairwise"} {
			// Listwise on null-free rows (the test then reads the same
			// rows); pairwise over nulls, each test over its pair's rows.
			rows := pearsonParityRows(n)
			if missing == "pairwise" {
				rows = matrixRows(n)
			}
			cfg := writeMatrixCohort(t, "parity.pulse", rows)
			for _, w := range []string{"", "f", "p"} {
				t.Run(fmt.Sprintf("n=%d/%s/weight=%q", n, missing, w), func(t *testing.T) {
					var tests []*types.Test
					for i := range members {
						for j := i + 1; j < len(members); j++ {
							tests = append(tests, &types.Test{
								Type: types.TEST_PEARSON_R, Field: members[i], Field2: members[j],
								Label: members[i] + "~" + members[j], Alpha: 0.1,
								Weight: covSpec("", "", 0, w, "").Weight,
							})
						}
					}
					spec := corrSpec("corr", members, w)
					spec.Params = json.RawMessage(`{"inference": true, "alpha": 0.1, "missing": "` + missing + `"}`)
					req := &types.Request{Cohort: &types.Cohort{Filename: "parity.pulse"}, Matrices: []types.MatrixSpec{spec}, Tests: tests}
					resp := processMatrices(t, cfg, req)
					aux := resp.Matrices[0].Auxiliary
					k := 0
					for i := range members {
						for j := i + 1; j < len(members); j++ {
							tr := resp.Tests[k]
							k++
							for _, c := range []struct {
								key  string
								want float64
							}{{"p", tr.PValue}, {"ci_lower", tr.Details["ci_low"].(float64)}, {"ci_upper", tr.Details["ci_high"].(float64)}} {
								for _, cell := range [][2]int{{i, j}, {j, i}} {
									got := aux[c.key].Values[cell[0]][cell[1]]
									if !(math.Abs(got-c.want) <= 1e-10*(1+math.Abs(c.want))) {
										t.Errorf("%s[%d][%d] = %.17g, TEST_PEARSON_R(%s) = %.17g", c.key, cell[0], cell[1], got, tr.Label, c.want)
									}
								}
							}
						}
					}
				})
			}
		}
	}
}

// TestMatrixInference_OffIsByteIdentical: params.inference absent or
// false leaves the response byte-identical (no auxiliary p / ci, no new
// warning, no new component); on adds exactly the three auxiliaries
// and leaves every other part's bytes alone.
func TestMatrixInference_OffIsByteIdentical(t *testing.T) {
	t.Parallel()

	cfg := writeMatrixCohort(t, "m.pulse", matrixRows(300))
	run := func(typ types.MatrixType, params string) (map[string]any, *types.Response) {
		t.Helper()
		spec := missingSpec(typ, []string{"x1", "x2", "x3"}, "p", params)
		resp := processMatrices(t, cfg, &types.Request{Cohort: &types.Cohort{Filename: "m.pulse"}, Matrices: []types.MatrixSpec{spec}})
		raw, err := json.Marshal(resp)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		return m, resp
	}
	for _, typ := range []types.MatrixType{types.MAT_CORRELATION, types.MAT_PARTIAL_CORRELATION} {
		for _, missing := range []string{"", `"missing": "pairwise"`} {
			if missing != "" && typ == types.MAT_PARTIAL_CORRELATION {
				missing += `, "repair": "nearest"` // the pairwise input is not PSD
			}
			t.Run(string(typ)+missing, func(t *testing.T) {
				join := func(extra string) string {
					parts := []string{}
					for _, s := range []string{missing, extra} {
						if s != "" {
							parts = append(parts, s)
						}
					}
					if len(parts) == 0 {
						return ""
					}
					return "{" + strings.Join(parts, ", ") + "}"
				}
				base, _ := run(typ, join(""))
				off, _ := run(typ, join(`"inference": false`))
				if a, b := mustJSON(t, base), mustJSON(t, off); a != b {
					t.Errorf("inference false moved the response:\n  absent: %s\n  false:  %s", a, b)
				}
				on, resp := run(typ, join(`"inference": true`))
				for _, k := range []string{"p", "ci_lower", "ci_upper"} {
					if resp.Matrices[0].Auxiliary[k] == nil {
						t.Fatalf("inference true: auxiliary.%s missing", k)
					}
				}
				mat := on["matrices"].([]any)[0].(map[string]any)
				aux := mat["auxiliary"].(map[string]any)
				for _, k := range []string{"p", "ci_lower", "ci_upper"} {
					delete(aux, k)
				}
				if len(aux) == 0 {
					delete(mat, "auxiliary")
				}
				// The inference's own warnings (the pairwise partial's N*
				// choice) are the other addition.
				if ws, ok := mat["warnings"].([]any); ok {
					var kept []any
					for _, w := range ws {
						d := w.(map[string]any)["details"].(map[string]any)
						if fmt.Sprint(d["outputs"]) == "[p ci_lower ci_upper]" {
							continue
						}
						kept = append(kept, w)
					}
					mat["warnings"] = kept
				}
				if a, b := mustJSON(t, base), mustJSON(t, on); a != b {
					t.Errorf("inference true moved a part besides its three auxiliaries:\n  off: %s\n  on:  %s", a, b)
				}
			})
		}
	}
}

// TestMatrixInference_SizeNulls: a cell too thin for inference is null
// (never collapsed to r) with PULSE_MATRIX_INSUFFICIENT_N scope
// "inference" naming the null outputs; a probability-weighted fold
// whose Kish n_eff is below 3 adds PULSE_WEIGHT_LOW_NEFF.
func TestMatrixInference_SizeNulls(t *testing.T) {
	t.Parallel()

	// Three complete rows, then x1 only on more rows: listwise n = 3.
	rows := [][5]float64{
		{1, 2, 4, 1, 1}, {2, 1, 5, 1, 1}, {3, 5, 3, 1, 1},
	}
	nan := math.NaN()
	pairwiseRows := append(append([][5]float64(nil), rows...),
		[5]float64{4, nan, 9, 1, 1}, [5]float64{5, nan, 1, 1, 1}, [5]float64{6, nan, 7, 1, 1})
	cases := []struct {
		name     string
		rows     [][5]float64
		typ      types.MatrixType
		params   string
		weight   string
		pNull    bool // p null on the (x1, x2) cell
		ciNull   bool
		scope    string // "", or the INSUFFICIENT_N inference shape: "matrix" | "pairs"
		outputs  []any
		lowNEff  bool
		pairwise bool
	}{
		// n = 3: df = 1 (p defined); the interval needs n ≥ 4.
		{name: "pearson_n3", rows: rows, typ: types.MAT_CORRELATION, params: `{"inference": true}`,
			ciNull: true, scope: "matrix", outputs: []any{"ci_lower", "ci_upper"}},
		// Frequency weight 1 on 3 rows: Σw = 3 is below the interval's
		// floor of 4 (TEST_PEARSON_R's; under frequency it reads Σw, so
		// weight 2 — Σw = 6 — would carry one, as its expansion does).
		{name: "pearson_freq_n3", rows: [][5]float64{{1, 2, 4, 1, 1}, {2, 1, 5, 1, 1}, {3, 5, 3, 1, 1}}, typ: types.MAT_CORRELATION,
			params: `{"inference": true}`, weight: "f", ciNull: true, scope: "matrix", outputs: []any{"ci_lower", "ci_upper"}},
		// Partial over 3 members (g = 1) on 4 rows: df = 4 − 2 − 1 = 1
		// (p defined); the interval needs 4 + g rows and N* − 3 − g > 0.
		{name: "partial_n4", rows: append(append([][5]float64(nil), rows...), [5]float64{4, 4, 1, 1, 1}), typ: types.MAT_PARTIAL_CORRELATION, params: `{"inference": true}`,
			ciNull: true, scope: "matrix", outputs: []any{"ci_lower", "ci_upper"}},
		// Pairwise: (x1, x2) and (x2, x3) on 3 rows (interval null),
		// (x1, x3) on 6.
		{name: "pearson_pairwise", rows: pairwiseRows, typ: types.MAT_CORRELATION, params: `{"inference": true, "missing": "pairwise"}`,
			ciNull: true, scope: "pairs", outputs: []any{"ci_lower", "ci_upper"}, pairwise: true},
		// Rank methods: Spearman's interval (Bonett–Wright) needs 4
		// rows, Kendall's (Fieller, se² = 0.437/(n − 4)) needs 5, so a
		// 4-row Kendall matrix carries p and no interval.
		{name: "spearman_n3", rows: rows, typ: types.MAT_CORRELATION, params: `{"inference": true, "method": "spearman"}`,
			ciNull: true, scope: "matrix", outputs: []any{"ci_lower", "ci_upper"}},
		{name: "kendall_n4", rows: append(append([][5]float64(nil), rows...), [5]float64{4, 4, 1, 1, 1}), typ: types.MAT_CORRELATION,
			params: `{"inference": true, "method": "kendall"}`, ciNull: true, scope: "matrix", outputs: []any{"ci_lower", "ci_upper"}},
		// Frequency weight 1 on 4 rows: Σw = 4 is below Fieller's
		// 5-row floor (weight 2 — Σw = 8 — would carry it).
		{name: "kendall_freq_n4", rows: [][5]float64{{1, 2, 4, 1, 1}, {2, 1, 5, 1, 1}, {3, 5, 3, 1, 1}, {4, 4, 1, 1, 1}}, typ: types.MAT_CORRELATION,
			params: `{"inference": true, "method": "kendall"}`, weight: "f", ciNull: true, scope: "matrix", outputs: []any{"ci_lower", "ci_upper"}},
		{name: "kendall_pairwise", rows: pairwiseRows, typ: types.MAT_CORRELATION, params: `{"inference": true, "missing": "pairwise", "method": "kendall"}`,
			ciNull: true, scope: "pairs", outputs: []any{"ci_lower", "ci_upper"}, pairwise: true},
		// Probability weights 1, 1, 50: Kish n_eff ≈ 1.08 < 3 → df ≤ 0.
		{name: "pearson_low_neff", rows: [][5]float64{{1, 2, 4, 1, 1}, {2, 1, 5, 1, 1}, {3, 5, 3, 1, 50}, {4, 4, 4, 1, 1}},
			typ: types.MAT_CORRELATION, params: `{"inference": true}`, weight: "p",
			pNull: true, ciNull: true, scope: "matrix", outputs: []any{"p", "ci_lower", "ci_upper"}, lowNEff: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := runOneMatrix(t, c.rows, missingSpec(c.typ, []string{"x1", "x2", "x3"}, c.weight, c.params))
			if r := res.Primary.Values[0][1]; math.IsNaN(r) {
				t.Fatalf("fixture: r(x1, x2) is null")
			}
			p, lo, hi := res.Auxiliary["p"].Values[0][1], res.Auxiliary["ci_lower"].Values[0][1], res.Auxiliary["ci_upper"].Values[0][1]
			if math.IsNaN(p) != c.pNull {
				t.Errorf("p = %v, want null %v", p, c.pNull)
			}
			if math.IsNaN(lo) != c.ciNull || math.IsNaN(hi) != c.ciNull {
				t.Errorf("interval = [%v, %v], want null %v (never collapsed to r = %v)", lo, hi, c.ciNull, res.Primary.Values[0][1])
			}
			if c.pairwise {
				if v := res.Auxiliary["ci_lower"].Values[0][2]; math.IsNaN(v) {
					t.Errorf("pairwise (x1, x3) rests on 6 rows, yet its interval is null")
				}
			}
			var w *types.ResponseWarning
			for _, x := range res.Warnings {
				if x.Code == string(errors.PULSE_MATRIX_INSUFFICIENT_N) && x.Details["scope"] == "inference" {
					w = x
				}
			}
			if w == nil {
				t.Fatalf("no PULSE_MATRIX_INSUFFICIENT_N scope inference (warnings %v)", matWarningCodes(res))
			}
			if c.scope == "pairs" {
				pairs := w.Details["pairs"].([]map[string]any)
				if len(pairs) != 2 || pairs[0]["row"] != "x1" || pairs[0]["col"] != "x2" || pairs[1]["row"] != "x2" || pairs[1]["col"] != "x3" ||
					fmt.Sprint(pairs[0]["outputs"]) != fmt.Sprint(c.outputs) {
					t.Errorf("pairs = %v, want (x1, x2) and (x2, x3) with outputs %v", pairs, c.outputs)
				}
			} else if fmt.Sprint(w.Details["outputs"]) != fmt.Sprint(c.outputs) {
				t.Errorf("outputs = %v, want %v", w.Details["outputs"], c.outputs)
			}
			if got := findWarning(res, errors.PULSE_WEIGHT_LOW_NEFF) != nil; got != c.lowNEff {
				t.Errorf("PULSE_WEIGHT_LOW_NEFF present = %v, want %v", got, c.lowNEff)
			}
			for _, k := range []string{"p", "ci_lower", "ci_upper"} {
				if v := res.Auxiliary[k].Values[1][1]; !math.IsNaN(v) {
					t.Errorf("%s diagonal = %v, want null", k, v)
				}
			}
		})
	}
}

// TestMatrixInference_ReturnGate: a `return` that drops
// matrices[*].auxiliary skips the inference computation (no work, no
// warning), the rest of the result unchanged; kept, it runs. `standard`
// keeps the inference keys a spec asked for (E4-G1), so it runs; a
// partial selection naming none of this spec's keys skips it.
func TestMatrixInference_ReturnGate(t *testing.T) {
	// serial: reads deltas of process-wide internal/processing work counters, which concurrent tests bump

	cfg := writeMatrixCohort(t, "m.pulse", matrixRows(200))
	spec := missingSpec(types.MAT_CORRELATION, []string{"x1", "x2", "x3"}, "", `{"inference": true}`)
	for _, c := range []struct {
		name string
		ret  *types.Return
		runs bool
	}{
		{"absent", nil, true},
		{"full", &types.Return{Preset: types.ReturnPresetFull}, true},
		{"standard", &types.Return{Preset: types.ReturnPresetStandard}, true},
		{"minimal", &types.Return{Preset: types.ReturnPresetMinimal}, false},
		{"standard_exclude_inference", &types.Return{Preset: types.ReturnPresetStandard, Exclude: []string{
			"matrices[*].auxiliary.p", "matrices[*].auxiliary.ci_lower", "matrices[*].auxiliary.ci_upper",
			"matrices[*].auxiliary.p_adjusted", "matrices[*].auxiliary.significant_adjusted"}}, false},
		{"include_other_key", &types.Return{Include: []string{"matrices[*].auxiliary.eigenvectors"}}, false},
		{"exclude_auxiliary", &types.Return{Exclude: []string{"matrices[*].auxiliary"}}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			before := processing.WorkStats()
			resp, err := New(cfg).Process(context.Background(), &types.Request{
				Cohort: &types.Cohort{Filename: "m.pulse"}, Matrices: []types.MatrixSpec{spec}, Return: c.ret,
			})
			if err != nil {
				t.Fatal(err)
			}
			got := processing.WorkStats().Sub(before).MatrixInferenceBuilds
			if c.runs != (got > 0) {
				t.Errorf("inference builds = %d, want runs %v", got, c.runs)
			}
			if has := resp.Matrices[0].Auxiliary["p"] != nil; has != c.runs {
				t.Errorf("auxiliary.p present = %v, want %v", has, c.runs)
			}
		})
	}
}

// TestMatrixInference_RankOffIsByteIdentical: on a rank method,
// params.inference absent or false leaves the response byte-identical;
// on adds exactly auxiliary.p / ci_lower / ci_upper (the primary, n,
// determinant and every other part keep their bytes).
func TestMatrixInference_RankOffIsByteIdentical(t *testing.T) {
	t.Parallel()

	cfg := writeMatrixCohort(t, "m.pulse", matrixRows(300))
	run := func(params string) (map[string]any, *types.Response) {
		t.Helper()
		spec := missingSpec(types.MAT_CORRELATION, []string{"x1", "x2", "x3"}, "f", params)
		resp := processMatrices(t, cfg, &types.Request{Cohort: &types.Cohort{Filename: "m.pulse"}, Matrices: []types.MatrixSpec{spec}})
		raw, err := json.Marshal(resp)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		return m, resp
	}
	for _, method := range []string{"spearman", "kendall"} {
		for _, missing := range []string{"listwise", "pairwise"} {
			t.Run(method+"/"+missing, func(t *testing.T) {
				head := `{"method": "` + method + `", "missing": "` + missing + `"`
				base, _ := run(head + `}`)
				off, _ := run(head + `, "inference": false}`)
				if a, b := mustJSON(t, base), mustJSON(t, off); a != b {
					t.Errorf("inference false moved the response:\n  absent: %s\n  false:  %s", a, b)
				}
				on, resp := run(head + `, "inference": true}`)
				for _, k := range []string{"p", "ci_lower", "ci_upper"} {
					v := resp.Matrices[0].Auxiliary[k]
					if v == nil {
						t.Fatalf("inference true: auxiliary.%s missing", k)
					}
					if math.IsNaN(v.Values[0][1]) {
						t.Errorf("auxiliary.%s[0][1] is null on 300 rows", k)
					}
				}
				mat := on["matrices"].([]any)[0].(map[string]any)
				aux := mat["auxiliary"].(map[string]any)
				for _, k := range []string{"p", "ci_lower", "ci_upper"} {
					delete(aux, k)
				}
				if len(aux) == 0 {
					delete(mat, "auxiliary")
				}
				if a, b := mustJSON(t, base), mustJSON(t, on); a != b {
					t.Errorf("inference true moved a part besides its three auxiliaries:\n  off: %s\n  on:  %s", a, b)
				}
			})
		}
	}
}

// TestMatrixInference_RankWeightRules: a rank method stays
// frequency-only under params.inference — a probability weight is
// PULSE_WEIGHT_UNSUPPORTED — and a frequency weight is the expansion:
// p and both bounds equal the unweighted run over the rows repeated w
// times (weight-0 rows dropped), the cor.test oracle's rule.
func TestMatrixInference_RankWeightRules(t *testing.T) {
	t.Parallel()

	cfg := writeMatrixCohort(t, "rank.pulse", rankRows(60))
	for _, method := range []string{"spearman", "kendall"} {
		spec := missingSpec(types.MAT_CORRELATION, []string{"x1", "x2", "x3"}, "p", `{"inference": true, "method": "`+method+`"}`)
		_, err := New(cfg).Process(context.Background(), &types.Request{Cohort: &types.Cohort{Filename: "rank.pulse"}, Matrices: []types.MatrixSpec{spec}})
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_WEIGHT_UNSUPPORTED {
			t.Errorf("%s with inference under a probability weight: err = %v, want PULSE_WEIGHT_UNSUPPORTED", method, err)
		}
	}

	// Frequency weights 0..3 on 40 rows (with ties from the copies), and
	// the rep() expansion of the same rows unweighted.
	var weighted, expanded [][5]float64
	for i := 0; i < 40; i++ {
		x1 := float64((i*37)%41) + 0.5
		x2 := float64((i*11)%17) + 0.25*float64(i%3)
		x3 := float64((i * 7) % 13)
		w := float64(i % 4)
		weighted = append(weighted, [5]float64{x1, x2, x3, w, 1})
		for k := 0; k < int(w); k++ {
			expanded = append(expanded, [5]float64{x1, x2, x3, 1, 1})
		}
	}
	for _, method := range []string{"spearman", "kendall"} {
		for _, missing := range []string{"listwise", "pairwise"} {
			params := `{"inference": true, "method": "` + method + `", "missing": "` + missing + `"}`
			got := runOneMatrix(t, weighted, missingSpec(types.MAT_CORRELATION, []string{"x1", "x2", "x3"}, "f", params))
			want := runOneMatrix(t, expanded, missingSpec(types.MAT_CORRELATION, []string{"x1", "x2", "x3"}, "", params))
			for _, k := range []string{"p", "ci_lower", "ci_upper"} {
				for i := 0; i < 3; i++ {
					for j := 0; j < 3; j++ {
						g, w := got.Auxiliary[k].Values[i][j], want.Auxiliary[k].Values[i][j]
						if i == j {
							continue
						}
						if !(math.Abs(g-w) <= 1e-12*(1+math.Abs(w))) {
							t.Errorf("%s/%s %s[%d][%d] = %.17g, expansion = %.17g", method, missing, k, i, j, g, w)
						}
					}
				}
			}
		}
	}
}

// TestMatrixInference_ZeroWeightRowsFloorLikeTest: a weight-0 row counts
// in the matrix's components n but never in inference's row floors —
// the cell's p and interval rest on the rows carrying mass, as
// TEST_PEARSON_R (and the rank tests) skip a weight-0 row from their raw
// n. Under a frequency weight the floors read Σw (U36 E1-S7, frequency
// ≡ expansion), to which a weight-0 row adds nothing. On the same pair
// and weights the cell is null exactly where the test refuses (p: below
// 3) or collapses its interval (below 4), and equals the test's figure
// elsewhere; the matrix's own n still counts the weight-0 rows.
func TestMatrixInference_ZeroWeightRowsFloorLikeTest(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name           string
		rows           [][5]float64 // x1, x2, x3, f, p
		weight         string
		method         string
		pNull, ciNull  bool
		componentsN    int
		testRefused    bool
		testCICollapse bool
	}{
		// Two unit rows (Σw = 2) and a weight-0 row: the raw count 3
		// would admit p; Σw does not, and the test refuses.
		{name: "freq_p_floor", weight: "f",
			rows:  [][5]float64{{1, 2, 4, 1, 1}, {2, 5, 5, 1, 1}, {3, 1, 3, 0, 1}},
			pNull: true, ciNull: true, componentsN: 3, testRefused: true},
		// Two massed rows of Σw = 10 and a weight-0 row: the expansion
		// is ten rows on two points, so r = 1 with p = 0 and the
		// degenerate interval [r, r] — defined, and the test's.
		{name: "freq_p_sigma_w", weight: "f",
			rows:        [][5]float64{{1, 2, 4, 5, 1}, {2, 5, 5, 5, 1}, {3, 1, 3, 0, 1}},
			componentsN: 3},
		// Three unit rows (Σw = 3) and a weight-0 row: the interval
		// needs 4; the test collapses it to r.
		{name: "freq_ci_floor", weight: "f",
			rows:   [][5]float64{{1, 2, 4, 1, 1}, {2, 1, 5, 1, 1}, {3, 5, 3, 1, 1}, {4, 4, 1, 0, 1}},
			ciNull: true, componentsN: 4, testCICollapse: true},
		// Probability weights: Kish n_eff never exceeds the massed rows,
		// so N* already bounds the floor; parity with weight-0 rows.
		{name: "prob_defined", weight: "p",
			rows:        [][5]float64{{1, 2, 4, 1, 2}, {2, 1, 5, 1, 1}, {3, 5, 3, 1, 3}, {4, 4, 1, 1, 1}, {5, 3, 2, 1, 2}, {6, 7, 2, 1, 0}},
			componentsN: 6},
		// Four massed rows and two weight-0 rows: both defined, equal.
		{name: "freq_defined", weight: "f",
			rows:        [][5]float64{{1, 2, 4, 2, 1}, {2, 1, 5, 1, 1}, {3, 5, 3, 3, 1}, {4, 4, 1, 1, 1}, {5, 9, 9, 0, 1}, {6, 0, 2, 0, 1}},
			componentsN: 6},
		// Rank methods share the floor: Spearman's interval needs
		// Σw ≥ 4, Kendall's p Σw ≥ 3; a weight-0 row adds nothing.
		{name: "spearman_ci_floor", weight: "f", method: "spearman",
			rows:   [][5]float64{{1, 2, 4, 1, 1}, {2, 1, 5, 1, 1}, {3, 5, 3, 1, 1}, {4, 4, 1, 0, 1}},
			ciNull: true, componentsN: 4},
		{name: "kendall_p_floor", weight: "f", method: "kendall",
			rows:  [][5]float64{{1, 2, 4, 1, 1}, {2, 5, 5, 1, 1}, {3, 1, 3, 0, 1}},
			pNull: true, ciNull: true, componentsN: 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			params := `{"inference": true}`
			if c.method != "" {
				params = `{"inference": true, "method": "` + c.method + `"}`
			}
			cfg := writeMatrixCohort(t, "zw.pulse", c.rows)
			full := processMatrices(t, cfg, &types.Request{
				Cohort:   &types.Cohort{Filename: "zw.pulse"},
				Matrices: []types.MatrixSpec{missingSpec(types.MAT_CORRELATION, []string{"x1", "x2"}, c.weight, params)},
			})
			res := full.Matrices[0]
			if math.IsNaN(res.Primary.Values[0][1]) {
				t.Fatalf("fixture: r(x1, x2) is null")
			}
			p, lo, hi := res.Auxiliary["p"].Values[0][1], res.Auxiliary["ci_lower"].Values[0][1], res.Auxiliary["ci_upper"].Values[0][1]
			if math.IsNaN(p) != c.pNull {
				t.Errorf("p = %v, want null %v (massed-row floor)", p, c.pNull)
			}
			if math.IsNaN(lo) != c.ciNull || math.IsNaN(hi) != c.ciNull {
				t.Errorf("interval = [%v, %v], want null %v (massed-row floor)", lo, hi, c.ciNull)
			}
			if c.pNull || c.ciNull {
				if findWarning(res, errors.PULSE_MATRIX_INSUFFICIENT_N) == nil {
					t.Errorf("no PULSE_MATRIX_INSUFFICIENT_N for the null outputs (warnings %v)", matWarningCodes(res))
				}
			}
			if full.Components == nil || len(full.Components.Matrices) != 1 || full.Components.Matrices[0].N != c.componentsN {
				t.Errorf("components = %+v, want matrix n %d (weight-0 rows still count)", full.Components, c.componentsN)
			}
			if c.method != "" {
				return
			}
			// The single-pair test on the same rows and weight.
			req := &types.Request{Cohort: &types.Cohort{Filename: "zw.pulse"}, Tests: []*types.Test{{
				Type: types.TEST_PEARSON_R, Field: "x1", Field2: "x2", Label: "r",
				Weight: covSpec("", "", 0, c.weight, "").Weight,
			}}}
			resp, err := processOrSlotErr(context.Background(), New(cfg), req)
			if c.testRefused {
				var ce *errors.CodedError
				if err == nil || !stderrors.As(err, &ce) || ce.Code != errors.PULSE_TEST_INSUFFICIENT_N {
					t.Fatalf("TEST_PEARSON_R err = %v, want PULSE_TEST_INSUFFICIENT_N", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("TEST_PEARSON_R: %v", err)
			}
			tr := resp.Tests[0]
			if !(math.Abs(p-tr.PValue) <= 1e-12*(1+math.Abs(tr.PValue))) {
				t.Errorf("p = %.17g, TEST_PEARSON_R p = %.17g", p, tr.PValue)
			}
			tlo, thi := tr.Details["ci_low"].(float64), tr.Details["ci_high"].(float64)
			if c.testCICollapse {
				if tlo != tr.Statistic || thi != tr.Statistic {
					t.Errorf("fixture: TEST_PEARSON_R interval [%v, %v] not collapsed to r", tlo, thi)
				}
				return
			}
			if !(math.Abs(lo-tlo) <= 1e-12) || !(math.Abs(hi-thi) <= 1e-12) {
				t.Errorf("interval = [%v, %v], TEST_PEARSON_R [%v, %v]", lo, hi, tlo, thi)
			}
		})
	}
}

// untiedRankRows: n rows, every member untied (x1 = i + ½, x2 / x3 the
// multiplicative residues i·a mod m for a prime m > n), unit weights.
func untiedRankRows(n, m, a, b int) [][5]float64 {
	rows := make([][5]float64, n)
	for i := range rows {
		rows[i] = [5]float64{float64(i) + 0.5, float64((i * a) % m), float64((i*b)%m) + 0.25, 1, 1}
	}
	return rows
}

// TestMatrixInference_RankTestParity: under a rank method every
// auxiliary.p cell equals TEST_SPEARMAN_R / TEST_KENDALL_TAU's p_value
// on the same pair, bit for bit (U28 E3-G2: one cor.test helper,
// kendallCorTestP / spearmanCorTestP) — across every cor.test switch:
// untied small n (both exact), untied n = 60 (Kendall normal, Spearman
// AS 89 Edgeworth), untied n = 1400 (Spearman t), ties with nulls
// (pairwise), unit frequency weights (exact) and integer frequency
// weights with weight-0 rows (ties).
func TestMatrixInference_RankTestParity(t *testing.T) {
	t.Parallel()

	members := []string{"x1", "x2", "x3"}
	cases := []struct {
		name    string
		rows    [][5]float64
		weight  string
		missing string
	}{
		{"untied8", untiedRankRows(8, 11, 3, 5), "", "listwise"},
		{"untied12_unit_freq", untiedRankRows(12, 13, 5, 7), "f", "listwise"},
		{"untied60", untiedRankRows(60, 61, 37, 23), "", "listwise"},
		{"untied1400", untiedRankRows(1400, 1409, 37, 101), "", "listwise"},
		{"tied_nulls", rankRows(80), "", "pairwise"},
		{"tied_freq", rankRows(80), "f", "pairwise"},
	}
	for _, c := range cases {
		cfg := writeMatrixCohort(t, "rp.pulse", c.rows)
		for _, method := range []string{"spearman", "kendall"} {
			t.Run(c.name+"/"+method, func(t *testing.T) {
				typ := types.TEST_SPEARMAN_R
				if method == "kendall" {
					typ = types.TEST_KENDALL_TAU
				}
				var tests []*types.Test
				for i := range members {
					for j := i + 1; j < len(members); j++ {
						tests = append(tests, &types.Test{Type: typ, Field: members[i], Field2: members[j],
							Label: members[i] + "~" + members[j], Weight: covSpec("", "", 0, c.weight, "").Weight})
					}
				}
				spec := missingSpec(types.MAT_CORRELATION, members, c.weight,
					`{"inference": true, "method": "`+method+`", "missing": "`+c.missing+`"}`)
				resp := processMatrices(t, cfg, &types.Request{Cohort: &types.Cohort{Filename: "rp.pulse"}, Matrices: []types.MatrixSpec{spec}, Tests: tests})
				p := resp.Matrices[0].Auxiliary["p"].Values
				k := 0
				for i := range members {
					for j := i + 1; j < len(members); j++ {
						tr := resp.Tests[k]
						k++
						if math.IsNaN(tr.PValue) || p[i][j] != tr.PValue || p[j][i] != tr.PValue {
							t.Errorf("p[%d][%d] = %.17g / %.17g, %s(%s) = %.17g", i, j, p[i][j], p[j][i], typ, tr.Label, tr.PValue)
						}
					}
				}
			})
		}
	}
}

// TestMatrixInference_FrequencyFloorIsExpansion (U36 E1-S7): under a
// frequency weight the inference row floors read Σw, so a cell over
// fewer raw rows than a floor whose Σw reaches it carries the p and
// interval of the unweighted run on the rep()-expanded rows — the
// fixtures the raw-row floor used to null.
func TestMatrixInference_FrequencyFloorIsExpansion(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name, params string
		rows         [][5]float64
	}{
		// Three raw rows, Σw = 6: the Pearson interval needs 4.
		{"pearson_ci", `{"inference": true}`, [][5]float64{{1, 2, 4, 2, 1}, {2, 1, 5, 2, 1}, {3, 5, 3, 2, 1}}},
		// Two raw rows, Σw = 4: p needs 3 (r = ±1 on two points).
		{"pearson_p", `{"inference": true}`, [][5]float64{{1, 2, 4, 2, 1}, {2, 5, 5, 2, 1}}},
		// Four raw rows, Σw = 8: Fieller's Kendall interval needs 5.
		{"kendall_ci", `{"inference": true, "method": "kendall"}`, [][5]float64{{1, 2, 4, 2, 1}, {2, 1, 5, 2, 1}, {3, 5, 3, 2, 1}, {4, 4, 1, 2, 1}}},
		// Three raw rows, Σw = 6: the Spearman interval needs 4.
		{"spearman_ci", `{"inference": true, "method": "spearman"}`, [][5]float64{{1, 2, 4, 2, 1}, {2, 1, 5, 2, 1}, {3, 5, 3, 2, 1}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			var expanded [][5]float64
			for _, r := range c.rows {
				for range int(r[3]) {
					e := r
					e[3] = 1
					expanded = append(expanded, e)
				}
			}
			got := runOneMatrix(t, c.rows, missingSpec(types.MAT_CORRELATION, []string{"x1", "x2", "x3"}, "f", c.params))
			want := runOneMatrix(t, expanded, missingSpec(types.MAT_CORRELATION, []string{"x1", "x2", "x3"}, "", c.params))
			for _, k := range []string{"p", "ci_lower", "ci_upper"} {
				for i := 0; i < 3; i++ {
					for j := 0; j < 3; j++ {
						if i == j {
							continue
						}
						g, w := got.Auxiliary[k].Values[i][j], want.Auxiliary[k].Values[i][j]
						if math.IsNaN(w) {
							t.Fatalf("fixture: expansion %s[%d][%d] is null", k, i, j)
						}
						if !(math.Abs(g-w) <= 1e-10*math.Abs(w)) && g != w {
							t.Errorf("%s[%d][%d] = %.17g, expansion = %.17g", k, i, j, g, w)
						}
					}
				}
			}
		})
	}
}
