package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"testing"

	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
	"gonum.org/v1/gonum/mat"
)

// matrix_merge_invariance_test.go holds the matrix family's request
// variants (every built-in operator × listwise / pairwise × weighting)
// and its straight-fold anchor; the worker-invariance gate that runs
// them is matrix_invariance_test.go (the shared harness).

// matrixInvarianceVariant is one request shape of the gate.
// typ is the operator (MAT_COVARIANCE when empty); pairwise selects
// params.missing "pairwise".
type matrixInvarianceVariant struct {
	weighted    bool
	withVehicle bool
	typ         types.MatrixType
	pairwise    bool
}

// matrixInvarianceVariants: every request shape for every built-in
// matrix operator, listwise and pairwise.
var matrixInvarianceVariants = func() []matrixInvarianceVariant {
	var out []matrixInvarianceVariant
	for _, typ := range types.AllMatrixTypes() {
		for _, pairwise := range []bool{false, true} {
			if pairwise && (typ == types.MAT_DISTANCE || typ == types.MAT_SEM) {
				continue // listwise only
			}
			for _, v := range []matrixInvarianceVariant{{false, false, "", false}, {true, false, "", false}, {false, true, "", false}, {true, true, "", false}} {
				if v.weighted && typ == types.MAT_SEM {
					// Frequency only, and these weights are fractional
					// (invalid under kind frequency): the frequency fit
					// is TestMatrixSEM_FrequencyWeightIsExpansion's.
					continue
				}
				v.typ = typ
				v.pairwise = pairwise
				out = append(out, v)
			}
		}
	}
	return out
}()

func (v matrixInvarianceVariant) matrixType() types.MatrixType {
	if v.typ == "" {
		return types.MAT_COVARIANCE
	}
	return v.typ
}

func (v matrixInvarianceVariant) String() string {
	return fmt.Sprintf("%s_%s_weighted_%v_vehicle_%v", v.matrixType(), v.mode(), v.weighted, v.withVehicle)
}

func (v matrixInvarianceVariant) mode() string {
	if v.pairwise {
		return "pairwise"
	}
	return "listwise"
}

func matrixInvarianceRequest(path string, v matrixInvarianceVariant) *types.Request {
	spec := types.MatrixSpec{Name: "mat", Type: v.matrixType(), Fields: []string{"x1", "x2", "x3"}}
	// Listwise carries max_drop_share 0, so its integer drop count —
	// summed across partitions — rides the compared warnings.
	spec.Params = json.RawMessage(`{"max_drop_share": 0}`)
	if v.pairwise {
		spec.Params = json.RawMessage(`{"missing": "pairwise"}`)
	}
	// MAT_CORRELATION also lists every pair under top_pairs, so the
	// ranking (and its tie-break) rides the compared run.
	if v.matrixType() == types.MAT_CORRELATION {
		spec.Params = json.RawMessage(`{"max_drop_share": 0, "summary": {"top_pairs": 3}}`)
		if v.pairwise {
			spec.Params = json.RawMessage(`{"missing": "pairwise", "summary": {"top_pairs": 3}}`)
		}
	}
	// MAT_PARTIAL_CORRELATION is a decomposition operator: these
	// pairwise correlations are not PSD, so its pairwise variant repairs
	// them (the repair is a function of the merged bits, so it is as
	// worker-invariant as they are).
	// MAT_RELIABILITY's omega decomposes the same input, so its
	// pairwise variant repairs too (and so runs the minres fit).
	// MAT_COLLINEARITY inverts and decomposes the same input.
	if (v.matrixType() == types.MAT_PARTIAL_CORRELATION || v.matrixType() == types.MAT_RELIABILITY || v.matrixType() == types.MAT_COLLINEARITY) && v.pairwise {
		spec.Params = json.RawMessage(`{"missing": "pairwise", "repair": "nearest"}`)
	}
	// MAT_PCA keeps every component (so the eigenvectors and loadings
	// are compared whole) and repairs its pairwise input like the other
	// decomposition operators; its eigensolver is gonum-backed, so the
	// bits are same-machine stable — which is what this gate compares.
	// The pairwise variant also varimax-rotates (U25 E2-S1): the
	// rotation is a pure function of the merged loadings, so its primary
	// and rotated_variance are as worker-invariant as they are.
	if v.matrixType() == types.MAT_PCA {
		spec.Params = json.RawMessage(`{"max_drop_share": 0, "components": 3}`)
		if v.pairwise {
			spec.Params = json.RawMessage(`{"missing": "pairwise", "components": 3, "repair": "nearest", "rotation": "varimax"}`)
		}
	}
	// MAT_FACTOR fits one factor (the most three members identify):
	// minres listwise unweighted, maximum likelihood listwise weighted
	// (with its likelihood figures); the pairwise variant repairs its
	// input like the other decomposition operators and runs principal
	// axis, so every extraction rides the gate. Each is a pure function
	// of the merged correlation (its eigensolves gonum-backed,
	// same-machine stable).
	if v.matrixType() == types.MAT_FACTOR {
		spec.Params = json.RawMessage(`{"max_drop_share": 0, "factors": 1}`)
		if v.weighted {
			spec.Params = json.RawMessage(`{"max_drop_share": 0, "factors": 1, "extraction": "ml"}`)
		}
		if v.pairwise {
			spec.Params = json.RawMessage(`{"missing": "pairwise", "factors": 1, "repair": "nearest", "extraction": "principal_axis"}`)
		}
	}
	// MAT_DISTANCE compares the x1 / x2 centroids of four GROUP_RANGE
	// buckets of x3 (a mergeable between), standardized; weighted, it
	// runs mahalanobis over the pooled within-bucket covariance.
	if v.matrixType() == types.MAT_DISTANCE {
		spec.Fields = []string{"x1", "x2"}
		metric := "euclidean"
		if v.weighted {
			metric = "mahalanobis"
		}
		spec.Params = json.RawMessage(`{"max_drop_share": 0, "standardize": true, "metric": "` + metric +
			`", "between": {"type": "GROUP_RANGE", "field": "x3", "interval": 250000}}`)
	}
	// MAT_SEM regresses x1 on x3 (df 1): the near-collinear x1 / x2
	// pair at these scales leaves a covariance no ML fit can factor
	// (PULSE_MATRIX_SINGULAR), so the model leaves x2 out.
	if v.matrixType() == types.MAT_SEM {
		spec.Fields = []string{"x1", "x3"}
		spec.Params = json.RawMessage(`{"max_drop_share": 0, "model": "x1 ~ x3"}`)
	}
	if v.weighted {
		spec.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindProbability})
	}
	req := &types.Request{
		Cohort:   &types.Cohort{Filename: path},
		Matrices: []types.MatrixSpec{spec},
	}
	if v.withVehicle {
		req.Aggregations = coMomentRequest(path, "listwise", false).Aggregations
	}
	return req
}

// referenceMatrixWords folds rows 0..n-1 straight into one listwise
// CoMoment and renders the variant's operator (Cov(1) or Corr) as the
// gate renders the matrix — the correctness anchor (wrong member, wrong
// weight, wrong operator) the engine's blocked result must agree with
// to rounding.
func referenceMatrixWords(t *testing.T, n int, v matrixInvarianceVariant) []uint64 {
	weighted := v.weighted
	t.Helper()
	mode := linalg.Listwise
	if v.pairwise {
		mode = linalg.Pairwise
	}
	c, err := linalg.NewCoMoment(3, mode)
	if err != nil {
		t.Fatal(err)
	}
	recs, nullAt := coMomentRows(n, 0)
	read := func(r, f int) float64 {
		if nullAt(r, f) {
			return math.NaN()
		}
		return math.Float64frombits(recs[r][f])
	}
	for r := range recs {
		w := 1.0
		if weighted {
			w = read(r, 3)
		}
		c.Add([]float64{read(r, 0), read(r, 1), read(r, 2)}, w)
	}
	cov := c.Cov(1)
	switch v.matrixType() {
	case types.MAT_CORRELATION, types.MAT_RELIABILITY:
		cov = c.Corr()
	case types.MAT_PCA, types.MAT_FACTOR:
		return nil // the loadings are an eigensolver's output: no FMA-free anchor
	case types.MAT_DISTANCE:
		return nil // bucket distances: anchored by the R oracle (internal/processing)
	case types.MAT_SEM:
		return nil // the implied covariance is an optimiser's output: anchored by the semopy fixtures (internal/sem)
	case types.MAT_COLLINEARITY:
		if v.pairwise {
			return nil // the repaired matrix has no independent anchor here
		}
		cov = c.Corr()
	case types.MAT_PARTIAL_CORRELATION:
		if v.pairwise {
			return nil // the repaired matrix has no independent anchor here
		}
		cov = referencePartial(t, c.Corr())
	}
	var words []uint64
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			words = append(words, math.Float64bits(cov.At(i, j)))
		}
	}
	return words
}

// referencePartial is the control-"all" partial correlation of r
// through gonum's general inverse — an anchor independent of the
// engine's reference InverseSPD.
func referencePartial(t *testing.T, r *linalg.Sym) *linalg.Sym {
	t.Helper()
	n := r.N()
	d := mat.NewDense(n, n, nil)
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			d.Set(i, j, r.At(i, j))
		}
	}
	var inv mat.Dense
	if err := inv.Inverse(d); err != nil {
		t.Fatalf("reference inverse: %v", err)
	}
	out, _ := linalg.NewSym(n, nil)
	for i := 0; i < n; i++ {
		out.Set(i, i, 1)
		for j := i + 1; j < n; j++ {
			out.Set(i, j, -inv.At(i, j)/math.Sqrt(inv.At(i, i)*inv.At(j, j)))
		}
	}
	return out
}

// TestMatrixOnly_RunComponents: a matrix-only request (no aggregation,
// no grouper) has no primary null field, so Run.NullRecords is 0 on the
// serial and both parallel arms alike, and the record counters are the
// cohort's — the parallel reducers must not assume a first aggregator.
func TestMatrixOnly_RunComponents(t *testing.T) {
	t.Parallel()

	schema := coMomentSchema()
	B := linalg.MergeBlockSize
	shardRows := []int{B + 3, 700, 2 * B}
	total := 0
	for _, n := range shardRows {
		total += n
	}
	cfg := fs.NewMemMap()
	if err := afero.WriteFile(cfg.Fs(), "archive.pulse", coMomentArchive(t, schema, shardRows), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	req := matrixInvarianceRequest("archive.pulse", matrixInvarianceVariant{})
	want := types.RunComponents{TotalRecords: int64(total), FilteredRecords: int64(total), ShardCount: len(shardRows)}
	for _, workers := range []int{1, 3} {
		svc := New(cfg)
		svc.SetShardWorkers(workers)
		resp, err := svc.Process(context.Background(), req)
		if err != nil {
			t.Fatalf("ShardWorkers=%d: %v", workers, err)
		}
		if resp.Components == nil || resp.Components.Run == nil {
			t.Fatalf("ShardWorkers=%d: no Components.Run", workers)
		}
		if got := *resp.Components.Run; got != want {
			t.Errorf("ShardWorkers=%d: Components.Run %+v, want %+v", workers, got, want)
		}
	}
}
