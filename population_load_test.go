package pulse

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/spf13/afero"

	perr "github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// population_load_test.go pins Request.populations at the facade
// (E2-S3): a vector fit's `save` writes a population file a plain
// Process loads by name; predict refuses every bad load with the
// runtime's code, message and details before execution (Process,
// Compose, ProcessChain) and resolves a loaded population's params.fit
// like the runtime; a profile without capability:models refuses the key
// as an unknown field.

const facadePopulation = "pops/brand" + model.PopulationSuffix

// populationFacadeFS is vecFacadeFS plus a saved population (a
// MAT_COVARIANCE fit over weeks 0–44) and broken population files.
func populationFacadeFS(t *testing.T) afero.Fs {
	t.Helper()
	fsys := vecFacadeFS(t)
	p, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	fit := vecFacadeMatrix(types.MAT_COVARIANCE, "")
	fit.Filterers = []*types.Filterer{{Type: types.FILTER_RANGE, Field: "week", Values: []string{"0", "44"}}}
	if _, err := p.Compose(context.Background(), &ComposedRequest{
		Fits:     []types.FitSpec{{Name: "brand", Request: fit, Save: facadePopulation}},
		Requests: []*Request{vecFacadeMatrix(types.MAT_COVARIANCE, `{"fit":"brand"}`)},
	}); err != nil {
		t.Fatal(err)
	}
	_ = afero.WriteFile(fsys, "pops/bad"+model.PopulationSuffix, []byte("{nope"), 0o644)
	_ = afero.WriteFile(fsys, "pops/v9"+model.PopulationSuffix, []byte(`{"population_format_version": 9}`), 0o644)
	_ = fsys.MkdirAll("pops/dir"+model.PopulationSuffix, 0o755)
	return fsys
}

func popRef(name, path string) types.PopulationRef {
	return types.PopulationRef{Name: name, Path: path}
}

// populationScoring is a Process over the facade cohort scoring
// ATTR_MAHALANOBIS against fit "brand", loading pops.
func populationScoring(pops ...types.PopulationRef) *Request {
	r := vecFacadeRequest("score")
	r.Populations = pops
	r.Attributes = []*types.Attribute{{Type: types.ATTR_MAHALANOBIS, Label: "d", Fields: []string{"a", "b", "c"}, Params: json.RawMessage(`{"fit":"brand"}`)}}
	r.Aggregations = []*types.Aggregation{{Type: types.AGG_SUM, Field: "d", Label: "sum_d"}}
	return r
}

// TestPopulationLoad_PredictRuntimeParity_Process: every population
// load refusal of a plain request — file missing / unreadable / invalid
// / future version, a malformed entry, a name bound twice, a name of
// the other kind — is refused by predict exactly as Process refuses it;
// a valid load predicts clean and runs.
func TestPopulationLoad_PredictRuntimeParity_Process(t *testing.T) {
	t.Parallel()

	fsys := populationFacadeFS(t)
	p, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	cohort, err := afero.ReadFile(fsys, vecFacadeCohort)
	if err != nil {
		t.Fatal(err)
	}
	collide := populationScoring(popRef("brand", facadePopulation), popRef("brand", facadePopulation))
	asModel := populationScoring(popRef("other", facadePopulation))
	writeFacadeModel(t, fsys, "pops/brand"+model.FileSuffix, "a")
	asModel.Models = []types.ModelRef{{Name: "brand", Path: "pops/brand" + model.FileSuffix}}
	twoMemberScoring := populationScoring(popRef("brand", facadePopulation))
	twoMemberScoring.Attributes[0].Fields = []string{"a", "b"}
	twoMemberMatrix := vecFacadeMatrix(types.MAT_CORRELATION, `{"fit":"brand"}`)
	twoMemberMatrix.Matrices[0].Fields = []string{"a", "c"}
	twoMemberMatrix.Populations = []types.PopulationRef{popRef("brand", facadePopulation)}
	cases := []struct {
		name string
		req  *Request
		code perr.Code
	}{
		{"missing", populationScoring(popRef("brand", "pops/none"+model.PopulationSuffix)), perr.PULSE_MODEL_FILE_UNREADABLE},
		{"unreadable", populationScoring(popRef("brand", "pops/dir"+model.PopulationSuffix)), perr.PULSE_MODEL_FILE_UNREADABLE},
		{"invalid", populationScoring(popRef("brand", "pops/bad"+model.PopulationSuffix)), perr.PULSE_MODEL_FILE_INVALID},
		{"version", populationScoring(popRef("brand", "pops/v9"+model.PopulationSuffix)), perr.PULSE_MODEL_VERSION_UNSUPPORTED},
		{"ref", populationScoring(popRef("brand", "pops/brand.json")), perr.PULSE_MODEL_REF_INVALID},
		{"collision", collide, perr.PULSE_MODEL_NAME_COLLISION},
		{"unknown", populationScoring(popRef("other", facadePopulation)), perr.PULSE_MODEL_UNKNOWN},
		{"kind mismatch", asModel, perr.PULSE_MODEL_KIND_MISMATCH},
		// The population's labels are known statically: a slot whose
		// members differ is refused by predict exactly as the bind
		// refuses it.
		{"attribute labels differ", twoMemberScoring, perr.PULSE_VECTOR_REFERENCE_MISMATCH},
		{"matrix labels differ", twoMemberMatrix, perr.PULSE_VECTOR_REFERENCE_MISMATCH},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, err := p.PredictBytes(ctx, cohort, tc.req)
			if err != nil {
				t.Fatal(err)
			}
			pe := findEnvError(t, env, tc.code)
			if tc.code != perr.PULSE_MODEL_UNKNOWN {
				for _, e := range env.Errors {
					if e.Code == string(perr.PULSE_MODEL_UNKNOWN) {
						t.Fatalf("an unloaded population is also reported unknown: %+v", env.Errors)
					}
				}
			}
			_, rerr := p.Process(ctx, tc.req)
			assertSameRefusal(t, "Process", rerr, pe)
		})
	}
	good := populationScoring(popRef("brand", facadePopulation))
	env, err := p.PredictBytes(ctx, cohort, good)
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Errors) != 0 {
		t.Fatalf("a valid load: predict errors %+v", env.Errors)
	}
	if _, err := p.Process(ctx, good); err != nil {
		t.Fatal(err)
	}
}

// TestPopulationLoad_PredictRuntimeParity_Batches: PredictCompose and
// PredictChain refuse a bad population load exactly as the runtime
// does, located at the slot / fit / stage, before any fit saves.
func TestPopulationLoad_PredictRuntimeParity_Batches(t *testing.T) {
	t.Parallel()

	fsys := populationFacadeFS(t)
	p, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	missing := popRef("brand", "pops/none"+model.PopulationSuffix)
	saveAt := "pops/fresh" + model.PopulationSuffix
	fit := func(pops ...types.PopulationRef) []types.FitSpec {
		r := vecFacadeMatrix(types.MAT_CORRELATION, "")
		r.Populations = pops
		return []types.FitSpec{{Name: "f", Request: r, Save: saveAt}}
	}
	composes := []struct {
		name string
		c    *ComposedRequest
		code perr.Code
		at   string
	}{
		{"slot load", &ComposedRequest{Fits: fit(), Requests: []*Request{populationScoring(missing)}}, perr.PULSE_MODEL_FILE_UNREADABLE, "request"},
		{"fit request load", &ComposedRequest{Fits: fit(missing), Requests: []*Request{vecFacadeMatrix(types.MAT_COVARIANCE, "")}}, perr.PULSE_MODEL_FILE_UNREADABLE, "fit"},
		{"slot population names a fit", &ComposedRequest{Fits: fit(), Requests: []*Request{populationScoring(popRef("f", facadePopulation))}}, perr.PULSE_MODEL_NAME_COLLISION, "request"},
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
			if ok, _ := afero.Exists(fsys, saveAt); ok {
				t.Fatal("the fit saved before the pre-pass refusal")
			}
		})
	}

	// A valid batch: a slot loading the saved population predicts and
	// runs clean.
	ok := &ComposedRequest{Fits: fit(), Requests: []*Request{populationScoring(popRef("brand", facadePopulation))}}
	env, err := p.PredictCompose(ctx, ok)
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Errors) != 0 {
		t.Fatalf("a valid batch: predict errors %+v", env.Errors)
	}
	if _, err := p.Compose(ctx, ok); err != nil {
		t.Fatal(err)
	}

	stage1 := &Request{Populations: []types.PopulationRef{missing}, Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "n", Label: "rows"}}}
	s0 := vecFacadeRequest("")
	s0.Cohort = nil
	s0.Aggregations = []*types.Aggregation{{Type: types.AGG_COUNT, Field: "a", Label: "n"}}
	chain := &ChainRequest{
		Cohort: &types.Cohort{Filename: vecFacadeCohort},
		Stages: []*types.ChainStage{{Name: "s0", Request: s0}, {Name: "s1", Request: stage1}},
	}
	env, err = p.PredictChain(ctx, chain)
	if err != nil {
		t.Fatal(err)
	}
	pe := findEnvError(t, env, perr.PULSE_MODEL_FILE_UNREADABLE)
	if pe.details["stage"] != 1 || pe.details["population"] != 0 {
		t.Fatalf("chain details %+v", pe.details)
	}
	_, rerr := p.ProcessChain(ctx, chain)
	assertSameRefusal(t, "ProcessChain", rerr, pe)
}

// TestHiddenPopulations_RefusedAsUnknownField: an instance without
// capability:models refuses a set `populations` key as
// PULSE_REQUEST_UNKNOWN_FIELD, runtime and predict alike; the
// unprofiled control loads.
func TestHiddenPopulations_RefusedAsUnknownField(t *testing.T) {
	t.Parallel()

	fsys := populationFacadeFS(t)
	control, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	feats := []string{"capability:process", string(types.AGG_SUM), string(types.ATTR_MAHALANOBIS)}
	h, err := New(Options{FS: fsys, FeatureProfile: &FeatureProfile{Profile: "no-models", Features: feats}})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(descx.HiddenSlotKeys(&types.Request{}, h.svc.InstanceSnapshot()), "populations") {
		t.Fatal("vacuous: populations is not hidden")
	}
	ctx := context.Background()
	req := populationScoring(popRef("brand", facadePopulation))
	_, err = h.Process(ctx, req)
	assertSlotRefusal(t, slotGateFromErr(err), "populations", []string{"populations"})
	cohort, err := afero.ReadFile(fsys, vecFacadeCohort)
	if err != nil {
		t.Fatal(err)
	}
	env, err := h.PredictBytes(ctx, cohort, req)
	out := slotGateFromErr(err)
	if out == nil {
		out = slotGateFromEnv(env)
	}
	assertSlotRefusal(t, out, "populations", []string{"populations"})
	if _, err := control.Process(ctx, req); err != nil {
		t.Fatal(err)
	}
}
