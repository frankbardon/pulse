package processing

import (
	"fmt"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/types"
)

// Weighted row tests — the engine half of .claude/reference/weighting.md
// (Weighted inference). A weight-aware built-in row test reads its
// slot's STAMPED weight (StampWeightsWith leaves it set only when a
// weight applies), folds each row into a weighting.Welford bucket with
// that row's weight, and evaluates the frequency formula on w*
// (weighting.Basis: N* = Σw under frequency, Kish n_eff under
// probability). Unweighted, every row weighs exactly 1, which
// reproduces the unweighted recurrence bit for bit.

// testWeight is the weight one built-in row test reads, plus the
// effective-n shortfalls its Finalize found. Embedded in each
// weight-aware row test.
type testWeight struct {
	spec  *types.WeightSpec
	basis weighting.Basis
	low   []lowNEff
}

// lowNEff is one group whose Kish n_eff fell below the test's floor.
type lowNEff struct {
	label, group string
	typ          types.TestType
	nEff         float64
	min          int
	split        bool
}

// newTestWeight reads a stamped test slot's resolved weight (nil: the
// test runs unweighted).
func newTestWeight(t *types.Test) testWeight {
	spec := t.Weight.Spec()
	return testWeight{spec: spec, basis: weighting.BasisOf(spec)}
}

// rowWeight is the weight row r contributes with: exactly 1 when the
// test is unweighted; the row's weight when it is valid and positive;
// ok=false otherwise — an invalid weight is excluded (and counted once,
// per weight field, by WeightRowTally) and a zero weight contributes
// nothing, so neither enters the test or its raw `n`. Call it only
// after the row's values are known present: a row dropped for a null
// value never has its weight judged.
func (tw *testWeight) rowWeight(r *Record) (float64, bool) {
	if tw.spec == nil {
		return 1, true
	}
	w, reason := readWeight(r, tw.spec)
	if reason != weighting.Valid || w == 0 {
		return 0, false
	}
	return w, true
}

// noteScalar writes sum_weights (and, under kind probability, n_eff)
// beside an unsplit test's scalar `n`; no-op unweighted.
func (tw *testWeight) noteScalar(details map[string]any, sumW, sumWSq float64) {
	if !tw.basis.Weighted() {
		return
	}
	details["sum_weights"] = sumW
	if tw.basis == weighting.Probability {
		details["n_eff"] = weighting.KishNEff(sumW, sumWSq)
	}
}

// noteGroups writes per-group sum_weights (and n_eff under kind
// probability) arrays beside a split test's per-group `n`, in the same
// order; no-op unweighted.
func (tw *testWeight) noteGroups(details map[string]any, buckets []*weighting.Welford) {
	if !tw.basis.Weighted() {
		return
	}
	sums := make([]float64, len(buckets))
	for i, b := range buckets {
		sums[i] = b.SumW
	}
	details["sum_weights"] = sums
	if tw.basis == weighting.Probability {
		neffs := make([]float64, len(buckets))
		for i, b := range buckets {
			neffs[i] = b.NEff()
		}
		details["n_eff"] = neffs
	}
}

// noteGroupSums is noteGroups for a test that keeps per-group Σw and
// Σw² itself (the buffered rank tests) rather than Welford buckets.
func (tw *testWeight) noteGroupSums(details map[string]any, sums, sumSqs []float64) {
	if !tw.basis.Weighted() {
		return
	}
	details["sum_weights"] = append([]float64(nil), sums...)
	if tw.basis == weighting.Probability {
		neffs := make([]float64, len(sums))
		for i := range sums {
			neffs[i] = weighting.KishNEff(sums[i], sumSqs[i])
		}
		details["n_eff"] = neffs
	}
}

// checkNEff records a shortfall when, under kind probability, nEff is
// below the test's raw-n floor min. group names the short group of a
// split test ("" and split=false for an unsplit test or a whole-sample
// floor).
func (tw *testWeight) checkNEff(spec *types.Test, split bool, group string, nEff float64, min int) {
	if tw.basis != weighting.Probability || nEff >= float64(min) {
		return
	}
	tw.low = append(tw.low, lowNEff{label: testLabel(spec), typ: spec.Type, group: group, split: split, nEff: nEff, min: min})
}

func (tw *testWeight) lowNEffs() []lowNEff { return tw.low }

// lowNEffReporter is a row test that can report effective-n shortfalls
// after Finalize.
type lowNEffReporter interface {
	lowNEffs() []lowNEff
}

// applyLowNEff attaches one PULSE_WEIGHT_LOW_NEFF warning per short
// group the finalized row tests recorded — details {test, type, group
// (split tests only), n_eff, min_required} — or, under strict, returns
// the first as an error instead.
func applyLowNEff(resp *types.Response, entries []rowTestEntry, strict bool) error {
	if resp == nil {
		return nil
	}
	for _, e := range entries {
		rep, ok := e.test.(lowNEffReporter)
		if !ok {
			continue
		}
		for _, l := range rep.lowNEffs() {
			where := ""
			details := map[string]any{"test": l.label, "type": string(l.typ), "n_eff": l.nEff, "min_required": l.min}
			if l.split {
				details["group"] = l.group
				where = fmt.Sprintf(" group %q", l.group)
			}
			msg := fmt.Sprintf("%s%s: effective sample size n_eff = %.4g is below the %d the test needs; its inference reads n_eff and is fragile", l.label, where, l.nEff, l.min)
			if strict {
				return errors.NewCodedErrorWithDetails(errors.PULSE_WEIGHT_LOW_NEFF, msg, details)
			}
			resp.Warnings = append(resp.Warnings, &types.ResponseWarning{
				Code:    string(errors.PULSE_WEIGHT_LOW_NEFF),
				Message: msg,
				Details: details,
			})
		}
	}
	return nil
}
