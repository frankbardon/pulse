package model

import (
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// population_registered_test.go pins the instance's registered
// populations (E2-S4): bound as vector fits under the registered name,
// the outermost layer of the one namespace, and a request entry that
// rebinds a registered name refused as a collision — runtime Set and
// predict signature alike.

func registeredFixture(t *testing.T) (*Set, FitCatalog) {
	t.Helper()
	f, err := DecodePopulationFile("pop"+PopulationSuffix, encodedPopulation(t, edgeVectorFit()))
	if err != nil {
		t.Fatal(err)
	}
	set, err := RegisteredSet(map[string]*PopulationFile{"brand": f, "alpha": f})
	if err != nil {
		t.Fatal(err)
	}
	cat := FitCatalog{
		"brand": RegisteredSignature("brand", f.Labels),
		"alpha": RegisteredSignature("alpha", f.Labels),
	}
	return set, cat
}

// TestRegisteredSet: every file binds as a vector fit under its
// registered name with the registered source; the runtime Set and
// predict's RegisteredSignature agree name for name. No files binds
// nothing.
func TestRegisteredSet(t *testing.T) {
	t.Parallel()

	if set, err := RegisteredSet(nil); err != nil || set != nil {
		t.Fatalf("no files: %v %v", set, err)
	}
	set, cat := registeredFixture(t)
	if got := set.Names(); !reflect.DeepEqual(got, []string{"alpha", "brand"}) {
		t.Fatalf("names = %v", got)
	}
	v, err := set.LookupVector("brand")
	if err != nil {
		t.Fatal(err)
	}
	sameVectorFit(t, v, edgeVectorFit())
	if v.Name != "brand" || v.Source != RegisteredSource() {
		t.Fatalf("bound as %q from %+v", v.Name, v.Source)
	}
	for _, n := range []string{"alpha", "brand"} {
		got, _ := set.Signature(n)
		want, _ := cat.Signature(n)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: set %+v, predict %+v", n, got, want)
		}
	}
}

// TestSetClone: a clone holds the same bindings and is independent —
// binding into it never reaches the original.
func TestSetClone(t *testing.T) {
	t.Parallel()

	set, _ := registeredFixture(t)
	c := set.Clone()
	if !reflect.DeepEqual(c.Names(), set.Names()) {
		t.Fatalf("clone names %v, want %v", c.Names(), set.Names())
	}
	if err := c.AddVector(&VectorFit{Name: "extra"}); err != nil {
		t.Fatal(err)
	}
	if set.Len() != 2 {
		t.Fatalf("clone mutated the original: %v", set.Names())
	}
	if (*Set)(nil).Clone().Len() != 0 {
		t.Fatal("nil clone not empty")
	}
}

// TestWithinCatalog: a nil outer is the inner catalog unchanged; else
// both layers resolve.
func TestWithinCatalog(t *testing.T) {
	t.Parallel()

	inner := CatalogFromFits([]types.FitSpec{{Name: "f", Request: olsReq(ols())}})
	if got := WithinCatalog(nil, inner); !reflect.DeepEqual(got, Catalog(inner)) {
		t.Fatalf("nil outer: %+v", got)
	}
	_, outer := registeredFixture(t)
	got := WithinCatalog(outer, inner)
	if !reflect.DeepEqual(got.Names(), []string{"alpha", "brand", "f"}) {
		t.Fatalf("layered names = %v", got.Names())
	}
}

var registeredSrc = map[string]any{"slot": SlotRegistered}

// TestCheckRefs_RegisteredCollision: a request `populations` or
// `models` entry naming a registered population is
// PULSE_MODEL_NAME_COLLISION naming the registered source first.
func TestCheckRefs_RegisteredCollision(t *testing.T) {
	t.Parallel()

	set, cat := registeredFixture(t)
	ok, mok := "p"+PopulationSuffix, "m"+FileSuffix
	for _, outer := range []Catalog{set, cat} {
		cerr := CheckRefs(Refs{Populations: pops("brand", ok)}, outer)
		want := map[string]any{"name": "brand", "sources": []any{registeredSrc, map[string]any{"slot": "populations", "index": 0, "path": ok}}}
		if cerr == nil || cerr.Code != errors.PULSE_MODEL_NAME_COLLISION || !reflect.DeepEqual(cerr.Details, want) {
			t.Fatalf("population: %v %+v", cerr, cerr)
		}
		if cerr := CheckRefs(Refs{Models: []types.ModelRef{{Name: "alpha", Path: mok}}}, outer); cerr == nil || cerr.Code != errors.PULSE_MODEL_NAME_COLLISION {
			t.Fatalf("model: %v", cerr)
		}
		if cerr := CheckRefs(Refs{Populations: pops("other", ok)}, outer); cerr != nil {
			t.Fatalf("distinct name refused: %v", cerr)
		}
	}
	if got := NameCollision("brand", RegisteredSource(), FitSource(0)).Message; got != `model name "brand" is bound twice: by the instance's registered populations and by fits[0]` {
		t.Fatalf("message = %q", got)
	}
}

// TestCheckComposeBatch_Registered: a fit named after a registered
// population, a fit request's entry and a slot's entry rebinding one,
// are each refused; a batch clear of the registered names passes.
func TestCheckComposeBatch_Registered(t *testing.T) {
	t.Parallel()

	_, outer := registeredFixture(t)
	ok := "p" + PopulationSuffix
	vecReq := func() *types.Request {
		return &types.Request{Matrices: []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Fields: []string{"a", "b"}}}}
	}
	withPops := func(r *types.Request, ps []types.PopulationRef) *types.Request { r.Populations = ps; return r }
	fits := []types.FitSpec{{Name: "f", Request: vecReq()}}
	runBatchCasesWithin(t, outer, []batchCase{
		{"clean", fits, []*types.Request{withPops(saveReq(), pops("g", ok))}, "", nil},
		{"fit named after a registered population", []types.FitSpec{{Name: "f", Request: vecReq()}, {Name: "brand", Request: vecReq()}}, nil, errors.PULSE_MODEL_NAME_COLLISION,
			map[string]any{"name": "brand", "fit": 1, "sources": []any{registeredSrc, map[string]any{"slot": "fits", "index": 1}}}},
		{"fit request rebinds a registered name", []types.FitSpec{{Name: "f", Request: withPops(saveReq(), pops("alpha", ok))}}, nil, errors.PULSE_MODEL_NAME_COLLISION,
			map[string]any{"name": "alpha", "fit": 0, "sources": []any{registeredSrc, map[string]any{"slot": "populations", "index": 0, "path": ok}}}},
		{"slot rebinds a registered name", fits, []*types.Request{withPops(saveReq(), pops("brand", ok))}, errors.PULSE_MODEL_NAME_COLLISION,
			map[string]any{"name": "brand", "request": 0, "sources": []any{registeredSrc, map[string]any{"slot": "populations", "index": 0, "path": ok}}}},
	})
	runBatchCases(t, []batchCase{{"no registered populations", []types.FitSpec{{Name: "brand", Request: vecReq()}}, nil, "", nil}})
}

// TestCheckChainModels_Registered: a stage rebinding a registered name
// is refused, located by details.stage.
func TestCheckChainModels_Registered(t *testing.T) {
	t.Parallel()

	_, outer := registeredFixture(t)
	r := saveReq()
	r.Populations = pops("brand", "p"+PopulationSuffix)
	cerr := CheckChainModels([]*types.ChainStage{{Request: saveReq()}, {Request: r}}, outer)
	if cerr == nil || cerr.Code != errors.PULSE_MODEL_NAME_COLLISION || cerr.Details["stage"] != 1 {
		t.Fatalf("stage collision: %v %+v", cerr, cerr)
	}
	if cerr := CheckChainModels([]*types.ChainStage{{Request: r}}, nil); cerr != nil {
		t.Fatalf("no registered populations: %v", cerr)
	}
}
