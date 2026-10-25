package model

import (
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// sem_test.go pins the SEM fit kind in the one namespace (U59 E3-S2):
// FitKindOf / CatalogFromFits classify a MAT_SEM fit request as sem,
// CheckFits judges its save by the SEM fit file's path rule, and the Set binds, copies and resolves a
// SEMFit beside the other kinds — each reader refusing the others'
// names with PULSE_MODEL_KIND_MISMATCH.

func semFitReq() *types.Request {
	return &types.Request{Matrices: []types.MatrixSpec{{Type: types.MAT_SEM, Fields: []string{"a", "b", "c"}}}}
}

func TestSEMFit_FitKindOf(t *testing.T) {
	if k := FitKindOf(semFitReq()); k != types.FitKindSEM {
		t.Fatalf("MAT_SEM fit request: kind %q, want sem", k)
	}
	cov := semFitReq()
	cov.Matrices[0].Type = types.MAT_COVARIANCE
	if k := FitKindOf(cov); k != types.FitKindVector {
		t.Fatalf("MAT_COVARIANCE fit request: kind %q, want vector", k)
	}
	two := semFitReq()
	two.Matrices = append(two.Matrices, two.Matrices[0])
	if k := FitKindOf(two); k != "" {
		t.Fatalf("two batteries: kind %q, want none", k)
	}
	cat := CatalogFromFits([]types.FitSpec{{Name: "s", Request: semFitReq()}})
	if sig := cat["s"]; sig.Kind != types.FitKindSEM {
		t.Fatalf("catalog signature %+v, want kind sem", sig)
	}
	if err := CheckFits([]types.FitSpec{{Name: "s", Request: semFitReq()}}); err != nil {
		t.Fatalf("a SEM fit request is refused: %v", err)
	}
	if err := CheckFits([]types.FitSpec{{Name: "s", Request: semFitReq(), Save: "fits/s.pulse-sem.json"}}); err != nil {
		t.Fatalf("a SEM fit's .pulse-sem.json save is refused: %v", err)
	}
	for _, c := range []struct{ path, reason string }{
		{"fits/s" + FileSuffix, ReasonSaveSuffix},
		{"fits/s" + PopulationSuffix, ReasonSaveSuffix},
		{"../s" + SEMSuffix, ReasonSaveEscapesRoot},
		{"fits/" + SEMSuffix, ReasonSaveNameMissing},
	} {
		err := CheckFits([]types.FitSpec{{Name: "s", Request: semFitReq(), Save: c.path}})
		ce, ok := err.(*errors.CodedError)
		if !ok || ce.Code != errors.PULSE_MODEL_SAVE_INVALID || ce.Details["reason"] != c.reason || ce.Details["fit"] != 0 {
			t.Fatalf("save %q on a SEM fit: %v, want reason %s", c.path, err, c.reason)
		}
	}
	// A regression or vector fit never saves under the SEM suffix.
	if err := CheckFits([]types.FitSpec{{Name: "r", Request: olsReq(ols()), Save: "r" + SEMSuffix}}); err == nil {
		t.Fatal("a regression fit saves a .pulse-sem.json")
	}
}

func TestSEMFit_SetNamespace(t *testing.T) {
	set := &Set{}
	sf := &SEMFit{Name: "s", Labels: []string{"a", "b"}}
	if err := set.AddSEM(sf); err != nil {
		t.Fatal(err)
	}
	if err := set.AddVector(&VectorFit{Name: "s"}); err == nil {
		t.Fatal("a vector fit rebinds a SEM fit's name")
	}
	if err := set.AddVector(&VectorFit{Name: "v"}); err != nil {
		t.Fatal(err)
	}
	if err := set.Add(&Bound{Name: "r", Type: types.REG_OLS}); err != nil {
		t.Fatal(err)
	}
	if set.Len() != 3 || len(set.Names()) != 3 || len(set.SEMNames()) != 1 {
		t.Fatalf("Len %d Names %v SEMNames %v", set.Len(), set.Names(), set.SEMNames())
	}
	if got, err := set.LookupSEM("s"); err != nil || got != sf {
		t.Fatalf("LookupSEM: %v %v", got, err)
	}
	kind := func(err error) (errors.Code, any, any) {
		ce, _ := err.(*errors.CodedError)
		if ce == nil {
			return "", nil, nil
		}
		return ce.Code, ce.Details["kind"], ce.Details["expected"]
	}
	for _, c := range []struct {
		name           string
		err            error
		code           errors.Code
		kind, expected any
	}{
		{"sem reader, vector", func() error { _, e := set.LookupSEM("v"); return e }(), errors.PULSE_MODEL_KIND_MISMATCH, "vector", "sem"},
		{"sem reader, regression", func() error { _, e := set.LookupSEM("r"); return e }(), errors.PULSE_MODEL_KIND_MISMATCH, "regression", "sem"},
		{"vector reader, sem", func() error { _, e := set.LookupVector("s"); return e }(), errors.PULSE_MODEL_KIND_MISMATCH, "sem", "vector"},
		{"regression reader, sem", func() error { _, e := set.Lookup("s"); return e }(), errors.PULSE_MODEL_KIND_MISMATCH, "sem", "regression"},
		{"unbound", func() error { _, e := set.LookupSEM("x"); return e }(), errors.PULSE_MODEL_UNKNOWN, nil, nil},
		{"static sem reader, vector", CheckSEMFit(set, "v"), errors.PULSE_MODEL_KIND_MISMATCH, "vector", "sem"},
		{"static vector reader, sem", CheckVectorFit(set, "s"), errors.PULSE_MODEL_KIND_MISMATCH, "sem", "vector"},
		{"static unbound", CheckSEMFit(set, "x"), errors.PULSE_MODEL_UNKNOWN, nil, nil},
	} {
		code, k, e := kind(c.err)
		if code != c.code || k != c.kind || e != c.expected {
			t.Errorf("%s: %v (kind %v expected %v), want %s %v %v", c.name, c.err, k, e, c.code, c.kind, c.expected)
		}
	}
	if sig, ok := set.Signature("s"); !ok || sig.Kind != types.FitKindSEM || len(sig.Members) != 2 {
		t.Fatalf("signature %+v", sig)
	}
	// Every copy carries the SEM fit.
	for name, cp := range map[string]*Set{"clone": set.Clone(), "capture": set.WithCapture(&VectorCapture{}), "records": set.WithRecords(nil)} {
		if _, err := cp.LookupSEM("s"); err != nil {
			t.Errorf("%s copy lost the SEM fit: %v", name, err)
		}
	}
	// A failed SEM fit is its own refusal to a SEM reader, a kind
	// mismatch to the others.
	failed := &Set{}
	if err := failed.AddFailed(Signature{Name: "s", Kind: types.FitKindSEM}, &types.SlotError{Code: string(errors.PULSE_MATRIX_SINGULAR), Message: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := failed.LookupSEM("s"); err == nil || err.(*errors.CodedError).Code != errors.PULSE_MATRIX_SINGULAR {
		t.Fatalf("failed SEM fit: %v", err)
	}
	if _, ok := failed.FailedOfKind("s", types.FitKindSEM); !ok {
		t.Fatal("FailedOfKind(sem) misses the failed SEM fit")
	}
	if _, err := failed.LookupVector("s"); err == nil || err.(*errors.CodedError).Code != errors.PULSE_MODEL_KIND_MISMATCH {
		t.Fatalf("vector reader of a failed SEM fit: %v", err)
	}
}

func TestSEMFit_ResultCopies(t *testing.T) {
	sf := &SEMFit{Operator: "MAT_SEM", Model: "F =~ a + b + c", Labels: []string{"a", "b", "c"}, Lavaan: true, NearestPD: true, MinEigenvalue: 1e-3,
		Groups: []SEMGroup{{Lambda: [][]float64{{1}}, Nu: []float64{2}}}}
	r := sf.Result()
	if r.Conventions != "lavaan" || r.NearestPDMinEigenvalue == nil || *r.NearestPDMinEigenvalue != 1e-3 || r.Latents == nil {
		t.Fatalf("result %+v", r)
	}
	r.Groups[0].Lambda[0][0] = 9
	if sf.Groups[0].Lambda[0][0] != 1 {
		t.Fatal("Result shares the bound fit's matrices")
	}
}
