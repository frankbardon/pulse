package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// MAT_COVARIANCE end to end through the service: the pinned
// numpy / statsmodels oracle (matrix_reference_values_test.go), the
// streaming path against the buffered one bit for bit, the diagonal
// against the variance aggregators, and the encoding layouts.

// matrixSchema: three nullable members, a frequency weight f and a
// probability weight p.
func matrixSchema() *encoding.Schema {
	names := []string{"x1", "x2", "x3", "f", "p"}
	fields := make([]encoding.Field, len(names))
	for i, n := range names {
		fields[i] = encoding.Field{Name: n, Type: encoding.FieldTypeF64, ByteOffset: 8 * i, CsvColumnIdx: i, Nullable: true}
	}
	return &encoding.Schema{Fields: fields}
}

// writeMatrixCohort writes rows (a NaN cell is null) to name on a
// hermetic filesystem.
func writeMatrixCohort(t *testing.T, name string, rows [][5]float64) *fs.Config {
	t.Helper()
	recs := make([][]uint64, len(rows))
	for i, r := range rows {
		recs[i] = make([]uint64, 5)
		for j, v := range r {
			if !math.IsNaN(v) {
				recs[i][j] = math.Float64bits(v)
			}
		}
	}
	data := writeNullablePulse(t, matrixSchema(), recs, func(r, f int) bool { return math.IsNaN(rows[r][f]) })
	cfg := fs.NewMemMap()
	if err := afero.WriteFile(cfg.Fs(), name, data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return cfg
}

// matrixRows generates n rows of one deterministic sequence: members
// with mixed magnitudes (so a different fold order shows in the low
// bits), nulls on x1 / x2, integer frequency weights and fractional
// probability weights, both with zero rows.
func matrixRows(n int) [][5]float64 {
	rows := make([][5]float64, n)
	for i := range rows {
		h := uint64(i)*0x9E3779B97F4A7C15 + 0x632BE59BD9B4E019
		u := func(k uint64) float64 {
			v := (h ^ (h >> 29) ^ k*0xBF58476D1CE4E5B9) * 0x94D049BB133111EB
			return float64(v>>11) / (1 << 53)
		}
		x1 := 1e4 + 37*u(1)
		x2 := 0.5*x1 + 1e-3*u(2) - 3e3
		x3 := u(3) * 1e6
		f := math.Floor(4 * u(4))
		p := 0.1 + 3*u(5)
		if i%23 == 7 {
			p = 0
		}
		if i%7 == 0 {
			x1 = math.NaN()
		}
		if i%11 == 3 {
			x2 = math.NaN()
		}
		rows[i] = [5]float64{x1, x2, x3, f, p}
	}
	return rows
}

func covSpec(name, vector string, ddof int, weight string, enc types.MatrixEncoding) types.MatrixSpec {
	s := types.MatrixSpec{
		Name:     name,
		Type:     types.MAT_COVARIANCE,
		Vector:   vector,
		Params:   json.RawMessage(fmt.Sprintf(`{"ddof": %d}`, ddof)),
		Encoding: enc,
	}
	switch weight {
	case "f":
		s.Weight = types.SlotWeightOf(types.WeightSpec{Field: "f", Kind: types.WeightKindFrequency})
	case "p":
		s.Weight = types.SlotWeightOf(types.WeightSpec{Field: "p", Kind: types.WeightKindProbability})
	}
	return s
}

func processMatrices(t *testing.T, cfg *fs.Config, req *types.Request) *types.Response {
	t.Helper()
	resp, err := New(cfg).Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(resp.Matrices) != len(req.Matrices) {
		t.Fatalf("Process: %d matrices, want %d", len(resp.Matrices), len(req.Matrices))
	}
	return resp
}

func closeTo(got, want, rel float64) bool {
	if math.IsNaN(got) || math.IsNaN(want) {
		return math.IsNaN(got) && math.IsNaN(want)
	}
	return math.Abs(got-want) <= rel+rel*math.Abs(want)
}

// TestMatrixCovariance_MatchesReference pins MAT_COVARIANCE against the
// numpy.cov / statsmodels DescrStatsW oracle: unweighted, frequency and
// probability weights, ddof 0 and 1, listwise over null cells, a
// zero-weight row kept.
func TestMatrixCovariance_MatchesReference(t *testing.T) {
	cfg := writeMatrixCohort(t, "ref.pulse", matrixRefRows)
	for _, c := range matrixRefCases {
		t.Run(fmt.Sprintf("weight=%q/ddof=%d", c.weight, c.ddof), func(t *testing.T) {
			req := &types.Request{
				Cohort:   &types.Cohort{Filename: "ref.pulse"},
				Vectors:  []types.VectorSpec{{Name: "x", Fields: []string{"x1", "x2", "x3"}}},
				Matrices: []types.MatrixSpec{covSpec("cov", "x", c.ddof, c.weight, "")},
			}
			res := processMatrices(t, cfg, req).Matrices[0]
			if res.Name != "cov" || res.Type != types.MAT_COVARIANCE {
				t.Fatalf("result identity = %q %s", res.Name, res.Type)
			}
			pr := res.Primary
			if pr.Kind != types.MatrixKindSquareSymmetric || pr.Encoding != types.MatrixEncodingFull {
				t.Fatalf("primary kind/encoding = %s/%s", pr.Kind, pr.Encoding)
			}
			for r := 0; r < 3; r++ {
				for col := 0; col < 3; col++ {
					if !closeTo(pr.Values[r][col], c.cov[r][col], 1e-12) {
						t.Errorf("cov[%d][%d] = %.17g, want %.17g", r, col, pr.Values[r][col], c.cov[r][col])
					}
				}
			}
			if det := res.Scalars["determinant"]; !closeTo(det, c.det, 1e-9) {
				t.Errorf("determinant = %.17g, want %.17g", det, c.det)
			}
		})
	}
}

// TestMatrixCovariance_StreamingEqualsBuffered: the same matrices from
// the streaming path (matrix-only request) and the buffered path (an
// AGG_MEDIAN forces it) are bit-identical, over a cohort spanning three
// merge blocks, every weight kind and ddof, both encodings.
func TestMatrixCovariance_StreamingEqualsBuffered(t *testing.T) {
	cfg := writeMatrixCohort(t, "big.pulse", matrixRows(2*4096+1234))
	schema := matrixSchema()
	var specs []types.MatrixSpec
	for _, w := range []string{"", "f", "p"} {
		for _, ddof := range []int{0, 1} {
			for _, enc := range []types.MatrixEncoding{"", types.MatrixEncodingUpper} {
				specs = append(specs, covSpec(fmt.Sprintf("c_%s_%d_%s", w, ddof, enc), "x", ddof, w, enc))
			}
		}
	}
	base := func() *types.Request {
		return &types.Request{
			Cohort:   &types.Cohort{Filename: "big.pulse"},
			Vectors:  []types.VectorSpec{{Name: "x", Fields: []string{"x1", "x2", "x3"}}},
			Matrices: specs,
		}
	}
	streamReq := base()
	bufReq := base()
	bufReq.Aggregations = []*types.Aggregation{{Type: types.AGG_MEDIAN, Field: "x3", Label: "med"}}
	if !processing.CanStreamRequest(streamReq, schema) {
		t.Fatal("matrix-only request does not stream")
	}
	if processing.CanStreamRequest(bufReq, schema) {
		t.Fatal("AGG_MEDIAN request streams; the buffered arm is not exercised")
	}
	streamed := processMatrices(t, cfg, streamReq).Matrices
	buffered := processMatrices(t, cfg, bufReq).Matrices
	for i := range specs {
		a, b := streamed[i], buffered[i]
		if a.Name != specs[i].Name {
			t.Fatalf("result %d name %q, want %q (spec order)", i, a.Name, specs[i].Name)
		}
		if math.Float64bits(a.Scalars["determinant"]) != math.Float64bits(b.Scalars["determinant"]) {
			t.Errorf("%s determinant: streamed %x buffered %x", a.Name,
				math.Float64bits(a.Scalars["determinant"]), math.Float64bits(b.Scalars["determinant"]))
		}
		for r := range a.Primary.Values {
			if len(a.Primary.Values[r]) != len(b.Primary.Values[r]) {
				t.Fatalf("%s row %d length differs", a.Name, r)
			}
			for c := range a.Primary.Values[r] {
				if math.Float64bits(a.Primary.Values[r][c]) != math.Float64bits(b.Primary.Values[r][c]) {
					t.Errorf("%s [%d][%d]: streamed %.17g buffered %.17g", a.Name, r, c, a.Primary.Values[r][c], b.Primary.Values[r][c])
				}
			}
		}
	}
	// The upper encoding is exactly the full matrix's upper triangle.
	for i := 0; i+1 < len(streamed); i += 2 {
		full, upper := streamed[i].Primary, streamed[i+1].Primary
		if upper.Encoding != types.MatrixEncodingUpper || full.Encoding != types.MatrixEncodingFull {
			t.Fatalf("encodings %s/%s", full.Encoding, upper.Encoding)
		}
		for r := range full.Values {
			if len(upper.Values[r]) != len(full.Values)-r {
				t.Fatalf("upper row %d has %d cells, want %d", r, len(upper.Values[r]), len(full.Values)-r)
			}
			for k, v := range upper.Values[r] {
				if math.Float64bits(v) != math.Float64bits(full.Values[r][r+k]) {
					t.Errorf("upper[%d][%d] != full[%d][%d]", r, k, r, r+k)
				}
				if math.Float64bits(full.Values[r][r+k]) != math.Float64bits(full.Values[r+k][r]) {
					t.Errorf("full matrix not symmetric at (%d,%d)", r, r+k)
				}
			}
		}
	}
}

// TestMatrixCovariance_DiagonalMatchesVarianceAggregators: a one-member
// covariance is that member's variance over the same rows — ddof 1 is
// AGG_WELFORD's sample variance, ddof 0 is AGG_VARIANCE — unweighted
// and under both weight kinds. The two recurrences differ in operation
// order, so agreement is to 1e-12 relative. Zero-weight rows are
// present: they add no mass on either side (the co-moment counts them
// toward n, the weighted aggregators skip them).
func TestMatrixCovariance_DiagonalMatchesVarianceAggregators(t *testing.T) {
	cfg := writeMatrixCohort(t, "diag.pulse", matrixRows(5000))
	for _, w := range []string{"", "f", "p"} {
		for _, field := range []string{"x1", "x2", "x3"} {
			t.Run(fmt.Sprintf("weight=%q/%s", w, field), func(t *testing.T) {
				var slotW types.SlotWeight
				if w != "" {
					slotW = covSpec("", "", 0, w, "").Weight
				}
				m0 := covSpec("d0", "", 0, w, "")
				m0.Fields = []string{field}
				m1 := covSpec("d1", "", 1, w, "")
				m1.Fields = []string{field}
				req := &types.Request{
					Cohort: &types.Cohort{Filename: "diag.pulse"},
					Aggregations: []*types.Aggregation{
						{Type: types.AGG_VARIANCE, Field: field, Label: "var", Weight: slotW},
						{Type: types.AGG_WELFORD, Field: field, Label: "wel", Weight: slotW},
					},
					Matrices: []types.MatrixSpec{m0, m1},
				}
				resp := processMatrices(t, cfg, req)
				variance, ok := resp.Data[0]["var"].(float64)
				if !ok {
					t.Fatalf("var = %T", resp.Data[0]["var"])
				}
				wel, ok := resp.Data[0]["wel"].(processing.WelfordTriple)
				if !ok {
					t.Fatalf("wel = %T", resp.Data[0]["wel"])
				}
				if got := resp.Matrices[0].Primary.Values[0][0]; !closeTo(got, variance, 1e-12) {
					t.Errorf("ddof 0 diagonal %.17g, AGG_VARIANCE %.17g", got, variance)
				}
				if got := resp.Matrices[1].Primary.Values[0][0]; !closeTo(got, wel.Variance, 1e-12) {
					t.Errorf("ddof 1 diagonal %.17g, AGG_WELFORD variance %.17g", got, wel.Variance)
				}
			})
		}
	}
}

// TestMatrixCovariance_UndefinedIsNullOnTheWire: with fewer rows than
// ddof allows, every cell and the determinant are NaN in Go and null on
// the wire, keys kept.
func TestMatrixCovariance_UndefinedIsNullOnTheWire(t *testing.T) {
	nan := math.NaN()
	cfg := writeMatrixCohort(t, "one.pulse", [][5]float64{{1, 2, 3, 1, 1}, {nan, 2, 3, 1, 1}})
	req := &types.Request{
		Cohort:   &types.Cohort{Filename: "one.pulse"},
		Matrices: []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Fields: []string{"x1", "x2"}}},
	}
	res := processMatrices(t, cfg, req).Matrices[0]
	if res.Name != "MAT_COVARIANCE" {
		t.Errorf("default name = %q, want MAT_COVARIANCE", res.Name)
	}
	if !math.IsNaN(res.Primary.Values[0][1]) || !math.IsNaN(res.Scalars["determinant"]) {
		t.Fatalf("one listwise row, ddof 1: want NaN cells, got %v det %v", res.Primary.Values, res.Scalars)
	}
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var back struct {
		Primary struct {
			Values [][]*float64 `json:"values"`
		} `json:"primary"`
		Scalars map[string]*float64 `json:"scalars"`
	}
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("Unmarshal %s: %v", raw, err)
	}
	if back.Primary.Values[0][1] != nil {
		t.Errorf("undefined cell on the wire = %v, want null (%s)", *back.Primary.Values[0][1], raw)
	}
	if d, ok := back.Scalars["determinant"]; !ok || d != nil {
		t.Errorf("determinant on the wire = %v (present %v), want a null key (%s)", d, ok, raw)
	}
}
