package integration_test

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/seriesfield"
	"github.com/frankbardon/pulse/types"
)

// csSlot is one Compose slot over the multi-grouper cohort: the rows
// of one sex, grouped by groups, carrying aggs.
func csSlot(f *mgFixture, label, sex string, groups []*types.Group, aggs ...*types.Aggregation) *types.Request {
	req := mgRequest(f, groups, aggs...)
	req.Label = label
	req.Filterers = []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "sex", Values: []string{sex}}}
	return req
}

// csZ (COUNT, label "zz_n") is listed FIRST but sorts LAST; csA (SUM y,
// label "aa_sum") is listed second but sorts FIRST — the alphabetical
// pick encodeSeriesRow made before U46 is never aggregations[0] here.
func csZ() *types.Aggregation {
	return &types.Aggregation{Type: types.AGG_COUNT, Field: "id", Label: "zz_n"}
}
func csA() *types.Aggregation {
	return &types.Aggregation{Type: types.AGG_SUM, Field: "y", Label: "aa_sum"}
}

// csOverlay is a Compose overlay of kind over reference "F" and target
// "M" (PANEL_INDEX_VS_REF also reads target "M2", the same rows).
func csOverlay(kind types.OverlayKind, field string) types.ComposeOverlaySpec {
	spec := types.ComposeOverlaySpec{Name: "o", Kind: kind, Scope: types.OverlayScopeGroup, Reference: "F", Targets: []string{"M"}}
	if kind == types.OverlayKindPanelIndexVsRef {
		spec.Targets = []string{"M", "M2"}
	}
	if field != "" {
		spec.Params = map[string]any{"field": field}
	}
	if kind == types.OverlayKindTVsRef || kind == types.OverlayKindZVsRef {
		// A scalar side needs explicit variance / sample size (U46 E4-S16).
		if spec.Params == nil {
			spec.Params = map[string]any{}
		}
		spec.Params["variance"], spec.Params["sample_size"] = 1.0, 2.0
	}
	return spec
}

// csCompose builds the F / M / M2 Compose request, every slot grouped
// by groups and carrying aggs.
func csCompose(f *mgFixture, spec types.ComposeOverlaySpec, groups func() []*types.Group, aggs ...func() *types.Aggregation) *types.ComposedRequest {
	slot := func(label, sex string) *types.Request {
		as := make([]*types.Aggregation, len(aggs))
		for i, a := range aggs {
			as[i] = a()
		}
		return csSlot(f, label, sex, groups(), as...)
	}
	return &types.ComposedRequest{
		Requests: []*types.Request{slot("F", "F"), slot("M", "M"), slot("M2", "M")},
		Overlays: []types.ComposeOverlaySpec{spec},
	}
}

func csRegion() []*types.Group { return []*types.Group{mgRegion()} }

// csRender renders every layer entry by entry: key and statistic.
func csRender(t *testing.T, resp *types.ComposedResponse) string {
	t.Helper()
	if resp == nil || len(resp.Overlays) == 0 {
		t.Fatalf("no overlay layer: %+v", resp)
	}
	var b strings.Builder
	for _, l := range resp.Overlays {
		if l.Payload.Series == nil || len(l.Payload.Series.Entries) == 0 {
			t.Fatalf("layer %s: want a non-empty series payload, got %+v", l.Name, l.Payload)
		}
		fmt.Fprintf(&b, "%s:", l.Name)
		for _, e := range l.Payload.Series.Entries {
			st := "absent"
			if e.Summary.Statistic != nil {
				st = fmt.Sprint(*e.Summary.Statistic)
			}
			fmt.Fprintf(&b, "%v=%s;", e.Key, st)
		}
	}
	return b.String()
}

func csPredict(t *testing.T, p *pulse.Pulse, req *types.ComposedRequest) *descriptor.Envelope {
	t.Helper()
	env, err := p.PredictCompose(context.Background(), req)
	if err != nil {
		t.Fatalf("PredictCompose: %v", err)
	}
	return env
}

// TestComposeSeriesOverlay_ScoresNamedAggregation: on every Compose
// kind whose SERIES arm scores one slot aggregation, slots carrying
// [zz_n, aa_sum] run (no PULSE_OVERLAY_KEY_SET_DIVERGENT from the
// folded second value) and equal the same overlay over single-
// aggregation slots: params.field "aa_sum" equals [aa_sum] alone, and
// absent field scores aggregations[0] — zz_n, never the alphabetically
// first aa_sum. The two figures differ, so neither equality is two
// copies of one default. Predict-compose admits every run.
func TestComposeSeriesOverlay_ScoresNamedAggregation(t *testing.T) {
	t.Parallel()

	if got := len(seriesfield.ComposeKinds()); got != 5 {
		t.Fatalf("seriesfield.ComposeKinds() = %d kinds, want 5", got)
	}
	f := newMGFixture(t, pulse.Options{})
	for _, kind := range seriesfield.ComposeKinds() {
		t.Run(string(kind), func(t *testing.T) {
			run := func(field string, aggs ...func() *types.Aggregation) string {
				req := csCompose(f, csOverlay(kind, field), csRegion, aggs...)
				if env := csPredict(t, f.p, req); len(env.Errors) > 0 {
					t.Fatalf("predict-compose: %+v", env.Errors)
				}
				resp, err := f.p.Compose(context.Background(), req)
				if err != nil {
					t.Fatalf("compose: %v", err)
				}
				return csRender(t, resp)
			}
			named := run("aa_sum", csZ, csA)
			if want := run("", csA); named != want {
				t.Errorf("field aa_sum over [zz_n, aa_sum]:\n %s\nwant [aa_sum] alone:\n %s", named, want)
			}
			def := run("", csZ, csA)
			if want := run("", csZ); def != want {
				t.Errorf("absent field over [zz_n, aa_sum]:\n %s\nwant aggregations[0] [zz_n] alone:\n %s", def, want)
			}
			if named == def {
				t.Errorf("aa_sum and zz_n scored identically: %s", def)
			}
		})
	}
}

// TestComposeSeriesOverlay_SlotsUntouched: the narrowing the overlay
// fold reads never reaches ComposedResponse.Responses — every slot's
// Data still carries both aggregations.
func TestComposeSeriesOverlay_SlotsUntouched(t *testing.T) {
	t.Parallel()

	f := newMGFixture(t, pulse.Options{})
	req := csCompose(f, csOverlay(types.OverlayKindIndexVsRef, "aa_sum"), csRegion, csZ, csA)
	resp, err := f.p.Compose(context.Background(), req)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	for i, r := range resp.Responses {
		for _, row := range r.Data {
			for _, col := range []string{"region", "zz_n", "aa_sum"} {
				if _, ok := row[col]; !ok {
					t.Fatalf("responses[%d] row %v lost column %s", i, row, col)
				}
			}
		}
	}
}

// TestComposeSeriesOverlay_FieldUnknownParity: a params.field naming no
// aggregation of a slot the overlay reads — the reference, or one
// target — is ONE PULSE_OVERLAY_FIELD_UNKNOWN from predict-compose and
// from the runtime, Details {index, kind, field, available, slot} equal.
func TestComposeSeriesOverlay_FieldUnknownParity(t *testing.T) {
	t.Parallel()

	f := newMGFixture(t, pulse.Options{})
	cases := []struct {
		name, field, slot string
		available         []string
	}{
		{"unknown everywhere", "revenue", "F", []string{"zz_n", "aa_sum"}},
		{"missing on one target", "aa_sum", "M", []string{"zz_n"}},
	}
	for _, kind := range seriesfield.ComposeKinds() {
		for _, c := range cases {
			t.Run(string(kind)+"/"+c.name, func(t *testing.T) {
				req := csCompose(f, csOverlay(kind, c.field), csRegion, csZ, csA)
				// Slot M drops aa_sum; F and M2 keep it.
				req.Requests[1].Aggregations = []*types.Aggregation{csZ()}
				// Index 1: the refused spec sits behind a valid one.
				req.Overlays = append([]types.ComposeOverlaySpec{csOverlay(types.OverlayKindIndexVsRef, "")}, req.Overlays...)
				_, err := f.p.Compose(context.Background(), req)
				ce := requireCodedError(t, err, perr.PULSE_OVERLAY_FIELD_UNKNOWN)
				want := map[string]any{
					"index":     1,
					"kind":      string(kind),
					"field":     c.field,
					"available": c.available,
					"slot":      c.slot,
				}
				if !reflect.DeepEqual(ce.Details, want) {
					t.Errorf("runtime details %#v, want %#v", ce.Details, want)
				}
				// One error, as the runtime stops at the first slot it
				// resolves (reference, then targets) — "unknown
				// everywhere" used to be one error per slot (#436).
				env := csPredict(t, f.p, req)
				if len(env.Errors) != 1 || env.Errors[0].Code != string(perr.PULSE_OVERLAY_FIELD_UNKNOWN) {
					t.Fatalf("predict-compose: want exactly one PULSE_OVERLAY_FIELD_UNKNOWN, got %+v", env.Errors)
				}
				if !reflect.DeepEqual(env.Errors[0].Details, ce.Details) {
					t.Errorf("predict details %#v != runtime %#v", env.Errors[0].Details, ce.Details)
				}
			})
		}
	}
}

// TestComposeSeriesOverlay_KeyFieldsDivergentParity: SERIES slots
// whose group columns differ by NAME (region vs sex, both
// GROUP_CATEGORY, so the grouper-type schema gate passes) can never
// align. The runtime key-set gate refuses them, and predict-compose now
// refuses them under the same code; equal group columns pass both.
func TestComposeSeriesOverlay_KeyFieldsDivergentParity(t *testing.T) {
	t.Parallel()

	f := newMGFixture(t, pulse.Options{})
	for _, kind := range []types.OverlayKind{types.OverlayKindIndexVsRef, types.OverlayKindDeltaVsRef} {
		t.Run(string(kind), func(t *testing.T) {
			req := csCompose(f, csOverlay(kind, ""), csRegion, csZ, csA)
			req.Requests[1].Groups = []*types.Group{mgSex()}
			_, err := f.p.Compose(context.Background(), req)
			requireCodedError(t, err, perr.PULSE_OVERLAY_KEY_SET_DIVERGENT)
			env := csPredict(t, f.p, req)
			var got map[string]any
			for _, e := range env.Errors {
				if e.Code == string(perr.PULSE_OVERLAY_KEY_SET_DIVERGENT) {
					got = e.Details
				}
			}
			want := map[string]any{
				"index":            0,
				"reference":        "F",
				"target":           "M",
				"reference_fields": []string{"region"},
				"target_fields":    []string{"sex"},
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("predict-compose KEY_SET_DIVERGENT details %#v, want %#v (errors %+v)", got, want, env.Errors)
			}
		})
	}
}

// TestComposeSeriesOverlay_DefaultedLabel: an aggregation authored
// without a Type is labelled by its smart default (AGG_SUM_y over the
// f64 y), which only the cohort schema decides. The runtime scores the
// field naming that label exactly as the explicitly typed twin;
// predict-compose resolves the same default from the slot's schema
// (header only) and admits it.
func TestComposeSeriesOverlay_DefaultedLabel(t *testing.T) {
	t.Parallel()

	f := newMGFixture(t, pulse.Options{})
	untyped := func() *types.Aggregation { return &types.Aggregation{Field: "y"} }
	typed := func() *types.Aggregation { return &types.Aggregation{Type: types.AGG_SUM, Field: "y"} }
	run := func(y func() *types.Aggregation) string {
		req := csCompose(f, csOverlay(types.OverlayKindIndexVsRef, "AGG_SUM_y"), csRegion, csZ, y)
		if env := csPredict(t, f.p, req); len(env.Errors) > 0 {
			t.Fatalf("predict-compose: %+v", env.Errors)
		}
		resp, err := f.p.Compose(context.Background(), req)
		if err != nil {
			t.Fatalf("compose: %v", err)
		}
		return csRender(t, resp)
	}
	if got, want := run(untyped), run(typed); got != want {
		t.Errorf("untyped y scored %s, want the typed twin %s", got, want)
	}
}
