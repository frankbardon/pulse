package service

import (
	"context"
	"fmt"
	"sync"

	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// fitRunObserver, when set (tests only), is told the name of every fit
// the pre-pass executes — the counter behind the run-once guarantee.
var fitRunObserver func(name string)

// slotModelsObserver, when set (tests only), is handed each Compose
// slot's label and the bound-model set its ctx carries, just before the
// slot's Process runs.
var slotModelsObserver func(label string, set *model.Set)

// runComposeFits is the Compose fit pre-pass: every ComposedRequest.Fits
// entry runs ONCE, before any slot, as an ordinary standalone Process
// (its own cohort, filters, features and weight — never a slot's), and
// its one regression is bound by name. workers > 1 runs the fits
// concurrently (ComposeParallel); the bound set and the results are
// identical either way, since each fit is an independent, deterministic
// Process. The structural rules (model.CheckFits) have already passed.
//
// A fit's own refusal keeps its code and gains details.fit — the
// 0-based entry index — the way a slot's gains details.request. With
// several failing fits the lowest-index one is reported, serial and
// parallel alike. No fits returns (nil, nil, nil): nothing is bound and
// ComposedResponse.Fits stays absent.
func (s *Service) runComposeFits(ctx context.Context, fits []types.FitSpec, workers int) (*model.Set, map[string]*types.RegressionResult, error) {
	n := len(fits)
	if n == 0 {
		return nil, nil, nil
	}
	results := make([]*types.RegressionResult, n)
	errs := make([]error, n)
	run := func(ctx context.Context, i int) {
		if fitRunObserver != nil {
			fitRunObserver(fits[i].Name)
		}
		resp, err := s.Process(ctx, model.FitRequest(fits[i]))
		if err != nil {
			errs[i] = err
			return
		}
		if resp == nil || len(resp.Regressions) != 1 || resp.Regressions[0] == nil {
			errs[i] = errors.NewCodedError(errors.PROCESSING_INTERNAL,
				fmt.Sprintf("fit %q produced no regression result", fits[i].Name))
			return
		}
		results[i] = resp.Regressions[0]
	}

	if workers <= 1 {
		for i := range fits {
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
			if run(ctx, i); errs[i] != nil {
				return nil, nil, fitFailure(fits, i, errs[i])
			}
		}
	} else {
		runCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		sem := make(chan struct{}, workers)
		var wg sync.WaitGroup
		var once sync.Once
		for i := range fits {
			if runCtx.Err() != nil {
				break
			}
			wg.Add(1)
			sem <- struct{}{}
			go func() {
				defer wg.Done()
				defer func() { <-sem }()
				if run(runCtx, i); errs[i] != nil {
					once.Do(cancel)
				}
			}()
		}
		wg.Wait()
		var failed []int
		for i, e := range errs {
			if e != nil {
				failed = append(failed, i)
			}
		}
		if len(failed) > 0 {
			w := failFastWinner(ctx, errs, failed)
			return nil, nil, fitFailure(fits, w, errs[w])
		}
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
	}

	set := &model.Set{}
	out := make(map[string]*types.RegressionResult, n)
	for i, f := range fits {
		b, err := model.Bind(f.Name, f.Request.Regressions[0], results[i])
		if err != nil {
			return nil, nil, fitFailure(fits, i, err)
		}
		if err := set.Add(b); err != nil {
			// CheckFits already refused a duplicate name; reaching
			// here is an invariant violation, still reported coded.
			return nil, nil, fitFailure(fits, i, err)
		}
		out[f.Name] = results[i]
	}
	return set, out, nil
}

// fitFailure locates fit i's error: a coded error keeps its code and
// message and gains details.fit; any other error is wrapped with the
// fit's name.
func fitFailure(fits []types.FitSpec, i int, err error) error {
	return fmt.Errorf("fit %q: %w", fits[i].Name, descx.RefusalAt(err, "fit", i))
}

// bindSlotModels returns slot ctx carrying the batch's bound-model set
// (unchanged when the batch declares no fits).
func bindSlotModels(ctx context.Context, set *model.Set, req *types.Request) context.Context {
	ctx = model.WithSet(ctx, set)
	if slotModelsObserver != nil && req != nil {
		slotModelsObserver(req.Label, model.SetFrom(ctx))
	}
	return ctx
}
