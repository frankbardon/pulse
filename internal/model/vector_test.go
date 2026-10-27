package model

import (
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func vectorFit(name string) *VectorFit {
	return &VectorFit{Name: name, Labels: []string{"a", "b"}, Members: []string{"a", "b"}, Basis: VectorBasisUnweighted,
		N: 3, NMass: 3, SumWeights: 3, SumWeightsSq: 3, Mean: []float64{1, 2}, M2: [][]float64{{2, 1}, {1, 2}}}
}

// TestSet_VectorFits: vector fits share the regression namespace (a
// collision either way is PULSE_MODEL_NAME_COLLISION), resolve by
// LookupVector, and an unbound or regression name is PULSE_MODEL_UNKNOWN
// listing the bound vector fits; WithModels carries them over.
func TestSet_VectorFits(t *testing.T) {
	t.Parallel()

	s := &Set{}
	if err := s.AddVector(vectorFit("pop")); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(&Bound{Name: "reg", Type: types.REG_OLS}); err != nil {
		t.Fatal(err)
	}
	if got := coded(t, s.AddVector(vectorFit("reg"))); got.Code != errors.PULSE_MODEL_NAME_COLLISION {
		t.Fatalf("vector over a regression: %v", got)
	}
	if got := coded(t, s.Add(&Bound{Name: "pop"})); got.Code != errors.PULSE_MODEL_NAME_COLLISION {
		t.Fatalf("regression over a vector: %v", got)
	}
	if got := coded(t, s.AddVector(vectorFit("pop"))); got.Code != errors.PULSE_MODEL_NAME_COLLISION {
		t.Fatalf("vector twice: %v", got)
	}
	v, err := s.LookupVector("pop")
	if err != nil || v.Name != "pop" {
		t.Fatalf("LookupVector(pop) = %v, %v", v, err)
	}
	ce := coded(t, func() error { _, err := s.LookupVector("nope"); return err }())
	if ce.Code != errors.PULSE_MODEL_UNKNOWN || !reflect.DeepEqual(ce.Details["available"], []string{"pop"}) {
		t.Fatalf("unknown: %v %v", ce, ce.Details)
	}
	// One namespace, two kinds: a name bound as the other kind is
	// PULSE_MODEL_KIND_MISMATCH carrying both kinds, either way round.
	ce = coded(t, func() error { _, err := s.LookupVector("reg"); return err }())
	if ce.Code != errors.PULSE_MODEL_KIND_MISMATCH || ce.Details["fit"] != "reg" || ce.Details["kind"] != "regression" || ce.Details["expected"] != "vector" {
		t.Fatalf("regression name: %v %v", ce, ce.Details)
	}
	ce = coded(t, func() error { _, err := s.Lookup("pop"); return err }())
	if ce.Code != errors.PULSE_MODEL_KIND_MISMATCH || ce.Details["fit"] != "pop" || ce.Details["kind"] != "vector" || ce.Details["expected"] != "regression" {
		t.Fatalf("regression Lookup of a vector fit: %v %v", ce, ce.Details)
	}
	ce = coded(t, func() error { _, err := s.Lookup("nope"); return err }())
	if ce.Code != errors.PULSE_MODEL_UNKNOWN || !reflect.DeepEqual(ce.Details["available"], []string{"reg"}) {
		t.Fatalf("regression unknown lists the regressions: %v %v", ce, ce.Details)
	}
	if !reflect.DeepEqual(s.Names(), []string{"pop", "reg"}) || !reflect.DeepEqual(s.RegressionNames(), []string{"reg"}) ||
		!reflect.DeepEqual(s.VectorNames(), []string{"pop"}) || s.Len() != 2 {
		t.Fatalf("names %v / %v / %v len %d", s.Names(), s.RegressionNames(), s.VectorNames(), s.Len())
	}
	if sig, ok := s.Signature("pop"); !ok || sig.Kind != types.FitKindVector || !reflect.DeepEqual(sig.Members, []string{"a", "b"}) {
		t.Fatalf("vector signature %+v %v", sig, ok)
	}
	if sig, ok := s.Signature("reg"); !ok || sig.EffectiveKind() != types.FitKindRegression {
		t.Fatalf("regression signature %+v %v", sig, ok)
	}
	var nilSet *Set
	if _, err := nilSet.LookupVector("x"); coded(t, err).Code != errors.PULSE_MODEL_UNKNOWN {
		t.Fatal("nil set")
	}
	out, err := s.WithModels(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if v, err := out.LookupVector("pop"); err != nil || v.Name != "pop" {
		t.Fatalf("WithModels dropped the vector fit: %v", err)
	}
}

// TestVectorFit_NStar is the fold's N*: Σw unweighted / frequency,
// Kish n_eff under probability (0 with no mass).
func TestVectorFit_NStar(t *testing.T) {
	t.Parallel()

	v := vectorFit("x")
	v.SumWeights, v.SumWeightsSq = 4, 6
	if got := v.NStar(); got != 4 {
		t.Fatalf("unweighted N* %v", got)
	}
	v.Basis = VectorBasisFrequency
	if got := v.NStar(); got != 4 {
		t.Fatalf("frequency N* %v", got)
	}
	v.Basis = VectorBasisProbability
	if got := v.NStar(); math.Float64bits(got) != math.Float64bits(4.0*4.0/6.0) {
		t.Fatalf("probability N* %v", got)
	}
	v.SumWeights, v.SumWeightsSq = 0, 0
	if got := v.NStar(); got != 0 {
		t.Fatalf("empty probability N* %v", got)
	}
	if v.P() != 2 {
		t.Fatal("P")
	}
}

// TestCheckVectorFit is the static vector-fit reference rule over the
// runtime Set and predict's FitCatalog alike: a vector name passes, an
// unbound one is PULSE_MODEL_UNKNOWN listing the vector fits only, a
// regression name PULSE_MODEL_KIND_MISMATCH; the regression consumers
// (CheckCoef, CheckScore) refuse a vector name the same way.
func TestCheckVectorFit(t *testing.T) {
	t.Parallel()

	set := &Set{}
	if err := set.AddVector(vectorFit("pop")); err != nil {
		t.Fatal(err)
	}
	if err := set.Add(&Bound{Name: "reg", Type: types.REG_OLS, Terms: []string{"x"}}); err != nil {
		t.Fatal(err)
	}
	vecReq := &types.Request{Matrices: []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Fields: []string{"a", "b"}}}}
	cat := CatalogFromFits([]types.FitSpec{{Name: "pop", Request: vecReq}, {Name: "reg", Request: olsReq(ols())}})
	for name, c := range map[string]Catalog{"runtime": set, "predict": cat} {
		t.Run(name, func(t *testing.T) {
			if cerr := CheckVectorFit(c, "pop"); cerr != nil {
				t.Fatalf("vector name: %v", cerr)
			}
			cerr := CheckVectorFit(c, "nope")
			if cerr == nil || cerr.Code != errors.PULSE_MODEL_UNKNOWN || !reflect.DeepEqual(cerr.Details["available"], []string{"pop"}) {
				t.Fatalf("unknown: %v", cerr)
			}
			cerr = CheckVectorFit(c, "reg")
			if cerr == nil || cerr.Code != errors.PULSE_MODEL_KIND_MISMATCH || cerr.Details["fit"] != "reg" || cerr.Details["expected"] != "vector" {
				t.Fatalf("regression: %v", cerr)
			}
			cerr = CheckCoef(c, "pop", "a", StatPoint)
			if cerr == nil || cerr.Code != errors.PULSE_MODEL_KIND_MISMATCH || cerr.Details["kind"] != "vector" {
				t.Fatalf("coef of a vector fit: %v", cerr)
			}
			cerr = CheckCoef(c, "nope", "a", StatPoint)
			if cerr == nil || cerr.Code != errors.PULSE_MODEL_UNKNOWN || !reflect.DeepEqual(cerr.Details["available"], []string{"reg"}) {
				t.Fatalf("coef unknown: %v", cerr)
			}
			_, _, cerr = CheckScore(&types.Attribute{Type: types.ATTR_REG_PREDICT, Params: []byte(`{"fit":"pop"}`)}, c)
			if cerr == nil || cerr.Code != errors.PULSE_MODEL_KIND_MISMATCH {
				t.Fatalf("score a vector fit: %v", cerr)
			}
		})
	}
	if cerr := CheckVectorFit(nil, "pop"); cerr == nil || cerr.Code != errors.PULSE_MODEL_UNKNOWN {
		t.Fatalf("nil catalog: %v", cerr)
	}
}

// TestVectorCapture: offers are kept in order, a nil capture ignores
// them, and WithCapture copies the namespace without mutating it.
func TestVectorCapture(t *testing.T) {
	t.Parallel()

	base := &Set{}
	if err := base.AddVector(vectorFit("pop")); err != nil {
		t.Fatal(err)
	}
	c := &VectorCapture{}
	s := base.WithCapture(c)
	if s.Capture() != c || base.Capture() != nil || (*Set)(nil).Capture() != nil {
		t.Fatal("capture not carried by the copy only")
	}
	if _, err := s.LookupVector("pop"); err != nil {
		t.Fatalf("copy lost the namespace: %v", err)
	}
	s.Capture().Offer(vectorFit("a"))
	s.Capture().Offer(vectorFit("b"))
	(*VectorCapture)(nil).Offer(vectorFit("c"))
	if got := c.Fits(); len(got) != 2 || got[0].Name != "a" || got[1].Name != "b" {
		t.Fatalf("offers %v", got)
	}
	if (*Set)(nil).WithCapture(c).Len() != 0 {
		t.Fatal("nil set copy not empty")
	}
}

// TestFitKindOf: a regression fit, a matrix or fitted-attribute vector
// fit, or "" for anything CheckFits refuses.
func TestFitKindOf(t *testing.T) {
	t.Parallel()

	mat := types.MatrixSpec{Type: types.MAT_COVARIANCE, Fields: []string{"a", "b"}}
	attr := &types.Attribute{Type: types.ATTR_MAHALANOBIS, Fields: []string{"a", "b"}}
	other := &types.Attribute{Type: types.ATTR_FORMULA, Expression: "a"}
	for name, c := range map[string]struct {
		req  *types.Request
		want types.FitKind
	}{
		"regression":      {olsReq(ols()), types.FitKindRegression},
		"matrix":          {&types.Request{Matrices: []types.MatrixSpec{mat}}, types.FitKindVector},
		"attribute":       {&types.Request{Attributes: []*types.Attribute{other, attr}}, types.FitKindVector},
		"both":            {&types.Request{Regressions: []*types.RegressionSpec{ols()}, Matrices: []types.MatrixSpec{mat}}, ""},
		"two batteries":   {&types.Request{Matrices: []types.MatrixSpec{mat}, Attributes: []*types.Attribute{attr}}, ""},
		"nothing":         {&types.Request{Attributes: []*types.Attribute{other}}, ""},
		"null regression": {olsReq(nil), ""},
		"nil":             {nil, ""},
	} {
		if got := FitKindOf(c.req); got != c.want {
			t.Errorf("%s: kind %q, want %q", name, got, c.want)
		}
	}
}
