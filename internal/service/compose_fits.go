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
// parallel alike. No fits returns all nils: nothing is bound and
// ComposedResponse.Fits stays absent.
//
// A fit's `save` (FitSpec.Save) is written once the fit has run, under
// the fit's name; a save on the fit request's own regression is written
// by its Process. Each overwrite comes back as a PULSE_MODEL_OVERWRITTEN
// warning with details.fit, in entry order — ComposedResponse.Warnings.
func (s *Service) runComposeFits(ctx context.Context, fits []types.FitSpec, pre *composeModels, workers int) (*model.Set, map[string]*types.RegressionResult, []*types.ResponseWarning, error) {
	n := len(fits)
	if n == 0 {
		return nil, nil, nil, nil
	}
	results := make([]*types.RegressionResult, n)
	saveWarnings := make([][]*types.ResponseWarning, n)
	errs := make([]error, n)
	run := func(ctx context.Context, i int) {
		if fitRunObserver != nil {
			fitRunObserver(fits[i].Name)
		}
		// The fit request's own `models`, loaded by the pre-pass: a fit
		// runs before the batch binds anything, so they are all it sees.
		fitSet, err := pre.fitSet(i)
		if err != nil {
			errs[i] = err
			return
		}
		resp, err := s.Process(withModelsBound(ctx, fitSet), model.FitRequest(fits[i]))
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
		// The fit's own response is not returned, so a model-file
		// overwrite its regression's `save` reported is lifted to the
		// batch (details.fit), then the fit's own `save` runs.
		for _, w := range resp.Warnings {
			if w != nil && w.Code == string(errors.PULSE_MODEL_OVERWRITTEN) {
				saveWarnings[i] = append(saveWarnings[i], fitWarning(w, i))
			}
		}
		w, err := s.saveFitModel(ctx, i, fits[i], results[i])
		if err != nil {
			errs[i] = err
			return
		}
		if w != nil {
			saveWarnings[i] = append(saveWarnings[i], w)
		}
	}

	if workers <= 1 {
		for i := range fits {
			if err := ctx.Err(); err != nil {
				return nil, nil, nil, err
			}
			if run(ctx, i); errs[i] != nil {
				return nil, nil, nil, fitFailure(fits, i, errs[i])
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
			return nil, nil, nil, fitFailure(fits, w, errs[w])
		}
		if err := ctx.Err(); err != nil {
			return nil, nil, nil, err
		}
	}

	set := &model.Set{}
	out := make(map[string]*types.RegressionResult, n)
	var warnings []*types.ResponseWarning
	for i, f := range fits {
		b, err := model.Bind(f.Name, f.Request.Regressions[0], results[i])
		if err != nil {
			return nil, nil, nil, fitFailure(fits, i, err)
		}
		b.Source = model.FitSource(i)
		if err := set.Add(b); err != nil {
			// CheckFits already refused a duplicate name; reaching
			// here is an invariant violation, still reported coded.
			return nil, nil, nil, fitFailure(fits, i, err)
		}
		out[f.Name] = results[i]
		// Entry order, serial and parallel alike.
		warnings = append(warnings, saveWarnings[i]...)
	}
	return set, out, warnings, nil
}

// fitWarning copies w with details.fit = i.
func fitWarning(w *types.ResponseWarning, i int) *types.ResponseWarning {
	details := make(map[string]any, len(w.Details)+1)
	for k, v := range w.Details {
		details[k] = v
	}
	details["fit"] = i
	return &types.ResponseWarning{Code: w.Code, Message: w.Message, Details: details}
}

// fitFailure locates fit i's error: a coded error keeps its code and
// message and gains details.fit; any other error is wrapped with the
// fit's name.
func fitFailure(fits []types.FitSpec, i int, err error) error {
	return fmt.Errorf("fit %q: %w", fits[i].Name, descx.RefusalAt(err, "fit", i))
}

// bindSlotModels returns slot ctx carrying the slot's final bound-model
// set — the batch's fits plus the slot's own loaded `models` — marked
// loaded, so the slot's Process does not load them again.
func bindSlotModels(ctx context.Context, set *model.Set, req *types.Request) context.Context {
	ctx = withModelsBound(ctx, set)
	if slotModelsObserver != nil && req != nil {
		slotModelsObserver(req.Label, model.SetFrom(ctx))
	}
	return ctx
}

// composeModels is the Compose pre-pass's loaded model files: each fit
// request's and each slot's `models`, read and decoded before any fit
// or slot runs, index-aligned with the batch's fits and slots.
type composeModels struct {
	fits     []types.FitSpec
	requests []*types.Request
	fitFiles [][]*model.File
	slots    [][]*model.File
}

// composeModelsPrepass runs the batch rule (model.CheckComposeBatch —
// the one PredictCompose runs) and then reads every `models` file, fit
// requests first, then slots, each refusal located by details.fit /
// details.request.
func (s *Service) composeModelsPrepass(composed *types.ComposedRequest) (*composeModels, error) {
	if cerr := model.CheckComposeBatch(composed.Fits, composed.Requests); cerr != nil {
		return nil, cerr
	}
	pre := &composeModels{
		fits:     composed.Fits,
		requests: composed.Requests,
		fitFiles: make([][]*model.File, len(composed.Fits)),
		slots:    make([][]*model.File, len(composed.Requests)),
	}
	for i, f := range composed.Fits {
		if f.Request == nil {
			continue
		}
		files, err := s.loadModelFiles(f.Request.Models)
		if err != nil {
			return nil, descx.RefusalAt(err, "fit", i)
		}
		pre.fitFiles[i] = files
	}
	for k, req := range composed.Requests {
		if req == nil {
			continue
		}
		files, err := s.loadModelFiles(req.Models)
		if err != nil {
			return nil, descx.RefusalAt(err, "request", k)
		}
		pre.slots[k] = files
	}
	return pre, nil
}

// fitSet is fit i's bound-model set: its request's own loaded models
// (nil when it loads none).
func (p *composeModels) fitSet(i int) (*model.Set, error) {
	if p == nil || p.fits[i].Request == nil || len(p.fits[i].Request.Models) == 0 {
		return nil, nil
	}
	return (*model.Set)(nil).WithModels(p.fits[i].Request.Models, p.fitFiles[i])
}

// slotSet is slot k's bound-model set: the batch's fits plus the slot's
// own loaded models.
func (p *composeModels) slotSet(fits *model.Set, k int) (*model.Set, error) {
	if p == nil || p.requests[k] == nil || len(p.requests[k].Models) == 0 {
		return fits, nil
	}
	set, err := fits.WithModels(p.requests[k].Models, p.slots[k])
	if err != nil {
		return nil, descx.RefusalAt(err, "request", k)
	}
	return set, nil
}
