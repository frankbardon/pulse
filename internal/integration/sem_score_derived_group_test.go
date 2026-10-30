package integration_test

import (
	"context"
	stderrors "errors"
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

// sem_score_derived_group_test.go is ATTR_SEM_SCORE over a DERIVED
// group (params.group naming a column the request computes — an
// earlier attribute's label or a feature output), inline and bound: the
// inline fit is MAT_SEM's derived-group fit, so its scores equal the
// cohort-field twin's bit for bit; a bound fit (a Compose `fits` entry
// or a saved file) carries the computed group's name and declared
// levels, and the scoring request must compute the same column, else
// SERVICE_VALIDATION fit_group_field — predict ≡ runtime. A row whose
// value is no level scores null (PULSE_MATRIX_GROUP_LEVEL_UNDECLARED),
// a declared level without fit rows warns PULSE_MATRIX_GROUP_LEVEL_EMPTY
// (Strict fails the run with it).

// periodAttr is the ATTR_FORMULA computing the group "period".
func periodAttr(expr string) *types.Attribute {
	return &types.Attribute{Type: types.ATTR_FORMULA, Label: "period", Expression: expr}
}

// semScoreDerivedReq is semScoreSlotReq with "period" (expr) computed
// before the score slot.
func semScoreDerivedReq(params map[string]any, expr string, withSum bool) *types.Request {
	r := semScoreSlotReq(params, withSum)
	r.Attributes = append([]*types.Attribute{periodAttr(expr)}, r.Attributes...)
	return r
}

// derivedInline is the inline params grouped by "period" (extra keys
// merged in).
func derivedInline(extra map[string]any) map[string]any {
	p := inlineParams()
	p["group"] = "period"
	for k, v := range extra {
		p[k] = v
	}
	return p
}

// derivedFitRequest is the brand fit grouped by "period" (= wave)
// under declared levels.
func derivedFitRequest(levels any) *types.Request {
	knobs := semFitKnobs()
	knobs["group"] = "period"
	if levels != nil {
		knobs["group_levels"] = levels
	}
	r := semFitRequest(knobs)
	r.Attributes = []*types.Attribute{periodAttr("wave")}
	return r
}

func processData(t *testing.T, p *pulse.Pulse, req *types.Request) (string, *types.Response) {
	t.Helper()
	resp, err := p.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	return dataJSON(t, resp.Data), resp
}

// TestSEMScore_DerivedGroupMatchesFieldGroup: scoring inline on a
// computed copy of the u8 wave field — its levels declared (from / to)
// or observed — gives the cohort-field fit's scores bit for bit, per
// wave; predict accepts the request and calls it buffered, as runtime
// runs it.
func TestSEMScore_DerivedGroupMatchesFieldGroup(t *testing.T) {
	t.Parallel()

	p, fsys := brandGroupCohort{csv: "brand", wave: encoding.FieldTypeU8}.build(t, pulse.Options{})
	want, _ := processData(t, p, byWave(semScoreSlotReq(inlineParams(), true)))
	if !strings.Contains(want, `"sum":`) || strings.Contains(want, `"sum":null`) {
		t.Fatalf("vacuous field fit: %s", want)
	}
	for _, c := range []struct {
		name  string
		extra map[string]any
	}{
		{"declared", map[string]any{"group_levels": fromTo, "group_reference": "from"}},
		{"observed", nil},
	} {
		req := byWave(semScoreDerivedReq(derivedInline(c.extra), "wave", true))
		got, resp := processData(t, p, req)
		if got != want {
			t.Fatalf("%s: derived scores differ from the field fit\n got %s\nwant %s", c.name, got, want)
		}
		if len(resp.Warnings) != 0 {
			t.Fatalf("%s: warnings %+v", c.name, resp.Warnings)
		}
		env := predictEnvelope(t, p, fsys, "brand.pulse", req)
		if len(env.Errors) != 0 {
			t.Fatalf("%s: predict refuses %+v", c.name, env.Errors)
		}
	}
}

// TestSEMScore_DerivedGroupBoundFit: a Compose fit grouped by a
// computed "period" under declared levels carries group_derived and
// group_levels; a scoring request computing "period" scores from it
// bit-equal to the inline fit, row-local (predict streams it); the
// saved file (sem_format_version 3) loads and scores the same.
func TestSEMScore_DerivedGroupBoundFit(t *testing.T) {
	t.Parallel()

	p, fsys := brandGroupCohort{csv: "brand", wave: encoding.FieldTypeU8}.build(t, pulse.Options{})
	ctx := context.Background()
	inline := derivedInline(map[string]any{"group_levels": fromTo})
	want, _ := processData(t, p, byWave(semScoreDerivedReq(inline, "wave", true)))
	const path = "fits/derived" + model.SEMSuffix
	composed := &pulse.ComposedRequest{
		Fits:     []types.FitSpec{{Name: "brand", Request: derivedFitRequest(fromTo), Save: path}},
		Requests: []*types.Request{byWave(semScoreDerivedReq(boundParams(), "wave", true))},
	}
	out, err := p.Compose(ctx, composed)
	if err != nil {
		t.Fatal(err)
	}
	if got := dataJSON(t, out.Responses[0].Data); got != want {
		t.Fatalf("bound scores differ from the inline fit\n got %s\nwant %s", got, want)
	}
	s := out.Fits["brand"].SEM
	if s == nil || s.Group != "period" || !s.GroupDerived || len(s.GroupLevels) != 2 ||
		s.GroupLevels[0] != (types.SEMFitGroupLevel{Value: 1, Label: "from"}) || s.GroupLevels[1] != (types.SEMFitGroupLevel{Value: 2, Label: "to"}) ||
		len(s.Groups) != 2 || s.Groups[0].Level != "from" {
		t.Fatalf("fit entry %s", dataJSON(t, s))
	}
	penv, err := p.PredictCompose(ctx, composed)
	if err != nil || len(penv.Errors) != 0 {
		t.Fatalf("predict compose: %v %+v", err, penv)
	}
	data, err := afero.ReadFile(fsys, path)
	if err != nil {
		t.Fatal(err)
	}
	file, err := model.DecodeSEMFile(path, data)
	if err != nil {
		t.Fatal(err)
	}
	if file.SEMFormatVersion != model.SEMFormatVersionDerivedGroup {
		t.Fatalf("sem_format_version %d, want %d", file.SEMFormatVersion, model.SEMFormatVersionDerivedGroup)
	}
	if g, w := dataJSON(t, file.SEMFit("brand").Result()), dataJSON(t, s); g != w {
		t.Fatalf("the file's fit differs from the compose entry\n got %s\nwant %s", g, w)
	}
	got, _ := processData(t, p, loaded(byWave(semScoreDerivedReq(boundParams(), "wave", true)), path))
	if got != want {
		t.Fatalf("loaded scores differ from the inline fit\n got %s\nwant %s", got, want)
	}
	lreq := loaded(semScoreDerivedReq(boundParams(), "wave", false), path)
	env := predictEnvelope(t, p, fsys, "brand.pulse", lreq)
	if len(env.Errors) != 0 || !env.Data.(*descriptor.PredictResult).Streamable {
		t.Fatalf("predict on the loaded fit: %+v", env)
	}
}

// TestSEMScore_DerivedGroupBoundUndefined: a bound fit grouped by a
// computed column scores only where the scoring request computes that
// column ahead of the slot: absent, computed after the slot, or a
// cohort field of the same name is SERVICE_VALIDATION fit_group_field —
// Compose, PredictCompose and the loaded file's Process / predict
// return the same entry.
func TestSEMScore_DerivedGroupBoundUndefined(t *testing.T) {
	t.Parallel()

	p, fsys := brandGroupCohort{csv: "brand", wave: encoding.FieldTypeU8}.build(t, pulse.Options{})
	ctx := context.Background()
	const path = "fits/undefined" + model.SEMSuffix
	if _, err := p.Compose(ctx, &pulse.ComposedRequest{
		Fits:     []types.FitSpec{{Name: "brand", Request: derivedFitRequest(fromTo), Save: path}},
		Requests: []*types.Request{semScoreDerivedReq(boundParams(), "wave", false)},
	}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		slot func() *types.Request
	}{
		{"absent", func() *types.Request { return semScoreSlotReq(boundParams(), false) }},
		{"computed after the slot", func() *types.Request {
			r := semScoreSlotReq(boundParams(), false)
			r.Attributes = append(r.Attributes, periodAttr("wave"))
			return r
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			composed := &pulse.ComposedRequest{
				Fits:     []types.FitSpec{{Name: "brand", Request: derivedFitRequest(fromTo)}},
				Requests: []*types.Request{c.slot()},
			}
			_, rerr := p.Compose(ctx, composed)
			var ce *errors.CodedError
			if !stderrors.As(rerr, &ce) || ce.Code != errors.SERVICE_VALIDATION || ce.Details["reason"] != "fit_group_field" ||
				ce.Details["group_field"] != "period" || ce.Details["fit"] != "brand" {
				t.Fatalf("runtime %v (%+v), want fit_group_field", rerr, ce)
			}
			env, err := p.PredictCompose(ctx, composed)
			if err != nil {
				t.Fatal(err)
			}
			semSameRefusal(t, "Compose", rerr, semEnvError(t, env, errors.SERVICE_VALIDATION))

			req := loaded(c.slot(), path)
			_, lerr := p.Process(ctx, req)
			if !stderrors.As(lerr, &ce) || ce.Details["reason"] != "fit_group_field" {
				t.Fatalf("loaded runtime %v, want fit_group_field", lerr)
			}
			semSameRefusal(t, "loaded", lerr, semEnvError(t, predictEnvelope(t, p, fsys, "brand.pulse", req), errors.SERVICE_VALIDATION))
		})
	}
}

// TestSEMScore_DerivedGroupLevelMismatch: scoring a declared-level
// bound fit where the request's "period" takes a value the fit never
// declared (3 for wave 2) nulls those rows and warns once
// PULSE_MATRIX_GROUP_LEVEL_UNDECLARED with their count; an inline fit
// declaring a level no fit row holds warns PULSE_MATRIX_GROUP_LEVEL_EMPTY
// (details.levels) and Strict fails the run with that code.
func TestSEMScore_DerivedGroupLevelMismatch(t *testing.T) {
	t.Parallel()

	p, _ := brandGroupCohort{csv: "brand", wave: encoding.FieldTypeU8}.build(t, pulse.Options{})
	ctx := context.Background()
	out, err := p.Compose(ctx, &pulse.ComposedRequest{
		Fits:     []types.FitSpec{{Name: "brand", Request: derivedFitRequest(fromTo)}},
		Requests: []*types.Request{byWave(semScoreDerivedReq(boundParams(), "wave == 2 ? 3 : wave", false))},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp := out.Responses[0]
	var undeclared *types.ResponseWarning
	for _, w := range resp.Warnings {
		if w.Code == string(errors.PULSE_MATRIX_GROUP_LEVEL_UNDECLARED) {
			if undeclared != nil {
				t.Fatalf("UNDECLARED twice: %+v", resp.Warnings)
			}
			undeclared = w
		}
	}
	if undeclared == nil || undeclared.Details["attribute"] != "sc" || undeclared.Details["group_field"] != "period" || undeclared.Details["fit"] != "brand" {
		t.Fatalf("warnings %+v, want one UNDECLARED on sc", resp.Warnings)
	}
	rows := resp.Data
	if len(rows) != 2 || dataJSON(t, rows[0]["n"]) == "0" || dataJSON(t, rows[1]["n"]) != "0" {
		t.Fatalf("per-wave counts %s, want wave 2 null", dataJSON(t, rows))
	}
	if n, _ := undeclared.Details["count"].(int64); n == 0 {
		t.Fatalf("count %v", undeclared.Details["count"])
	}

	three := append(append([]map[string]any(nil), fromTo...), map[string]any{"value": 3, "name": "later"})
	req := semScoreDerivedReq(derivedInline(map[string]any{"group_levels": three}), "wave", false)
	_, resp2 := processData(t, p, req)
	var empty *types.ResponseWarning
	for _, w := range resp2.Warnings {
		if w.Code == string(errors.PULSE_MATRIX_GROUP_LEVEL_EMPTY) {
			empty = w
		}
	}
	if empty == nil || empty.Details["attribute"] != "sc" || empty.Details["group_field"] != "period" {
		t.Fatalf("warnings %+v, want EMPTY on sc", resp2.Warnings)
	}
	if lv, _ := empty.Details["levels"].([]string); len(lv) != 1 || lv[0] != "later" {
		t.Fatalf("EMPTY levels %v", empty.Details["levels"])
	}
	strict, _ := brandGroupCohort{csv: "brand", wave: encoding.FieldTypeU8}.build(t, pulse.Options{Strict: true})
	_, serr := strict.Process(ctx, req)
	var ce *errors.CodedError
	if !stderrors.As(serr, &ce) || ce.Code != errors.PULSE_MATRIX_GROUP_LEVEL_EMPTY {
		t.Fatalf("strict: %v, want PULSE_MATRIX_GROUP_LEVEL_EMPTY", serr)
	}
}

// TestSEMScore_DerivedGroupInlineRefusals: an inline group naming a
// label computed after the slot is unknown (bad_params), group_levels
// on a cohort field is refused — predict ≡ runtime.
func TestSEMScore_DerivedGroupInlineRefusals(t *testing.T) {
	t.Parallel()

	p, fsys := brandGroupCohort{csv: "brand", wave: encoding.FieldTypeU8}.build(t, pulse.Options{})
	later := semScoreSlotReq(derivedInline(nil), false)
	later.Attributes = append(later.Attributes, periodAttr("wave"))
	onField := semScoreSlotReq(map[string]any{"model": semScoreCFA, "group": "wave", "group_levels": fromTo, "latent": "service"}, false)
	for _, c := range []struct {
		name  string
		req   *types.Request
		param string
	}{
		{"computed after the slot", later, "group"},
		{"group_levels on a cohort field", onField, "group_levels"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, rerr := p.Process(context.Background(), c.req)
			var ce *errors.CodedError
			if !stderrors.As(rerr, &ce) || ce.Details["reason"] != "bad_params" || ce.Details["param"] != c.param {
				t.Fatalf("runtime %v, want bad_params on %s", rerr, c.param)
			}
			semSameRefusal(t, c.name, rerr, semEnvError(t, predictEnvelope(t, p, fsys, "brand.pulse", c.req), ce.Code))
		})
	}
}
