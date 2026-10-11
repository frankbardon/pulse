package pulse

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"reflect"
	"slices"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// model_save_test.go pins the model-file `save` at the facade: predict
// refuses a bad save path exactly as the runtime does, a profile without
// capability:model_save refuses the key as an unknown field, and the
// batch warnings ride outside the Compose `return`.

// TestModelSave_PredictRuntimeParity: a bad regression save path (plain
// request) and a bad fit save path (Compose) are refused by predict
// with the runtime's code, message and details.
func TestModelSave_PredictRuntimeParity(t *testing.T) {
	p, err := New(Options{FS: fitsFacadeFS(t)})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	cohort, err := afero.ReadFile(p.fsys, fitsFacadeCohort)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"", "m.json", "../m" + model.FileSuffix, "dir/" + model.FileSuffix} {
		req := facadeFitRequest("0", "90")
		req.Regressions[0].Save = path
		if path == "" {
			req.Regressions[0].Save = "  "
		}
		env, err := p.PredictBytes(ctx, cohort, req)
		if err != nil {
			t.Fatal(err)
		}
		var pe *descxEnvelopeError
		for _, e := range env.Errors {
			if e.Code == string(perr.PULSE_MODEL_SAVE_INVALID) {
				pe = &descxEnvelopeError{e.Code, e.Message, e.Details}
			}
		}
		if pe == nil {
			t.Fatalf("%q: predict errors = %+v", path, env.Errors)
		}
		_, rerr := p.Process(ctx, req)
		var ce *perr.CodedError
		if !stderrors.As(rerr, &ce) || string(ce.Code) != pe.code || ce.Message != pe.message || !reflect.DeepEqual(ce.Details, pe.details) {
			t.Errorf("%q: runtime %v\npredict %+v", path, rerr, pe)
		}
	}

	c := &ComposedRequest{
		Fits:     []types.FitSpec{{Name: "a", Request: facadeFitRequest("0", "90"), Save: "a.json"}},
		Requests: []*Request{facadeFitSlot("s")},
	}
	env, err := p.PredictCompose(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Errors) != 1 || env.Errors[0].Code != string(perr.PULSE_MODEL_SAVE_INVALID) {
		t.Fatalf("predict compose errors = %+v", env.Errors)
	}
	pe := env.Errors[0]
	for name, run := range map[string]func() error{
		"Compose":         func() error { _, err := p.Compose(ctx, c); return err },
		"ComposeParallel": func() error { _, err := p.ComposeParallel(ctx, c, ComposeOptions{MaxWorkers: 2}); return err },
	} {
		var ce *perr.CodedError
		if err := run(); !stderrors.As(err, &ce) || string(ce.Code) != pe.Code || ce.Message != pe.Message || !reflect.DeepEqual(ce.Details, pe.Details) {
			t.Errorf("%s: runtime %v\npredict %+v", name, err, pe)
		}
	}
}

type descxEnvelopeError struct {
	code, message string
	details       map[string]any
}

// TestHiddenModelSave_RefusedAsUnknownField: an instance offering
// Process, Compose, fits and REG_OLS but not capability:model_save
// refuses a set `save` — on a regression and on a fit — as
// PULSE_REQUEST_UNKNOWN_FIELD at that object, runtime and predict alike,
// and writes nothing; the unprofiled control saves.
func TestHiddenModelSave_RefusedAsUnknownField(t *testing.T) {
	fsys := fitsFacadeFS(t)
	control, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	feats := []string{"capability:process", "capability:compose", "capability:compose_fits", string(types.AGG_SUM), string(types.REG_OLS), string(types.FILTER_RANGE)}
	h, err := New(Options{FS: fsys, FeatureProfile: &FeatureProfile{Profile: "no-save", Features: feats}})
	if err != nil {
		t.Fatal(err)
	}
	inst := h.svc.InstanceSnapshot()
	for _, sample := range []any{&types.RegressionSpec{}, &types.FitSpec{}} {
		if !slices.Contains(descx.HiddenSlotKeys(sample, inst), "save") {
			t.Fatalf("vacuous: %T save is not hidden", sample)
		}
		if slices.Contains(descx.HiddenSlotKeys(sample, control.svc.InstanceSnapshot()), "save") {
			t.Fatalf("%T save hidden on the unprofiled instance", sample)
		}
	}
	ctx := context.Background()
	req := facadeFitRequest("0", "90")
	req.Regressions[0].Save = "r" + model.FileSuffix
	c := &ComposedRequest{Fits: []types.FitSpec{{Name: "a", Request: facadeFitRequest("0", "90"), Save: "f" + model.FileSuffix}}, Requests: []*Request{facadeFitSlot("s")}}

	check := func(name string, out *slotGateOutcome, path string, extra string, idx int) {
		t.Helper()
		assertSlotRefusal(t, out, "save", []string{"save"})
		if out.details["path"] != path {
			t.Errorf("%s: details.path = %v, want %s", name, out.details["path"], path)
		}
		if extra != "" && out.details[extra] != idx {
			t.Errorf("%s: details.%s = %v, want %d", name, extra, out.details[extra], idx)
		}
	}
	_, err = h.Process(ctx, req)
	check("Process", slotGateFromErr(err), "regressions[0]", "", 0)
	cohort, err := afero.ReadFile(fsys, fitsFacadeCohort)
	if err != nil {
		t.Fatal(err)
	}
	env, err := h.PredictBytes(ctx, cohort, req)
	out := slotGateFromErr(err)
	if out == nil {
		out = slotGateFromEnv(env)
	}
	check("PredictBytes", out, "regressions[0]", "", 0)
	_, err = h.Compose(ctx, c)
	check("Compose", slotGateFromErr(err), "fits[0]", "fit", 0)
	cenv, err := h.PredictCompose(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	check("PredictCompose", slotGateFromEnv(cenv), "fits[0]", "fit", 0)
	for _, f := range []string{"r", "f"} {
		if ok, _ := afero.Exists(fsys, f+model.FileSuffix); ok {
			t.Fatalf("a refused save wrote %s", f)
		}
	}
	if _, err := control.Process(ctx, req); err != nil {
		t.Fatal(err)
	}
	if ok, _ := afero.Exists(fsys, "r"+model.FileSuffix); !ok {
		t.Fatal("control did not save")
	}
}

// TestModelSave_WarningsOutsideComposeReturn: a Compose-level `return`
// shapes the top-level overlays only; the batch `warnings` (a fit save's
// overwrite) are always emitted.
func TestModelSave_WarningsOutsideComposeReturn(t *testing.T) {
	p, err := New(Options{FS: fitsFacadeFS(t)})
	if err != nil {
		t.Fatal(err)
	}
	c := &ComposedRequest{
		Fits:     []types.FitSpec{{Name: "train", Request: facadeFitRequest("0", "90"), Save: "train" + model.FileSuffix}},
		Requests: []*Request{facadeFitSlot("s")},
		Return:   &types.Return{Include: []string{"overlays"}, Precision: 1},
	}
	ctx := context.Background()
	if _, err := p.Compose(ctx, c); err != nil {
		t.Fatal(err)
	}
	resp, err := p.Compose(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Returned == nil || len(resp.Warnings) != 1 {
		t.Fatalf("vacuous: returned %v warnings %+v", resp.Returned, resp.Warnings)
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Warnings []types.ResponseWarning `json:"warnings"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire.Warnings) != 1 || wire.Warnings[0].Code != string(perr.PULSE_MODEL_OVERWRITTEN) {
		t.Fatalf("batch warnings dropped by the Compose return: %s", raw)
	}
	// An unnamed regression's model is named after its file.
	slot := facadeFitSlot("s")
	slot.Regressions = []*types.RegressionSpec{{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}, Save: "dir/slot" + model.FileSuffix}}
	for range 2 {
		r, err := p.Process(ctx, slot)
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Warnings) == 1 && r.Warnings[0].Details["name"] != "slot" {
			t.Fatalf("overwrite names %v", r.Warnings[0].Details["name"])
		}
	}
	data, _ := afero.ReadFile(p.fsys, "dir/slot"+model.FileSuffix)
	if f, err := model.DecodeFile("x", data); err != nil || f.Name != "slot" {
		t.Fatalf("unnamed save: %+v %v", f, err)
	}
}
