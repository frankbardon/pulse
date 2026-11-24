package service

import (
	"context"
	"maps"
	"slices"
	"strconv"
	"sync"

	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/regcompare"
	"github.com/frankbardon/pulse/internal/slotfail"
	"github.com/frankbardon/pulse/types"
)

// compareRefusal is the batch's `compare` rules (regcompare.CheckSweep,
// predict's too, over the same expanded sweep slots) under this
// instance's default weight: each comparison's plan (index-aligned)
// when every entry passes, nil without any. It runs right after the
// optimize blocks' rules, before any fit or slot — the point predict
// (validateComposeExpanded) runs it.
func (s *Service) compareRefusal(composed *types.ComposedRequest, ext *composeExpansion) ([]regcompare.Plan, error) {
	var slots []*types.Request
	if ext != nil {
		slots = ext.sweepFits.Slots
	}
	plans, cerr := regcompare.CheckSweep(composed.Compare, composed.Fits, slots, s.defaultWeight, descx.ComparableRegressions(s.instance, nil))
	if cerr != nil {
		return nil, cerr
	}
	return plans, nil
}

// rebindComparisons judges every sweep-bound comparison again on the
// winning sweep slot (regcompare.Rebind) once phase 0 has ranked it:
// each from_sweep fit read as model.SweepFitRequest of the rank-1 slot,
// the request composeFitPass.bindWinner refitted. It returns the plans
// to run and each refused entry's per-slot error by index — the
// refusal's own code (PULSE_COMPARISON_INVALID), message and details
// plus details.sweep_slot / sweep_label naming the winner. Without a
// sweep-bound plan, or with an empty ranking (the bound fit failed, and
// the comparison fails alone on its code in Compute), the plans pass
// through.
func (s *Service) rebindComparisons(plans []regcompare.Plan, composed *types.ComposedRequest, ext *composeExpansion) ([]regcompare.Plan, map[int]*types.SlotError) {
	if !slices.ContainsFunc(plans, func(p regcompare.Plan) bool { return p.SweepBound }) ||
		ext == nil || ext.rank == nil || !ext.rank.done || len(ext.rank.ranked) == 0 {
		return plans, nil
	}
	win := ext.rank.ranked[0]
	k := ext.rank.explicit + win.Slot
	if k < 0 || k >= len(composed.Requests) {
		return plans, nil
	}
	fits := slices.Clone(composed.Fits)
	for i, f := range fits {
		if f.FromSweep != nil {
			fits[i].Request = model.SweepFitRequest(composed.Requests[k], f.FromSweep.Regression)
		}
	}
	out, refused := regcompare.Rebind(composed.Compare, plans, fits, s.defaultWeight, descx.ComparableRegressions(s.instance, nil))
	if len(refused) == 0 {
		return out, nil
	}
	errs := make(map[int]*types.SlotError, len(refused))
	for i, ce := range refused {
		se := &types.SlotError{Code: string(ce.Code), Message: ce.Message, Details: maps.Clone(ce.Details)}
		if se.Details == nil {
			se.Details = map[string]any{}
		}
		se.Details["sweep_slot"] = win.Slot
		se.Details["sweep_label"] = win.Label
		se.Message = "the winning sweep slot " + strconv.Quote(win.Label) + ": " + se.Message
		errs[i] = se
	}
	return out, errs
}

// comparisonFigures collects the comparison figures (model.RegressionFigures)
// of every fit a batch's comparisons read: a Compose run carries it on
// the fit pre-pass's ctx (withComparisonFigures); each such regression
// fit then runs with a model.VectorCapture on its bound set, its engine
// offers its figures there at finalize (regression.OfferingFigures, read
// by the processor off the set), and the pass records them here by fit
// name. Nothing reaches a request, a result or the public API, so no
// fit's bytes move and the caller's request is never touched. Safe for
// the concurrent fits of ComposeParallel.
type comparisonFigures struct {
	names map[string]bool
	mu    sync.Mutex
	figs  map[string][]*model.RegressionFigures
	// vectors are the bound states of the vector fits a battery
	// comparison reads (recordComparisonVector), by fit name.
	vectors map[string]*model.VectorFit
}

// newComparisonFigures is the collector for compare's fits, nil without
// any comparison.
func newComparisonFigures(compare []types.FitComparisonSpec) *comparisonFigures {
	if len(compare) == 0 {
		return nil
	}
	return &comparisonFigures{names: regcompare.FitNames(compare), figs: map[string][]*model.RegressionFigures{}, vectors: map[string]*model.VectorFit{}}
}

type comparisonFiguresKey struct{}

// withComparisonFigures returns ctx carrying cf (ctx itself for nil).
func withComparisonFigures(ctx context.Context, cf *comparisonFigures) context.Context {
	if cf == nil {
		return ctx
	}
	return context.WithValue(ctx, comparisonFiguresKey{}, cf)
}

// captureFor returns set with a fresh capture (and the capture) when ctx's
// collector wants fit name's figures; set and nil otherwise.
func captureFor(ctx context.Context, name string, set *model.Set) (*model.Set, *model.VectorCapture) {
	cf, _ := ctx.Value(comparisonFiguresKey{}).(*comparisonFigures)
	if cf == nil || !cf.names[name] {
		return set, nil
	}
	c := &model.VectorCapture{}
	return set.WithCapture(c), c
}

// record files fit name's offered figures (ctx's collector; a no-op
// without one or a capture).
func recordComparisonFigures(ctx context.Context, name string, c *model.VectorCapture) {
	cf, _ := ctx.Value(comparisonFiguresKey{}).(*comparisonFigures)
	if cf == nil || c == nil {
		return
	}
	cf.mu.Lock()
	cf.figs[name] = c.RegressionFigures()
	cf.mu.Unlock()
}

// recordComparisonVector files vector fit name's closed state when a
// comparison reads it (ctx's collector; a no-op without one).
func recordComparisonVector(ctx context.Context, name string, vf *model.VectorFit) {
	cf, _ := ctx.Value(comparisonFiguresKey{}).(*comparisonFigures)
	if cf == nil || vf == nil || !cf.names[name] {
		return
	}
	cf.mu.Lock()
	cf.vectors[name] = vf
	cf.mu.Unlock()
}

// vector is vector fit name's recorded state (nil when none: a failed
// fit).
func (cf *comparisonFigures) vector(name string) *model.VectorFit {
	if cf == nil {
		return nil
	}
	cf.mu.Lock()
	defer cf.mu.Unlock()
	return cf.vectors[name]
}

// of is fit name's figures: nil when none was offered (a failed fit),
// and an invariant violation when its engine offered more than once.
func (cf *comparisonFigures) of(name string) (*model.RegressionFigures, error) {
	if cf == nil {
		return nil, nil
	}
	cf.mu.Lock()
	defer cf.mu.Unlock()
	got := cf.figs[name]
	switch len(got) {
	case 0:
		return nil, nil
	case 1:
		return got[0], nil
	}
	return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
		"fit "+name+" offered its comparison figures more than once",
		map[string]any{"fit": name, "offers": len(got)})
}

// runComposeCompare runs every comparison once the fits have run (fits:
// the batch's ComposedResponse.Fits entries), in declaration order,
// reading no record. A comparison that fails on its data — a fit it
// reads failed on its data, or the fits' row counts differ — fails
// alone: its result keeps {name, restricted, full, test, error} and a
// PULSE_SLOT_FAILED warning (slot_kind comparisons, the entry index and
// name) lifts to the batch; under Strict the batch fails with the
// comparison's own code instead. Any other fault is fatal. Serial and
// ComposeParallel call it identically, so both answer the same bytes.
// A sweep-bound comparison is first judged again on the winning slot
// (rebindComparisons): a refusal there fails it alone the same way, its
// own PULSE_COMPARISON_INVALID naming the winner. No comparison returns
// all nils.
func (s *Service) runComposeCompare(plans []regcompare.Plan, fits map[string]*types.FitResult, figs *comparisonFigures, composed *types.ComposedRequest, ext *composeExpansion) ([]types.FitComparisonResult, []*types.ResponseWarning, error) {
	if len(plans) == 0 {
		return nil, nil, nil
	}
	plans, refused := s.rebindComparisons(plans, composed, ext)
	reg := func(name string) *types.RegressionResult {
		if f := fits[name]; f != nil {
			return f.RegressionResult
		}
		return nil
	}
	out := make([]types.FitComparisonResult, len(plans))
	var warnings []*types.ResponseWarning
	for i, p := range plans {
		if se := refused[i]; se != nil {
			// The winning sweep slot's fits do not compare: the
			// comparison fails alone, before any figure is read.
			out[i] = types.FitComparisonResult{Name: p.Name, Restricted: p.Restricted, Full: p.Full, Test: p.Test, Error: se}
			f := slotfail.Failure{Kind: slotfail.KindComparisons, Index: i, Label: p.Name, Err: se}
			if s.strict {
				return nil, nil, slotfail.StrictError(f)
			}
			warnings = append(warnings, slotfail.Warning(f))
			continue
		}
		if p.Battery {
			// Two vector fits: member-wise from the bound co-moments.
			vec := func(name string) *types.VectorFitResult {
				if f := fits[name]; f != nil {
					return f.Vector
				}
				return nil
			}
			res, err := regcompare.ComputeBattery(p, vec(p.Restricted), vec(p.Full), figs.vector(p.Restricted), figs.vector(p.Full))
			if err != nil {
				return nil, nil, err
			}
			out[i] = res
			if res.Error != nil {
				f := slotfail.Failure{Kind: slotfail.KindComparisons, Index: i, Label: p.Name, Err: res.Error}
				if s.strict {
					return nil, nil, slotfail.StrictError(f)
				}
				warnings = append(warnings, slotfail.Warning(f))
			}
			continue
		}
		g0, err := figs.of(p.Restricted)
		if err != nil {
			return nil, nil, err
		}
		g1, err := figs.of(p.Full)
		if err != nil {
			return nil, nil, err
		}
		res, err := regcompare.Compute(p, reg(p.Restricted), reg(p.Full), g0, g1)
		if err != nil {
			return nil, nil, err
		}
		out[i] = res
		if res.Error != nil {
			f := slotfail.Failure{Kind: slotfail.KindComparisons, Index: i, Label: p.Name, Err: res.Error}
			if s.strict {
				return nil, nil, slotfail.StrictError(f)
			}
			warnings = append(warnings, slotfail.Warning(f))
		}
	}
	return out, warnings, nil
}
