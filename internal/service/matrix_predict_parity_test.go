package service

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// pairCovNotPSDRows: x1 and x2 agree on the two rows they share
// (pair covariance 50) but sit at their mean on every other row they
// own, so each variance (≈ 7.1) is far below the pair covariance — a
// pairwise 2 × 2 covariance that is not PSD. The 2 × 2 correlation
// over the same rows is r = 1 on the shared rows: PSD.
func pairCovNotPSDRows() [][5]float64 {
	nan := math.NaN()
	rows := [][5]float64{{0, 0, 1, 1, 1}, {10, 10, 2, 1, 1}}
	for i := 0; i < 6; i++ {
		rows = append(rows, [5]float64{5, nan, 3, 1, 1}, [5]float64{nan, 5, 4, 1, 1})
	}
	return rows
}

// TestMatrixPredict_MatchesRuntime: for every operator × missing mode ×
// member set over fixtures that do and do not trip
// PULSE_MATRIX_NOT_PSD, predict's per-matrix report agrees with the
// run — shape and axis are the result's, and a runtime NOT_PSD warning
// only ever appears where predict said pairwise_psd_risk (the one
// shared rule, vectors.Matrix.PSDRisk). Each fixture's NOT_PSD shapes
// are asserted to really fire, so the parity is not vacuous.
func TestMatrixPredict_MatchesRuntime(t *testing.T) {
	fixtures := []struct {
		name  string
		rows  [][5]float64
		fires map[string]bool // "<type>/<p>" pairwise shapes that must warn
	}{
		{"three-way", nonPSDRows(), map[string]bool{"MAT_COVARIANCE/3": true, "MAT_CORRELATION/3": true, "MAT_PARTIAL_CORRELATION/3": true}},
		{"pair-cov", pairCovNotPSDRows(), map[string]bool{"MAT_COVARIANCE/2": true}},
		{"reference", matrixRows(500), nil},
	}
	for _, fx := range fixtures {
		cfg := writeMatrixCohort(t, "p.pulse", fx.rows)
		data, err := afero.ReadFile(cfg.Fs(), "p.pulse")
		if err != nil {
			t.Fatal(err)
		}
		for _, typ := range types.AllMatrixTypes() {
			for _, mode := range []string{"listwise", "pairwise"} {
				for _, members := range [][]string{{"x1", "x2", "x3"}, {"x1", "x2"}} {
					label := fmt.Sprintf("%s/%s/%s/%d", fx.name, typ, mode, len(members))
					params := `{"missing": "` + mode + `"}`
					if vectors.IsDecomposition(typ) {
						// A decomposition operator refuses a non-PSD
						// input unrepaired; repaired, it warns.
						params = `{"missing": "` + mode + `", "repair": "nearest"}`
					}
					spec := types.MatrixSpec{Name: "m", Type: typ, Vector: "v", Params: json.RawMessage(params)}
					req := &types.Request{
						Cohort:   &types.Cohort{Filename: "p.pulse"},
						Vectors:  []types.VectorSpec{{Name: "v", Fields: members}},
						Matrices: []types.MatrixSpec{spec},
					}
					env := descx.Predict(bytes.NewReader(data), req, &descx.PredictOptions{})
					if len(env.Errors) != 0 {
						t.Fatalf("%s: predict errors %v", label, env.Errors)
					}
					pr := env.Data.(*descriptor.PredictResult)
					if len(pr.Matrices) != 1 {
						t.Fatalf("%s: predict reports %d matrices, want 1", label, len(pr.Matrices))
					}
					mp := pr.Matrices[0]
					resp, err := New(cfg).Process(context.Background(), req)
					if err != nil {
						// A decomposition of a singular input (perfectly
						// collinear members on these small fixtures)
						// refuses; that is not a PSD-risk question.
						var ce *errors.CodedError
						if vectors.IsDecomposition(typ) && stderrors.As(err, &ce) && ce.Code == errors.PULSE_MATRIX_SINGULAR {
							continue
						}
						t.Fatalf("%s: Process: %v", label, err)
					}
					res := resp.Matrices[0]

					if mp.Name != res.Name || mp.Type != res.Type {
						t.Errorf("%s: predict %s/%s, runtime %s/%s", label, mp.Name, mp.Type, res.Name, res.Type)
					}
					if got := [2]int{len(res.Primary.RowKeys), len(res.Primary.ColumnKeys)}; got != mp.Shape {
						t.Errorf("%s: predict shape %v, runtime %v", label, mp.Shape, got)
					}
					if !reflect.DeepEqual(mp.AxisKeys, res.Primary.RowKeys) || !reflect.DeepEqual(res.Primary.RowKeys, res.Primary.ColumnKeys) {
						t.Errorf("%s: predict axis %v, runtime rows %v cols %v", label, mp.AxisKeys, res.Primary.RowKeys, res.Primary.ColumnKeys)
					}
					if mp.Missing != mode || (res.Auxiliary["n"] != nil) != (mode == "pairwise") {
						t.Errorf("%s: predict missing %q, runtime auxiliary.n present %v", label, mp.Missing, res.Auxiliary["n"] != nil)
					}
					if mp.Encoding != res.Primary.Encoding {
						t.Errorf("%s: predict encoding %q, runtime %q", label, mp.Encoding, res.Primary.Encoding)
					}
					warned := findWarning(res, errors.PULSE_MATRIX_NOT_PSD) != nil
					if warned && !mp.PairwisePSDRisk {
						t.Errorf("%s: runtime warns PULSE_MATRIX_NOT_PSD but predict says no pairwise_psd_risk", label)
					}
					key := fmt.Sprintf("%s/%d", typ, len(members))
					if mode == "pairwise" && fx.fires[key] {
						if !warned {
							t.Errorf("%s: fixture must trip PULSE_MATRIX_NOT_PSD (warnings %v)", label, matWarningCodes(res))
						}
						if !mp.PairwisePSDRisk {
							t.Errorf("%s: a non-PSD shape without pairwise_psd_risk", label)
						}
					}
				}
			}
		}
	}
}

// TestMatrixPredict_BucketEstimateMatchesRuntime: predict's grouped
// estimate (vectors.EstimateBuckets) agrees with the run — exactly
// when every reachable key carries rows, and as an upper bound when a
// filter (or the data) leaves keys empty; estimated_cells and
// estimated_bytes scale with it, and a data-dependent grouper reports
// "unknown" with no figures.
func TestMatrixPredict_BucketEstimateMatchesRuntime(t *testing.T) {
	cfg := groupedMatrixCohort(t, 2*linalg.MergeBlockSize+31)
	data, err := afero.ReadFile(cfg.Fs(), "g.pulse")
	if err != nil {
		t.Fatal(err)
	}
	onlyAB := []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "region", Values: []string{"a", "b"}}}
	cases := []struct {
		name    string
		group   *types.Group
		filters []*types.Filterer
		basis   string
		exact   bool // every estimated key carries rows
	}{
		{"category", &types.Group{Type: types.GROUP_CATEGORY, Field: "region"}, nil, "dictionary", true},
		{"include", &types.Group{Type: types.GROUP_CATEGORY, Field: "region", Include: []string{"c", "zz", "a"}}, nil, "include", true},
		{"per-element", &types.Group{Type: types.GROUP_SET_PER_ELEMENT, Field: "tags"}, nil, "dictionary", true},
		{"filtered category is an upper bound", &types.Group{Type: types.GROUP_CATEGORY, Field: "region"}, onlyAB, "dictionary", false},
		{"range", &types.Group{Type: types.GROUP_RANGE, Field: "x3", Interval: 50}, nil, "unknown", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := groupedMatrixRequest(c.group)
			req.Filterers = c.filters
			env := descx.Predict(bytes.NewReader(data), req, &descx.PredictOptions{})
			if len(env.Errors) != 0 {
				t.Fatalf("predict errors %v", env.Errors)
			}
			pr := env.Data.(*descriptor.PredictResult)
			resp := processMatrices2(t, cfg, req)
			if len(pr.Matrices) != len(req.Matrices) {
				t.Fatalf("predict reports %d matrices for %d specs", len(pr.Matrices), len(req.Matrices))
			}
			perSpec := int64(len(resp.Matrices) / len(req.Matrices))
			if perSpec != int64(len(resp.Data)) || perSpec == 0 {
				t.Fatalf("runtime %d matrices over %d buckets", len(resp.Matrices), len(resp.Data))
			}
			for i, mp := range pr.Matrices {
				if mp.BucketBasis != c.basis {
					t.Errorf("%s: bucket_basis %q, want %q", mp.Name, mp.BucketBasis, c.basis)
				}
				if c.basis == "unknown" {
					if mp.EstimatedBuckets != nil {
						t.Errorf("%s: unknown basis estimates %d buckets", mp.Name, *mp.EstimatedBuckets)
					}
					continue
				}
				if mp.EstimatedBuckets == nil {
					t.Fatalf("%s: no estimated_buckets", mp.Name)
				}
				est := *mp.EstimatedBuckets
				if perSpec > est || (c.exact && perSpec != est) || (!c.exact && perSpec == est) {
					t.Errorf("%s: predict estimates %d buckets, runtime emits %d (exact %v)", mp.Name, est, perSpec, c.exact)
				}
				p := int64(len(resp.Matrices[i*int(perSpec)].Primary.RowKeys))
				// The run holds one CoMoment per populated merge block per
				// bucket: the cohort spans three blocks.
				const blocks = 3
				if *mp.EstimatedCells != est*p*p || *mp.EstimatedBytes != blocks*est*mp.AccumulatorBytes {
					t.Errorf("%s: cells %d / bytes %d, want %d / %d", mp.Name, *mp.EstimatedCells, *mp.EstimatedBytes, est*p*p, blocks*est*mp.AccumulatorBytes)
				}
			}
		})
	}
}
