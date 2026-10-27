package pulse

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"reflect"
	"slices"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/descriptor"
	perr "github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// model_load_test.go pins Request.models at the facade: predict refuses
// every bad load with the runtime's code,
// message and details before execution (Process, Compose and
// ComposeParallel, ProcessChain, FacetSchema), predict resolves a
// loaded model's references like the runtime, and a profile without
// capability:models refuses the key as an unknown field.

const facadeModel = "models/m" + model.FileSuffix

// writeFacadeModel saves a hand-built OLS model file at path whose terms
// are predictors (coefficients 1 each, intercept 0).
func writeFacadeModel(t *testing.T, fsys afero.Fs, path string, predictors ...string) {
	t.Helper()
	coefs := map[string]float64{model.InterceptKey: 0}
	for _, p := range predictors {
		coefs[p] = 1
	}
	f, err := model.NewFile("m", &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: predictors},
		&types.RegressionResult{Type: types.REG_OLS, Coefficients: coefs}, model.Provenance{})
	if err != nil {
		t.Fatal(err)
	}
	data, err := f.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(fsys, path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// loadFacadeFS is the fits facade cohort plus model files: a good model
// on x, one on a missing column, one on the categorical region, a
// malformed file, a future version and a directory.
func loadFacadeFS(t *testing.T) afero.Fs {
	t.Helper()
	fsys := fitsFacadeFS(t)
	writeFacadeModel(t, fsys, facadeModel, "x")
	writeFacadeModel(t, fsys, "models/nope"+model.FileSuffix, "nope")
	writeFacadeModel(t, fsys, "models/region"+model.FileSuffix, "region")
	_ = afero.WriteFile(fsys, "models/bad"+model.FileSuffix, []byte("{nope"), 0o644)
	_ = afero.WriteFile(fsys, "models/v9"+model.FileSuffix, []byte(`{"model_format_version": 9}`), 0o644)
	_ = fsys.MkdirAll("models/dir"+model.FileSuffix, 0o755)
	return fsys
}

func scoringRequest(models ...types.ModelRef) *Request {
	return &Request{
		Label:        "score",
		Cohort:       &types.Cohort{Filename: fitsFacadeCohort},
		Models:       models,
		Attributes:   []*types.Attribute{{Type: types.ATTR_REG_PREDICT, Label: "p", Params: json.RawMessage(`{"fit":"m"}`)}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "p", Label: "sum_p"}},
	}
}

func ref(name, path string) types.ModelRef { return types.ModelRef{Name: name, Path: path} }

// findEnvError returns the predict error carrying code, or fails.
func findEnvError(t *testing.T, env *descriptor.Envelope, code perr.Code) descxEnvelopeError {
	t.Helper()
	for _, e := range env.Errors {
		if e.Code == string(code) {
			return descxEnvelopeError{e.Code, e.Message, e.Details}
		}
	}
	t.Fatalf("predict reports no %s: %+v", code, env.Errors)
	return descxEnvelopeError{}
}

// assertSameRefusal: the runtime error carries predict's code, message
// and details exactly.
func assertSameRefusal(t *testing.T, what string, rerr error, pe descxEnvelopeError) {
	t.Helper()
	var ce *perr.CodedError
	if !stderrors.As(rerr, &ce) || string(ce.Code) != pe.code || ce.Message != pe.message || !reflect.DeepEqual(ce.Details, pe.details) {
		t.Errorf("%s: runtime %v %+v\npredict %+v", what, rerr, ce, pe)
	}
}

// TestModelLoad_PredictRuntimeParity_Process: every load refusal of a
// plain request — file missing / unreadable / invalid / future version,
// a malformed entry, a name bound twice, a predictor the scoring frame
// lacks or holds as a categorical — is refused by predict exactly as
// Process refuses it.
func TestModelLoad_PredictRuntimeParity_Process(t *testing.T) {
	t.Parallel()

	fsys := loadFacadeFS(t)
	p, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	cohort, err := afero.ReadFile(fsys, fitsFacadeCohort)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		req  *Request
		code perr.Code
	}{
		{"missing", scoringRequest(ref("m", "models/none"+model.FileSuffix)), perr.PULSE_MODEL_FILE_UNREADABLE},
		{"unreadable", scoringRequest(ref("m", "models/dir"+model.FileSuffix)), perr.PULSE_MODEL_FILE_UNREADABLE},
		{"invalid", scoringRequest(ref("m", "models/bad"+model.FileSuffix)), perr.PULSE_MODEL_FILE_INVALID},
		{"version", scoringRequest(ref("m", "models/v9"+model.FileSuffix)), perr.PULSE_MODEL_VERSION_UNSUPPORTED},
		{"ref", scoringRequest(ref("", facadeModel)), perr.PULSE_MODEL_REF_INVALID},
		{"collision", scoringRequest(ref("m", facadeModel), ref("m", "models/nope"+model.FileSuffix)), perr.PULSE_MODEL_NAME_COLLISION},
		{"predictor missing", scoringRequest(ref("m", "models/nope"+model.FileSuffix)), perr.PULSE_MODEL_PREDICTOR_MISSING},
		{"predictor not numeric", scoringRequest(ref("m", "models/region"+model.FileSuffix)), perr.PULSE_MODEL_PREDICTOR_NOT_NUMERIC},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, err := p.PredictBytes(ctx, cohort, tc.req)
			if err != nil {
				t.Fatal(err)
			}
			pe := findEnvError(t, env, tc.code)
			if tc.code == perr.PULSE_MODEL_FILE_UNREADABLE || tc.code == perr.PULSE_MODEL_FILE_INVALID {
				for _, e := range env.Errors {
					if e.Code == string(perr.PULSE_MODEL_UNKNOWN) {
						t.Fatalf("an unloaded model is also reported unknown: %+v", env.Errors)
					}
				}
			}
			_, rerr := p.Process(ctx, tc.req)
			assertSameRefusal(t, "Process", rerr, pe)
		})
	}
	// A good load resolves: predict reports no model error.
	env, err := p.PredictBytes(ctx, cohort, scoringRequest(ref("m", facadeModel)))
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Errors) != 0 {
		t.Fatalf("a valid load: predict errors %+v", env.Errors)
	}
}

// TestModelLoad_PredictRuntimeParity_Batches: PredictCompose,
// PredictChain and PredictFacet refuse a bad load exactly as the
// runtime refuses it, located at the slot / fit / stage.
func TestModelLoad_PredictRuntimeParity_Batches(t *testing.T) {
	t.Parallel()

	fsys := loadFacadeFS(t)
	p, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	missing := ref("m", "models/none"+model.FileSuffix)
	fitLoading := facadeFitRequest("0", "90")
	fitLoading.Models = []types.ModelRef{missing}
	fit := func() []types.FitSpec {
		return []types.FitSpec{{Name: "f", Request: facadeFitRequest("0", "90"), Save: "f" + model.FileSuffix}}
	}
	composes := []struct {
		name string
		c    *ComposedRequest
		code perr.Code
		at   string
	}{
		{"slot load", &ComposedRequest{Fits: fit(), Requests: []*Request{facadeFitSlot("a"), scoringRequest(missing)}}, perr.PULSE_MODEL_FILE_UNREADABLE, "request"},
		{"fit request load", &ComposedRequest{Fits: []types.FitSpec{{Name: "f", Request: fitLoading}}, Requests: []*Request{facadeFitSlot("a")}}, perr.PULSE_MODEL_FILE_UNREADABLE, "fit"},
		{"slot model names a fit", &ComposedRequest{Fits: []types.FitSpec{{Name: "m", Request: facadeFitRequest("0", "90")}}, Requests: []*Request{scoringRequest(ref("m", facadeModel))}}, perr.PULSE_MODEL_NAME_COLLISION, "request"},
	}
	for _, tc := range composes {
		t.Run(tc.name, func(t *testing.T) {
			env, err := p.PredictCompose(ctx, tc.c)
			if err != nil {
				t.Fatal(err)
			}
			if len(env.Errors) != 1 {
				t.Fatalf("predict errors %+v, want the one pre-pass refusal", env.Errors)
			}
			pe := findEnvError(t, env, tc.code)
			if _, ok := pe.details[tc.at]; !ok {
				t.Fatalf("details %+v lack %s", pe.details, tc.at)
			}
			_, rerr := p.Compose(ctx, tc.c)
			assertSameRefusal(t, "Compose", rerr, pe)
			_, rerr = p.ComposeParallel(ctx, tc.c, ComposeOptions{MaxWorkers: 2})
			assertSameRefusal(t, "ComposeParallel", rerr, pe)
			if ok, _ := afero.Exists(fsys, "f"+model.FileSuffix); ok {
				t.Fatal("the fit saved before the pre-pass refusal")
			}
		})
	}

	stage1 := &Request{Models: []types.ModelRef{missing}, Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "sum_y", Label: "n"}}}
	chain := &ChainRequest{
		Cohort: &types.Cohort{Filename: fitsFacadeCohort},
		Stages: []*types.ChainStage{{Name: "s0", Request: facadeFitSlot("")}, {Name: "s1", Request: stage1}},
	}
	chain.Stages[0].Request.Label = ""
	chain.Stages[0].Request.Aggregations[0].Label = "sum_y"
	env, err := p.PredictChain(ctx, chain)
	if err != nil {
		t.Fatal(err)
	}
	pe := findEnvError(t, env, perr.PULSE_MODEL_FILE_UNREADABLE)
	if pe.details["stage"] != 1 {
		t.Fatalf("chain details %+v", pe.details)
	}
	_, rerr := p.ProcessChain(ctx, chain)
	assertSameRefusal(t, "ProcessChain", rerr, pe)

	facet := &FacetRequest{Cohort: &types.Cohort{Filename: fitsFacadeCohort}, Fields: []string{"region"}, Models: []types.ModelRef{missing}}
	env, err = p.PredictFacet(ctx, facet)
	if err != nil {
		t.Fatal(err)
	}
	pe = findEnvError(t, env, perr.PULSE_MODEL_FILE_UNREADABLE)
	_, rerr = p.FacetSchema(ctx, facet)
	assertSameRefusal(t, "FacetSchema", rerr, pe)

	// A good load: the facet filter's coef() and a chain stage's score
	// resolve in predict too.
	facet.Models = []types.ModelRef{ref("m", facadeModel)}
	facet.Filterers = []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: `coef("m", "x") > 0`}}
	env, err = p.PredictFacet(ctx, facet)
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Errors) != 0 {
		t.Fatalf("a valid facet load: predict errors %+v", env.Errors)
	}
	if _, err := p.FacetSchema(ctx, facet); err != nil {
		t.Fatal(err)
	}
}

// TestHiddenModels_RefusedAsUnknownField: an instance without
// capability:models refuses a set `models` key (Request and
// FacetRequest) as PULSE_REQUEST_UNKNOWN_FIELD, runtime and predict
// alike; the unprofiled control loads.
func TestHiddenModels_RefusedAsUnknownField(t *testing.T) {
	t.Parallel()

	fsys := loadFacadeFS(t)
	control, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	feats := []string{"capability:process", "capability:facet", string(types.AGG_SUM), string(types.ATTR_REG_PREDICT), string(types.REG_OLS), string(types.FILTER_RANGE)}
	h, err := New(Options{FS: fsys, FeatureProfile: &FeatureProfile{Profile: "no-models", Features: feats}})
	if err != nil {
		t.Fatal(err)
	}
	inst := h.svc.InstanceSnapshot()
	for _, sample := range []any{&types.Request{}, &types.FacetRequest{}} {
		if !slices.Contains(descx.HiddenSlotKeys(sample, inst), "models") {
			t.Fatalf("vacuous: %T models is not hidden", sample)
		}
	}
	ctx := context.Background()
	req := scoringRequest(ref("m", facadeModel))
	_, err = h.Process(ctx, req)
	assertSlotRefusal(t, slotGateFromErr(err), "models", []string{"models"})
	cohort, err := afero.ReadFile(fsys, fitsFacadeCohort)
	if err != nil {
		t.Fatal(err)
	}
	env, err := h.PredictBytes(ctx, cohort, req)
	out := slotGateFromErr(err)
	if out == nil {
		out = slotGateFromEnv(env)
	}
	assertSlotRefusal(t, out, "models", []string{"models"})
	facet := &FacetRequest{Cohort: &types.Cohort{Filename: fitsFacadeCohort}, Fields: []string{"region"}, Models: []types.ModelRef{ref("m", facadeModel)}}
	_, err = h.FacetSchema(ctx, facet)
	assertSlotRefusal(t, slotGateFromErr(err), "models", []string{"models"})
	if _, err := control.Process(ctx, req); err != nil {
		t.Fatal(err)
	}
}
