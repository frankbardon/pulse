package model

import (
	"fmt"
	"slices"

	"github.com/frankbardon/pulse/types"
)

// sweep_fits.go is the static half of a sweep-bound fit — a `fits`
// entry carrying `from_sweep` instead of a request (types.FitFromSweep):
// the rules CheckFitsIn judges it by, and the fit request its static
// signature (CatalogFromFits, FitKindOf, predict's FitSummary) is read
// from. Runtime and predict build the same SweepScope from the same
// expansion, so both refuse the same batch with the same error.

// The from_sweep PULSE_FIT_INVALID reasons.
const (
	// ReasonFromSweepWithRequest: the entry sets both `request` and
	// `from_sweep`.
	ReasonFromSweepWithRequest = "from_sweep_with_request"
	// ReasonFromSweepNoSweep: the batch has no `sweep` to bind from.
	ReasonFromSweepNoSweep = "from_sweep_no_sweep"
	// ReasonFromSweepNoRank: the sweep has no `rank`, so no slot is the
	// winner.
	ReasonFromSweepNoRank = "from_sweep_no_rank"
	// ReasonFromSweepRegressionUnknown: the sweep body holds no
	// regression of that name.
	ReasonFromSweepRegressionUnknown = "from_sweep_regression_unknown"
	// ReasonFromSweepSignatureVaries: the regression's type, target,
	// predictors or term catalog differs between sweep slots (it reads
	// an axis), so no one static signature describes the fit.
	ReasonFromSweepSignatureVaries = "from_sweep_signature_varies"
	// ReasonFromSweepCycle: a sweep slot reads the fit it binds (coef(),
	// sweep_axis(), a params.fit) — the slots run before the fit
	// exists.
	ReasonFromSweepCycle = "from_sweep_cycle"
)

// SweepAxisFunc is the expr function that reads a sweep-bound fit's
// winning axis value: sweep_axis("<fit>", "<axis>"). A sweep slot
// naming the fit it binds in one is a from_sweep cycle.
const SweepAxisFunc = "sweep_axis"

// SweepScope is the static face of the Compose sweep a from_sweep fit
// binds from, built once from the batch's expansion: whether the
// request as written carries a sweep and a rank, and the expanded
// sweep slots (the effective requests, in expansion order). Reads
// reports whether a slot reads the bound-model name fit (any reference
// a slot resolves against the batch's fits); nil reads nothing. The
// zero value is a sweep-free batch.
type SweepScope struct {
	Present bool
	Ranked  bool
	Slots   []*types.Request
	Reads   func(slot *types.Request, fit string) bool
}

// SweepRegression is the regression of slot whose `name` is name, nil
// when there is none (the first such regression).
func SweepRegression(slot *types.Request, name string) *types.RegressionSpec {
	if slot == nil || name == "" {
		return nil
	}
	for _, r := range slot.Regressions {
		if r != nil && r.Name == name {
			return r
		}
	}
	return nil
}

// SweepFitRequest is the fit request a sweep-bound fit runs over slot:
// the slot's rows — its cohort, joins, filters, windows, features,
// non-battery attributes, vectors, loaded files, zone, weight and
// params — and its regression named regression as the ONE regression,
// its `save` cleared (the fit's own `save` writes the bound fit). Every
// part a fit refuses or never reads is dropped: groups, crosstab,
// aggregations, tests, matrices, the fitted vector attributes,
// outputs, sort, overlays, multiplicity, components switch, `return`
// and label. Nil when slot holds no such regression. slot is never
// mutated.
//
// It is the one rule: predict reads the fit's static signature off it
// (over the first sweep slot — the signature is axis-invariant), and
// the runtime refits the winning slot through it.
func SweepFitRequest(slot *types.Request, regression string) *types.Request {
	reg := SweepRegression(slot, regression)
	if reg == nil {
		return nil
	}
	r := *slot
	rc := *reg
	rc.Save = ""
	r.Regressions = []*types.RegressionSpec{&rc}
	r.Label = ""
	r.Aggregations, r.Groups, r.Outputs, r.Sort = nil, nil, nil, nil
	r.Tests, r.PostTests, r.Matrices, r.Crosstab, r.Overlays = nil, nil, nil, nil, nil
	r.DisableComponents, r.Multiplicity, r.Return = nil, nil, nil
	if slices.ContainsFunc(slot.Attributes, func(a *types.Attribute) bool { return a != nil && IsVectorFitAttribute(a.Type) }) {
		r.Attributes = nil
		for _, a := range slot.Attributes {
			if a == nil || !IsVectorFitAttribute(a.Type) {
				r.Attributes = append(r.Attributes, a)
			}
		}
	}
	return &r
}

// ResolveSweepFits returns fits with every from_sweep entry's Request
// filled by SweepFitRequest over the scope's first sweep slot — the
// representative CatalogFromFits, FitKindOf and predict read the static
// signature from (CheckFitsIn proved it axis-invariant). FromSweep
// stays set, so the runtime still knows to bind the winning slot. A
// list without from_sweep comes back as the same slice; the caller's
// list is never mutated. Run it only after CheckFitsIn passed.
func ResolveSweepFits(fits []types.FitSpec, scope SweepScope) []types.FitSpec {
	if !slices.ContainsFunc(fits, func(f types.FitSpec) bool { return f.FromSweep != nil }) {
		return fits
	}
	out := slices.Clone(fits)
	for i, f := range out {
		if f.FromSweep == nil || f.Request != nil || len(scope.Slots) == 0 {
			continue
		}
		out[i].Request = SweepFitRequest(scope.Slots[0], f.FromSweep.Regression)
	}
	return out
}

// checkSweepFit judges fits[i], a from_sweep entry, against the scope;
// the first fault wins: a request beside it, no sweep, no rank, no
// regression of that name in the first sweep slot, a type / target /
// predictors / term catalog that differs in any later slot (or a slot
// lacking the regression), and a sweep slot reading the fit.
func checkSweepFit(i int, f types.FitSpec, scope SweepScope) error {
	reg := f.FromSweep.Regression
	switch {
	case f.Request != nil:
		return fitInvalid(i, f.Name, ReasonFromSweepWithRequest,
			"sets both request and from_sweep; a fit runs its own request or binds the sweep's winner, not both", nil)
	case !scope.Present:
		return fitInvalid(i, f.Name, ReasonFromSweepNoSweep,
			"binds from_sweep, but the batch has no sweep; add a ranked sweep or give the fit a request", nil)
	case !scope.Ranked:
		return fitInvalid(i, f.Name, ReasonFromSweepNoRank,
			"binds from_sweep, but the sweep has no rank; add sweep.rank so one slot is the winner", nil)
	}
	var first *types.RegressionSpec
	if len(scope.Slots) > 0 {
		first = SweepRegression(scope.Slots[0], reg)
	}
	if first == nil {
		return fitInvalid(i, f.Name, ReasonFromSweepRegressionUnknown,
			fmt.Sprintf("binds from_sweep regression %q, but the sweep body holds no regression of that name", reg),
			map[string]any{"regression": reg, "available": sweepRegressionNames(scope.Slots)})
	}
	for s := 1; s < len(scope.Slots); s++ {
		other := SweepRegression(scope.Slots[s], reg)
		if other == nil || !sameSweepSignature(first, other) {
			return fitInvalid(i, f.Name, ReasonFromSweepSignatureVaries,
				fmt.Sprintf("binds from_sweep regression %q, whose type, target or predictors differ in sweep slot %d; keep them out of the sweep axes", reg, s),
				map[string]any{"regression": reg, "slot": s})
		}
	}
	if scope.Reads != nil {
		for s, slot := range scope.Slots {
			if scope.Reads(slot, f.Name) {
				return fitInvalid(i, f.Name, ReasonFromSweepCycle,
					fmt.Sprintf("is bound from the sweep, but sweep slot %d reads it; the sweep slots run before the fit exists", s),
					map[string]any{"regression": reg, "slot": s})
			}
		}
	}
	return nil
}

// sameSweepSignature reports whether a and b bind the same static
// signature: type, target, predictors and (a model regression's)
// declared term catalog.
func sameSweepSignature(a, b *types.RegressionSpec) bool {
	if a.Type != b.Type || a.Target != b.Target || !slices.Equal(a.Predictors, b.Predictors) {
		return false
	}
	ta, oka := ModelRegressionTerms(a)
	tb, okb := ModelRegressionTerms(b)
	return oka == okb && slices.Equal(ta, tb)
}

// sweepRegressionNames are the named regressions of the first sweep
// slot, in order (non-nil).
func sweepRegressionNames(slots []*types.Request) []string {
	out := []string{}
	if len(slots) == 0 || slots[0] == nil {
		return out
	}
	for _, r := range slots[0].Regressions {
		if r != nil && r.Name != "" {
			out = append(out, r.Name)
		}
	}
	return out
}
