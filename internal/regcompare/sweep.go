package regcompare

import (
	"maps"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// sweep.go is a comparison reading a sweep-bound fit (`fits[].from_sweep`,
// U69 #643). The fit is refitted on the rank-1 sweep slot, known only
// once the sweep has run, so the rule splits in two:
//
//   - statically (CheckSweep — predict and the runtime's pre-fit rule,
//     over the same expanded slots) the comparison is judged against
//     the fit request of EVERY slot that could win
//     (model.SweepFitRequest; two from_sweep sides bind the same slot).
//     Every slot refusing is a refusal — the first slot's, unchanged.
//     Some refusing, or the passing slots resolving another test or
//     distribution, is Unresolved: the Plan is the first passing slot's
//     and predict lists the refusing slots. The regression's type,
//     target and predictors are axis-invariant (model.CheckFitsIn's
//     from_sweep_signature_varies), so the terms rules (not_nested,
//     no_extra_terms, type_*) never vary by slot; what varies is what an
//     axis can reach — the rows (request_mismatch), a modifier
//     (requires), the family and so the test. An adaptive sweep's later
//     slots are judged at their representative point, as every static
//     rule judges them.
//   - at run (Rebind) every sweep-bound plan is judged again on the
//     winner's fit request; a refusal there fails the comparison alone.

// Unresolved is a sweep-bound comparison's undecided part.
type Unresolved struct {
	// Slots is the number of sweep slots judged.
	Slots int
	// Refused are the slots whose fit request the comparison refuses,
	// in slot order.
	Refused []SlotRefusal
	// Varies names the resolved parts the passing slots disagree on:
	// "test", "distribution".
	Varies []string
}

// SlotRefusal is one sweep slot a comparison refuses: its index among
// the sweep slots (RankEntry.Slot's numbering), its label and the
// refusal's details.reason.
type SlotRefusal struct {
	Slot   int
	Label  string
	Reason string
}

// sweepSides are the from_sweep fits comparison c names, in
// restricted, full order (a name naming none is fitOf's to refuse).
func sweepSides(c *types.FitComparisonSpec, byName map[string]*types.FitSpec) []string {
	var out []string
	for _, name := range []string{c.Restricted, c.Full} {
		if f := byName[name]; f != nil && f.FromSweep != nil && (len(out) == 0 || out[0] != name) {
			out = append(out, name)
		}
	}
	return out
}

// checkEntry judges comparison i: checkOne as the fits read, or — a
// side bound from the sweep and slots given — over every slot.
func checkEntry(i int, c *types.FitComparisonSpec, byName map[string]*types.FitSpec, available []string, slots []*types.Request, defaultWeight *types.WeightSpec, declared func(types.RegressionType) bool) (Plan, *errors.CodedError) {
	bound := sweepSides(c, byName)
	if len(bound) == 0 || len(slots) == 0 {
		p, err := checkOne(i, c, byName, available, defaultWeight, declared)
		p.SweepBound = len(bound) > 0
		return p, err
	}
	var (
		plan    *Plan
		first   *errors.CodedError
		refused []SlotRefusal
		varies  []string
	)
	for s, slot := range slots {
		view := maps.Clone(byName)
		for _, name := range bound {
			f := *byName[name]
			f.Request = model.SweepFitRequest(slot, f.FromSweep.Regression)
			view[name] = &f
		}
		p, err := checkOne(i, c, view, available, defaultWeight, declared)
		if err != nil {
			if first == nil {
				first = err
			}
			reason, _ := err.Details["reason"].(string)
			label := ""
			if slot != nil {
				label = slot.Label
			}
			refused = append(refused, SlotRefusal{Slot: s, Label: label, Reason: reason})
			continue
		}
		if plan == nil {
			plan = &p
			continue
		}
		if p.Test != plan.Test {
			varies = addOnce(varies, "test")
		}
		if p.Distribution != plan.Distribution {
			varies = addOnce(varies, "distribution")
		}
	}
	if plan == nil {
		return Plan{Index: i, Name: c.Name, Restricted: c.Restricted, Full: c.Full}, first
	}
	plan.SweepBound = true
	if len(refused) > 0 || len(varies) > 0 {
		plan.Unresolved = &Unresolved{Slots: len(slots), Refused: refused, Varies: varies}
	}
	return *plan, nil
}

func addOnce(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

// Rebind is the run-time half of a sweep-bound comparison: each plan
// with SweepBound is judged again over fits as bound — every from_sweep
// entry's Request the winning slot's fit request (model.SweepFitRequest
// of the rank-1 slot, composeFitPass.bindWinner's) — under the same
// default weight and declared predicate. It returns the plans (a copy;
// a passing one replaced by its winner's plan) and each refused
// entry's error by index (nil when none refuses). Other plans pass
// through untouched.
func Rebind(compare []types.FitComparisonSpec, plans []Plan, fits []types.FitSpec, defaultWeight *types.WeightSpec, declared func(types.RegressionType) bool) ([]Plan, map[int]*errors.CodedError) {
	out := make([]Plan, len(plans))
	copy(out, plans)
	var refused map[int]*errors.CodedError
	byName, available := indexFits(fits)
	for k, p := range plans {
		if !p.SweepBound || p.Index < 0 || p.Index >= len(compare) {
			continue
		}
		np, err := checkOne(p.Index, &compare[p.Index], byName, available, defaultWeight, declared)
		if err != nil {
			if refused == nil {
				refused = map[int]*errors.CodedError{}
			}
			refused[k] = err
			continue
		}
		np.SweepBound = true
		out[k] = np
	}
	return out, refused
}
