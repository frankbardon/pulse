package pulse_test

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

// Chain-stage series overlays (OVERLAY_INDEX_VS_STAGE /
// OVERLAY_DELTA_VS_STAGE) score the aggregation params.field names on
// each stage and key on the stage's group columns alone — the Compose
// rule (compose_series_field_test.go) per stage.

// chA / chZ are stage 0's aggregations over the multi-grouper cohort:
// zz_n (COUNT) is listed FIRST but sorts LAST, aa_sum (SUM y) is listed
// second but sorts FIRST — the alphabetical pick encodeSeriesRow made
// before U46 is never aggregations[0] here (csZ / csA).
//
// Stage 1 re-groups stage 0's rows by region: its zz_n is SUM(zz_n)
// (stage 0's value again) and its aa_sum is COUNT(aa_sum) (1 per
// region), so the two measures index differently against stage 0.
func chStage1(label string) *types.Aggregation {
	if label == "zz_n" {
		return &types.Aggregation{Type: types.AGG_SUM, Field: "zz_n", Label: "zz_n"}
	}
	return &types.Aggregation{Type: types.AGG_COUNT, Field: "aa_sum", Label: "aa_sum"}
}

// chChain builds the two-stage chain: stage 0 the F rows grouped by
// region carrying the labels in order, stage 1 those rows re-grouped by
// region carrying the same labels (chStage1), and one whole-chain
// overlay of kind, target stage 1 (the default), ref stage 0.
func chChain(f *mgFixture, kind types.OverlayKind, field string, labels ...string) *types.ChainRequest {
	s0 := make([]*types.Aggregation, len(labels))
	s1 := make([]*types.Aggregation, len(labels))
	for i, l := range labels {
		if l == "zz_n" {
			s0[i] = csZ()
		} else {
			s0[i] = csA()
		}
		s1[i] = chStage1(l)
	}
	stage0 := csSlot(f, "", "F", csRegion(), s0...)
	stage0.Cohort = nil
	zero := 0
	spec := &types.ChainOverlaySpec{Name: "o", Kind: kind, Scope: types.OverlayScopeGroup, Ref: types.StageRef{Index: &zero}}
	if field != "" {
		spec.Params = map[string]any{"field": field}
	}
	return &types.ChainRequest{
		Cohort: &types.Cohort{Filename: f.cohort},
		Stages: []*types.ChainStage{
			{Name: "base", Request: stage0},
			{Name: "regroup", Request: &types.Request{Groups: csRegion(), Aggregations: s1}},
		},
		Overlays: []*types.ChainOverlaySpec{spec},
	}
}

// chRender renders every chain layer entry by entry: key and statistic.
func chRender(t *testing.T, resp *types.ChainResponse) string {
	t.Helper()
	if resp == nil || len(resp.Overlays) == 0 {
		t.Fatalf("no overlay layer: %+v", resp)
	}
	var b strings.Builder
	for _, l := range resp.Overlays {
		if l.Payload.Series == nil || len(l.Payload.Series.Entries) == 0 {
			t.Fatalf("layer %s: want a non-empty series payload, got %+v (warnings %+v)", l.Name, l.Payload, l.Warnings)
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

func chPredict(t *testing.T, p *pulse.Pulse, req *types.ChainRequest) *descriptor.Envelope {
	t.Helper()
	env, err := p.PredictChain(context.Background(), req)
	if err != nil {
		t.Fatalf("PredictChain: %v", err)
	}
	return env
}

// TestChainSeriesOverlay_ScoresNamedAggregation: on both whole-chain
// kinds, stages carrying [zz_n, aa_sum] equal the same overlay over
// single-aggregation stages: params.field "aa_sum" equals [aa_sum]
// alone, and absent field scores aggregations[0] — zz_n, never the
// alphabetically first aa_sum. The two figures differ, so neither
// equality is two copies of one default. Predict-chain admits every run.
func TestChainSeriesOverlay_ScoresNamedAggregation(t *testing.T) {
	if got := len(seriesfield.ChainKinds()); got != 2 {
		t.Fatalf("seriesfield.ChainKinds() = %d kinds, want 2", got)
	}
	f := newMGFixture(t, pulse.Options{})
	for _, kind := range seriesfield.ChainKinds() {
		t.Run(string(kind), func(t *testing.T) {
			run := func(field string, labels ...string) string {
				req := chChain(f, kind, field, labels...)
				if env := chPredict(t, f.p, req); len(env.Errors) > 0 {
					t.Fatalf("predict-chain: %+v", env.Errors)
				}
				resp, err := f.p.ProcessChain(context.Background(), req)
				if err != nil {
					t.Fatalf("process chain: %v", err)
				}
				return chRender(t, resp)
			}
			named := run("aa_sum", "zz_n", "aa_sum")
			if want := run("", "aa_sum"); named != want {
				t.Errorf("field aa_sum over [zz_n, aa_sum]:\n %s\nwant [aa_sum] alone:\n %s", named, want)
			}
			def := run("", "zz_n", "aa_sum")
			if want := run("", "zz_n"); def != want {
				t.Errorf("absent field over [zz_n, aa_sum]:\n %s\nwant aggregations[0] [zz_n] alone:\n %s", def, want)
			}
			if named == def {
				t.Errorf("aa_sum and zz_n scored identically: %s", def)
			}
		})
	}
}

// TestChainSeriesOverlay_StagesUntouched: the narrowing the overlay
// fold reads never reaches ChainResponse.Stages — every stage's Data
// still carries both aggregations.
func TestChainSeriesOverlay_StagesUntouched(t *testing.T) {
	f := newMGFixture(t, pulse.Options{})
	resp, err := f.p.ProcessChain(context.Background(), chChain(f, types.OverlayKindIndexVsStage, "aa_sum", "zz_n", "aa_sum"))
	if err != nil {
		t.Fatalf("process chain: %v", err)
	}
	for i, r := range resp.Stages {
		if len(r.Data) == 0 {
			t.Fatalf("stages[%d] has no rows", i)
		}
		for _, row := range r.Data {
			for _, col := range []string{"region", "zz_n", "aa_sum"} {
				if _, ok := row[col]; !ok {
					t.Fatalf("stages[%d] row %v lost column %s", i, row, col)
				}
			}
		}
	}
}

// TestChainSeriesOverlay_FieldUnknownParity: a params.field naming no
// aggregation of a stage the overlay reads — the target, or only the
// reference — is PULSE_OVERLAY_FIELD_UNKNOWN from predict-chain and
// from the runtime, Details {index, kind, field, available, which,
// stage_index} equal (target checked before ref on both sides).
func TestChainSeriesOverlay_FieldUnknownParity(t *testing.T) {
	f := newMGFixture(t, pulse.Options{})
	cases := []struct {
		name, field, which string
		stage              int
		available          []string
	}{
		{"unknown everywhere", "revenue", "target", 1, []string{"zz_n", "aa_sum"}},
		{"missing on the reference", "aa_sum", "ref", 0, []string{"zz_n"}},
	}
	for _, kind := range seriesfield.ChainKinds() {
		for _, c := range cases {
			t.Run(string(kind)+"/"+c.name, func(t *testing.T) {
				req := chChain(f, kind, c.field, "zz_n", "aa_sum")
				// Stage 0 drops aa_sum; stage 1 then counts zz_n instead.
				req.Stages[0].Request.Aggregations = []*types.Aggregation{csZ()}
				req.Stages[1].Request.Aggregations[1] = &types.Aggregation{Type: types.AGG_COUNT, Field: "zz_n", Label: "aa_sum"}
				// Index 1: the refused spec sits behind a valid one.
				zero := 0
				valid := &types.ChainOverlaySpec{Kind: types.OverlayKindIndexVsStage, Scope: types.OverlayScopeGroup, Ref: types.StageRef{Index: &zero}}
				req.Overlays = append([]*types.ChainOverlaySpec{valid}, req.Overlays...)
				env := chPredict(t, f.p, req)
				_, err := f.p.ProcessChain(context.Background(), req)
				ce := requireCodedError(t, err, perr.PULSE_OVERLAY_FIELD_UNKNOWN)
				want := map[string]any{
					"index":       1,
					"kind":        string(kind),
					"field":       c.field,
					"available":   c.available,
					"which":       c.which,
					"stage_index": c.stage,
				}
				if !reflect.DeepEqual(ce.Details, want) {
					t.Errorf("runtime details %#v, want %#v", ce.Details, want)
				}
				// Predict accumulates (a field unknown on both stages is
				// two errors); its FIRST is the runtime's refusal.
				if len(env.Errors) == 0 || env.Errors[0].Code != string(perr.PULSE_OVERLAY_FIELD_UNKNOWN) {
					t.Fatalf("predict-chain: want PULSE_OVERLAY_FIELD_UNKNOWN first, got %+v", env.Errors)
				}
				if !reflect.DeepEqual(env.Errors[0].Details, ce.Details) {
					t.Errorf("predict details %#v != runtime %#v", env.Errors[0].Details, ce.Details)
				}
			})
		}
	}
}

// TestChainSeriesOverlay_DefaultedLabel: a stage-0 aggregation authored
// without a Type is labelled by its smart default (AGG_SUM_y over the
// f64 y), which only the cohort schema decides. The runtime scores the
// field naming that label exactly as the explicitly typed twin;
// predict-chain resolves the same default on the stage and admits it.
func TestChainSeriesOverlay_DefaultedLabel(t *testing.T) {
	f := newMGFixture(t, pulse.Options{})
	run := func(y *types.Aggregation) string {
		req := chChain(f, types.OverlayKindIndexVsStage, "AGG_SUM_y", "zz_n")
		req.Stages[0].Request.Aggregations = append(req.Stages[0].Request.Aggregations, y)
		req.Stages[1].Request.Aggregations = append(req.Stages[1].Request.Aggregations,
			&types.Aggregation{Type: types.AGG_COUNT, Field: "AGG_SUM_y", Label: "AGG_SUM_y"})
		if env := chPredict(t, f.p, req); len(env.Errors) > 0 {
			t.Fatalf("predict-chain: %+v", env.Errors)
		}
		resp, err := f.p.ProcessChain(context.Background(), req)
		if err != nil {
			t.Fatalf("process chain: %v", err)
		}
		return chRender(t, resp)
	}
	if got, want := run(&types.Aggregation{Field: "y"}), run(&types.Aggregation{Type: types.AGG_SUM, Field: "y"}); got != want {
		t.Errorf("untyped y scored %s, want the typed twin %s", got, want)
	}
}

// TestChainSeriesOverlay_KeyDivergenceParity: stages keyed on different
// group columns (stage 0 region x sex, stage 1 region) are not refused
// by the chain runtime — every target key misses the reference, each a
// PULSE_OVERLAY_REF_ZERO layer warning — so predict-chain refuses
// nothing either. (Compose differs: its runtime key-set gate refuses.)
func TestChainSeriesOverlay_KeyDivergenceParity(t *testing.T) {
	f := newMGFixture(t, pulse.Options{})
	req := chChain(f, types.OverlayKindIndexVsStage, "", "zz_n")
	req.Stages[0].Request.Groups = []*types.Group{mgRegion(), mgSex()}
	if env := chPredict(t, f.p, req); len(env.Errors) > 0 {
		t.Fatalf("predict-chain refused what the runtime runs: %+v", env.Errors)
	}
	resp, err := f.p.ProcessChain(context.Background(), req)
	if err != nil {
		t.Fatalf("process chain: %v", err)
	}
	l := resp.Overlays[0]
	if len(l.Warnings) == 0 || l.Warnings[0].Code != string(perr.PULSE_OVERLAY_REF_ZERO) {
		t.Fatalf("want PULSE_OVERLAY_REF_ZERO warnings for every unmatched key, got %+v", l.Warnings)
	}
	if l.Payload.Series != nil && len(l.Payload.Series.Entries) > 0 {
		t.Errorf("divergent keys matched: %+v", l.Payload.Series.Entries)
	}
}
