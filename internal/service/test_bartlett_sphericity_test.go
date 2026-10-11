package service

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"testing"

	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
)

// test_bartlett_sphericity_test.go holds TEST_BARTLETT_SPHERICITY to
// MAT_PCA's bartlett_* scalars BIT FOR BIT on the same rows — both read
// the one shared core (processing.bartlettSphericity) over the same
// block-folded co-moment — and, through MAT_PCA's mv_pca.json, to
// psych::cortest.bartlett.

// sphericityVsPCA runs one request carrying a MAT_PCA slot and a
// TEST_BARTLETT_SPHERICITY test over the same members and weight, and
// returns the test result and the PCA scalars.
func sphericityVsPCA(t *testing.T, cfg *fs.Config, path string, fields []string, weight *types.WeightSpec) (*types.TestResult, map[string]float64) {
	t.Helper()
	req := &types.Request{
		Cohort:   &types.Cohort{Filename: path},
		Weight:   weight,
		Matrices: []types.MatrixSpec{pcaSpec("pca", fields, map[string]any{"missing": vectors.MissingListwise}, "none")},
		Tests:    []*types.Test{{Type: types.TEST_BARTLETT_SPHERICITY, Fields: append([]string(nil), fields...)}},
	}
	resp, err := New(cfg).Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(resp.Tests) != 1 || len(resp.Matrices) != 1 {
		t.Fatalf("%d tests, %d matrices", len(resp.Tests), len(resp.Matrices))
	}
	return resp.Tests[0], resp.Matrices[0].Scalars
}

func assertBitEqualBartlett(t *testing.T, res *types.TestResult, sc map[string]float64) {
	t.Helper()
	for name, pair := range map[string][2]float64{
		"chisq": {res.Statistic, sc["bartlett_chisq"]},
		"df":    {res.DF, sc["bartlett_df"]},
		"p":     {res.PValue, sc["bartlett_p"]},
	} {
		if math.Float64bits(pair[0]) != math.Float64bits(pair[1]) {
			t.Errorf("%s: test %.17g, MAT_PCA %.17g (bits differ)", name, pair[0], pair[1])
		}
	}
}

// TestBartlettSphericity_EqualsPCABartlett: on every mv_pca.json
// correlation / listwise case (attitude, mtcars, likert ties, the
// weighted and nulls fixtures; unweighted, frequency and probability)
// the test's statistic, df and p equal MAT_PCA's bartlett_* scalars bit
// for bit and psych::cortest.bartlett within the PCA oracle tolerance.
func TestBartlettSphericity_EqualsPCABartlett(t *testing.T) {
	cases, dir := loadPCAOracle(t)
	cfg := fixtureFS(t, dir)
	ran := map[string]int{}
	for _, c := range cases {
		if c.On != vectors.PCAOnCorrelation || c.Missing != vectors.MissingListwise {
			continue
		}
		ran[c.Weight]++
		t.Run(fmt.Sprintf("%s/p%d/%s", c.Fixture, len(c.Fields), c.Weight), func(t *testing.T) {
			var w *types.WeightSpec
			switch c.Weight {
			case "frequency":
				w = &types.WeightSpec{Field: "w_freq", Kind: types.WeightKindFrequency}
			case "probability":
				w = &types.WeightSpec{Field: "w_prob", Kind: types.WeightKindProbability}
			}
			res, sc := sphericityVsPCA(t, cfg, filepath.Join(dir, c.Fixture+".pulse"), c.Fields, w)
			assertBitEqualBartlett(t, res, sc)
			pcaNear(t, "chisq vs R", res.Statistic, c.BartlettChisq)
			pcaNear(t, "df vs R", res.DF, c.BartlettDF)
			pcaNear(t, "p vs R", res.PValue, c.BartlettP)
		})
	}
	if ran["none"] == 0 || ran["frequency"] == 0 || ran["probability"] == 0 {
		t.Fatalf("parity must cover every weight kind, ran %v", ran)
	}
}

// TestBartlettSphericity_EqualsPCABartlettManyBlocks: past one merge
// block (linalg.MergeBlockSize rows), with listwise nulls and zero
// probability weights, the test still folds the matrix slot's blocks
// and so stays bit-equal to MAT_PCA, unweighted and weighted.
func TestBartlettSphericity_EqualsPCABartlettManyBlocks(t *testing.T) {
	cfg := groupedMatrixCohort(t, 2*linalg.MergeBlockSize+31)
	for name, w := range map[string]*types.WeightSpec{
		"none":        nil,
		"probability": {Field: "w", Kind: types.WeightKindProbability},
	} {
		t.Run(name, func(t *testing.T) {
			res, sc := sphericityVsPCA(t, cfg, "g.pulse", []string{"x1", "x2", "x3"}, w)
			if math.IsNaN(res.Statistic) {
				t.Fatalf("statistic is null: %v", res.Warnings)
			}
			assertBitEqualBartlett(t, res, sc)
		})
	}
}
