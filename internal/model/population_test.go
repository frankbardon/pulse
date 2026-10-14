package model

import (
	"bytes"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// population_test.go pins the population file (E2-S3): the codec's
// bit-exact round trip and strict decode, the vector-fit save-path rule,
// and the `populations` load rule shared by the runtime and predict —
// its static entry check, read + decode, binding (as a vector fit in the
// one namespace) and the batch / chain halves.

// edgeVectorFit is a 3-member fit whose figures exercise every Float
// edge: negative zero, subnormals, extremes, an undefined (NaN) mean
// and an infinite co-moment pair.
func edgeVectorFit() *VectorFit {
	nz := math.Copysign(0, -1)
	return &VectorFit{
		Name:   "pop",
		Labels: []string{"q1", "q2", "q3"}, Members: []string{"a", "b", "c"},
		Basis: VectorBasisProbability, N: 11, NMass: 9, NWeightInvalid: 2,
		SumWeights: 0.1 + 0.2, SumWeightsSq: math.SmallestNonzeroFloat64,
		Mean: []float64{nz, math.NaN(), 1e-300},
		M2: [][]float64{
			{math.MaxFloat64, 2.2250738585072009e-308, math.Inf(1)},
			{2.2250738585072009e-308, 123456789.12345679, -3.5},
			{math.Inf(1), -3.5, nz},
		},
		Operator: "MAT_COVARIANCE",
	}
}

func encodedPopulation(t *testing.T, v *VectorFit) []byte {
	t.Helper()
	f, err := NewPopulationFile("pop", v, Provenance{Cohort: "c.pulse", RequestDigest: "d"})
	if err != nil {
		t.Fatal(err)
	}
	data, err := f.Encode()
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func sameVectorFit(t *testing.T, got, want *VectorFit) {
	t.Helper()
	if !reflect.DeepEqual(got.Labels, want.Labels) || !reflect.DeepEqual(got.Members, want.Members) ||
		got.Basis != want.Basis || got.N != want.N || got.NMass != want.NMass || got.NWeightInvalid != want.NWeightInvalid ||
		got.Operator != want.Operator {
		t.Fatalf("state differs:\n got %+v\nwant %+v", got, want)
	}
	if !sameBits(got.SumWeights, want.SumWeights) || !sameBits(got.SumWeightsSq, want.SumWeightsSq) {
		t.Fatalf("weight sums differ: %v %v vs %v %v", got.SumWeights, got.SumWeightsSq, want.SumWeights, want.SumWeightsSq)
	}
	sameFloats(t, "mean", got.Mean, want.Mean)
	if len(got.M2) != len(want.M2) {
		t.Fatalf("m2 rows = %d, want %d", len(got.M2), len(want.M2))
	}
	for i := range want.M2 {
		sameFloats(t, "m2", got.M2[i], want.M2[i])
	}
}

// TestPopulationFile_RoundTripBitExact: a vector fit saved and loaded
// back is the same state to the bit (NaN null, ±Inf strings, negative
// zero and subnormals exact), bound under the loading name; re-encoding
// the decoded file reproduces the bytes.
func TestPopulationFile_RoundTripBitExact(t *testing.T) {
	want := edgeVectorFit()
	data := encodedPopulation(t, want)
	for _, frag := range []string{`"population_format_version": 1`, `"basis": "probability"`, `null`, `"+Inf"`, `-0`, `"m2"`, `"provenance"`} {
		if !strings.Contains(string(data), frag) {
			t.Errorf("encoded file lacks %s:\n%s", frag, data)
		}
	}
	f, err := DecodePopulationFile("p"+PopulationSuffix, data)
	if err != nil {
		t.Fatal(err)
	}
	got := f.VectorFit("loaded")
	if got.Name != "loaded" {
		t.Fatalf("name = %q, want the loading name", got.Name)
	}
	sameVectorFit(t, got, want)
	again, err := f.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(again, data) {
		t.Fatalf("re-encode differs:\n%s\nvs\n%s", again, data)
	}
	if f.Provenance.Cohort != "c.pulse" || f.Name != "pop" {
		t.Fatalf("informational fields lost: %+v", f)
	}
}

// TestNewPopulationFile_RefusesMalformed: a state the decode would
// refuse is never written (an engine invariant, PROCESSING_INTERNAL).
func TestNewPopulationFile_RefusesMalformed(t *testing.T) {
	v := vectorFit("v")
	v.M2[0][1] = 5 // not symmetric
	if _, err := NewPopulationFile("v", v, Provenance{}); coded(t, err).Code != errors.PROCESSING_INTERNAL {
		t.Fatalf("got %v", err)
	}
	if _, err := NewPopulationFile("v", nil, Provenance{}); coded(t, err).Code != errors.PROCESSING_INTERNAL {
		t.Fatalf("nil fit: %v", err)
	}
}

func TestDecodePopulationFile_Refusals(t *testing.T) {
	good := string(encodedPopulation(t, vectorFit("v")))
	edit := func(old, new string) string {
		if !strings.Contains(good, old) {
			t.Fatalf("fixture lacks %q:\n%s", old, good)
		}
		return strings.Replace(good, old, new, 1)
	}
	cases := []struct {
		name   string
		doc    string
		code   errors.Code
		reason string
	}{
		{"json", "{", errors.PULSE_MODEL_FILE_INVALID, ReasonFileJSON},
		{"no version", edit(`"population_format_version": 1,`, ""), errors.PULSE_MODEL_FILE_INVALID, ReasonFileVersionMissing},
		{"null version", edit(`"population_format_version": 1`, `"population_format_version": null`), errors.PULSE_MODEL_FILE_INVALID, ReasonFileVersionMissing},
		{"version string", edit(`"population_format_version": 1`, `"population_format_version": "1"`), errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"future version", edit(`"population_format_version": 1`, `"population_format_version": 2`), errors.PULSE_MODEL_VERSION_UNSUPPORTED, ""},
		{"a model file", string(encodedFullFit(t)), errors.PULSE_MODEL_FILE_INVALID, ReasonFileVersionMissing},
		{"unknown key", edit(`"name": "pop",`, `"name": "pop", "extra": 1,`), errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"no labels", edit(`"labels": [
    "a",
    "b"
  ]`, `"labels": []`), errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"repeated label", edit(`"labels": [
    "a",
    "b"
  ]`, `"labels": ["a", "a"]`), errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"blank label", edit(`"labels": [
    "a",
    "b"
  ]`, `"labels": ["a", " "]`), errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"members unlike labels", edit(`"members": [
    "a",
    "b"
  ]`, `"members": ["a"]`), errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"basis", edit(`"basis": "unweighted"`, `"basis": "analytic"`), errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"negative n", edit(`"n": 3`, `"n": -1`), errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"n_mass above n", edit(`"n_mass": 3`, `"n_mass": 4`), errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"undefined weight sum", edit(`"sum_weights": 3`, `"sum_weights": null`), errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"negative weight sum", edit(`"sum_weights_sq": 3`, `"sum_weights_sq": -3`), errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"mean length", edit(`"mean": [
    1,
    2
  ]`, `"mean": [1]`), errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"m2 rows", edit(`"m2": [
    [
      2,
      1
    ],
    [
      1,
      2
    ]
  ]`, `"m2": [[2, 1]]`), errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"m2 row length", edit(`"m2": [
    [
      2,
      1
    ],`, `"m2": [[2],`), errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"m2 asymmetric", edit(`"m2": [
    [
      2,
      1
    ],`, `"m2": [[2, 1.5],`), errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecodePopulationFile("p"+PopulationSuffix, []byte(tc.doc))
			ce := coded(t, err)
			if ce.Code != tc.code {
				t.Fatalf("code = %s, want %s (%v)", ce.Code, tc.code, ce)
			}
			if ce.Details["path"] != "p"+PopulationSuffix {
				t.Errorf("details.path = %v", ce.Details["path"])
			}
			if tc.reason != "" && ce.Details["reason"] != tc.reason {
				t.Errorf("reason = %v, want %s (%v)", ce.Details["reason"], tc.reason, ce)
			}
		})
	}
	if _, err := DecodePopulationFile("p"+PopulationSuffix, []byte(good)); err != nil {
		t.Fatalf("the unedited fixture is refused: %v", err)
	}
}

func TestCheckPopulationSavePath(t *testing.T) {
	cases := map[string]string{
		"p" + PopulationSuffix:         "",
		"d/e/p" + PopulationSuffix:     "",
		"":                             ReasonSavePathEmpty,
		"p" + FileSuffix:               ReasonSaveSuffix,
		"p.json":                       ReasonSaveSuffix,
		"d/" + PopulationSuffix:        ReasonSaveNameMissing,
		"../p" + PopulationSuffix:      ReasonSaveEscapesRoot,
		"d/../../p" + PopulationSuffix: ReasonSaveEscapesRoot,
	}
	for p, reason := range cases {
		cerr := CheckPopulationSavePath(p)
		if reason == "" {
			if cerr != nil {
				t.Errorf("%q refused: %v", p, cerr)
			}
			continue
		}
		if cerr == nil || cerr.Code != errors.PULSE_MODEL_SAVE_INVALID || cerr.Details["reason"] != reason || cerr.Details["path"] != p {
			t.Errorf("%q: got %v %+v, want reason %s", p, cerr, cerr, reason)
		}
	}
}

func pops(pairs ...string) []types.PopulationRef {
	var out []types.PopulationRef
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, types.PopulationRef{Name: pairs[i], Path: pairs[i+1]})
	}
	return out
}

func TestCheckRefs_Populations(t *testing.T) {
	ok, mok := "p"+PopulationSuffix, "m"+FileSuffix
	fits := CatalogFromFits([]types.FitSpec{{Name: "f", Request: olsReq(ols())}})
	cases := []struct {
		name    string
		refs    Refs
		outer   Catalog
		code    errors.Code
		details map[string]any
	}{
		{"valid", Refs{Models: refs("m", mok), Populations: pops("p", ok, "q", "d/q"+PopulationSuffix)}, fits, "", nil},
		{"name empty", Refs{Populations: pops("", ok)}, nil, errors.PULSE_MODEL_REF_INVALID,
			map[string]any{"population": 0, "name": "", "path": ok, "reason": ReasonRefNameEmpty}},
		{"model suffix", Refs{Populations: pops("p", ok, "q", mok)}, nil, errors.PULSE_MODEL_REF_INVALID,
			map[string]any{"population": 1, "name": "q", "path": mok, "reason": ReasonSaveSuffix}},
		{"escapes", Refs{Populations: pops("p", "../"+ok)}, nil, errors.PULSE_MODEL_REF_INVALID,
			map[string]any{"population": 0, "name": "p", "path": "../" + ok, "reason": ReasonSaveEscapesRoot}},
		{"twice", Refs{Populations: pops("p", ok, "p", "o"+PopulationSuffix)}, nil, errors.PULSE_MODEL_NAME_COLLISION,
			map[string]any{"name": "p", "sources": []any{
				map[string]any{"slot": "populations", "index": 0, "path": ok},
				map[string]any{"slot": "populations", "index": 1, "path": "o" + PopulationSuffix}}}},
		{"collides with a model", Refs{Models: refs("x", mok), Populations: pops("x", ok)}, nil, errors.PULSE_MODEL_NAME_COLLISION,
			map[string]any{"name": "x", "sources": []any{
				map[string]any{"slot": "models", "index": 0, "path": mok},
				map[string]any{"slot": "populations", "index": 0, "path": ok}}}},
		{"collides with a fit", Refs{Populations: pops("f", ok)}, fits, errors.PULSE_MODEL_NAME_COLLISION,
			map[string]any{"name": "f", "sources": []any{
				map[string]any{"slot": "fits", "index": 0},
				map[string]any{"slot": "populations", "index": 0, "path": ok}}}},
		{"models judged first", Refs{Models: refs("", mok), Populations: pops("", ok)}, nil, errors.PULSE_MODEL_REF_INVALID,
			map[string]any{"model": 0, "name": "", "path": mok, "reason": ReasonRefNameEmpty}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cerr := CheckRefs(tc.refs, tc.outer)
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

func TestLoadFiles_Populations(t *testing.T) {
	ok := "p" + PopulationSuffix
	files := map[string][]byte{
		ok:                        encodedPopulation(t, vectorFit("v")),
		"m" + FileSuffix:          encodedFullFit(t),
		"io" + PopulationSuffix:   nil,
		"bad" + PopulationSuffix:  []byte(`{"population_format_version": 9}`),
		"junk" + PopulationSuffix: []byte(`[`),
	}
	read := memRead(files)
	loaded, cerr := LoadFiles(Refs{Models: refs("m", "m"+FileSuffix), Populations: pops("a", ok, "b", ok)}, read)
	if cerr != nil {
		t.Fatal(cerr)
	}
	if len(loaded.Models) != 1 || len(loaded.Populations) != 2 || loaded.Populations[1].Labels[1] != "b" {
		t.Fatalf("loaded = %+v", loaded)
	}
	if l, cerr := LoadFiles(Refs{}, read); l != nil || cerr != nil {
		t.Fatalf("empty refs loaded %v %v", l, cerr)
	}
	cases := []struct {
		name, path string
		code       errors.Code
		reason     string
	}{
		{"missing", "gone" + PopulationSuffix, errors.PULSE_MODEL_FILE_UNREADABLE, ReasonFileMissing},
		{"unreadable", "io" + PopulationSuffix, errors.PULSE_MODEL_FILE_UNREADABLE, ReasonFileUnreadable},
		{"version", "bad" + PopulationSuffix, errors.PULSE_MODEL_VERSION_UNSUPPORTED, ""},
		{"json", "junk" + PopulationSuffix, errors.PULSE_MODEL_FILE_INVALID, ReasonFileJSON},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, cerr := LoadFiles(Refs{Populations: pops("a", ok, "z", tc.path)}, read)
			if cerr == nil || cerr.Code != tc.code || cerr.Details["population"] != 1 || cerr.Details["name"] != "z" || cerr.Details["path"] != tc.path {
				t.Fatalf("got %v %+v", cerr, cerr)
			}
			if tc.reason != "" && cerr.Details["reason"] != tc.reason {
				t.Fatalf("reason = %v, want %s", cerr.Details["reason"], tc.reason)
			}
			if !strings.HasPrefix(cerr.Message, "populations[1]: ") {
				t.Fatalf("message = %q", cerr.Message)
			}
		})
	}
	// No reader is unreadable, never a panic.
	if _, cerr := LoadFiles(Refs{Populations: pops("a", ok)}, nil); cerr == nil || cerr.Code != errors.PULSE_MODEL_FILE_UNREADABLE {
		t.Fatalf("nil reader: %v", cerr)
	}
}

// TestSetWithLoaded: a loaded population binds as a vector fit under
// the entry's name (Source populations[k]) beside the outer set's and
// the loaded models' names; the outer set is untouched; the predict
// catalog sees the same names, kinds and members.
func TestSetWithLoaded(t *testing.T) {
	ok, mok := "p"+PopulationSuffix, "m"+FileSuffix
	read := memRead(map[string][]byte{ok: encodedPopulation(t, edgeVectorFit()), mok: encodedFullFit(t)})
	outer := &Set{}
	if err := outer.AddVector(vectorFit("fitted")); err != nil {
		t.Fatal(err)
	}
	rs := Refs{Models: refs("m", mok), Populations: pops("pop", ok)}
	if cerr := CheckRefs(rs, outer); cerr != nil {
		t.Fatal(cerr)
	}
	loaded, cerr := LoadFiles(rs, read)
	if cerr != nil {
		t.Fatal(cerr)
	}
	set, err := outer.WithLoaded(rs, loaded)
	if err != nil {
		t.Fatal(err)
	}
	if got := set.Names(); !reflect.DeepEqual(got, []string{"fitted", "m", "pop"}) {
		t.Fatalf("names = %v", got)
	}
	if outer.Len() != 1 {
		t.Fatalf("outer mutated: %v", outer.Names())
	}
	vf, err := set.LookupVector("pop")
	if err != nil {
		t.Fatal(err)
	}
	sameVectorFit(t, vf, edgeVectorFit())
	if vf.Source != PopulationSource(0, ok) {
		t.Fatalf("source = %+v", vf.Source)
	}
	if _, err := set.Lookup("pop"); coded(t, err).Code != errors.PULSE_MODEL_KIND_MISMATCH {
		t.Fatalf("regression lookup of a population: %v", err)
	}
	// Predict's static view: the same names, kinds, members and sources.
	cat := CatalogWithLoaded(outer, rs, loaded)
	for _, n := range set.Names() {
		want, _ := set.Signature(n)
		got, ok := cat.Signature(n)
		if !ok || got.EffectiveKind() != want.EffectiveKind() || !reflect.DeepEqual(got.Members, want.Members) || got.Source != want.Source {
			t.Errorf("%s: catalog %+v, set %+v", n, got, want)
		}
	}
	if cerr := CheckVectorFit(cat, "pop"); cerr != nil {
		t.Fatalf("catalog population is not a vector fit: %v", cerr)
	}
	// Nothing to load returns the outer set itself.
	if same, err := outer.WithLoaded(Refs{}, nil); err != nil || same != outer {
		t.Fatalf("empty refs: %v %v", same, err)
	}
}

// TestCheckComposeBatch_Populations: the batch rule judges every fit
// request's and slot's `populations` (a slot's against the fits'
// names), and a vector fit's population save takes part in the
// duplicate-path rule.
func TestCheckComposeBatch_Populations(t *testing.T) {
	ok := "p" + PopulationSuffix
	vecReq := func() *types.Request {
		return &types.Request{Matrices: []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Fields: []string{"a", "b"}}}}
	}
	fits := []types.FitSpec{{Name: "f", Request: vecReq(), Save: ok}}
	withPops := func(r *types.Request, ps []types.PopulationRef) *types.Request { r.Populations = ps; return r }
	runBatchCases(t, []batchCase{
		{"clean", fits, []*types.Request{withPops(saveReq(), pops("g", ok))}, "", nil},
		{"slot population collides with a fit", fits, []*types.Request{withPops(saveReq(), pops("f", ok))}, errors.PULSE_MODEL_NAME_COLLISION,
			map[string]any{"name": "f", "request": 0, "sources": []any{
				map[string]any{"slot": "fits", "index": 0}, map[string]any{"slot": "populations", "index": 0, "path": ok}}}},
		{"fit request population", []types.FitSpec{{Name: "f", Request: withPops(saveReq(), pops("x", "x.json"))}}, nil, errors.PULSE_MODEL_REF_INVALID,
			map[string]any{"population": 0, "name": "x", "path": "x.json", "reason": ReasonSaveSuffix, "fit": 0}},
		{"population save duplicated", []types.FitSpec{{Name: "f", Request: vecReq(), Save: ok}, {Name: "g", Request: vecReq(), Save: "./" + ok}}, nil, errors.PULSE_MODEL_SAVE_INVALID,
			map[string]any{"path": "./" + ok, "reason": ReasonSaveDuplicate, "sites": []any{"fits[0]", "fits[1]"}}},
	})
}

func TestCheckChainModels_Populations(t *testing.T) {
	stage := func(r *types.Request) *types.ChainStage { return &types.ChainStage{Request: r} }
	r := saveReq()
	r.Populations = pops("p", "p"+FileSuffix)
	cerr := CheckChainModels([]*types.ChainStage{stage(saveReq()), stage(r)})
	if cerr == nil || cerr.Code != errors.PULSE_MODEL_REF_INVALID || cerr.Details["stage"] != 1 || cerr.Details["population"] != 0 || cerr.Details["reason"] != ReasonSaveSuffix {
		t.Fatalf("stage population ref: %v %+v", cerr, cerr)
	}
}
