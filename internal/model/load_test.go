package model

import (
	stderrors "errors"
	"fmt"
	"io/fs"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// load_test.go pins the `models` rule shared by the runtime and predict:
// the static entry check, the read + decode, the binding (bit-identical
// to File.Bind, stamped with its source) and the batch / chain save
// rules.

// memRead is a ReadFunc over an in-memory file map; a missing path is
// fs.ErrNotExist, a path mapped to nil an I/O fault.
func memRead(files map[string][]byte) ReadFunc {
	return func(p string) ([]byte, error) {
		data, ok := files[p]
		if !ok {
			return nil, fmt.Errorf("open %s: %w", p, fs.ErrNotExist)
		}
		if data == nil {
			return nil, stderrors.New("input/output error")
		}
		return data, nil
	}
}

// encodedFullFit is fullFit's model file bytes.
func encodedFullFit(t *testing.T) []byte {
	t.Helper()
	spec, res := fullFit()
	f, err := NewFile("m", spec, res, Provenance{})
	if err != nil {
		t.Fatal(err)
	}
	data, err := f.Encode()
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func refs(pairs ...string) []types.ModelRef {
	var out []types.ModelRef
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, types.ModelRef{Name: pairs[i], Path: pairs[i+1]})
	}
	return out
}

func TestCheckModelRefs(t *testing.T) {
	ok := "m" + FileSuffix
	fits := CatalogFromFits([]types.FitSpec{{Name: "a", Request: olsReq(ols())}, {Name: "f", Request: olsReq(ols())}})
	cases := []struct {
		name    string
		refs    []types.ModelRef
		outer   Catalog
		code    errors.Code
		details map[string]any
	}{
		{"valid", refs("x", ok, "y", "dir/y"+FileSuffix), fits, "", nil},
		{"name empty", refs(" ", ok), nil, errors.PULSE_MODEL_REF_INVALID,
			map[string]any{"model": 0, "name": " ", "path": ok, "reason": ReasonRefNameEmpty}},
		{"path empty", refs("x", ok, "y", ""), nil, errors.PULSE_MODEL_REF_INVALID,
			map[string]any{"model": 1, "name": "y", "path": "", "reason": ReasonSavePathEmpty}},
		{"suffix", refs("x", "m.json"), nil, errors.PULSE_MODEL_REF_INVALID,
			map[string]any{"model": 0, "name": "x", "path": "m.json", "reason": ReasonSaveSuffix}},
		{"stem", refs("x", "d/"+FileSuffix), nil, errors.PULSE_MODEL_REF_INVALID,
			map[string]any{"model": 0, "name": "x", "path": "d/" + FileSuffix, "reason": ReasonSaveNameMissing}},
		{"escapes", refs("x", "../"+ok), nil, errors.PULSE_MODEL_REF_INVALID,
			map[string]any{"model": 0, "name": "x", "path": "../" + ok, "reason": ReasonSaveEscapesRoot}},
		{"twice in models", refs("x", ok, "x", "n"+FileSuffix), nil, errors.PULSE_MODEL_NAME_COLLISION,
			map[string]any{"name": "x", "sources": []any{
				map[string]any{"slot": "models", "index": 0, "path": ok},
				map[string]any{"slot": "models", "index": 1, "path": "n" + FileSuffix}}}},
		{"collides with a fit", refs("x", ok, "f", ok), fits, errors.PULSE_MODEL_NAME_COLLISION,
			map[string]any{"name": "f", "sources": []any{
				map[string]any{"slot": "fits", "index": 1},
				map[string]any{"slot": "models", "index": 1, "path": ok}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cerr := CheckModelRefs(tc.refs, tc.outer)
			if tc.code == "" {
				if cerr != nil {
					t.Fatalf("refused: %v", cerr)
				}
				return
			}
			if cerr == nil || cerr.Code != tc.code || !reflect.DeepEqual(cerr.Details, tc.details) {
				t.Fatalf("got %v %+v, want %s %+v", cerr, cerr, tc.code, tc.details)
			}
		})
	}
}

func TestLoadModelFiles(t *testing.T) {
	good := encodedFullFit(t)
	files := map[string][]byte{
		"good" + FileSuffix: good,
		"bad" + FileSuffix:  []byte("{not json"),
		"v3" + FileSuffix:   []byte(`{"model_format_version": 3}`),
		"io" + FileSuffix:   nil,
	}
	got, cerr := LoadModelFiles(refs("a", "good"+FileSuffix), memRead(files))
	if cerr != nil || len(got) != 1 || got[0].Type != types.REG_OLS {
		t.Fatalf("load: %v %+v", cerr, got)
	}
	if got, cerr := LoadModelFiles(nil, nil); got != nil || cerr != nil {
		t.Fatal("no refs must read nothing")
	}
	cases := []struct {
		path   string
		read   ReadFunc
		code   errors.Code
		reason any
	}{
		{"none" + FileSuffix, memRead(files), errors.PULSE_MODEL_FILE_UNREADABLE, ReasonFileMissing},
		{"io" + FileSuffix, memRead(files), errors.PULSE_MODEL_FILE_UNREADABLE, ReasonFileUnreadable},
		{"good" + FileSuffix, nil, errors.PULSE_MODEL_FILE_UNREADABLE, ReasonFileUnreadable},
		{"bad" + FileSuffix, memRead(files), errors.PULSE_MODEL_FILE_INVALID, ReasonFileJSON},
		{"v3" + FileSuffix, memRead(files), errors.PULSE_MODEL_VERSION_UNSUPPORTED, nil},
	}
	for _, tc := range cases {
		// The fault is the second entry: details.model locates it.
		_, cerr := LoadModelFiles(refs("a", "good"+FileSuffix, "b", tc.path), tc.read)
		if tc.read == nil {
			_, cerr = LoadModelFiles(refs("a", "good"+FileSuffix), nil)
		}
		if cerr == nil || cerr.Code != tc.code {
			t.Fatalf("%s: got %v, want %s", tc.path, cerr, tc.code)
		}
		if cerr.Details["reason"] != tc.reason {
			t.Errorf("%s: reason %v, want %v", tc.path, cerr.Details["reason"], tc.reason)
		}
		wantModel, wantName := 1, "b"
		if tc.read == nil {
			wantModel, wantName = 0, "a"
		}
		if cerr.Details["model"] != wantModel || cerr.Details["name"] != wantName {
			t.Errorf("%s: details %+v, want model %d name %s", tc.path, cerr.Details, wantModel, wantName)
		}
	}
}

// TestSetWithModels: a loaded model binds bit-identically to File.Bind
// under its entry's name, stamped models[j]; the outer set is never
// mutated; predict's catalog gives every name the runtime's Signature.
func TestSetWithModels(t *testing.T) {
	data := encodedFullFit(t)
	f, err := DecodeFile("m"+FileSuffix, data)
	if err != nil {
		t.Fatal(err)
	}
	outer := &Set{}
	fit, err := Bind("fit", ols(), &types.RegressionResult{Type: types.REG_OLS, Coefficients: map[string]float64{InterceptKey: 1, "x": 2}})
	if err != nil {
		t.Fatal(err)
	}
	fit.Source = FitSource(0)
	if err := outer.Add(fit); err != nil {
		t.Fatal(err)
	}
	rs := refs("loaded", "m"+FileSuffix)
	set, err := outer.WithModels(rs, []*File{f})
	if err != nil {
		t.Fatal(err)
	}
	if outer.Len() != 1 || set.Len() != 2 {
		t.Fatalf("outer %d names, extended %d; want 1 and 2", outer.Len(), set.Len())
	}
	got, err := set.Lookup("loaded")
	if err != nil {
		t.Fatal(err)
	}
	want, err := f.Bind("loaded")
	if err != nil {
		t.Fatal(err)
	}
	sameBound(t, got, want)
	if got.Source != ModelSource(0, "m"+FileSuffix) {
		t.Fatalf("source = %+v", got.Source)
	}
	cat := CatalogWithModels(CatalogFromFits([]types.FitSpec{{Name: "fit", Request: olsReq(ols())}}), rs, []*File{f})
	if !reflect.DeepEqual(cat.Names(), set.Names()) {
		t.Fatalf("catalog names %v, set names %v", cat.Names(), set.Names())
	}
	for _, n := range set.Names() {
		rt, _ := set.Signature(n)
		pr, _ := cat.Signature(n)
		if !reflect.DeepEqual(rt, pr) {
			t.Errorf("%s: runtime %+v, predict %+v", n, rt, pr)
		}
	}
	var none *Set
	if s, err := none.WithModels(nil, nil); err != nil || s.Len() != 0 {
		t.Fatal("nil set must extend to an empty set")
	}
}

// TestCheckComposeBatch_Models: the batch rule's load half — a fit
// request's `models` see nothing outside it (details.fit), a slot's are
// judged against the fits' names (details.request, both sources).
func TestCheckComposeBatch_Models(t *testing.T) {
	a := "a" + FileSuffix
	fits := []types.FitSpec{{Name: "f", Request: olsReq(ols()), Save: a}}
	withModels := func(r *types.Request, rs []types.ModelRef) *types.Request { r.Models = rs; return r }
	runBatchCases(t, []batchCase{
		{"clean", fits, []*types.Request{withModels(saveReq(), refs("g", a))}, "", nil},
		{"slot model collides with a fit", fits, []*types.Request{withModels(saveReq(), refs("f", a))}, errors.PULSE_MODEL_NAME_COLLISION,
			map[string]any{"name": "f", "request": 0, "sources": []any{
				map[string]any{"slot": "fits", "index": 0}, map[string]any{"slot": "models", "index": 0, "path": a}}}},
		{"fit request model", []types.FitSpec{{Name: "f", Request: withModels(saveReq(), refs("", a))}}, nil, errors.PULSE_MODEL_REF_INVALID,
			map[string]any{"model": 0, "name": "", "path": a, "reason": ReasonRefNameEmpty, "fit": 0}},
		{"a fit request's own names are not the batch's", []types.FitSpec{{Name: "f", Request: withModels(saveReq(), refs("f", a))}}, nil, "", nil},
	})
}

func TestCheckChainModels(t *testing.T) {
	stage := func(r *types.Request) *types.ChainStage { return &types.ChainStage{Request: r} }
	ok := saveReq()
	ok.Models = refs("m", "m"+FileSuffix)
	again := saveReq()
	again.Models = refs("m", "m"+FileSuffix) // each stage its own namespace
	if cerr := CheckChainModels([]*types.ChainStage{stage(ok), nil, stage(again)}, nil); cerr != nil {
		t.Fatalf("refused: %v", cerr)
	}
	r := saveReq()
	r.Models = refs("m", "nope.json")
	cerr := CheckChainModels([]*types.ChainStage{stage(ok), stage(r)}, nil)
	if cerr == nil || cerr.Code != errors.PULSE_MODEL_REF_INVALID || cerr.Details["stage"] != 1 || cerr.Details["reason"] != ReasonSaveSuffix {
		t.Fatalf("stage model ref: %v %+v", cerr, cerr)
	}
}
