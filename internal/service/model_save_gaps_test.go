package service

import (
	"context"
	stderrors "errors"
	"reflect"
	"testing"

	"github.com/spf13/afero"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// model_save_gaps_test.go pins the places a `save` used to be dropped
// or clobbered silently: a chain stage after the first now writes its
// model; Materialize refuses a saving regression; two saves naming one
// file are refused before anything runs; and a Compose slot's bad save
// path is refused before any fit or slot runs.

func saveCoded(t *testing.T, err error, code perr.Code) *perr.CodedError {
	t.Helper()
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != code {
		t.Fatalf("got %v, want %s", err, code)
	}
	return ce
}

func assertAbsent(t *testing.T, fsys afero.Fs, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if ok, _ := afero.Exists(fsys, p); ok {
			t.Fatalf("%s was written before the refusal", p)
		}
	}
}

// chainWithStageSave: stage 0 sums y and x per week bucket (and, with
// stage0Save, fits y on x saving there); stage 1 fits sum_y on x over
// those rows, saving at stage1Path.
func chainWithStageSave(stage0Save, stage1Path string) *types.ChainRequest {
	stage0 := &types.Request{
		Groups:       []*types.Group{{Type: types.GROUP_RANGE, Field: "week", Interval: 4}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "y", Label: "sum_y"}, {Type: types.AGG_SUM, Field: "x", Label: "x"}},
	}
	if stage0Save != "" {
		stage0.Regressions = []*types.RegressionSpec{{Type: types.REG_OLS, Name: "row", Target: "y", Predictors: []string{"x"}, Save: stage0Save}}
	}
	stage1 := &types.Request{
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "sum_y", Label: "n"}},
		Regressions:  []*types.RegressionSpec{{Type: types.REG_OLS, Name: "agg", Target: "sum_y", Predictors: []string{"x"}, Save: stage1Path}},
	}
	return &types.ChainRequest{
		Cohort: &types.Cohort{Filename: fitsCohort},
		Stages: []*types.ChainStage{{Name: "bucket", Request: stage0}, {Name: "fit", Request: stage1}},
	}
}

// TestModelSave_ChainNeverWrites: a chain admits no regression on any
// stage (the chain gate), so a saving regression — on a later stage or
// on stage 0 — is refused, never silently dropped, and no model file is
// written: stage 0's is refused before it runs.
func TestModelSave_ChainNeverWrites(t *testing.T) {
	t.Parallel()

	svc := fitsService(t)
	first, later := "chain/row"+model.FileSuffix, "chain/agg"+model.FileSuffix
	ce := saveCoded(t, ignoreResp(svc.ProcessChain(context.Background(), chainWithStageSave("", later))), perr.PULSE_CHAIN_NOT_MERGEABLE)
	if ce.Details["stage_index"] != 1 {
		t.Fatalf("details %+v", ce.Details)
	}
	ce = saveCoded(t, ignoreResp(svc.ProcessChain(context.Background(), chainWithStageSave(first, later))), perr.PULSE_CHAIN_NOT_MERGEABLE)
	if ce.Details["stage_index"] != 0 {
		t.Fatalf("details %+v", ce.Details)
	}
	assertAbsent(t, svc.fs.Fs(), first, later)
}

func ignoreResp[T any](_ T, err error) error { return err }

// TestModelSave_MaterializeRefusesSave: Materialize admits no
// regression, so a saving one is refused — never silently dropped —
// and no file is written.
func TestModelSave_MaterializeRefusesSave(t *testing.T) {
	t.Parallel()

	svc := fitsService(t)
	req := saveRequest(&types.RegressionSpec{Type: types.REG_OLS, Name: "m", Target: "y", Predictors: []string{"x"}})
	req.Features = []*types.Feature{{Type: types.FEAT_LOG, Field: "x", Label: "lx"}}
	ce := saveCoded(t, svc.Materialize(context.Background(), req, &valueSink{}), perr.PULSE_MATERIALIZE_SLOT_UNSUPPORTED)
	if ce.Details["slot"] != "regressions" {
		t.Fatalf("details %+v", ce.Details)
	}
	assertAbsent(t, svc.fs.Fs(), savePath)
}

// TestModelSave_DuplicatePathsRefused: two saves naming one file — in a
// request, or across a Compose batch's fits and slots — are refused
// before anything runs, naming both sites.
func TestModelSave_DuplicatePathsRefused(t *testing.T) {
	t.Parallel()

	svc := fitsService(t)
	ctx := context.Background()
	other := "models/n" + model.FileSuffix
	req := &types.Request{Cohort: &types.Cohort{Filename: fitsCohort}, Regressions: []*types.RegressionSpec{
		{Type: types.REG_OLS, Name: "a", Target: "y", Predictors: []string{"x"}, Save: savePath},
		{Type: types.REG_OLS, Name: "b", Target: "y", Predictors: []string{"week"}, Save: other},
		{Type: types.REG_OLS, Name: "c", Target: "y", Predictors: []string{"x", "week"}, Save: "models/./m" + model.FileSuffix},
	}}
	ce := saveCoded(t, ignoreResp(svc.Process(ctx, req)), perr.PULSE_MODEL_SAVE_INVALID)
	if want := []any{"regressions[0]", "regressions[2]"}; ce.Details["reason"] != model.ReasonSaveDuplicate || !reflect.DeepEqual(ce.Details["sites"], want) {
		t.Fatalf("details %+v", ce.Details)
	}
	assertAbsent(t, svc.fs.Fs(), savePath, other)

	slot := fitsSlot("s", "0", "90")
	slot.Regressions = []*types.RegressionSpec{{Type: types.REG_OLS, Name: "s", Target: "y", Predictors: []string{"x"}, Save: savePath}}
	c := &types.ComposedRequest{
		Fits:     []types.FitSpec{{Name: "f", Request: fitRequest("0", "90"), Save: savePath}},
		Requests: []*types.Request{slot},
	}
	for mode := range map[string]bool{"serial": true, "parallel": true} {
		var err error
		if mode == "serial" {
			_, err = svc.Compose(ctx, c)
		} else {
			_, err = svc.ComposeParallel(ctx, c, ComposeOptions{MaxWorkers: 2})
		}
		ce := saveCoded(t, err, perr.PULSE_MODEL_SAVE_INVALID)
		if want := []any{"fits[0]", "requests[0].regressions[0]"}; !reflect.DeepEqual(ce.Details["sites"], want) {
			t.Fatalf("%s: details %+v", mode, ce.Details)
		}
		assertAbsent(t, svc.fs.Fs(), savePath)
	}
}

// TestModelSave_ComposeSlotPathBeforeAnyRun: a later slot's bad save
// path is refused in the pre-pass — before the fit (whose own save is
// valid) or any earlier slot runs, so nothing is written.
func TestModelSave_ComposeSlotPathBeforeAnyRun(t *testing.T) {
	// serial: swaps the package-level service.fitRunObserver test hook

	svc := fitsService(t)
	ctx := context.Background()
	fitSaved := "models/f" + model.FileSuffix
	earlySaved := "models/early" + model.FileSuffix
	early := fitsSlot("early", "0", "90")
	early.Regressions = []*types.RegressionSpec{{Type: types.REG_OLS, Name: "e", Target: "y", Predictors: []string{"x"}, Save: earlySaved}}
	late := fitsSlot("late", "0", "90")
	late.Regressions = []*types.RegressionSpec{{Type: types.REG_OLS, Name: "l", Target: "y", Predictors: []string{"x"}, Save: "../late" + model.FileSuffix}}
	c := &types.ComposedRequest{
		Fits:     []types.FitSpec{{Name: "f", Request: fitRequest("0", "90"), Save: fitSaved}},
		Requests: []*types.Request{early, late},
	}
	runs := 0
	fitRunObserver = func(string) { runs++ }
	defer func() { fitRunObserver = nil }()
	for _, mode := range []string{"serial", "parallel"} {
		var err error
		if mode == "serial" {
			_, err = svc.Compose(ctx, c)
		} else {
			_, err = svc.ComposeParallel(ctx, c, ComposeOptions{MaxWorkers: 2})
		}
		ce := saveCoded(t, err, perr.PULSE_MODEL_SAVE_INVALID)
		want := map[string]any{"path": "../late" + model.FileSuffix, "reason": model.ReasonSaveEscapesRoot, "regression": 0, "request": 1}
		if !reflect.DeepEqual(ce.Details, want) {
			t.Fatalf("%s: details %+v, want %+v", mode, ce.Details, want)
		}
		assertAbsent(t, svc.fs.Fs(), fitSaved, earlySaved)
	}
	if runs != 0 {
		t.Fatalf("%d fits ran before the refusal", runs)
	}
}
