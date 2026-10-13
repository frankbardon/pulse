package processing

import (
	"fmt"
	"math"
	"strconv"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
)

// bartlettSphericityRow implements TEST_BARTLETT_SPHERICITY: Bartlett's
// test that the correlation matrix R of a VECTOR (the p members of the
// slot's Vector / Fields, resolved and stashed on the spec copy by
// buildRowTests) is the identity — whether the members share enough
// correlation to factor. One sample; split_by is refused.
//
// The statistic is the shared core MAT_PCA's bartlett_* scalars read
// (bartlettSphericity over R = CoMoment.Corr at N* = sphericityNStar),
// and the rows fold exactly as a matrix slot's do: one listwise
// co-moment per merge block, keyed by the record's stamped position,
// combined through the one fixed tree (BlockCoMoments.Tree). On the
// same rows the statistic, df and p therefore equal MAT_PCA's bit for
// bit, under any row count. A record no source stamped (a join or
// chain stage) is keyed by its arrival order instead.
//
// Weights follow the one rule: N* is N unweighted, Σw under frequency
// weights, Kish n_eff under probability weights; a row with an invalid
// or zero weight is excluded before the fold, as on every row test. A
// singular R (a member constant or a linear combination of others)
// warns PULSE_MATRIX_SINGULAR, and N* ≤ 1 + (2p + 5)/6 warns
// PULSE_MATRIX_INSUFFICIENT_N; both leave a null statistic and p.
type bartlettSphericityRow struct {
	spec    *types.Test
	members []string
	// fill reads members (the shared member fill).
	fill  memberFill
	alpha float64

	x      []float64
	blocks *BlockCoMoments
	// seq counts folded rows: the fallback block key of an
	// unpositioned record.
	seq int
	// dropped counts filter-passing rows skipped because a member was
	// null: the listwise drop.
	dropped int64

	testWeight
}

func newBartlettSphericityRow(spec *types.Test, schema *encoding.Schema) (RowTest, error) {
	p := len(spec.Fields)
	if p < 2 {
		return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
			"TEST_BARTLETT_SPHERICITY requires vector or fields with at least 2 members (the correlation matrix it tests)",
			map[string]any{"test": testLabel(spec), "p": p})
	}
	alpha := spec.Alpha
	if alpha == 0 {
		alpha = 0.05
	}
	if alpha <= 0 || alpha >= 1 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INVALID_ALPHA,
			fmt.Sprintf("alpha %g not in (0, 1)", spec.Alpha),
			map[string]any{"alpha": spec.Alpha})
	}
	if err := vectors.SphericityParams(testLabel(spec), spec); err != nil {
		return nil, err
	}
	blocks, err := NewBlockCoMoments(p, linalg.Listwise)
	if err != nil {
		return nil, err
	}
	return &bartlettSphericityRow{
		spec:       spec,
		members:    append([]string(nil), spec.Fields...),
		fill:       memberFillOf(spec.Fields, schema),
		alpha:      alpha,
		x:          make([]float64, p),
		blocks:     blocks,
		testWeight: newTestWeight(spec),
	}, nil
}

// UpdateRow folds one record. A null member drops it (listwise) and
// counts it; a weighted test then skips a row whose weight is invalid
// or zero.
func (b *bartlettSphericityRow) UpdateRow(record *Record) error {
	if !b.fill.complete(record, b.x) {
		b.dropped++
		return nil
	}
	w, ok := b.rowWeight(record)
	if !ok {
		return nil
	}
	key, positioned := record.MergeBlock()
	if !positioned {
		key = BlockKey{Block: b.seq / linalg.MergeBlockSize, Head: true}
	}
	b.seq++
	return b.blocks.AddAt(key, b.x, w)
}

// Finalize emits the TestResult and resets the state.
func (b *bartlettSphericityRow) Finalize() (*types.TestResult, error) {
	p := len(b.members)
	defer func() {
		b.blocks, _ = NewBlockCoMoments(p, linalg.Listwise)
		b.seq = 0
		b.dropped = 0
	}()
	cm, err := b.blocks.Tree()
	if err != nil {
		return nil, err
	}
	n := cm.N()
	if n < 2 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_INSUFFICIENT_N,
			fmt.Sprintf("TEST_BARTLETT_SPHERICITY requires n ≥ 2 complete rows, got %d", n),
			map[string]any{"n": n, "min_required": 2, "p": p})
	}
	nStar, _ := sphericityNStar(cm, b.basis)
	res := &types.TestResult{
		Label: testLabel(b.spec),
		Type:  types.TEST_BARTLETT_SPHERICITY,
		DF:    float64(p * (p - 1) / 2),
		Alpha: b.alpha,
		Details: map[string]any{
			"members":     append([]string(nil), b.members...),
			"n":           n,
			"n_dropped":   b.dropped,
			"determinant": math.NaN(),
		},
	}
	b.noteWeights(res.Details, []*linalg.CoMoment{cm}, false)
	b.checkNEff(b.spec, false, "", cm.NEff(), 2)
	res.Statistic, res.PValue = math.NaN(), math.NaN()
	r := cm.Corr()
	if !judgeable(r) {
		res.Warnings = append(res.Warnings, string(errors.PULSE_MATRIX_SINGULAR)+
			": a member of "+fmt.Sprint(b.members)+" is constant, so the correlation matrix is undefined; Bartlett's test is null")
		return res, nil
	}
	st := bartlettSphericity(r, nStar)
	switch {
	case st.Singular:
		res.Warnings = append(res.Warnings, string(errors.PULSE_MATRIX_SINGULAR)+
			": the correlation matrix of "+fmt.Sprint(b.members)+" is singular (a member is a linear combination of others), so it has no determinant above 0; Bartlett's test is null")
		return res, nil
	case st.Insufficient:
		res.Warnings = append(res.Warnings, string(errors.PULSE_MATRIX_INSUFFICIENT_N)+
			": Bartlett's test needs an inference size N* above 1 + (2p + 5)/6 = "+strconv.FormatFloat(st.MinNStar, 'g', 6, 64)+
			" for "+strconv.Itoa(p)+" members; N* is "+strconv.FormatFloat(nStar, 'g', 6, 64)+", so the statistic is null")
		return res, nil
	}
	res.Statistic = st.ChiSq
	res.PValue = st.PValue
	res.RejectNull = res.PValue < b.alpha
	res.Details["determinant"] = st.Det
	return res, nil
}
