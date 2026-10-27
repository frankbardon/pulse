package service

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"reflect"
	"strconv"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// matrix_standardize_test.go is VectorSpec.standardize "zscore" end to
// end: a standardized vector equals the same matrices over its members
// z-scored beforehand (population SD over the listwise rows, frequency
// weights respected), MAT_SEM over it reproduces semopy on the
// externally z-scored data, and the result is bit-identical across the
// streamed, buffered, decode-parallel and sharded paths.

// readF64CSV reads every column of a numeric CSV.
func readF64CSV(t *testing.T, path string) ([]string, [][]float64) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	recs, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	rows := make([][]float64, len(recs)-1)
	for r, rec := range recs[1:] {
		rows[r] = make([]float64, len(rec))
		for c, s := range rec {
			if rows[r][c], err = strconv.ParseFloat(s, 64); err != nil {
				t.Fatal(err)
			}
		}
	}
	return recs[0], rows
}

// writeF64Cohort writes rows (f64 columns cols, null where null says)
// as name on cfg.
func writeF64Cohort(t *testing.T, cfg *fs.Config, name string, cols []string, rows [][]float64, null func(r, c int) bool) {
	t.Helper()
	fields := make([]encoding.Field, len(cols))
	for i, n := range cols {
		fields[i] = encoding.Field{Name: n, Type: encoding.FieldTypeF64, ByteOffset: 8 * i, CsvColumnIdx: i, Nullable: true}
	}
	recs := make([][]uint64, len(rows))
	for r, row := range rows {
		recs[r] = make([]uint64, len(row))
		for c, v := range row {
			recs[r][c] = math.Float64bits(v)
		}
	}
	if null == nil {
		null = func(int, int) bool { return false }
	}
	if err := afero.WriteFile(cfg.Fs(), name, writeNullablePulse(t, &encoding.Schema{Fields: fields}, recs, null), 0o644); err != nil {
		t.Fatal(err)
	}
}

// zScoreRows is the external materialization: columns [0, k) z-scored
// with the weighted mean and population SD (divisor Σw) over the rows
// keep admits; every other cell is copied.
func zScoreRows(rows [][]float64, k int, w func(r int) float64, keep func(r int) bool) [][]float64 {
	out := make([][]float64, len(rows))
	for r := range rows {
		out[r] = append([]float64(nil), rows[r]...)
	}
	for c := 0; c < k; c++ {
		var sw, sx float64
		for r, row := range rows {
			if keep(r) {
				sw += w(r)
				sx += w(r) * row[c]
			}
		}
		mean := sx / sw
		var ss float64
		for r, row := range rows {
			if keep(r) {
				d := row[c] - mean
				ss += w(r) * d * d
			}
		}
		sd := math.Sqrt(ss / sw)
		for r := range rows {
			if keep(r) {
				out[r][c] = (rows[r][c] - mean) / sd
			}
		}
	}
	return out
}

func assertValuesClose(t *testing.T, label string, got, want *types.MatrixValues, tol float64) {
	t.Helper()
	if got == nil || want == nil || len(got.Values) != len(want.Values) {
		t.Fatalf("%s: shapes differ (%v vs %v)", label, got, want)
	}
	for r := range want.Values {
		for c, w := range want.Values[r] {
			g := got.Values[r][c]
			if math.IsNaN(w) != math.IsNaN(g) || (!math.IsNaN(w) && semRel(g, w) > tol) {
				t.Errorf("%s [%d][%d] = %.17g, want %.17g", label, r, c, g, w)
			}
		}
	}
}

// TestMatrixStandardize_EqualsMaterializedZScore: over a synthetic
// battery, unweighted and under a frequency weight, a standardized
// vector's MAT_COVARIANCE (ddof 0 and 1) and MAT_SEM equal the same
// operators over the members z-scored beforehand; the ddof-0
// covariance is the raw members' correlation; the components floor is
// the unstandardized run's.
func TestMatrixStandardize_EqualsMaterializedZScore(t *testing.T) {
	t.Parallel()

	cols, rows := readF64CSV(t, semTestdata+"std_data.csv")
	desc, err := os.ReadFile(semTestdata + "std_model.txt")
	if err != nil {
		t.Fatal(err)
	}
	k := len(cols)
	for _, weighted := range []bool{false, true} {
		t.Run(fmt.Sprintf("weighted=%v", weighted), func(t *testing.T) {
			w := func(int) float64 { return 1 }
			if weighted {
				w = func(r int) float64 { return float64(1 + r%3) }
			}
			withW := func(rs [][]float64) [][]float64 {
				out := make([][]float64, len(rs))
				for r, row := range rs {
					out[r] = append(append([]float64(nil), row...), w(r))
				}
				return out
			}
			all := func(int) bool { return true }
			cfg := fs.NewMemMap()
			names := append(append([]string(nil), cols...), "w")
			writeF64Cohort(t, cfg, "raw.pulse", names, withW(rows), nil)
			writeF64Cohort(t, cfg, "z.pulse", names, withW(zScoreRows(rows, k, w, all)), nil)

			semParams, _ := json.Marshal(map[string]any{"model": string(desc)})
			specs := func() []types.MatrixSpec {
				out := []types.MatrixSpec{
					{Name: "cov0", Type: types.MAT_COVARIANCE, Vector: "v", Params: json.RawMessage(`{"ddof": 0}`)},
					{Name: "cov1", Type: types.MAT_COVARIANCE, Vector: "v"},
					{Name: "sem", Type: types.MAT_SEM, Vector: "v", Params: semParams},
					{Name: "r", Type: types.MAT_CORRELATION, Vector: "v"},
				}
				if weighted {
					for i := range out {
						out[i].Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency})
					}
				}
				return out
			}
			run := func(file string, std types.VectorStandardize) *types.Response {
				return processMatrices(t, cfg, &types.Request{
					Cohort:   &types.Cohort{Filename: file},
					Vectors:  []types.VectorSpec{{Name: "v", Fields: cols, Standardize: std}},
					Matrices: specs(),
				})
			}
			std := run("raw.pulse", types.VectorStandardizeZScore)
			mat := run("z.pulse", "")
			raw := run("raw.pulse", "")

			assertValuesClose(t, "cov0", std.Matrices[0].Primary, mat.Matrices[0].Primary, 1e-12)
			assertValuesClose(t, "cov1", std.Matrices[1].Primary, mat.Matrices[1].Primary, 1e-12)
			assertValuesClose(t, "cov0 vs raw correlation", std.Matrices[0].Primary, raw.Matrices[3].Primary, 1e-14)
			assertValuesClose(t, "correlation", std.Matrices[3].Primary, raw.Matrices[3].Primary, 1e-14)

			s, m := std.Matrices[2], mat.Matrices[2]
			if s.Error != nil || m.Error != nil {
				t.Fatalf("sem errors: %v / %v", s.Error, m.Error)
			}
			assertValuesClose(t, "sem implied", s.Primary, m.Primary, 1e-6)
			assertValuesClose(t, "sem estimates", s.Auxiliary["estimates"], m.Auxiliary["estimates"], 1e-6)
			for _, key := range semScalarKeys() {
				if d := semRel(s.Scalars[key], m.Scalars[key]); math.IsNaN(m.Scalars[key]) != math.IsNaN(s.Scalars[key]) || d > 1e-6 {
					t.Errorf("sem %s = %.12g, materialized %.12g", key, s.Scalars[key], m.Scalars[key])
				}
			}
			for i := range specs() {
				if a, b := std.Components.Matrices[i], raw.Components.Matrices[i]; !reflect.DeepEqual(a, b) {
					t.Errorf("components %d: standardized %+v, raw %+v", i, a, b)
				}
			}
		})
	}
}

func semScalarKeys() []string {
	out := make([]string, 0, len(semStatKeys)+2)
	for _, v := range semStatKeys {
		out = append(out, v)
	}
	return append(out, "objective", "n")
}

// TestMatrixStandardize_ListwiseSampleAndZeroSpread: the SD is the
// listwise survivors' (a row with a null member is dropped before the
// z-score, exactly as the materialization drops it), and a zero-spread
// member is left as folded — its covariance row stays the raw run's
// zeros, its correlation null — never a division by zero.
func TestMatrixStandardize_ListwiseSampleAndZeroSpread(t *testing.T) {
	t.Parallel()

	var rows [][]float64
	for i := 0; i < 40; i++ {
		x := float64(i)
		rows = append(rows, []float64{1.5 + math.Mod(x*7, 11), 2.25*math.Mod(x, 5) + 0.5*x, math.Mod(x*13, 17) - 4, 3})
	}
	null := func(r, c int) bool { return c == 0 && r%6 == 1 }
	keep := func(r int) bool { return r%6 != 1 }
	cfg := fs.NewMemMap()
	cols := []string{"a", "b", "c", "k"}
	writeF64Cohort(t, cfg, "raw.pulse", cols, rows, null)
	writeF64Cohort(t, cfg, "z.pulse", cols, zScoreRows(rows, 3, func(int) float64 { return 1 }, keep), null)
	specs := []types.MatrixSpec{
		{Name: "cov0", Type: types.MAT_COVARIANCE, Vector: "v", Params: json.RawMessage(`{"ddof": 0}`)},
		{Name: "pc", Type: types.MAT_PARTIAL_CORRELATION, Fields: []string{"a", "b"}, Params: json.RawMessage(`{"control": ["c"]}`)},
	}
	run := func(file string, members []string, std types.VectorStandardize) *types.Response {
		return processMatrices(t, cfg, &types.Request{
			Cohort:   &types.Cohort{Filename: file},
			Vectors:  []types.VectorSpec{{Name: "v", Fields: members, Standardize: std}},
			Matrices: specs[:1],
		})
	}
	std := run("raw.pulse", []string{"a", "b", "c"}, types.VectorStandardizeZScore)
	mat := run("z.pulse", []string{"a", "b", "c"}, "")
	assertValuesClose(t, "listwise z", std.Matrices[0].Primary, mat.Matrices[0].Primary, 1e-12)
	if c := std.Components.Matrices[0]; c.N != 33 || c.NListwiseDropped != 7 {
		t.Errorf("components n %d dropped %d, want 33 / 7", c.N, c.NListwiseDropped)
	}
	for i := 0; i < 3; i++ {
		if d := std.Matrices[0].Primary.Values[i][i]; semRel(d, 1) > 1e-15 {
			t.Errorf("standardized variance [%d] = %.17g, want 1", i, d)
		}
	}

	// A constant member: the raw run's covariance row (zeros) is kept.
	zs := run("raw.pulse", []string{"a", "k", "b"}, types.VectorStandardizeZScore).Matrices[0].Primary
	rs := run("raw.pulse", []string{"a", "k", "b"}, "").Matrices[0].Primary
	for j := 0; j < 3; j++ {
		if zs.Values[1][j] != rs.Values[1][j] || zs.Values[j][1] != rs.Values[j][1] {
			t.Errorf("zero-spread row/col [%d]: standardized %v / %v, raw %v / %v", j, zs.Values[1][j], zs.Values[j][1], rs.Values[1][j], rs.Values[j][1])
		}
	}
	if semRel(zs.Values[0][0], 1) > 1e-15 || semRel(zs.Values[2][2], 1) > 1e-15 {
		t.Errorf("spread members not standardized beside a constant: %v", zs.Values)
	}

	// A partial correlation over a standardized battery with a control
	// outside it equals the raw one (scale invariant; the control keeps
	// its scale).
	pcStd := processMatrices(t, cfg, &types.Request{
		Cohort:   &types.Cohort{Filename: "raw.pulse"},
		Vectors:  []types.VectorSpec{{Name: "v", Fields: []string{"a", "b"}, Standardize: types.VectorStandardizeZScore}},
		Matrices: []types.MatrixSpec{{Name: "pc", Type: types.MAT_PARTIAL_CORRELATION, Vector: "v", Params: json.RawMessage(`{"control": ["c"]}`)}},
	}).Matrices[0].Primary
	pcRaw := processMatrices(t, cfg, &types.Request{Cohort: &types.Cohort{Filename: "raw.pulse"}, Matrices: specs[1:]}).Matrices[0].Primary
	assertValuesClose(t, "partial correlation", pcStd, pcRaw, 1e-12)
}

// TestMatrixSEM_StandardizedMatchesSemopy: MAT_SEM over a standardized
// vector of the raw synthetic battery reproduces semopy 2.3.11 fitted
// to the same battery z-scored externally (population SD) — the
// default setup its default fit, the converging setup its tight fit.
func TestMatrixSEM_StandardizedMatchesSemopy(t *testing.T) {
	t.Parallel()

	desc, err := os.ReadFile(semTestdata + "std_model.txt")
	if err != nil {
		t.Fatal(err)
	}
	cfg, cols := semCohort(t, semTestdata+"std_data.csv", nil, nil)
	for _, su := range []struct {
		name, ref string
		params    map[string]any
	}{
		{"default", "default", nil},
		{"fisher_rank2", "tight", map[string]any{"solver": "fisher", "information": "rank2"}},
	} {
		t.Run(su.name, func(t *testing.T) {
			var ref semopyFixture
			raw, err := os.ReadFile(semTestdata + "std_semopy_" + su.ref + ".json")
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(raw, &ref); err != nil {
				t.Fatal(err)
			}
			params := map[string]any{"model": string(desc)}
			for k, v := range su.params {
				params[k] = v
			}
			p, _ := json.Marshal(params)
			resp := processMatrices(t, cfg, &types.Request{
				Cohort:   &types.Cohort{Filename: "sem.pulse"},
				Vectors:  []types.VectorSpec{{Name: "items", Fields: cols, Standardize: types.VectorStandardizeZScore}},
				Matrices: []types.MatrixSpec{{Name: "sem", Type: types.MAT_SEM, Vector: "items", Params: p}},
			})
			checkSEMAgainstSemopy(t, resp.Matrices[0], ref, 1e-6)
		})
	}
}

// standardizeInvarianceRequest is a standardized MAT_COVARIANCE
// (probability-weighted, listwise with a heavy-drop warning) or MAT_SEM
// over the co-moment gate's cohort.
func standardizeInvarianceRequest(path string, typ types.MatrixType) *types.Request {
	spec := types.MatrixSpec{Name: "mat", Type: typ, Vector: "z", Params: json.RawMessage(`{"max_drop_share": 0}`),
		Weight: types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindProbability})}
	fields := []string{"x1", "x2", "x3"}
	if typ == types.MAT_SEM {
		fields = []string{"x1", "x3"}
		spec.Params = json.RawMessage(`{"max_drop_share": 0, "model": "x1 ~ x3"}`)
		spec.Weight = types.SlotWeight{}
	}
	return &types.Request{
		Cohort:   &types.Cohort{Filename: path},
		Vectors:  []types.VectorSpec{{Name: "z", Fields: fields, Standardize: types.VectorStandardizeZScore}},
		Matrices: []types.MatrixSpec{spec},
	}
}
