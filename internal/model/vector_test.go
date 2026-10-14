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
	ce = coded(t, func() error { _, err := s.LookupVector("reg"); return err }())
	if ce.Code != errors.PULSE_MODEL_UNKNOWN || ce.Details["kind"] != "regression" {
		t.Fatalf("regression name: %v %v", ce, ce.Details)
	}
	if _, err := s.Lookup("pop"); coded(t, err).Code != errors.PULSE_MODEL_UNKNOWN {
		t.Fatalf("regression Lookup of a vector fit: %v", err)
	}
	if !reflect.DeepEqual(s.Names(), []string{"reg"}) || !reflect.DeepEqual(s.VectorNames(), []string{"pop"}) || s.Len() != 1 {
		t.Fatalf("names %v / %v len %d", s.Names(), s.VectorNames(), s.Len())
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
