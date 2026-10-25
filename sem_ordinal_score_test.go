package pulse_test

import (
	"context"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// sem_ordinal_score_test.go pins ATTR_SEM_SCORE over an ordinal SEM fit
// (U59 E4-S4) at the facade: lavaan's empirical Bayes modal scores of a
// WLSMV fit — inline, from a Compose `fits` entry and from a saved
// .pulse-sem.json (version 2) — bit-equal across the three, ungrouped
// and by wave; Bartlett refused on an ordinal fit in Process, Compose
// and a loaded file, predict ≡ runtime; an ordered value outside the
// fit's categories scores null with PULSE_SEM_SCORE_LEVEL_UNSEEN. The
// figures themselves are held to lavaan in internal/sem and
// internal/processing.

// semOrdKnobs are the WLSMV fit of the brand CFA with recommend a
// numeric indicator of service (mixed ordered / continuous), grouped
// by wave under thresholds, loadings and intercepts when grouped.
func semOrdKnobs(grouped bool) map[string]any {
	k := map[string]any{"model": semScoreCFA, "estimator": "wlsmv", "ordered": brandOrdered}
	if grouped {
		k["group"], k["group_equal"] = "wave", []string{"thresholds", "loadings", "intercepts"}
	}
	return k
}

func semOrdInline(grouped bool) map[string]any {
	p := semOrdKnobs(grouped)
	p["latent"] = "service"
	return p
}

const semOrdFitPath = "fits/brand_ord" + model.SEMSuffix

// TestSEMScore_OrdinalBoundAndLoadedMatchInline: the bound slot's
// figures (count / min / max / sum of every row's EBM score) equal the
// inline ordinal fit's, ungrouped and grouped; the compose entry carries
// the coding and thresholds; the saved file is version 2 and scores
// bit-equal (Process and a loaded slot); predict accepts all three and
// streams the bound and loaded slots.
func TestSEMScore_OrdinalBoundAndLoadedMatchInline(t *testing.T) {
	p, fsys := brandGroupCohort{csv: "brand", wave: encoding.FieldTypeCategoricalU8}.build(t, pulse.Options{})
	ctx := context.Background()
	for _, grouped := range []bool{false, true} {
		want, err := p.Process(ctx, semScoreSlotReq(semOrdInline(grouped), true))
		if err != nil {
			t.Fatalf("grouped=%v inline: %v", grouped, err)
		}
		wantJSON := dataJSON(t, want.Data) + dataJSON(t, want.Warnings)
		if !strings.Contains(wantJSON, `"sum":`) || strings.Contains(wantJSON, `"sum":null`) {
			t.Fatalf("vacuous: %s", wantJSON)
		}
		creq := &pulse.ComposedRequest{
			Fits:     []types.FitSpec{{Name: "brand", Request: semFitRequest(semOrdKnobs(grouped)), Save: semOrdFitPath}},
			Requests: []*types.Request{semScoreSlotReq(boundParams(), true)},
		}
		out, err := p.Compose(ctx, creq)
		if err != nil {
			t.Fatalf("grouped=%v compose: %v", grouped, err)
		}
		if got := dataJSON(t, out.Responses[0].Data) + dataJSON(t, out.Responses[0].Warnings); got != wantJSON {
			t.Fatalf("grouped=%v: bound figures differ from the inline fit\n got %s\nwant %s", grouped, got, wantJSON)
		}
		s := out.Fits["brand"].SEM
		if s == nil || s.Estimator != "wlsmv" || len(s.Ordered) != len(brandOrdered) || len(s.Categories) != len(brandOrdered) ||
			len(s.Groups[0].Thresholds) != len(brandOrdered) || len(s.Groups[0].Thresholds[0]) != len(s.Categories[0])-1 {
			t.Fatalf("grouped=%v: sem fit %s", grouped, dataJSON(t, s))
		}
		data, err := afero.ReadFile(fsys, semOrdFitPath)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), `"sem_format_version": 2`) {
			t.Fatal("an ordinal fit file is not version 2")
		}
		resp, err := p.Process(ctx, loaded(semScoreSlotReq(boundParams(), true), semOrdFitPath))
		if err != nil {
			t.Fatal(err)
		}
		if got := dataJSON(t, resp.Data) + dataJSON(t, resp.Warnings); got != wantJSON {
			t.Fatalf("grouped=%v: loaded scores differ from the inline fit's\n got %s\nwant %s", grouped, got, wantJSON)
		}
		env := predictEnvelope(t, p, fsys, "brand.pulse", semScoreSlotReq(semOrdInline(grouped), false))
		if len(env.Errors) != 0 || !env.Data.(*descriptor.PredictResult).Streamable {
			t.Fatalf("grouped=%v: predict inline %+v", grouped, env.Errors)
		}
		penv, err := p.PredictCompose(ctx, creq)
		if err != nil || len(penv.Errors) != 0 {
			t.Fatalf("predict compose: %v %+v", err, penv.Errors)
		}
		lenv, err := p.PredictEnvelope(ctx, loaded(semScoreSlotReq(boundParams(), false), semOrdFitPath))
		if err != nil || len(lenv.Errors) != 0 || !lenv.Data.(*descriptor.PredictResult).Streamable {
			t.Fatalf("predict loaded: %v %+v", err, lenv)
		}
	}
}

// TestSEMScore_OrdinalBartlettRefusedPredictMatchesRuntime: method
// "bartlett" on an ordinal fit is SERVICE_VALIDATION bad_params (param
// method) — inline, bound to a Compose fit, bound to a failed one and
// loaded from a file — and predict reports the same entry; "regression"
// is accepted everywhere.
func TestSEMScore_OrdinalBartlettRefusedPredictMatchesRuntime(t *testing.T) {
	p, fsys := brandGroupCohort{csv: "brand", wave: encoding.FieldTypeCategoricalU8}.build(t, pulse.Options{})
	ctx := context.Background()
	bart := func(params map[string]any) map[string]any {
		params["method"] = "bartlett"
		return params
	}
	check := func(what string, err error, env *descriptor.Envelope) {
		t.Helper()
		var ce *errors.CodedError
		if !asCoded(err, &ce) || ce.Code != errors.SERVICE_VALIDATION || ce.Details["reason"] != "bad_params" || ce.Details["param"] != "method" {
			t.Fatalf("%s: %v, want SERVICE_VALIDATION bad_params (method)", what, err)
		}
		sameEntry(t, env, err)
	}
	req := semScoreSlotReq(bart(semOrdInline(false)), false)
	_, err := p.Process(ctx, req)
	check("inline", err, predictEnvelope(t, p, fsys, "brand.pulse", req))

	creq := &pulse.ComposedRequest{
		Fits:     []types.FitSpec{{Name: "brand", Request: semFitRequest(semOrdKnobs(false))}},
		Requests: []*types.Request{semScoreSlotReq(bart(boundParams()), false)},
	}
	_, err = p.Compose(ctx, creq)
	penv, perr := p.PredictCompose(ctx, creq)
	if perr != nil {
		t.Fatal(perr)
	}
	check("bound", err, penv)

	// A fit that fails on its data (one complete row) is still ordinal:
	// the method is refused before the failure is reported.
	thin := semFitRequest(semOrdKnobs(false))
	thin.Filterers = []*types.Filterer{{Type: types.FILTER_RANGE, Field: "q1", Values: []string{"-1", "-1"}}}
	creq.Fits[0].Request = thin
	_, err = p.Compose(ctx, creq)
	if penv, perr = p.PredictCompose(ctx, creq); perr != nil {
		t.Fatal(perr)
	}
	check("failed fit", err, penv)

	if _, err := p.Compose(ctx, semSaveComposeWith(semOrdKnobs(false), semOrdFitPath, semScoreSlotReq(boundParams(), false))); err != nil {
		t.Fatal(err)
	}
	lreq := loaded(semScoreSlotReq(bart(boundParams()), false), semOrdFitPath)
	_, err = p.Process(ctx, lreq)
	lenv, perr := p.PredictEnvelope(ctx, lreq)
	if perr != nil {
		t.Fatal(perr)
	}
	check("loaded", err, lenv)
}

// semSaveComposeWith fits brand with knobs under save and scores slot.
func semSaveComposeWith(knobs map[string]any, save string, slot *types.Request) *pulse.ComposedRequest {
	return &pulse.ComposedRequest{
		Fits:     []types.FitSpec{{Name: "brand", Request: semFitRequest(knobs), Save: save}},
		Requests: []*types.Request{slot},
	}
}

// TestSEMScore_OrdinalUnseenCategory: a fit taken on rows without the
// lowest q1 category codes q1 over the categories it saw; scoring every
// row nulls exactly the rows holding the unseen value, with one
// PULSE_SEM_SCORE_LEVEL_UNSEEN warning (reason category_unseen) counting
// them — bound and loaded alike — and every other row scores.
func TestSEMScore_OrdinalUnseenCategory(t *testing.T) {
	p, _ := brandGroupCohort{csv: "brand", wave: encoding.FieldTypeCategoricalU8}.build(t, pulse.Options{})
	ctx := context.Background()
	fit := semFitRequest(semOrdKnobs(false))
	fit.Filterers = []*types.Filterer{{Type: types.FILTER_RANGE, Field: "q1", Values: []string{"2", "5"}}}
	count := &types.Request{Cohort: &types.Cohort{Filename: "brand.pulse"},
		Filterers:    []*types.Filterer{{Type: types.FILTER_RANGE, Field: "q1", Values: []string{"1", "1"}}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "q1", Label: "n"}}}
	cr, err := p.Process(ctx, count)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dataJSON(t, cr.Data), `"n":`) {
		t.Fatalf("count %s", dataJSON(t, cr.Data))
	}
	out, err := p.Compose(ctx, &pulse.ComposedRequest{
		Fits:     []types.FitSpec{{Name: "brand", Request: fit, Save: semOrdFitPath}},
		Requests: []*types.Request{semScoreSlotReq(boundParams(), false)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if c := out.Fits["brand"].SEM.Categories[0]; len(c) != 4 || c[0] != 2 {
		t.Fatalf("q1 categories %v, want 2..5", c)
	}
	loadedResp, err := p.Process(ctx, loaded(semScoreSlotReq(boundParams(), false), semOrdFitPath))
	if err != nil {
		t.Fatal(err)
	}
	for _, resp := range []*types.Response{out.Responses[0], loadedResp} {
		var w *types.ResponseWarning
		for _, x := range resp.Warnings {
			if x.Code == string(errors.PULSE_SEM_SCORE_LEVEL_UNSEEN) {
				w = x
			}
		}
		if w == nil || w.Details["reason"] != "category_unseen" {
			t.Fatalf("warnings %s", dataJSON(t, resp.Warnings))
		}
		vars, rows := w.Details["variables"].([]string), w.Details["rows"].([]int64)
		if len(vars) != 1 || vars[0] != "q1" || len(rows) != 1 {
			t.Fatalf("details %s", dataJSON(t, w.Details))
		}
		unseen, n, scored := int(rows[0]), countOf(t, cr), countOf(t, resp)
		if unseen != n || n == 0 || scored != 1200-unseen {
			t.Fatalf("unseen %d rows, q1 = 1 on %d, scored %d", unseen, n, scored)
		}
	}
}

// countOf is an ungrouped response's AGG_COUNT "n".
func countOf(t *testing.T, r *types.Response) int {
	t.Helper()
	if len(r.Data) != 1 {
		t.Fatalf("data %s", dataJSON(t, r.Data))
	}
	switch v := r.Data[0]["n"].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	}
	t.Fatalf("n = %#v", r.Data[0]["n"])
	return 0
}
