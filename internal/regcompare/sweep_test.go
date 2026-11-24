package regcompare

import (
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// sweepSlot is one expanded sweep slot: rows filtered to x ∈ [0, hi],
// holding regressions "small" (y ~ a) and "big" (y ~ a + b) — built by
// mk so a test can vary the family or a modifier per slot.
func sweepSlot(label, hi string, mk func(name string, preds ...string) *types.RegressionSpec) *types.Request {
	return &types.Request{
		Label:     label,
		Cohort:    &types.Cohort{Filename: "c.pulse"},
		Filterers: []*types.Filterer{{Type: types.FILTER_RANGE, Field: "x", Values: []string{"0", hi}}},
		Regressions: []*types.RegressionSpec{
			mk("small", "a"),
			mk("big", "a", "b"),
		},
	}
}

func olsNamed(name string, preds ...string) *types.RegressionSpec {
	s := ols(preds...)
	s.Name = name
	return s
}

// sweepFits are the batch fits: m0 / m1 bound from the sweep's
// regressions r0 / r1 ("" makes that side the request-bound req).
func sweepFits(r0, r1 string, req *types.Request) []types.FitSpec {
	side := func(name, reg string) types.FitSpec {
		if reg == "" {
			return types.FitSpec{Name: name, Request: req}
		}
		return types.FitSpec{Name: name, FromSweep: &types.FitFromSweep{Regression: reg}}
	}
	return []types.FitSpec{side("m0", r0), side("m1", r1)}
}

// filtered is a request-bound OLS fit over rows x ∈ [0, hi].
func filtered(hi string, preds ...string) *types.Request {
	r := fitReq(ols(preds...))
	r.Filterers = []*types.Filterer{{Type: types.FILTER_RANGE, Field: "x", Values: []string{"0", hi}}}
	return r
}

func threeSlots() []*types.Request {
	return []*types.Request{sweepSlot("hi_80", "80", olsNamed), sweepSlot("hi_40", "40", olsNamed), sweepSlot("hi_60", "60", olsNamed)}
}

// TestCheckSweep_EverySlotRefuses: a comparison every sweep slot refuses
// is refused statically with the first slot's error, unchanged — the
// swapped sides (terms are axis-invariant) and a request-bound side
// whose rows no slot matches.
func TestCheckSweep_EverySlotRefuses(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		fits   []types.FitSpec
		reason string
	}{
		{"swapped", sweepFits("big", "small", nil), "not_nested"},
		{"no slot's rows", sweepFits("", "big", filtered("50", "a")), "request_mismatch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := CheckSweep(one(""), tc.fits, threeSlots(), nil, nil)
			if err == nil || err.Code != errors.PULSE_COMPARISON_INVALID || err.Details["reason"] != tc.reason {
				t.Fatalf("got %v, want reason %s", err, tc.reason)
			}
		})
	}
}

// TestCheckSweep_Resolved: two sides bound from the same sweep compare
// in every slot (one winner binds both) — no Unresolved, SweepBound set,
// the plan the request-bound twin's.
func TestCheckSweep_Resolved(t *testing.T) {
	t.Parallel()
	plans, err := CheckSweep(one(""), sweepFits("small", "big", nil), threeSlots(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	p := plans[0]
	if !p.SweepBound || p.Unresolved != nil || p.Test != types.ComparisonTestF || p.Distribution != types.ComparisonDistributionF ||
		!reflect.DeepEqual(p.Terms, []string{"b"}) {
		t.Fatalf("plan %+v", p)
	}
}

// TestCheckSweep_SomeSlotsRefuse: a request-bound side matching one
// slot's rows passes on that slot only — the plan is that slot's, and
// Unresolved lists the other slots with their reason.
func TestCheckSweep_SomeSlotsRefuse(t *testing.T) {
	t.Parallel()
	plans, err := CheckSweep(one(""), sweepFits("", "big", filtered("40", "a")), threeSlots(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := &Unresolved{Slots: 3, Refused: []SlotRefusal{{0, "hi_80", "request_mismatch"}, {2, "hi_60", "request_mismatch"}}}
	if p := plans[0]; !p.SweepBound || !reflect.DeepEqual(p.Unresolved, want) || p.Test != types.ComparisonTestF {
		t.Fatalf("plan %+v, unresolved %+v", p, p.Unresolved)
	}
}

// TestCheckSweep_Varies: a family that is a sweep axis resolves another
// default test (and distribution) per slot — Unresolved names both, the
// plan is the first slot's.
func TestCheckSweep_Varies(t *testing.T) {
	t.Parallel()
	fam := func(family string) func(string, ...string) *types.RegressionSpec {
		return func(name string, preds ...string) *types.RegressionSpec {
			s := glm(family, preds...)
			s.Name = name
			return s
		}
	}
	slots := []*types.Request{sweepSlot("pois", "80", fam("poisson")), sweepSlot("gamma", "80", fam("gamma"))}
	plans, err := CheckSweep(one(""), sweepFits("small", "big", nil), slots, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := &Unresolved{Slots: 2, Varies: []string{"test", "distribution"}}
	if p := plans[0]; !reflect.DeepEqual(p.Unresolved, want) || p.Test != types.ComparisonTestLRT || p.Family != "poisson" {
		t.Fatalf("plan %+v, unresolved %+v", p, p.Unresolved)
	}
}

// TestCheckSweep_NoSlotsJudgesTheRequest: without slots a from_sweep
// side is judged as its (resolved) request reads — Check's view.
func TestCheckSweep_NoSlotsJudgesTheRequest(t *testing.T) {
	t.Parallel()
	fits := sweepFits("", "big", filtered("40", "a"))
	fits[1].Request = filtered("40", "a", "b")
	plans, err := Check(one(""), fits, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !plans[0].SweepBound || plans[0].Unresolved != nil {
		t.Fatalf("plan %+v", plans[0])
	}
}

// TestRebind: the winner's fit requests decide — a winner whose rows
// match passes (its plan replaces the static one), one whose rows differ
// is refused with the rule's own error; a plan not bound from the sweep
// is never re-judged.
func TestRebind(t *testing.T) {
	t.Parallel()
	compare := append(one(""), types.FitComparisonSpec{Name: "plain", Restricted: "p0", Full: "p1"})
	static, err := CheckSweep(compare, append(sweepFits("", "big", filtered("40", "a")),
		types.FitSpec{Name: "p0", Request: fitReq(ols("a"))}, types.FitSpec{Name: "p1", Request: fitReq(ols("a", "b"))}), threeSlots(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if static[1].SweepBound {
		t.Fatalf("plain plan marked sweep-bound: %+v", static[1])
	}
	bound := func(hi string) []types.FitSpec {
		f := sweepFits("", "big", filtered("40", "a"))
		f[1].Request = filtered(hi, "a", "b")
		// p0 / p1 now differ, so a re-judged plain plan would refuse.
		return append(f, types.FitSpec{Name: "p0", Request: fitReq(ols("a"))}, types.FitSpec{Name: "p1", Request: fitReq(&types.RegressionSpec{Type: types.REG_OLS, Target: "z", Predictors: []string{"a", "b"}})})
	}
	plans, refused := Rebind(compare, static, bound("40"), nil, nil)
	if len(refused) != 0 || plans[0].Unresolved != nil || !plans[0].SweepBound || plans[0].Test != types.ComparisonTestF {
		t.Fatalf("winner 40: refused %v, plan %+v", refused, plans[0])
	}
	_, refused = Rebind(compare, static, bound("80"), nil, nil)
	if len(refused) != 1 || refused[0] == nil || refused[0].Code != errors.PULSE_COMPARISON_INVALID || refused[0].Details["reason"] != "request_mismatch" {
		t.Fatalf("winner 80: refused %v", refused)
	}
}
