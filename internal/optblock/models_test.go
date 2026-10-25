package optblock_test

import (
	"io/fs"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/optblock"
	"github.com/frankbardon/pulse/types"
)

// savedDemand is a saved model file on price (a REG_OLS `save`).
func savedDemand(t *testing.T) []byte {
	t.Helper()
	f, err := model.NewFile("saved", &types.RegressionSpec{Type: types.REG_OLS, Target: "units", Predictors: []string{"price"}},
		&types.RegressionResult{Type: types.REG_OLS, Coefficients: map[string]float64{model.InterceptKey: 120, "price": -4}}, model.Provenance{})
	if err != nil {
		t.Fatal(err)
	}
	data, err := f.Encode()
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// fileLoader loads from files (path → bytes) with model.LoadFiles.
func fileLoader(files map[string][]byte, reads *int) optblock.Loader {
	return func(refs model.Refs) (*model.Loaded, *errors.CodedError) {
		*reads++
		return model.LoadFiles(refs, func(p string) ([]byte, error) {
			if b, ok := files[p]; ok {
				return b, nil
			}
			return nil, fs.ErrNotExist
		})
	}
}

// TestOptimize_BlockModels: a block's `models` bind above the batch
// fits for its coef() calls — CheckLoaded returns the decoded files and
// a later Compile with Env.Loaded reuses them without reading again —
// while every load refusal keeps its own code and gains the block's
// location: a malformed entry, a name a fit already binds, a missing
// file, a term the loaded model lacks.
func TestOptimize_BlockModels(t *testing.T) {
	files := map[string][]byte{"models/saved" + model.FileSuffix: savedDemand(t)}
	block := func(models []types.ModelRef, objective string) []types.OptimizeSpec {
		s := validBlock()
		s.Models, s.Objective = models, objective
		return []types.OptimizeSpec{s}
	}
	ok := []types.ModelRef{{Name: "saved", Path: "models/saved" + model.FileSuffix}}
	reads := 0
	env := testEnv()
	env.Outer, env.Load = model.CatalogFromFits(demandFits()), fileLoader(files, &reads)
	specs := block(ok, `p * (coef("saved", "(intercept)") + coef("saved", "price") * p) + coef("demand", "price") * q`)
	loaded, cerr := optblock.CheckLoaded(specs, env)
	if cerr != nil || len(loaded) != 1 || loaded[0] == nil || len(loaded[0].Models) != 1 || reads != 1 {
		t.Fatalf("CheckLoaded %v %+v (reads %d)", cerr, loaded, reads)
	}
	env.Loaded = loaded
	c, cerr := optblock.Compile(&specs[0], 0, env, exprenv.Bound{Coef: exprenv.Placeholder})
	if cerr != nil || reads != 1 || c.Loaded != loaded[0] || len(c.Refs.Models) != 1 {
		t.Fatalf("Compile with Loaded: %v, reads %d, loaded %p vs %p", cerr, reads, c.Loaded, loaded[0])
	}

	cases := []struct {
		name   string
		models []types.ModelRef
		obj    string
		code   errors.Code
		field  string
	}{
		{"blank name", []types.ModelRef{{Name: " ", Path: "models/saved" + model.FileSuffix}}, "p", errors.PULSE_MODEL_REF_INVALID, "optimize[0].models"},
		{"collides with a fit", []types.ModelRef{{Name: "demand", Path: "models/saved" + model.FileSuffix}}, "p", errors.PULSE_MODEL_NAME_COLLISION, "optimize[0].models"},
		{"missing file", []types.ModelRef{{Name: "gone", Path: "models/gone" + model.FileSuffix}}, "p", errors.PULSE_MODEL_FILE_UNREADABLE, "optimize[0].models"},
		{"unknown term", ok, `coef("saved", "region") * p`, errors.PULSE_MODEL_TERM_UNKNOWN, "optimize[0].objective"},
		{"model of another block", nil, `coef("saved", "price") * p`, errors.PULSE_MODEL_UNKNOWN, "optimize[0].objective"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := testEnv()
			n := 0
			env.Outer, env.Load = model.CatalogFromFits(demandFits()), fileLoader(files, &n)
			loaded, cerr := optblock.CheckLoaded(block(tc.models, tc.obj), env)
			if cerr == nil || cerr.Code != tc.code || cerr.Details["optimize"] != 0 || cerr.Details["field"] != tc.field || loaded != nil {
				t.Fatalf("got %v %v (loaded %v), want %s at %s", cerr, cerr.Details, loaded, tc.code, tc.field)
			}
		})
	}

	// No loader refuses any file as unreadable, never panics.
	env = testEnv()
	_, cerr = optblock.CheckLoaded(block(ok, "p"), env)
	if cerr == nil || cerr.Code != errors.PULSE_MODEL_FILE_UNREADABLE {
		t.Fatalf("nil loader: %v", cerr)
	}
}
