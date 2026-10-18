package feature

import (
	"fmt"
	"math/rand"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fieldaccept"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/internal/targetencode"
	"github.com/frankbardon/pulse/types"
)

func init() {
	register(types.FEAT_TARGET_ENCODE, newTargetEncode)
}

// targetEncode replaces a categorical value with the mean of a numeric
// target field over the TRAINING rows sharing that category. Params
// (internal/targetencode, shared with predict):
//
//	{"target": "label", "smoothing": 0.0, "fit_on": "split == 0", "folds": 5, "rng_seed": 7}
//
// Smoothing applies the standard additive prior toward the global mean:
// encoded = (count_cat * mean_cat + smoothing * global_mean) / (count_cat + smoothing).
//
// Training rows: the rows params.fit_on admits (a boolean expression
// compiled like FILTER_EXPRESSION by the engine, BindFitOn; a null
// operand admits nothing), or every row without it. Only training rows
// with a non-null category and target feed the category and global
// means; EVERY row is encoded from them, an unseen category with the
// training global mean. A holdout row's outcome therefore never moves a
// value — the leakage guarantee.
//
// Out-of-fold (params.folds = k): each training row is dealt a fold —
// the training rows in order, in blocks of k, each block a seeded
// permutation of 0..k-1 (rng_seed) — and is encoded from the training
// rows OUTSIDE its fold (their category and global means); a row whose
// out-of-fold set holds no outcome is null. Rows fit_on does not admit
// get the full-training encoding.
//
// Two passes: PrePass tallies, Finalize freezes the means, EmitRow
// writes; Compute is that drive over the slice. The fold deal and
// fit_on are re-read in EmitRow, so rows must be emitted in the
// PrePass order (as FEAT_TRAIN_TEST_SPLIT's are).
//
// Without fit_on every encoded value includes the row's own target and
// the validation/test rows' targets — features run before filters.
// Predict flags such an encoder with PULSE_FEAT_TARGET_LEAKAGE_RISK
// (internal/descriptor TargetLeakageWarnings) and Process repeats it,
// refusing under strict; fit_on silences it.
type targetEncode struct {
	label  string
	params targetencode.Params
	// fitOn is the bound fit_on predicate (nil without fit_on).
	fitOn    RowPredicate
	fitBound bool
	// Streaming state (PrePass / Finalize / EmitRow).
	st *targetState
}

// targetStat tallies sum and count for one category.
type targetStat struct {
	sum   float64
	count int
}

// targetTally is one set of training means: per category and global.
type targetTally struct {
	per         map[string]*targetStat
	globalSum   float64
	globalCount int
}

func newTargetTally() *targetTally {
	return &targetTally{per: make(map[string]*targetStat, 16)}
}

func (t *targetTally) add(cat string, y float64) {
	st := t.per[cat]
	if st == nil {
		st = &targetStat{}
		t.per[cat] = st
	}
	st.sum += y
	st.count++
	t.globalSum += y
	t.globalCount++
}

// merge folds o into t, category by category.
func (t *targetTally) merge(o *targetTally) {
	for cat, s := range o.per {
		st := t.per[cat]
		if st == nil {
			st = &targetStat{}
			t.per[cat] = st
		}
		st.sum += s.sum
		st.count += s.count
	}
	t.globalSum += o.globalSum
	t.globalCount += o.globalCount
}

// encode is the row's value from tally: ok=false when the tally holds
// no outcome (the row is null).
func (t *targetTally) encode(cat string, smoothing float64) (float64, bool) {
	if t.globalCount == 0 {
		return 0, false
	}
	return encodedValue(t.per, cat, t.globalSum/float64(t.globalCount), smoothing), true
}

// targetState is one run's tallies and fold deals.
type targetState struct {
	full *targetTally
	// folds holds each fold's own training tally during PrePass;
	// Finalize replaces each with its OUT-of-fold tally (every other
	// fold merged, in fold order).
	folds    []*targetTally
	prepDeal *foldDealer
	emitDeal *foldDealer
	final    bool
}

func (c *targetEncode) newState() *targetState {
	s := &targetState{full: newTargetTally()}
	if k := c.params.Folds; k > 0 {
		s.folds = make([]*targetTally, k)
		for i := range s.folds {
			s.folds[i] = newTargetTally()
		}
		s.prepDeal = newFoldDealer(k, c.params.Seed())
		s.emitDeal = newFoldDealer(k, c.params.Seed())
	}
	return s
}

// foldDealer deals folds to the training rows in order: blocks of k,
// each a Fisher-Yates permutation of 0..k-1 drawn from one seeded
// source (Int63n only, whose seeded sequence is stable), so the folds
// are balanced to within one row and the deal depends on nothing but
// the seed and the training rows' order.
type foldDealer struct {
	rng  *rand.Rand
	perm []int
	pos  int
}

func newFoldDealer(k int, seed int64) *foldDealer {
	return &foldDealer{rng: rand.New(rand.NewSource(seed)), perm: make([]int, k), pos: k}
}

func (d *foldDealer) next() int {
	k := len(d.perm)
	if d.pos == k {
		for i := range d.perm {
			d.perm[i] = i
		}
		for i := k - 1; i > 0; i-- {
			j := int(d.rng.Int63n(int64(i + 1)))
			d.perm[i], d.perm[j] = d.perm[j], d.perm[i]
		}
		d.pos = 0
	}
	f := d.perm[d.pos]
	d.pos++
	return f
}

var (
	_ StreamingComputer = (*targetEncode)(nil)
	_ FitOnBinder       = (*targetEncode)(nil)
)

func newTargetEncode(feat *types.Feature, schema *encoding.Schema) (Computer, error) {
	if feat.Field == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"FEAT_TARGET_ENCODE requires a field")
	}
	if len(feat.Params) == 0 {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"FEAT_TARGET_ENCODE requires params with a 'target' field")
	}
	var p targetencode.Params
	if err := paramread.Decode(paramread.Feature, string(feat.Type), feat.Params, &p); err != nil {
		return nil, errors.WrapCodedError(err, errors.PROCESSING_CONFIG,
			"parsing FEAT_TARGET_ENCODE params")
	}
	if ce := targetencode.Check(p); ce != nil {
		return nil, ce
	}
	if schema != nil {
		if cerr := fieldaccept.Check(string(types.FEAT_TARGET_ENCODE), fieldaccept.SlotField, feat.Field, schema); cerr != nil {
			return nil, cerr
		}
		if cerr := fieldaccept.Check(string(types.FEAT_TARGET_ENCODE), fieldaccept.SlotTarget, p.Target, schema); cerr != nil {
			return nil, cerr
		}
	}

	label := feat.Label
	if label == "" {
		label = fmt.Sprintf("TARGET_%s", feat.Field)
	}
	return &targetEncode{label: label, params: p}, nil
}

// FitOnExpression implements FitOnBinder.
func (c *targetEncode) FitOnExpression() string { return c.params.FitOn }

// BindFitOn implements FitOnBinder.
func (c *targetEncode) BindFitOn(pred RowPredicate) {
	c.fitOn, c.fitBound = pred, true
}

// training reports whether r is a training row.
func (c *targetEncode) training(r Record) (bool, error) {
	if c.params.FitOn == "" {
		return true, nil
	}
	if !c.fitBound || c.fitOn == nil {
		return false, errors.NewCodedError(errors.PROCESSING_INTERNAL,
			"FEAT_TARGET_ENCODE params.fit_on was not bound before the pre-pass")
	}
	return c.fitOn(r)
}

func (c *targetEncode) Compute(records []Record, field string) (map[string]Output, error) {
	if len(records) == 0 {
		return map[string]Output{c.label: {Values: []float64{}}}, nil
	}
	st := c.newState()
	for _, r := range records {
		if err := c.prePass(st, r, field); err != nil {
			return nil, err
		}
	}
	c.finalize(st)
	values := make([]float64, len(records))
	nulls := make([]bool, len(records))
	for i, r := range records {
		v, ok, err := c.emit(st, r, field)
		if err != nil {
			return nil, err
		}
		if ok {
			values[i] = v
		} else {
			nulls[i] = true
		}
	}
	return map[string]Output{c.label: {Values: values, Nulls: nulls}}, nil
}

// prePass folds one record into st: a training row with a non-null
// category and target into the full tally and, out-of-fold, into its
// dealt fold's. Every training row is dealt a fold, nulls included, so
// the deal reads only fit_on.
func (c *targetEncode) prePass(st *targetState, r Record, field string) error {
	train, err := c.training(r)
	if err != nil || !train {
		return err
	}
	fold := -1
	if st.prepDeal != nil {
		fold = st.prepDeal.next()
	}
	s, sOk := r.LevelValue(field)
	y, yOk := r.NumericValue(c.params.Target)
	if !sOk || !yOk {
		return nil
	}
	st.full.add(s, y)
	if fold >= 0 {
		st.folds[fold].add(s, y)
	}
	return nil
}

// finalize turns each fold's own tally into its out-of-fold tally:
// every OTHER fold merged in fold order.
func (c *targetEncode) finalize(st *targetState) {
	if len(st.folds) > 0 {
		oof := make([]*targetTally, len(st.folds))
		for f := range st.folds {
			t := newTargetTally()
			for g, other := range st.folds {
				if g != f {
					t.merge(other)
				}
			}
			oof[f] = t
		}
		st.folds = oof
	}
	st.final = true
}

// emit is one record's value: ok=false for a null.
func (c *targetEncode) emit(st *targetState, r Record, field string) (float64, bool, error) {
	tally := st.full
	if st.emitDeal != nil {
		train, err := c.training(r)
		if err != nil {
			return 0, false, err
		}
		if train {
			tally = st.folds[st.emitDeal.next()]
		}
	}
	s, ok := r.LevelValue(field)
	if !ok {
		return 0, false, nil
	}
	v, ok := tally.encode(s, c.params.Smoothing)
	return v, ok, nil
}

// PrePass folds the record into the running tallies. Rows with a null
// category or null target contribute nothing — same as Compute.
func (c *targetEncode) PrePass(r Record, field string) error {
	if c.st == nil {
		c.st = c.newState()
	}
	return c.prePass(c.st, r, field)
}

// Finalize freezes the tallies. When no training outcome was observed
// every emitted row is null.
func (c *targetEncode) Finalize() error {
	if c.st == nil {
		c.st = c.newState()
	}
	c.finalize(c.st)
	return nil
}

// EmitRow returns the record's encoded value. Mirrors Compute's
// per-row branch.
func (c *targetEncode) EmitRow(r Record, field string) (map[string]Output, error) {
	if c.st == nil || !c.st.final {
		return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL,
			"FEAT_TARGET_ENCODE EmitRow before Finalize")
	}
	v, ok, err := c.emit(c.st, r, field)
	if err != nil {
		return nil, err
	}
	if !ok {
		return map[string]Output{c.label: {Values: []float64{0}, Nulls: []bool{true}}}, nil
	}
	return map[string]Output{c.label: {Values: []float64{v}}}, nil
}

// encodedValue is the shared smoothing math: returns globalMean for an
// unseen category, the per-cat mean otherwise, with optional additive
// smoothing toward globalMean.
func encodedValue(per map[string]*targetStat, s string, globalMean, smoothing float64) float64 {
	st, found := per[s]
	if !found || st.count == 0 {
		return globalMean
	}
	mean := st.sum / float64(st.count)
	if smoothing > 0 {
		return (float64(st.count)*mean + smoothing*globalMean) / (float64(st.count) + smoothing)
	}
	return mean
}
