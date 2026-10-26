package service

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/processing"
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
// A batch with a sweep-bound fit runs the same pass in two halves
// around its sweep slots instead (runComposeFitPhases).
//
// A fit that fails on its DATA (a data-dependent refusal its fit
// request's Process turned into a failed slot — slotfail) fails alone:
// its ComposedResponse.Fits entry carries `error` (types.SlotError), its
// name binds as a failed fit (model.Set.AddFailed — no model, the
// static signature kept), its own PULSE_SLOT_FAILED warning lifts to
// the batch with details.fit, and no `save` file is written; every other
// fit and slot runs. A slot scoring from it reads an all-null column
// (ATTR_REG_PREDICT, PULSE_FIT_UNAVAILABLE); any other reader is refused
// with the fit's own code. Under Strict the fit request's Process
// already failed with that code, which fails the batch below.
//
// Any other fit refusal keeps its code and gains details.fit — the
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
// population file, a SEM fit's SEM fit file; a save on the fit request's own regression is
// written by its Process. Each overwrite comes back as a PULSE_MODEL_OVERWRITTEN
// warning with details.fit, in entry order — ComposedResponse.Warnings.
func (s *Service) runComposeFits(ctx context.Context, fits []types.FitSpec, pre *composeModels, workers int) (*model.Set, map[string]*types.FitResult, []*types.ResponseWarning, error) {
	if len(fits) == 0 {
		// Nothing fitted: every slot layers over the registered
		// populations alone.
		return s.populations, nil, nil, nil
	}
	pass := s.newComposeFitPass(fits, pre)
	all := pass.every()
	if err := pass.execute(ctx, workers, all); err != nil {
		return nil, nil, nil, err
	}
	return pass.bind(all)
}

// composeFitPass is one batch's fit pre-pass, split so a sweep-bound
// batch (compose_sweep_bind.go) can run its authored fits, then its
// sweep slots, then the fits bound from the sweep's winner: execute
// runs the fits a mask selects, bind binds every fit a mask selects
// into a fresh set. runComposeFits runs and binds every fit at once.
// Each per-fit slice is index-aligned with fits; a fit's state is
// written by its own execution only, so the pool needs no lock.
type composeFitPass struct {
	s    *Service
	fits []types.FitSpec
	pre  *composeModels
	// winnerFiles[i] is a from_sweep fit's winning slot's loaded
	// models (the pre-pass read them for that slot), the files its
	// fit request binds beneath it.
	winnerFiles []*model.Loaded
	// winnerAxes[i] is a from_sweep fit's winning slot's axis values
	// (RankEntry.Axes) — what its sweep_axis() reads — and sweepAxes
	// the batch's sweep's axis names, the static axes of a sweep-bound
	// fit that failed (no winner).
	winnerAxes []map[string]any
	sweepAxes  []string
	results    []*types.RegressionResult
	vectorFits []*model.VectorFit
	semFits    []*model.SEMFit
	// failed[i] is fit i's data failure (nil when it fitted), and
	// failedOp a failed vector fit's battery operator.
	failed       []*types.SlotError
	failedOp     []string
	saveWarnings [][]*types.ResponseWarning
	errs         []error
}

// newComposeFitPass is the pass over fits — a copy, so resolving a
// sweep-bound fit's request never touches the caller's list.
func (s *Service) newComposeFitPass(fits []types.FitSpec, pre *composeModels) *composeFitPass {
	n := len(fits)
	return &composeFitPass{
		s:            s,
		fits:         slices.Clone(fits),
		pre:          pre,
		winnerFiles:  make([]*model.Loaded, n),
		winnerAxes:   make([]map[string]any, n),
		results:      make([]*types.RegressionResult, n),
		vectorFits:   make([]*model.VectorFit, n),
		semFits:      make([]*model.SEMFit, n),
		failed:       make([]*types.SlotError, n),
		failedOp:     make([]string, n),
		saveWarnings: make([][]*types.ResponseWarning, n),
		errs:         make([]error, n),
	}
}

// every is the mask selecting every fit.
func (p *composeFitPass) every() []bool {
	m := make([]bool, len(p.fits))
	for i := range m {
		m[i] = true
	}
	return m
}

// fitSet is fit i's bound-model set: the pre-pass's, or — for a
// sweep-bound fit — the registered populations plus the winning slot's
// own loaded models and populations.
func (p *composeFitPass) fitSet(i int) (*model.Set, error) {
	if p.fits[i].FromSweep == nil || p.pre == nil {
		return p.pre.fitSet(i)
	}
	refs := model.RequestRefs(p.fits[i].Request)
	if refs.Empty() {
		return p.pre.base, nil
	}
	return p.pre.base.WithLoaded(refs, p.winnerFiles[i])
}

// run executes fit i into the pass's per-fit state.
func (p *composeFitPass) run(ctx context.Context, i int) {
	s, fits := p.s, p.fits
	if fitRunObserver != nil {
		fitRunObserver(fits[i].Name)
	}
	// The fit request's own `models`, loaded by the pre-pass: a fit
	// runs before the batch binds anything, so they are all it sees.
	fitSet, err := p.fitSet(i)
	if err != nil {
		p.errs[i] = err
		return
	}
	if model.FitKindOf(fits[i].Request) == types.FitKindSEM {
		sf, fail, err := s.runSEMFit(ctx, fits[i], fitSet)
		if err != nil {
			p.errs[i] = err
			return
		}
		if fail != nil {
			p.failed[i], p.failedOp[i] = fail.err, fail.operator
			p.saveWarnings[i] = append(p.saveWarnings[i], fail.warnings(i)...)
			return
		}
		p.semFits[i] = sf
		// The fit's `save` writes its SEM fit file under the fit's
		// name (the overwrite warning lifts to the batch).
		w, err := s.saveFitSEM(ctx, i, fits[i], sf)
		if err != nil {
			p.errs[i] = err
			return
		}
		if w != nil {
			p.saveWarnings[i] = append(p.saveWarnings[i], w)
		}
		return
	}
	if model.FitKindOf(fits[i].Request) == types.FitKindVector {
		vf, fail, err := s.runVectorFit(ctx, fits[i], fitSet)
		if err != nil {
			p.errs[i] = err
			return
		}
		if fail != nil {
			p.failed[i], p.failedOp[i] = fail.err, fail.operator
			p.saveWarnings[i] = append(p.saveWarnings[i], fail.warnings(i)...)
			return
		}
		p.vectorFits[i] = vf
		// The fit's `save` writes its population file under the
		// fit's name (the overwrite warning lifts to the batch).
		w, err := s.saveFitPopulation(ctx, i, fits[i], vf)
		if err != nil {
			p.errs[i] = err
			return
		}
		if w != nil {
			p.saveWarnings[i] = append(p.saveWarnings[i], w)
		}
		return
	}
	resp, err := s.Process(withModelsBound(ctx, fitSet), model.FitRequest(fits[i]))
	if err != nil {
		p.errs[i] = err
		return
	}
	if resp == nil || len(resp.Regressions) != 1 || resp.Regressions[0] == nil {
		p.errs[i] = errors.NewCodedError(errors.PROCESSING_INTERNAL,
			fmt.Sprintf("fit %q produced no regression result", fits[i].Name))
		return
	}
	p.results[i] = resp.Regressions[0]
	if se := resp.Regressions[0].Error; se != nil {
		// Failed on its data: no model to bind or save; the fit's
		// own PULSE_SLOT_FAILED warning lifts to the batch.
		p.failed[i] = se
		p.saveWarnings[i] = append(p.saveWarnings[i], slotFailedWarnings(resp, i)...)
		return
	}
	// The fit's own response is not returned, so a model-file
	// overwrite its regression's `save` reported is lifted to the
	// batch (details.fit), then the fit's own `save` runs.
	for _, w := range resp.Warnings {
		if w != nil && w.Code == string(errors.PULSE_MODEL_OVERWRITTEN) {
			p.saveWarnings[i] = append(p.saveWarnings[i], fitWarning(w, i))
		}
	}
	// ... and so is the fit's own regression warnings (an extension
	// model's diagnostics), which the result keeps too.
	for _, w := range p.results[i].Warnings {
		if w != nil {
			p.saveWarnings[i] = append(p.saveWarnings[i], fitWarning(w, i))
		}
	}
	w, err := s.saveFitModel(ctx, i, fits[i], p.results[i])
	if err != nil {
		p.errs[i] = err
		return
	}
	if w != nil {
		p.saveWarnings[i] = append(p.saveWarnings[i], w)
	}
}

// execute runs every fit mask selects, each once; workers > 1 runs
// them concurrently. The first failure — by entry index, serial and
// parallel alike — is returned located by details.fit.
func (p *composeFitPass) execute(ctx context.Context, workers int, mask []bool) error {
	fits, errs := p.fits, p.errs
	n := len(fits)
	// runRecovered is run with a panic recovered into PROCESSING_PANIC
	// as fit i's error (recoverInto): on the pool it must be (a worker
	// goroutine panic escapes every other recover), and the serial arm
	// uses it too so a panicking fit fails identically under any worker
	// count.
	runRecovered := func(ctx context.Context, i int) {
		defer recoverInto(panicOpComposeFits, p.s.disablePanicRecovery, &errs[i])
		p.run(ctx, i)
	}

	if workers <= 1 {
		for i := range fits {
			if !mask[i] {
				continue
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if runRecovered(ctx, i); errs[i] != nil {
				return fitFailure(fits, i, errs[i])
			}
		}
		return nil
	}
	// Fail-fast, yet the serial loop's winner: fit i's failure cancels
	// (and stops launching) only the fits AFTER it, so every earlier
	// fit runs to completion and the lowest-index failure is the one
	// reported (failFastWinner) — never a race between two fits that
	// both fail on their data under Strict.
	ctxs := make([]context.Context, n)
	cancels := make([]context.CancelFunc, n)
	for i := range fits {
		ctxs[i], cancels[i] = context.WithCancel(ctx)
	}
	defer func() {
		for _, c := range cancels {
			c()
		}
	}()
	var mu sync.Mutex
	stopAt := n // no fit at or after stopAt is launched
	fail := func(i int) {
		mu.Lock()
		defer mu.Unlock()
		if i >= stopAt {
			return
		}
		stopAt = i
		for j := i + 1; j < n; j++ {
			cancels[j]()
		}
	}
	launchable := func(i int) bool {
		mu.Lock()
		defer mu.Unlock()
		return i < stopAt
	}
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	for i := range fits {
		if ctx.Err() != nil || !launchable(i) {
			break
		}
		if !mask[i] {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		// joinOnReturn, never a deferred wg.Done: a fit re-panicking
		// under DisablePanicRecovery must not release wg.Wait before
		// the crash.
		go joinOnReturn(&wg, func() {
			defer func() { <-sem }()
			if runRecovered(ctxs[i], i); errs[i] != nil {
				fail(i)
			}
		})
	}
	wg.Wait()
	var failed []int
	for i, e := range errs {
		if e != nil && mask[i] {
			failed = append(failed, i)
		}
	}
	if len(failed) > 0 {
		w := failFastWinner(ctx, errs, failed)
		return fitFailure(fits, w, errs[w])
	}
	return ctx.Err()
}

// bind binds every fit mask selects — each executed (or, a sweep-bound
// fit with no winner, failed) — in entry order over the instance's
// registered populations (CheckComposeBatch refused a fit name one
// already binds), and returns the set, the ComposedResponse.Fits
// entries and the lifted warnings, entry order.
func (p *composeFitPass) bind(mask []bool) (*model.Set, map[string]*types.FitResult, []*types.ResponseWarning, error) {
	fits := p.fits
	set := p.s.populations.Clone()
	out := make(map[string]*types.FitResult, len(fits))
	var warnings []*types.ResponseWarning
	for i, f := range fits {
		if !mask[i] {
			continue
		}
		if se := p.failed[i]; se != nil {
			sig := failedSignature(f, i)
			if sig.SweepBound {
				sig = model.FitCatalog{f.Name: sig}.WithSweepAxes(p.sweepAxes)[f.Name]
			}
			if err := set.AddFailed(sig, se); err != nil {
				return nil, nil, nil, fitFailure(fits, i, err)
			}
			switch {
			case p.results[i] != nil:
				out[f.Name] = &types.FitResult{RegressionResult: p.results[i]}
			case model.FitKindOf(f.Request) == types.FitKindSEM:
				out[f.Name] = &types.FitResult{SEM: &types.SEMFitResult{Operator: p.failedOp[i], Error: se}}
			default:
				out[f.Name] = &types.FitResult{Vector: &types.VectorFitResult{Operator: p.failedOp[i], Error: se}}
			}
			warnings = append(warnings, p.saveWarnings[i]...)
			continue
		}
		if sf := p.semFits[i]; sf != nil {
			sf.Name, sf.Source = f.Name, model.FitSource(i)
			if err := set.AddSEM(sf); err != nil {
				return nil, nil, nil, fitFailure(fits, i, err)
			}
			out[f.Name] = &types.FitResult{SEM: sf.Result()}
			warnings = append(warnings, p.saveWarnings[i]...)
			continue
		}
		if vf := p.vectorFits[i]; vf != nil {
			vf.Name, vf.Source = f.Name, model.FitSource(i)
			summary, err := processing.VectorFitResult(vf)
			if err != nil {
				return nil, nil, nil, fitFailure(fits, i, err)
			}
			if err := set.AddVector(vf); err != nil {
				return nil, nil, nil, fitFailure(fits, i, err)
			}
			out[f.Name] = &types.FitResult{Vector: summary}
			warnings = append(warnings, p.saveWarnings[i]...)
			continue
		}
		b, err := model.Bind(f.Name, f.Request.Regressions[0], p.results[i])
		if err != nil {
			return nil, nil, nil, fitFailure(fits, i, err)
		}
		b.Source = model.FitSource(i)
		if f.FromSweep != nil {
			b.SweepBound, b.SweepAxes = true, p.winnerAxes[i]
		}
		if err := set.Add(b); err != nil {
			// CheckFits already refused a duplicate name; reaching
			// here is an invariant violation, still reported coded.
			return nil, nil, nil, fitFailure(fits, i, err)
		}
		out[f.Name] = &types.FitResult{RegressionResult: p.results[i]}
		// Entry order, serial and parallel alike.
		warnings = append(warnings, p.saveWarnings[i]...)
	}
	return set, out, warnings, nil
}

// vectorFitFailure is a vector fit's data failure: its battery's error
// and operator, and the fit request's response (its PULSE_SLOT_FAILED
// warnings lift to the batch).
type vectorFitFailure struct {
	err      *types.SlotError
	operator string
	resp     *types.Response
}

// warnings are the failure's PULSE_SLOT_FAILED warnings with
// details.fit = i.
func (v *vectorFitFailure) warnings(i int) []*types.ResponseWarning {
	return slotFailedWarnings(v.resp, i)
}

// runVectorFit runs a vector fit request with a capture on its bound
// set and returns the one closed state its battery offered (unnamed:
// the caller binds it) — or, when the battery (its one MAT_* slot)
// failed on its data, that failure: a failed battery binds nothing,
// whether or not it offered a state before failing. No offer, or more
// than one, from a battery that did not fail is an engine invariant
// violation (PROCESSING_INTERNAL) — CheckFits admitted one battery.
func (s *Service) runVectorFit(ctx context.Context, f types.FitSpec, fitSet *model.Set) (*model.VectorFit, *vectorFitFailure, error) {
	capture := &model.VectorCapture{}
	resp, err := s.Process(withModelsBound(ctx, fitSet.WithCapture(capture)), model.FitRequest(f))
	if err != nil {
		return nil, nil, err
	}
	if resp != nil {
		for k := range resp.Matrices {
			if se := resp.Matrices[k].Error; se != nil {
				return nil, &vectorFitFailure{err: se, operator: string(resp.Matrices[k].Type), resp: resp}, nil
			}
		}
	}
	got := capture.Fits()
	if len(got) != 1 {
		return nil, nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
			fmt.Sprintf("vector fit %q closed %d battery states; a vector fit closes exactly one", f.Name, len(got)),
			map[string]any{"name": f.Name, "states": len(got)})
	}
	return got[0], nil, nil
}

// runSEMFit runs a SEM fit request (its one MAT_SEM matrix) with a
// capture on its bound set and returns the SEM fit its finalizer
// closed (unnamed: the caller binds it) — or, when the slot failed on
// its data, that failure (an undefined input fails the slot of a SEM
// fit request — finalizeSEM — so there is always a fit or a failure).
// No offer, or more than one, from a slot that did not fail is an
// engine invariant violation (PROCESSING_INTERNAL).
func (s *Service) runSEMFit(ctx context.Context, f types.FitSpec, fitSet *model.Set) (*model.SEMFit, *vectorFitFailure, error) {
	capture := &model.VectorCapture{}
	resp, err := s.Process(withModelsBound(ctx, fitSet.WithCapture(capture)), model.FitRequest(f))
	if err != nil {
		return nil, nil, err
	}
	if resp != nil {
		for k := range resp.Matrices {
			if se := resp.Matrices[k].Error; se != nil {
				return nil, &vectorFitFailure{err: se, operator: string(resp.Matrices[k].Type), resp: resp}, nil
			}
		}
	}
	got := capture.SEMFits()
	if len(got) == 1 {
		return got[0], nil, nil
	}
	return nil, nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
		fmt.Sprintf("SEM fit %q closed %d fits; a SEM fit closes exactly one", f.Name, len(got)),
		map[string]any{"name": f.Name, "fits": len(got)})
}

// slotFailedWarnings are resp's PULSE_SLOT_FAILED warnings — a fit
// request's failed battery or regression — each with details.fit = i.
func slotFailedWarnings(resp *types.Response, i int) []*types.ResponseWarning {
	var out []*types.ResponseWarning
	for _, w := range resp.Warnings {
		if w != nil && w.Code == string(errors.PULSE_SLOT_FAILED) {
			out = append(out, fitWarning(w, i))
		}
	}
	return out
}

// failedSignature is failed fit i's static signature — the one
// predict's FitCatalog holds for it (model.CatalogFromFits), so a
// reference to it is judged at runtime exactly as in predict.
func failedSignature(f types.FitSpec, i int) model.Signature {
	sig := model.CatalogFromFits([]types.FitSpec{f})[f.Name]
	sig.Source = model.FitSource(i)
	return sig
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
