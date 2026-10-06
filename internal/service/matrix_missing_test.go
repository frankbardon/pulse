package service

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// U16 E3-S2: params.missing ("listwise" | "pairwise"), the pairwise N
// auxiliary, and the per-matrix data-quality warnings
// (PULSE_MATRIX_NOT_PSD, _LISTWISE_HEAVY_DROP, _INSUFFICIENT_N,
// _ZERO_VARIANCE) in MatrixResult.Warnings.

var x123 = []string{"x1", "x2", "x3"}

// missingSpec is a spec over inline members with raw params, weighted
// by the f / p column when weight names one.
func missingSpec(typ types.MatrixType, fields []string, weight, params string) types.MatrixSpec {
	s := types.MatrixSpec{Name: "m", Type: typ, Fields: fields}
	if params != "" {
		s.Params = json.RawMessage(params)
	}
	s.Weight = covSpec("", "", 0, weight, "").Weight
	return s
}

func runOneMatrix(t *testing.T, rows [][5]float64, spec types.MatrixSpec) types.MatrixResult {
	t.Helper()
	cfg := writeMatrixCohort(t, "m.pulse", rows)
	return processMatrices(t, cfg, &types.Request{
		Cohort:   &types.Cohort{Filename: "m.pulse"},
		Matrices: []types.MatrixSpec{spec},
	}).Matrices[0]
}

// warningCodes returns the codes of res.Warnings in order.
func matWarningCodes(res types.MatrixResult) []string {
	var out []string
	for _, w := range res.Warnings {
		out = append(out, w.Code)
	}
	return out
}

func findWarning(res types.MatrixResult, code errors.Code) *types.ResponseWarning {
	for _, w := range res.Warnings {
		if w.Code == string(code) {
			return w
		}
	}
	return nil
}

// TestMatrixPairwise_MatchesReference pins pairwise MAT_COVARIANCE
// (ddof 0 / 1) and MAT_CORRELATION against the per-pair numpy /
// statsmodels oracle, unweighted and under both weight kinds, with the
// pairwise N in auxiliary.n.
func TestMatrixPairwise_MatchesReference(t *testing.T) {
	for _, c := range matrixRefPairwiseCases {
		t.Run(fmt.Sprintf("%s/weight=%q/ddof=%d", c.typ, c.weight, c.ddof), func(t *testing.T) {
			params := `{"missing": "pairwise"}`
			if c.typ == string(types.MAT_COVARIANCE) {
				params = fmt.Sprintf(`{"missing": "pairwise", "ddof": %d}`, c.ddof)
			}
			res := runOneMatrix(t, matrixRefRows, missingSpec(types.MatrixType(c.typ), x123, c.weight, params))
			for r := 0; r < 3; r++ {
				for col := 0; col < 3; col++ {
					if !closeTo(res.Primary.Values[r][col], c.m[r][col], 1e-12) {
						t.Errorf("[%d][%d] = %.17g, want %.17g", r, col, res.Primary.Values[r][col], c.m[r][col])
					}
				}
			}
			if det := res.Scalars["determinant"]; !closeTo(det, c.det, 1e-9) {
				t.Errorf("determinant = %.17g, want %.17g", det, c.det)
			}
			n := res.Auxiliary["n"]
			if n == nil {
				t.Fatal("pairwise result has no auxiliary.n")
			}
			for r := 0; r < 3; r++ {
				for col := 0; col < 3; col++ {
					if n.Values[r][col] != float64(matrixRefPairwiseN[r][col]) {
						t.Errorf("n[%d][%d] = %v, want %d", r, col, n.Values[r][col], matrixRefPairwiseN[r][col])
					}
				}
			}
		})
	}
}

// TestMatrixPairwise_AuxiliaryNShape: auxiliary.n is present only
// under pairwise, with the primary's kind, encoding, keys and labels;
// an explicit "listwise" equals the default bit for bit.
func TestMatrixPairwise_AuxiliaryNShape(t *testing.T) {
	cfg := writeMatrixCohort(t, "m.pulse", matrixRefRows)
	for _, typ := range types.AllMatrixTypes() {
		for _, enc := range types.AllMatrixEncodings() {
			t.Run(fmt.Sprintf("%s/%s", typ, enc), func(t *testing.T) {
				mk := func(params string) types.MatrixResult {
					spec := types.MatrixSpec{Name: "m", Type: typ, Vector: "v", Encoding: enc}
					if params != "" {
						spec.Params = json.RawMessage(params)
					}
					return processMatrices(t, cfg, &types.Request{
						Cohort:   &types.Cohort{Filename: "m.pulse"},
						Vectors:  []types.VectorSpec{{Name: "v", Fields: []string{"x3", "x1", "x2"}, Labels: []string{"C", "A", "B"}}},
						Matrices: []types.MatrixSpec{spec},
					}).Matrices[0]
				}
				def, list, pair := mk(""), mk(`{"missing": "listwise"}`), mk(`{"missing": "pairwise"}`)
				if def.Auxiliary != nil || list.Auxiliary != nil {
					t.Fatalf("listwise carries auxiliary %v / %v", def.Auxiliary, list.Auxiliary)
				}
				a, b := mustJSON(t, def), mustJSON(t, list)
				if a != b {
					t.Errorf("explicit listwise differs from the default:\n%s\n%s", b, a)
				}
				n := pair.Auxiliary["n"]
				if n == nil || len(pair.Auxiliary) != 1 {
					t.Fatalf("pairwise auxiliary = %v, want exactly n", pair.Auxiliary)
				}
				p := pair.Primary
				if n.Kind != p.Kind || n.Encoding != p.Encoding || !reflect.DeepEqual(n.RowKeys, p.RowKeys) ||
					!reflect.DeepEqual(n.ColumnKeys, p.ColumnKeys) || !reflect.DeepEqual(n.Labels, p.Labels) {
					t.Errorf("auxiliary.n header %+v differs from primary %+v", n, p)
				}
				for r := range p.Values {
					if len(n.Values[r]) != len(p.Values[r]) {
						t.Errorf("row %d: n has %d cells, primary %d", r, len(n.Values[r]), len(p.Values[r]))
					}
				}
				// Axis order x3, x1, x2: N(x3, x1) = 10, N(x3, x3) = 11.
				if n.Values[0][0] != 11 || n.Values[0][1] != 10 {
					t.Errorf("n row 0 = %v", n.Values[0])
				}
				if !strings.Contains(mustJSON(t, pair), `"auxiliary":{"n":{"kind":"square_symmetric"`) {
					t.Errorf("wire form lacks auxiliary.n: %s", mustJSON(t, pair))
				}
			})
		}
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

// nonPSDRows: three members observed only two at a time — x1 = x2 on
// rows 0-2, x1 = x3 on rows 3-5, x3 = −x2 on rows 6-8 — so the pairwise
// correlations are r12 = 1, r13 = 1, r23 = −1: no data set has them
// (the third pivot of the reference Cholesky is negative).
func nonPSDRows() [][5]float64 {
	nan := math.NaN()
	return [][5]float64{
		{1, 1, nan, 1, 1}, {2, 2, nan, 1, 1}, {4, 4, nan, 1, 1},
		{1, nan, 1, 1, 1}, {3, nan, 3, 1, 1}, {5, nan, 5, 1, 1},
		{nan, 1, -1, 1, 1}, {nan, 2, -2, 1, 1}, {nan, 6, -6, 1, 1},
	}
}

// TestMatrixWarnings_NotPSD: a crafted pairwise fixture is reported
// non-PSD on both operators with the failing pivot named; a collinear
// (singular but PSD) pairwise matrix and the listwise mode are not.
func TestMatrixWarnings_NotPSD(t *testing.T) {
	for _, typ := range types.AllMatrixTypes() {
		t.Run(string(typ), func(t *testing.T) {
			res := runOneMatrix(t, nonPSDRows(), missingSpec(typ, x123, "", `{"missing": "pairwise"}`))
			w := findWarning(res, errors.PULSE_MATRIX_NOT_PSD)
			if w == nil {
				t.Fatalf("no PULSE_MATRIX_NOT_PSD; warnings %v", matWarningCodes(res))
			}
			if w.Details["pivot"] != 2 || w.Details["member"] != "x3" || w.Details["checked"] != 3 || w.Details["matrix"] != "m" {
				t.Errorf("details = %v, want pivot 2 (x3) of 3", w.Details)
			}
			if !math.IsNaN(res.Scalars["determinant"]) {
				t.Errorf("determinant = %v, want NaN", res.Scalars["determinant"])
			}

			// Collinear complete rows: x2 = 2·x1, x3 = x1 + x2 — singular,
			// PSD; within tolerance, no warning.
			rows := make([][5]float64, 20)
			for i := range rows {
				x := float64(i*i%7) + 0.5*float64(i)
				rows[i] = [5]float64{x, 2 * x, 3 * x, 1, 1}
			}
			if c := matWarningCodes(runOneMatrix(t, rows, missingSpec(typ, x123, "", `{"missing": "pairwise"}`))); len(c) != 0 {
				t.Errorf("collinear pairwise matrix warned %v", c)
			}

			// Listwise never judges PSD (and here no row is complete).
			lw := runOneMatrix(t, nonPSDRows(), missingSpec(typ, x123, "", ""))
			if findWarning(lw, errors.PULSE_MATRIX_NOT_PSD) != nil {
				t.Error("listwise matrix carries PULSE_MATRIX_NOT_PSD")
			}
		})
	}
}

// TestMatrixWarnings_NotPSDOnReferenceCorrelation: the reference rows'
// pairwise correlation matrices are not positive semidefinite (numpy's
// Cholesky refuses them — determinant NaN in the oracle), and the
// warning agrees for every weight column; the pairwise covariances are
// positive definite and carry no warning.
func TestMatrixWarnings_NotPSDOnReferenceCorrelation(t *testing.T) {
	for _, c := range matrixRefPairwiseCases {
		params := `{"missing": "pairwise"}`
		res := runOneMatrix(t, matrixRefRows, missingSpec(types.MatrixType(c.typ), x123, c.weight, params))
		got := findWarning(res, errors.PULSE_MATRIX_NOT_PSD) != nil
		if want := math.IsNaN(c.det); got != want {
			t.Errorf("%s weight=%q ddof=%d: NOT_PSD = %v, oracle says not PD = %v", c.typ, c.weight, c.ddof, got, want)
		}
	}
}

// TestMatrixWarnings_ListwiseHeavyDrop: the reference rows lose 3 of 12
// rows listwise (share 0.25). max_drop_share 0.2 warns with the counts;
// 0.25 (not exceeded) and no max_drop_share do not.
func TestMatrixWarnings_ListwiseHeavyDrop(t *testing.T) {
	for _, typ := range types.AllMatrixTypes() {
		for _, c := range []struct {
			params string
			warn   bool
		}{
			{`{"max_drop_share": 0.2}`, true},
			{`{"missing": "listwise", "max_drop_share": 0}`, true},
			{`{"max_drop_share": 0.25}`, false},
			{``, false},
		} {
			t.Run(fmt.Sprintf("%s/%s", typ, c.params), func(t *testing.T) {
				res := runOneMatrix(t, matrixRefRows, missingSpec(typ, x123, "", c.params))
				w := findWarning(res, errors.PULSE_MATRIX_LISTWISE_HEAVY_DROP)
				if (w != nil) != c.warn {
					t.Fatalf("heavy-drop warning = %v, want %v (warnings %v)", w, c.warn, matWarningCodes(res))
				}
				if w == nil {
					return
				}
				if w.Details["dropped"] != int64(3) || w.Details["rows"] != int64(12) || w.Details["share"] != 0.25 {
					t.Errorf("details = %v, want dropped 3 of 12 rows (0.25)", w.Details)
				}
			})
		}
	}
	// The drop count includes rows whose weight is invalid only among the
	// complete rows: an invalid-weight complete row is not "dropped".
	rows := append([][5]float64(nil), matrixRefRows...)
	rows[0][4] = -1 // complete row, invalid probability weight
	res := runOneMatrix(t, rows, missingSpec(types.MAT_COVARIANCE, x123, "p", `{"max_drop_share": 0.2}`))
	if w := findWarning(res, errors.PULSE_MATRIX_LISTWISE_HEAVY_DROP); w == nil || w.Details["dropped"] != int64(3) || w.Details["rows"] != int64(12) {
		t.Errorf("weighted heavy drop = %v", w)
	}
}

// TestMatrixWarnings_InsufficientN: a one-row cohort and an all-zero
// weight column warn at matrix scope; a pairwise pair with one row warns
// at pair scope naming the pair.
func TestMatrixWarnings_InsufficientN(t *testing.T) {
	for _, typ := range types.AllMatrixTypes() {
		t.Run(string(typ), func(t *testing.T) {
			one := runOneMatrix(t, matrixRefRows[:1], missingSpec(typ, x123, "", ""))
			w := findWarning(one, errors.PULSE_MATRIX_INSUFFICIENT_N)
			if w == nil || w.Details["scope"] != "matrix" || w.Details["n"] != int64(1) {
				t.Fatalf("one-row warning = %v", w)
			}
			if c := matWarningCodes(one); len(c) != 1 {
				t.Errorf("one-row matrix warned %v, want INSUFFICIENT_N alone", c)
			}

			zero := append([][5]float64(nil), matrixRefRows...)
			for i := range zero {
				zero[i][4] = 0
			}
			zw := runOneMatrix(t, zero, missingSpec(typ, x123, "p", ""))
			if w := findWarning(zw, errors.PULSE_MATRIX_INSUFFICIENT_N); w == nil || w.Details["scope"] != "matrix" || w.Details["sum_weights"] != 0.0 {
				t.Errorf("zero-mass warning = %v", w)
			}

			// Pairwise: x1 and x3 share one row.
			nan := math.NaN()
			rows := [][5]float64{
				{1, 2, nan, 1, 1}, {2, 3, nan, 1, 1}, {4, 1, nan, 1, 1},
				{nan, 5, 1, 1, 1}, {nan, 6, 3, 1, 1}, {7, nan, 2, 1, 1},
			}
			pw := runOneMatrix(t, rows, missingSpec(typ, x123, "", `{"missing": "pairwise"}`))
			w = findWarning(pw, errors.PULSE_MATRIX_INSUFFICIENT_N)
			if w == nil || w.Details["scope"] != "pairs" {
				t.Fatalf("pair warning = %v", w)
			}
			want := []map[string]any{{"row": "x1", "col": "x3", "n": int64(1)}}
			if !reflect.DeepEqual(w.Details["pairs"], want) {
				t.Errorf("pairs = %v, want %v", w.Details["pairs"], want)
			}
			if !math.IsNaN(pw.Primary.Values[0][2]) || pw.Auxiliary["n"].Values[0][2] != 1 {
				t.Errorf("thin pair cell = %v (n %v), want null over 1 row", pw.Primary.Values[0][2], pw.Auxiliary["n"].Values[0][2])
			}
		})
	}
}

// TestMatrixWarnings_ZeroVariance: a constant member is named on both
// operators; the correlation nulls its row and column, the covariance
// keeps them at 0 (a defined figure).
func TestMatrixWarnings_ZeroVariance(t *testing.T) {
	rows := pearsonParityRows(40)
	for i := range rows {
		rows[i][1] = 3.5 // x2 constant
	}
	for _, typ := range types.AllMatrixTypes() {
		for _, mode := range []string{`{"missing": "listwise"}`, `{"missing": "pairwise"}`} {
			t.Run(string(typ)+mode, func(t *testing.T) {
				res := runOneMatrix(t, rows, missingSpec(typ, x123, "", mode))
				w := findWarning(res, errors.PULSE_MATRIX_ZERO_VARIANCE)
				if w == nil || !reflect.DeepEqual(w.Details["members"], []string{"x2"}) {
					t.Fatalf("zero-variance warning = %v (warnings %v)", w, matWarningCodes(res))
				}
				cell := res.Primary.Values[1][1]
				if typ == types.MAT_CORRELATION && !math.IsNaN(cell) {
					t.Errorf("correlation diagonal of the constant member = %v, want null", cell)
				}
				if typ == types.MAT_COVARIANCE && cell != 0 {
					t.Errorf("covariance diagonal of the constant member = %v, want 0", cell)
				}
			})
		}
	}
	// A varying battery warns nothing.
	if c := matWarningCodes(runOneMatrix(t, pearsonParityRows(40), missingSpec(types.MAT_CORRELATION, x123, "", ""))); len(c) != 0 {
		t.Errorf("clean matrix warned %v", c)
	}
}

// TestMatrixWarnings_WireShape: matrix warnings marshal as the
// {code, message, details} entries of every other warning slot.
func TestMatrixWarnings_WireShape(t *testing.T) {
	res := runOneMatrix(t, nonPSDRows(), missingSpec(types.MAT_CORRELATION, x123, "", `{"missing": "pairwise"}`))
	var wire struct {
		Warnings []map[string]json.RawMessage `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(mustJSON(t, res)), &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire.Warnings) == 0 {
		t.Fatal("no warnings on the wire")
	}
	for _, w := range wire.Warnings {
		if len(w) != 3 || w["code"] == nil || w["message"] == nil || w["details"] == nil {
			t.Errorf("warning keys = %v, want code/message/details", w)
		}
	}
	if _, ok := errors.ParseCode(strings.Trim(string(wire.Warnings[0]["code"]), `"`)); !ok {
		t.Errorf("warning code %s is not a registered code", wire.Warnings[0]["code"])
	}
}
