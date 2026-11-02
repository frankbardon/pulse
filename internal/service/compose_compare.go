package service

import (
	"context"
	"sync"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/regcompare"
	"github.com/frankbardon/pulse/internal/slotfail"
	"github.com/frankbardon/pulse/types"
)

// compareRefusal is the batch's `compare` rules (regcompare.Check,
// predict's too) under this instance's default weight: each
// comparison's plan (index-aligned) when every entry passes, nil
// without any. It runs right after the optimize blocks' rules, before
// any fit or slot — the point predict (validateComposeExpanded) runs it.
func (s *Service) compareRefusal(composed *types.ComposedRequest) ([]regcompare.Plan, error) {
	plans, cerr := regcompare.Check(composed.Compare, composed.Fits, s.defaultWeight)
	if cerr != nil {
		return nil, cerr
	}
	return plans, nil
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
}

// newComparisonFigures is the collector for compare's fits, nil without
// any comparison.
func newComparisonFigures(compare []types.FitComparisonSpec) *comparisonFigures {
	if len(compare) == 0 {
		return nil
	}
	return &comparisonFigures{names: regcompare.FitNames(compare), figs: map[string][]*model.RegressionFigures{}}
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
// No comparison returns all nils.
func (s *Service) runComposeCompare(plans []regcompare.Plan, fits map[string]*types.FitResult, figs *comparisonFigures) ([]types.FitComparisonResult, []*types.ResponseWarning, error) {
	if len(plans) == 0 {
		return nil, nil, nil
	}
	reg := func(name string) *types.RegressionResult {
		if f := fits[name]; f != nil {
			return f.RegressionResult
		}
		return nil
	}
	out := make([]types.FitComparisonResult, len(plans))
	var warnings []*types.ResponseWarning
	for i, p := range plans {
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
