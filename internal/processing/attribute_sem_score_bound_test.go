package processing

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// attribute_sem_score_bound_test.go pins ATTR_SEM_SCORE's params.fit
// (U59 E3-S2): a SEM fit captured from a MAT_SEM fit request's slot and
// bound back scores every row to the bit of the inline fit over the
// same fit rows — single-group (covariance-only and mean structure) and
// multi-group, both methods, on every arm (the bound slot streams
// grouped too) — and the bound slot's refusals and failure modes.

// captureSEMFit runs a MAT_SEM slot with params over the brand fixture
// on a set carrying a capture and returns the one SEM fit its finalizer
// offered, named name.
func captureSEMFit(t *testing.T, schema *encoding.Schema, mk func() []*Record, name string, params map[string]any) *model.SEMFit {
	t.Helper()
	capture := &model.VectorCapture{}
	recs := mk()
	for i, r := range recs {
		r.SetMergePosition(0, 0, i) // the matrix slot's blocked merge reads it
	}
	p := NewProcessor(schema)
	p.SetModels((&model.Set{}).WithCapture(capture))
	req := &types.Request{Matrices: []types.MatrixSpec{{Name: "sem", Type: types.MAT_SEM, Fields: brandMembers, Params: semScoreParamsJSON(t, params)}}}
	resp, err := p.Process(context.Background(), req, NewSliceIterator(recs))
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Matrices) != 1 || resp.Matrices[0].Error != nil {
		t.Fatalf("MAT_SEM fit failed: %+v", resp.Matrices)
	}
	if got := capture.Fits(); len(got) != 0 {
		t.Fatalf("a MAT_SEM battery offered %d vector fits; it binds a SEM fit only", len(got))
	}
	fits := capture.SEMFits()
	if len(fits) != 1 {
		t.Fatalf("MAT_SEM offered %d SEM fits, want 1", len(fits))
	}
	fits[0].Name = name
	return fits[0]
}

func semSetOf(t *testing.T, fits ...*model.SEMFit) *model.Set {
	t.Helper()
	set := &model.Set{}
	for _, f := range fits {
		if err := set.AddSEM(f); err != nil {
			t.Fatal(err)
		}
	}
	return set
}

// semScoreSlot is a one-attribute request scoring "score" over
// brandMembers with params.
func semScoreSlot(t *testing.T, params map[string]any) func() *types.Request {
	return func() *types.Request {
		return &types.Request{Attributes: []*types.Attribute{{Type: types.ATTR_SEM_SCORE, Label: "score", Fields: brandMembers, Params: semScoreParamsJSON(t, params)}}}
	}
}

func warningCodes(ws []*types.ResponseWarning) []string {
	out := []string{}
	for _, w := range ws {
		out = append(out, w.Code)
	}
	return out
}

// TestSEMScore_BoundMatchesInline: a captured SEM fit bound through
// params.fit scores every row to the bit of the inline fit with the same
// knobs (the fit rows are every row) — covariance-only, mean structure
// and the joint two-wave scalar fit, each latent by both methods — on
// the streaming, grouped-streaming and buffered arms, with the inline
// fit's warnings (none).
func TestSEMScore_BoundMatchesInline(t *testing.T) {
	t.Parallel()

	schema, mk := brandSEMFixture(t, nil, nil)
	desc := brandModel(t, "brand_ms")
	for _, c := range []struct {
		name  string
		knobs map[string]any
	}{
		{"covariance", map[string]any{}},
		{"meanstructure", map[string]any{"meanstructure": true}},
		{"groups", map[string]any{"group": "wave", "group_equal": []string{"loadings", "intercepts"}}},
	} {
		knobs := map[string]any{"model": desc}
		for k, v := range c.knobs {
			knobs[k] = v
		}
		sf := captureSEMFit(t, schema, mk, "brand", knobs)
		set := semSetOf(t, sf)
		for _, method := range []string{"regression", "bartlett"} {
			for _, lat := range sf.Latents {
				t.Run(c.name+"/"+method+"/"+lat, func(t *testing.T) {
					inline := map[string]any{"latent": lat, "method": method}
					for k, v := range knobs {
						inline[k] = v
					}
					want, _, wantResp := runFittedArms(t, schema, mk, "score", nil, semScoreSlot(t, inline))
					got, _, gotResp := runFittedArms(t, schema, mk, "score", set, semScoreSlot(t, map[string]any{"fit": "brand", "latent": lat, "method": method}))
					if !sameBits(got, want) {
						t.Fatalf("bound scores differ from the inline fit's\n got %v\nwant %v", got[:5], want[:5])
					}
					scored := 0
					for _, v := range got {
						if !math.IsNaN(v) {
							scored++
						}
					}
					if scored == 0 {
						t.Fatal("vacuous: no row scored")
					}
					if g, w := warningCodes(gotResp.Warnings), warningCodes(wantResp.Warnings); len(g) != 0 || len(w) != 0 {
						t.Fatalf("warnings: bound %v, inline %v", g, w)
					}
				})
			}
		}
	}
}

// TestSEMScore_BoundReadsTheFit is the falsification arm: a bound fit
// whose stored parameters are perturbed (one loading, one latent mean)
// no longer reproduces the inline scores — the equality reads the
// stored matrices, never the rows.
func TestSEMScore_BoundReadsTheFit(t *testing.T) {
	t.Parallel()

	schema, mk := brandSEMFixture(t, nil, nil)
	knobs := map[string]any{"model": brandModel(t, "brand_ms"), "group": "wave", "group_equal": []string{"loadings", "intercepts"}}
	inline := map[string]any{"latent": "value"}
	for k, v := range knobs {
		inline[k] = v
	}
	want, _, _ := runFittedArms(t, schema, mk, "score", nil, semScoreSlot(t, inline))
	for name, mut := range map[string]func(*model.SEMFit){
		"lambda": func(f *model.SEMFit) { f.Groups[1].Lambda[4][1] *= 1.01 },
		"alpha":  func(f *model.SEMFit) { f.Groups[1].Alpha[1] += 0.01 },
	} {
		t.Run(name, func(t *testing.T) {
			sf := captureSEMFit(t, schema, mk, "brand", knobs)
			mut(sf)
			got, _, _ := runFittedArms(t, schema, mk, "score", semSetOf(t, sf), semScoreSlot(t, map[string]any{"fit": "brand", "latent": "value"}))
			if sameBits(got, want) {
				t.Fatalf("a %s-perturbed fit still reproduces the inline scores: the equality cannot bite", name)
			}
		})
	}
}

// TestSEMScore_BoundRefusals: params.fit beside a fit knob or fit_on is
// refused before any record (SERVICE_VALIDATION fit_and_model /
// fit_and_fit_on); an unbound name is PULSE_MODEL_UNKNOWN, a vector
// fit's PULSE_MODEL_KIND_MISMATCH (details.attribute), a vector
// consumer naming the SEM fit PULSE_MODEL_KIND_MISMATCH the other way;
// a latent the fit's model lacks is latent_unknown; member labels other
// than the fit's are PULSE_VECTOR_REFERENCE_MISMATCH.
func TestSEMScore_BoundRefusals(t *testing.T) {
	t.Parallel()

	schema, mk := brandSEMFixture(t, nil, nil)
	sf := captureSEMFit(t, schema, mk, "brand", map[string]any{"model": brandModel(t, "brand_ms"), "meanstructure": true})
	set := semSetOf(t, sf)
	if err := set.AddVector(&model.VectorFit{Name: "vec", Labels: []string{"q1", "q2"}, Members: []string{"q1", "q2"}}); err != nil {
		t.Fatal(err)
	}
	run := func(attr *types.Attribute) error {
		p := NewProcessor(schema)
		p.SetModels(set)
		_, err := p.Process(context.Background(), &types.Request{Attributes: []*types.Attribute{attr}}, NewSliceIterator(mk()))
		return err
	}
	slot := func(params map[string]any) *types.Attribute {
		return &types.Attribute{Type: types.ATTR_SEM_SCORE, Label: "score", Fields: brandMembers, Params: semScoreParamsJSON(t, params)}
	}
	for _, c := range []struct {
		name   string
		attr   *types.Attribute
		code   errors.Code
		reason string
	}{
		{"fit_and_model", slot(map[string]any{"fit": "brand", "model": "F =~ q1 + q2 + q3"}), errors.SERVICE_VALIDATION, "fit_and_model"},
		{"fit_and_group", slot(map[string]any{"fit": "brand", "group": "wave"}), errors.SERVICE_VALIDATION, "fit_and_model"},
		{"fit_and_fit_on", slot(map[string]any{"fit": "brand", "fit_on": []*types.Filterer{{Type: types.FILTER_TRUE}}}), errors.SERVICE_VALIDATION, "fit_and_fit_on"},
		{"unknown", slot(map[string]any{"fit": "nope", "latent": "value"}), errors.PULSE_MODEL_UNKNOWN, ""},
		{"vector_kind", slot(map[string]any{"fit": "vec", "latent": "value"}), errors.PULSE_MODEL_KIND_MISMATCH, ""},
		{"sem_into_vector", &types.Attribute{Type: types.ATTR_MAHALANOBIS, Label: "d", Fields: brandMembers, Params: semScoreParamsJSON(t, map[string]any{"fit": "brand"})}, errors.PULSE_MODEL_KIND_MISMATCH, ""},
		{"latent_unknown", slot(map[string]any{"fit": "brand", "latent": "loyalty"}), errors.SERVICE_VALIDATION, "latent_unknown"},
		{"labels_differ", &types.Attribute{Type: types.ATTR_SEM_SCORE, Label: "score", Fields: brandMembers[:9], Params: semScoreParamsJSON(t, map[string]any{"fit": "brand", "latent": "value"})}, errors.PULSE_VECTOR_REFERENCE_MISMATCH, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := run(c.attr)
			ce, ok := err.(*errors.CodedError)
			if !ok || ce.Code != c.code {
				t.Fatalf("got %v, want %s", err, c.code)
			}
			if c.reason != "" && ce.Details["reason"] != c.reason {
				t.Fatalf("reason %v, want %s (%s)", ce.Details["reason"], c.reason, ce.Message)
			}
			if c.code == errors.PULSE_MODEL_KIND_MISMATCH {
				want := map[string]string{"vector_kind": "vector", "sem_into_vector": "sem"}[c.name]
				if ce.Details["kind"] != want {
					t.Fatalf("details.kind %v, want %s", ce.Details["kind"], want)
				}
			}
		})
	}
}

// TestSEMScore_BoundFailedFitNulls: a SEM fit bound as failed (its
// Compose fit failed on its data) nulls every score with one
// PULSE_FIT_UNAVAILABLE warning naming the fit's code; the slot is
// still row-local (streams).
func TestSEMScore_BoundFailedFitNulls(t *testing.T) {
	t.Parallel()

	schema, mk := brandSEMFixture(t, nil, nil)
	set := &model.Set{}
	if err := set.AddFailed(model.Signature{Name: "brand", Kind: types.FitKindSEM}, &types.SlotError{Code: string(errors.PULSE_MATRIX_SINGULAR), Message: "singular"}); err != nil {
		t.Fatal(err)
	}
	got, _, resp := runFittedArms(t, schema, mk, "score", set, semScoreSlot(t, map[string]any{"fit": "brand"}))
	for i, v := range got {
		if !math.IsNaN(v) {
			t.Fatalf("row %d scored %v from a failed fit", i, v)
		}
	}
	if len(resp.Warnings) != 1 || resp.Warnings[0].Code != string(errors.PULSE_FIT_UNAVAILABLE) || resp.Warnings[0].Details["code"] != string(errors.PULSE_MATRIX_SINGULAR) {
		wj, _ := json.Marshal(resp.Warnings)
		t.Fatalf("warnings %s, want one PULSE_FIT_UNAVAILABLE carrying PULSE_MATRIX_SINGULAR", wj)
	}
}

// TestSEMScore_BoundUnseenLevelByLabel: a bound multi-group fit matches
// each row's group to its levels by LABEL — on a cohort whose wave
// dictionary orders the levels differently ("2" first) every row scores
// as on the fit's own cohort — and a level the fit has no parameters for
// scores null with PULSE_SEM_SCORE_LEVEL_UNSEEN.
func TestSEMScore_BoundUnseenLevelByLabel(t *testing.T) {
	t.Parallel()

	schema, mk := brandSEMFixture(t, nil, nil)
	sf := captureSEMFit(t, schema, mk, "brand", map[string]any{"model": brandModel(t, "brand_ms"), "group": "wave"})
	set := semSetOf(t, sf)
	want, _, _ := runFittedArms(t, schema, mk, "score", set, semScoreSlot(t, map[string]any{"fit": "brand", "latent": "service"}))

	// The same rows over a dictionary {"2", "1", "3"}, and every 7th row
	// moved to level "3" (unseen by the fit).
	re, mkRe := brandSEMFixtureDict(t, []string{"2", "1", "3"}, func(r int) string {
		if r%7 == 3 {
			return "3"
		}
		return ""
	})
	got, _, resp := runFittedArms(t, re, mkRe, "score", set, semScoreSlot(t, map[string]any{"fit": "brand", "latent": "service"}))
	unseen := 0
	for r := range got {
		if r%7 == 3 {
			if !math.IsNaN(got[r]) {
				t.Fatalf("row %d of unseen level 3 scored %v", r, got[r])
			}
			unseen++
			continue
		}
		if math.Float64bits(got[r]) != math.Float64bits(want[r]) {
			t.Fatalf("row %d: %v on the reordered dictionary, %v on the fit's", r, got[r], want[r])
		}
	}
	if len(resp.Warnings) != 1 || resp.Warnings[0].Code != string(errors.PULSE_SEM_SCORE_LEVEL_UNSEEN) {
		t.Fatalf("warnings %v, want one PULSE_SEM_SCORE_LEVEL_UNSEEN", warningCodes(resp.Warnings))
	}
	rows, _ := resp.Warnings[0].Details["rows"].([]int64)
	if len(rows) != 1 || rows[0] != int64(unseen) {
		t.Fatalf("unseen tally %v, want [%d]", resp.Warnings[0].Details["rows"], unseen)
	}
}

// TestSEMScore_BoundMergeTally: the bound slot's unseen-level tally is a
// per-run integer the parallel reducers fold (MergeTally): two
// partitions' instances scoring halves of the rows report, merged, the
// serial instance's warning; resetTally clears it.
func TestSEMScore_BoundMergeTally(t *testing.T) {
	t.Parallel()

	schema, mk := brandSEMFixture(t, nil, nil)
	sf := captureSEMFit(t, schema, mk, "brand", map[string]any{"model": brandModel(t, "brand_ms"), "group": "wave"})
	set := semSetOf(t, sf)
	re, mkRe := brandSEMFixture(t, []string{"3"}, func(r int) string {
		if r%5 == 0 {
			return "3"
		}
		return ""
	})
	build := func() *semScoreAttribute {
		attr := &types.Attribute{Type: types.ATTR_SEM_SCORE, Label: "score", Fields: brandMembers, Params: semScoreParamsJSON(t, map[string]any{"fit": "brand", "latent": "value"})}
		comp, err := newSEMScoreAttribute(attr, re)
		if err != nil {
			t.Fatal(err)
		}
		a := comp.(*semScoreAttribute)
		if err := a.bindFitOn(nil, set, nil, nil); err != nil {
			t.Fatal(err)
		}
		if !a.prebuiltFit() {
			t.Fatal("a bound slot is not prebuilt")
		}
		return a
	}
	recs := mkRe()
	score := func(a *semScoreAttribute, rs []*Record) {
		for _, r := range rs {
			if _, err := a.Row(r, "score"); err != nil {
				t.Fatal(err)
			}
		}
	}
	serial := build()
	score(serial, recs)
	left, right := build(), build()
	score(left, recs[:500])
	score(right, recs[500:])
	if err := left.MergeTally(right); err != nil {
		t.Fatal(err)
	}
	ws, wm := serial.attributeWarnings(), left.attributeWarnings()
	sj, _ := json.Marshal(ws)
	mj, _ := json.Marshal(wm)
	if len(ws) != 1 || ws[0].Code != string(errors.PULSE_SEM_SCORE_LEVEL_UNSEEN) || string(sj) != string(mj) {
		t.Fatalf("merged warnings %s, serial %s", mj, sj)
	}
	left.resetTally()
	if len(left.attributeWarnings()) != 0 {
		t.Fatal("resetTally kept the unseen tally")
	}
}
