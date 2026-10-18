package model

import (
	stderrors "errors"
	"slices"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// failedSet binds a failed regression fit "m" (terms x) and a failed
// vector fit "pop".
func failedSet(t *testing.T) *Set {
	t.Helper()
	s := &Set{}
	if err := s.AddFailed(Signature{Name: "m", Type: types.REG_GLM, Terms: []string{"x"}, Kind: types.FitKindRegression, Source: FitSource(0)},
		&types.SlotError{Code: string(errors.PROCESSING_REGRESSION_SEPARATION), Message: "separated", Details: map[string]any{"predictors": []string{"x"}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddFailed(Signature{Name: "pop", Kind: types.FitKindVector, Source: FitSource(1)},
		&types.SlotError{Code: string(errors.PULSE_MATRIX_SINGULAR), Message: "singular"}); err != nil {
		t.Fatal(err)
	}
	return s
}

func codeOf(err error) errors.Code {
	var ce *errors.CodedError
	if stderrors.As(err, &ce) {
		return ce.Code
	}
	return ""
}

// TestSet_FailedFit: a failed fit stays in the one namespace under its
// static signature (every derived set keeps it) — so a reference passes
// the same static checks as in predict — while every model reader is
// refused with the fit's OWN code and details.fit, a reader of the
// other kind with PULSE_MODEL_KIND_MISMATCH, and a second binding of
// the name collides.
func TestSet_FailedFit(t *testing.T) {
	base := failedSet(t)
	derived := map[string]*Set{
		"base":    base,
		"clone":   base.Clone(),
		"models":  must(base.WithModels(nil, nil)),
		"capture": base.WithCapture(&VectorCapture{}),
		"records": base.WithRecords(map[string]RecordBinding{"k": {}}),
	}
	for name, s := range derived {
		if sig, ok := s.Signature("m"); !ok || sig.Type != types.REG_GLM || !slices.Equal(sig.Terms, []string{"x"}) {
			t.Errorf("%s: signature %+v %v", name, sig, ok)
		}
		if se, ok := s.Failed("m"); !ok || se.Code != string(errors.PROCESSING_REGRESSION_SEPARATION) {
			t.Errorf("%s: Failed(m) %+v %v", name, se, ok)
		}
		if !slices.Equal(s.Names(), []string{"m", "pop"}) || s.Len() != 2 ||
			!slices.Equal(s.RegressionNames(), []string{"m"}) || !slices.Equal(s.VectorNames(), []string{"pop"}) {
			t.Errorf("%s: names %v / %v / %v", name, s.Names(), s.RegressionNames(), s.VectorNames())
		}
		_, err := s.Lookup("m")
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != errors.PROCESSING_REGRESSION_SEPARATION || ce.Details["fit"] != "m" || ce.Details["predictors"] == nil {
			t.Errorf("%s: Lookup(m) %v", name, err)
		}
		if _, err := s.LookupVector("pop"); codeOf(err) != errors.PULSE_MATRIX_SINGULAR {
			t.Errorf("%s: LookupVector(pop) %v", name, err)
		}
		if _, err := s.LookupVector("m"); codeOf(err) != errors.PULSE_MODEL_KIND_MISMATCH {
			t.Errorf("%s: LookupVector(m) %v", name, err)
		}
		if _, err := s.Lookup("pop"); codeOf(err) != errors.PULSE_MODEL_KIND_MISMATCH {
			t.Errorf("%s: Lookup(pop) %v", name, err)
		}
		if _, cerr := Coef(s, "m", "x", StatPoint); cerr == nil || cerr.Code != errors.PROCESSING_REGRESSION_SEPARATION {
			t.Errorf("%s: coef(m) %v", name, cerr)
		}
		if err := CheckVectorFit(s, "pop"); err != nil {
			t.Errorf("%s: CheckVectorFit(pop) %v, want the static check to pass", name, err)
		}
	}
	if err := base.Add(&Bound{Name: "m"}); codeOf(err) != errors.PULSE_MODEL_NAME_COLLISION {
		t.Errorf("rebinding a failed name: %v", err)
	}
	// Clone never shares the failed map with its source.
	c := base.Clone()
	if err := c.AddFailed(Signature{Name: "z"}, &types.SlotError{Code: "X"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := base.Failed("z"); ok {
		t.Error("AddFailed on a clone reached its source")
	}
	w := FitUnavailable("m", "score", &types.SlotError{Code: "PROCESSING_REGRESSION_SEPARATION"})
	if w.Code != string(errors.PULSE_FIT_UNAVAILABLE) || w.Details["fit"] != "m" || w.Details["attribute"] != "score" ||
		w.Details["code"] != "PROCESSING_REGRESSION_SEPARATION" || w.Message == "" {
		t.Errorf("warning %+v", w)
	}
}

func must(s *Set, err error) *Set {
	if err != nil {
		panic(err)
	}
	return s
}
