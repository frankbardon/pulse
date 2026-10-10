package service

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// matrix_rank_test.go is MAT_CORRELATION params.method "spearman" /
// "kendall" end to end: per-pair parity with TEST_SPEARMAN_R /
// TEST_KENDALL_TAU, the R oracle (cor(method = …)), the frequency-only
// weight class, and serial execution under the parallel knobs.

// rankRows: tie-heavy members (x1, x2 on a few levels, x3 continuous),
// nulls on x1 / x2, integer frequency weights f in 0..3 and fractional
// probability weights p.
func rankRows(n int) [][5]float64 {
	rows := make([][5]float64, n)
	for i := range rows {
		h := uint64(i)*0x9E3779B97F4A7C15 + 0x632BE59BD9B4E019
		u := func(k uint64) float64 {
			v := (h ^ (h >> 29) ^ k*0xBF58476D1CE4E5B9) * 0x94D049BB133111EB
			return float64(v>>11) / (1 << 53)
		}
		x1 := math.Floor(7 * u(1))
		x2 := math.Floor(x1/2 + 3*u(2))
		x3 := 1e3*u(3) - x1*40
		f := math.Floor(4 * u(4))
		p := 0.1 + 3*u(5)
		if i%9 == 4 {
			x1 = math.NaN()
		}
		if i%13 == 6 {
			x2 = math.NaN()
		}
		rows[i] = [5]float64{x1, x2, x3, f, p}
	}
	return rows
}

func rankSpec(name, method, missing, weight string) types.MatrixSpec {
	params := map[string]any{"method": method}
	if missing != "" {
		params["missing"] = missing
	}
	raw, _ := json.Marshal(params)
	s := corrSpec(name, []string{"x1", "x2", "x3"}, weight)
	s.Params = raw
	return s
}

// TestMatrixRankCorrelation_TestParity: every off-diagonal cell of a
// rank matrix is the matching test's statistic over the same rows, BIT
// FOR BIT (one shared kernel, the same rows in the same order): ρ =
// TEST_SPEARMAN_R, τ-b = TEST_KENDALL_TAU, in both field orders,
// unweighted and under a frequency weight with zero-weight rows. Under
// pairwise each cell rests on the pair's own rows — exactly the rows
// the test keeps; under listwise the cohort has no nulls, so the row
// sets agree too.
func TestMatrixRankCorrelation_TestParity(t *testing.T) {
	members := []string{"x1", "x2", "x3"}
	methods := map[string]types.TestType{"spearman": types.TEST_SPEARMAN_R, "kendall": types.TEST_KENDALL_TAU}
	for _, missing := range []string{"listwise", "pairwise"} {
		rows := rankRows(300)
		if missing == "listwise" {
			for i := range rows {
				for j := 0; j < 2; j++ {
					if math.IsNaN(rows[i][j]) {
						rows[i][j] = float64(i % 5)
					}
				}
			}
		}
		cfg := writeMatrixCohort(t, "rank.pulse", rows)
		for method, testType := range methods {
			for _, w := range []string{"", "f"} {
				t.Run(fmt.Sprintf("%s/%s/weight=%q", missing, method, w), func(t *testing.T) {
					var tests []*types.Test
					for i, a := range members {
						for j, b := range members {
							if i != j {
								tests = append(tests, &types.Test{Type: testType, Field: a, Field2: b, Label: a + "~" + b,
									Weight: covSpec("", "", 0, w, "").Weight})
							}
						}
					}
					req := &types.Request{
						Cohort:   &types.Cohort{Filename: "rank.pulse"},
						Matrices: []types.MatrixSpec{rankSpec("r", method, missing, w)},
						Tests:    tests,
					}
					resp := processMatrices(t, cfg, req)
					if len(resp.Tests) != len(tests) {
						t.Fatalf("%d test results, want %d", len(resp.Tests), len(tests))
					}
					res := resp.Matrices[0]
					if res.Auxiliary["p"] != nil {
						t.Errorf("rank method emitted auxiliary.p")
					}
					vals := res.Primary.Values
					k := 0
					for i := range members {
						for j := range members {
							if i == j {
								if vals[i][j] != 1 {
									t.Errorf("diagonal [%d][%d] = %v, want exactly 1", i, j, vals[i][j])
								}
								continue
							}
							ref := resp.Tests[k].Statistic
							k++
							if got := vals[i][j]; math.Float64bits(got) != math.Float64bits(ref) {
								t.Errorf("[%d][%d] = %.17g, %s(%s, %s) = %.17g: want the same bits", i, j, got, testType, members[i], members[j], ref)
							}
						}
					}
				})
			}
		}
	}
}

// TestMatrixRankCorrelation_DiffersFromPearson guards against a rank
// method silently running the Pearson co-moment path.
func TestMatrixRankCorrelation_DiffersFromPearson(t *testing.T) {
	cfg := writeMatrixCohort(t, "rank.pulse", rankRows(200))
	req := &types.Request{
		Cohort: &types.Cohort{Filename: "rank.pulse"},
		Matrices: []types.MatrixSpec{
			rankSpec("pearson", "pearson", "", ""), rankSpec("spearman", "spearman", "", ""), rankSpec("kendall", "kendall", "", ""),
		},
	}
	ms := processMatrices(t, cfg, req).Matrices
	p, s, k := ms[0].Primary.Values[0][2], ms[1].Primary.Values[0][2], ms[2].Primary.Values[0][2]
	if p == s || s == k || p == k {
		t.Fatalf("x1~x3: pearson %v, spearman %v, kendall %v — want three different figures", p, s, k)
	}
}

// TestMatrixRankCorrelation_ZeroSpreadIsNull: a constant member's row
// and column, diagonal included, are NaN under a rank method too, and
// top_pairs skips them.
func TestMatrixRankCorrelation_ZeroSpreadIsNull(t *testing.T) {
	rows := rankRows(80)
	for i := range rows {
		rows[i][1] = 3 // x2 constant (nulls overwritten)
	}
	cfg := writeMatrixCohort(t, "flat.pulse", rows)
	for _, method := range []string{"spearman", "kendall"} {
		for _, missing := range []string{"listwise", "pairwise"} {
			spec := rankSpec("r", method, missing, "")
			spec.Params = json.RawMessage(`{"method": "` + method + `", "missing": "` + missing + `", "summary": {"top_pairs": 5}}`)
			res := processMatrices(t, cfg, &types.Request{Cohort: &types.Cohort{Filename: "flat.pulse"}, Matrices: []types.MatrixSpec{spec}}).Matrices[0]
			v := res.Primary.Values
			for r := 0; r < 3; r++ {
				for c := 0; c < 3; c++ {
					if touches := r == 1 || c == 1; touches != math.IsNaN(v[r][c]) {
						t.Errorf("%s/%s [%d][%d] = %v; zero-spread member x2 is axis 1", method, missing, r, c, v[r][c])
					}
				}
			}
			pairs, _ := res.Vectors["top_pairs"].([]types.MatrixPair)
			if len(pairs) != 1 || pairs[0].Row != "x1" || pairs[0].Col != "x3" || pairs[0].R != v[0][2] {
				t.Errorf("%s/%s top_pairs = %+v, want only x1~x3 = %v", method, missing, pairs, v[0][2])
			}
			if findWarning(res, errors.PULSE_MATRIX_ZERO_VARIANCE) == nil {
				t.Errorf("%s/%s: no PULSE_MATRIX_ZERO_VARIANCE (warnings %v)", method, missing, matWarningCodes(res))
			}
		}
	}
}

// TestMatrixRankCorrelation_ProbabilityWeightRefused: a rank method is
// frequency-only — a probability weight from the slot or the request
// is PULSE_WEIGHT_UNSUPPORTED with supported_kinds [frequency] and the
// method named; Pearson keeps both kinds; a slot opting out runs.
func TestMatrixRankCorrelation_ProbabilityWeightRefused(t *testing.T) {
	cfg := writeMatrixCohort(t, "rank.pulse", rankRows(60))
	probability := types.WeightSpec{Field: "p", Kind: types.WeightKindProbability}
	for _, method := range []string{"spearman", "kendall"} {
		for _, viaRequest := range []bool{false, true} {
			req := &types.Request{Cohort: &types.Cohort{Filename: "rank.pulse"}, Matrices: []types.MatrixSpec{rankSpec("r", method, "", "p")}}
			if viaRequest {
				req.Matrices[0].Weight = types.SlotWeight{}
				req.Weight = &probability
			}
			_, err := New(cfg).Process(context.Background(), req)
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_WEIGHT_UNSUPPORTED {
				t.Fatalf("%s (request weight %v): err = %v, want PULSE_WEIGHT_UNSUPPORTED", method, viaRequest, err)
			}
			if ce.Details["method"] != method || ce.Details["slot"] != "matrices[0]" || !strings.Contains(ce.Message, method) {
				t.Errorf("%s: refusal details %v / message %q, want slot matrices[0] and the method", method, ce.Details, ce.Message)
			}
			if k, _ := ce.Details["supported_kinds"].([]string); len(k) != 1 || k[0] != "frequency" {
				t.Errorf("%s: supported_kinds = %v, want [frequency]", method, ce.Details["supported_kinds"])
			}
		}
		// Opted out on the slot under a probability request weight: runs unweighted.
		req := &types.Request{Cohort: &types.Cohort{Filename: "rank.pulse"}, Weight: &probability,
			Matrices: []types.MatrixSpec{rankSpec("r", method, "", "")}}
		req.Matrices[0].Weight = types.NullSlotWeight()
		processMatrices(t, cfg, req)
	}
	processMatrices(t, cfg, &types.Request{Cohort: &types.Cohort{Filename: "rank.pulse"}, Matrices: []types.MatrixSpec{rankSpec("r", "pearson", "", "p")}})
}

// TestMatrixRankCorrelation_SerialUnderParallelKnobs: a rank method is
// not mergeable, so the merge gate refuses the request and DecodeWorkers
// / ShardWorkers do not fan it out: the result is the serial one bit
// for bit, on a single file above the parallel-decode threshold and on
// a shard archive of the same rows.
func TestMatrixRankCorrelation_SerialUnderParallelKnobs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the 100K-record rank-matrix parallel-knob gate in -short mode")
	}
	schema := coMomentSchema()
	dir := t.TempDir()
	osFs := afero.NewOsFs()
	cfg, err := fs.New(fs.WithFs(osFs), fs.WithDataDir(dir))
	if err != nil {
		t.Fatalf("fs.New: %v", err)
	}
	n := parallelDecodeRecordThreshold + 1234
	recs, nullAt := coMomentRows(n, 0)
	single := filepath.Join(dir, "single.pulse")
	if err := afero.WriteFile(osFs, single, writeNullablePulse(t, schema, recs, nullAt), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	archive := filepath.Join(dir, "archive.pulse")
	if err := afero.WriteFile(osFs, archive, coMomentArchive(t, schema, []int{40_000, 1_234, 60_000}), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	run := func(path, method, missing string, decode, shard int) []uint64 {
		t.Helper()
		spec := types.MatrixSpec{Name: "r", Type: types.MAT_CORRELATION, Fields: []string{"x1", "x2", "x3"},
			Params: json.RawMessage(`{"method": "` + method + `", "missing": "` + missing + `"}`)}
		req := &types.Request{Cohort: &types.Cohort{Filename: path}, Matrices: []types.MatrixSpec{spec}}
		if processing.CanMergeRequest(req, schema) {
			t.Fatalf("%s: the merge gate admits a rank-method request", method)
		}
		svc := New(cfg)
		svc.SetDecodeWorkers(decode)
		svc.SetShardWorkers(shard)
		resp, err := svc.Process(context.Background(), req)
		if err != nil {
			t.Fatalf("Process(decode=%d, shard=%d): %v", decode, shard, err)
		}
		var words []uint64
		for _, row := range resp.Matrices[0].Primary.Values {
			for _, v := range row {
				words = append(words, math.Float64bits(v))
			}
		}
		return words
	}
	for _, method := range []string{"spearman", "kendall"} {
		for _, missing := range []string{"listwise", "pairwise"} {
			label := method + "/" + missing
			serial := run(single, method, missing, 1, 0)
			if got := run(single, method, missing, 8, 0); firstWordDiff(got, serial) != -1 {
				t.Errorf("%s: DecodeWorkers=8 differs from serial", label)
			}
			archSerial := run(archive, method, missing, 0, 1)
			if got := run(archive, method, missing, 0, 4); firstWordDiff(got, archSerial) != -1 {
				t.Errorf("%s: ShardWorkers=4 differs from serial", label)
			}
		}
	}
}

// rankOracle is one mv_rank_correlation.json case (R cor()).
type rankOracle struct {
	Fixture  string       `json:"fixture"`
	Fields   []string     `json:"fields"`
	Missing  string       `json:"missing"`
	Weight   string       `json:"weight"`
	PairN    [][]float64  `json:"pair_n"`
	Spearman [][]*float64 `json:"spearman"`
	Kendall  [][]*float64 `json:"kendall_tau_b"`
}

// TestMatrixRankCorrelation_MatchesROracle pins both rank methods to R
// cor(X, method = "spearman" | "kendall", use = "complete.obs" |
// "pairwise.complete.obs") on the committed fixtures
// (internal/processing/testdata/reference, scripts/reference/
// gen_multivariate.R): unweighted, and under the w_freq frequency weight
// against R on the rep()-expanded rows (zero-weight rows included in the
// fixture). Unweighted pairwise also pins auxiliary.n to R's pair n.
func TestMatrixRankCorrelation_MatchesROracle(t *testing.T) {
	refDir, err := filepath.Abs("../processing/testdata/reference")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(refDir, "mv_rank_correlation.json"))
	if err != nil {
		t.Fatal(err)
	}
	if i := strings.LastIndex(string(raw), "\n// golden-hash:"); i >= 0 {
		raw = raw[:i]
	}
	var golden struct {
		Cases []rankOracle `json:"cases"`
	}
	if err := json.Unmarshal(raw, &golden); err != nil {
		t.Fatalf("decode oracle: %v", err)
	}
	if len(golden.Cases) == 0 {
		t.Fatal("oracle has no cases")
	}
	fixtures := filepath.Join(refDir, "fixtures")
	cfg, err := fs.New(fs.WithFs(afero.NewOsFs()), fs.WithDataDir(fixtures))
	if err != nil {
		t.Fatalf("fs.New: %v", err)
	}
	for _, c := range golden.Cases {
		t.Run(fmt.Sprintf("%s/%s/%s", c.Fixture, c.Missing, c.Weight), func(t *testing.T) {
			var specs []types.MatrixSpec
			for _, method := range []string{"spearman", "kendall"} {
				s := types.MatrixSpec{Name: method, Type: types.MAT_CORRELATION, Fields: c.Fields,
					Params: json.RawMessage(`{"method": "` + method + `", "missing": "` + c.Missing + `"}`)}
				if c.Weight == "frequency" {
					s.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w_freq", Kind: types.WeightKindFrequency})
				}
				specs = append(specs, s)
			}
			resp := processMatrices(t, cfg, &types.Request{
				Cohort:   &types.Cohort{Filename: filepath.Join(fixtures, c.Fixture+".pulse")},
				Matrices: specs,
			})
			for k, want := range [][][]*float64{c.Spearman, c.Kendall} {
				got := resp.Matrices[k].Primary.Values
				for i := range want {
					for j := range want[i] {
						g := got[i][j]
						if want[i][j] == nil {
							if !math.IsNaN(g) {
								t.Errorf("%s [%d][%d] = %v, R gives NA", specs[k].Name, i, j, g)
							}
							continue
						}
						if w := *want[i][j]; !(math.Abs(g-w) <= 1e-12+1e-12*math.Abs(w)) {
							t.Errorf("%s [%d][%d] = %.17g, R = %.17g", specs[k].Name, i, j, g, w)
						}
					}
				}
				if c.Missing == "pairwise" && c.Weight == "none" {
					n := resp.Matrices[k].Auxiliary["n"].Values
					for i := range c.PairN {
						for j := range c.PairN[i] {
							if n[i][j] != c.PairN[i][j] {
								t.Errorf("%s auxiliary.n[%d][%d] = %v, R pair n = %v", specs[k].Name, i, j, n[i][j], c.PairN[i][j])
							}
						}
					}
				}
			}
		})
	}
}
