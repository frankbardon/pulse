package service

import (
	"context"
	"fmt"
	"sync"

	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/internal/slotfail"
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
// A vector fit's request runs with a model.VectorCapture on its bound
// set (model.Set.WithCapture): the battery — its one MAT_* slot or
// fitted attribute — offers its closed state there at finalize, on
// whichever arm runs it, and exactly one offer is bound
// (model.Set.AddVector); its ComposedResponse.Fits entry is the
// processing.VectorFitResult summary.
//
// A fit's `save` (FitSpec.Save) is written once the fit has run, under
// the fit's name — a regression fit's model file, a vector fit's
// population file; a save on the fit request's own regression is
// written by its Process. Each overwrite comes back as a PULSE_MODEL_OVERWRITTEN
// warning with details.fit, in entry order — ComposedResponse.Warnings.
func (s *Service) runComposeFits(ctx context.Context, fits []types.FitSpec, pre *composeModels, workers int) (*model.Set, map[string]*types.FitResult, []*types.ResponseWarning, error) {
	n := len(fits)
	if n == 0 {
		// Nothing fitted: every slot layers over the registered
		// populations alone.
		return s.populations, nil, nil, nil
	}
	results := make([]*types.RegressionResult, n)
	vectorFits := make([]*model.VectorFit, n)
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
		if model.FitKindOf(fits[i].Request) == types.FitKindVector {
			vf, err := s.runVectorFit(ctx, fits[i], fitSet)
			if err != nil {
				errs[i] = err
				return
			}
			vectorFits[i] = vf
			// The fit's `save` writes its population file under the
			// fit's name (the overwrite warning lifts to the batch).
			w, err := s.saveFitPopulation(ctx, i, fits[i], vf)
			if err != nil {
				errs[i] = err
				return
			}
			if w != nil {
				saveWarnings[i] = append(saveWarnings[i], w)
			}
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
		if se := resp.Regressions[0].Error; se != nil {
			// A fit that failed on its data has no model to bind: the
			// fit fails with its own code, as before per-slot results.
			errs[i] = slotfail.AsError(se)
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

	// The fits bind over the instance's registered populations
	// (CheckComposeBatch refused a fit name one already binds).
	set := s.populations.Clone()
	out := make(map[string]*types.FitResult, n)
	var warnings []*types.ResponseWarning
	for i, f := range fits {
		if vf := vectorFits[i]; vf != nil {
			vf.Name, vf.Source = f.Name, model.FitSource(i)
			summary, err := processing.VectorFitResult(vf)
			if err != nil {
				return nil, nil, nil, fitFailure(fits, i, err)
			}
			if err := set.AddVector(vf); err != nil {
				return nil, nil, nil, fitFailure(fits, i, err)
			}
			out[f.Name] = &types.FitResult{Vector: summary}
			warnings = append(warnings, saveWarnings[i]...)
			continue
		}
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
		out[f.Name] = &types.FitResult{RegressionResult: results[i]}
		// Entry order, serial and parallel alike.
		warnings = append(warnings, saveWarnings[i]...)
	}
	return set, out, warnings, nil
}

// runVectorFit runs a vector fit request with a capture on its bound
// set and returns the one closed state its battery offered (unnamed:
// the caller binds it). No offer, or more than one, is an engine
// invariant violation (PROCESSING_INTERNAL) — CheckFits admitted one
// battery.
func (s *Service) runVectorFit(ctx context.Context, f types.FitSpec, fitSet *model.Set) (*model.VectorFit, error) {
	capture := &model.VectorCapture{}
	if _, err := s.Process(withModelsBound(ctx, fitSet.WithCapture(capture)), model.FitRequest(f)); err != nil {
		return nil, err
	}
	got := capture.Fits()
	if len(got) != 1 {
		return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
			fmt.Sprintf("vector fit %q closed %d battery states; a vector fit closes exactly one", f.Name, len(got)),
			map[string]any{"name": f.Name, "states": len(got)})
	}
	return got[0], nil
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

// composeModels is the Compose pre-pass's loaded files: each fit
// request's and each slot's `models` and `populations`, read and
// decoded before any fit or slot runs, index-aligned with the batch's
// fits and slots.
type composeModels struct {
	// base is the instance's registered populations: what a fit
	// request sees bound beneath its own entries (nil for none).
	base     *model.Set
	fits     []types.FitSpec
	requests []*types.Request
	fitFiles []*model.Loaded
	slots    []*model.Loaded
}

// composeModelsPrepass runs the batch rule (model.CheckComposeBatch —
// the one PredictCompose runs) and then reads every `models` and
// `populations` file, fit requests first, then slots, each refusal
// located by details.fit / details.request.
func (s *Service) composeModelsPrepass(composed *types.ComposedRequest) (*composeModels, error) {
	if cerr := model.CheckComposeBatch(composed.Fits, composed.Requests, s.populationCatalog()); cerr != nil {
		return nil, cerr
	}
	pre := &composeModels{
		base:     s.populations,
		fits:     composed.Fits,
		requests: composed.Requests,
		fitFiles: make([]*model.Loaded, len(composed.Fits)),
		slots:    make([]*model.Loaded, len(composed.Requests)),
	}
	for i, f := range composed.Fits {
		if f.Request == nil {
			continue
		}
		files, err := s.loadModelFiles(model.RequestRefs(f.Request))
		if err != nil {
			return nil, descx.RefusalAt(err, "fit", i)
		}
		pre.fitFiles[i] = files
	}
	for k, req := range composed.Requests {
		if req == nil {
			continue
		}
		files, err := s.loadModelFiles(model.RequestRefs(req))
		if err != nil {
			return nil, descx.RefusalAt(err, "request", k)
		}
		pre.slots[k] = files
	}
	return pre, nil
}

// fitSet is fit i's bound-model set: the registered populations plus
// its request's own loaded models and populations (nil when neither
// binds anything).
func (p *composeModels) fitSet(i int) (*model.Set, error) {
	if p == nil {
		return nil, nil
	}
	if p.fits[i].Request == nil {
		return p.base, nil
	}
	refs := model.RequestRefs(p.fits[i].Request)
	if refs.Empty() {
		return p.base, nil
	}
	return p.base.WithLoaded(refs, p.fitFiles[i])
}

// slotSet is slot k's bound-model set: the batch's fits plus the slot's
// own loaded models and populations.
func (p *composeModels) slotSet(fits *model.Set, k int) (*model.Set, error) {
	if p == nil || p.requests[k] == nil {
		return fits, nil
	}
	refs := model.RequestRefs(p.requests[k])
	if refs.Empty() {
		return fits, nil
	}
	set, err := fits.WithLoaded(refs, p.slots[k])
	if err != nil {
		return nil, descx.RefusalAt(err, "request", k)
	}
	return set, nil
}
