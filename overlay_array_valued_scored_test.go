package pulse_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// The Compose and whole-chain array-valued refusal judges only the
// SCORED aggregation (E4-S8): each slot / stage is narrowed to its
// group columns plus that one value column before a row is read, so an
// unscored vector aggregation beside it never reaches the fold. A slot
// scoring the vector aggregation — by params.field or by default as
// aggregations[0] — is refused exactly as before.
//
// Every parity case runs PREDICT FIRST on a fresh request.

// scoredVecFixture: two cohorts (so the two Compose slots differ) and
// an instance over them.
func scoredVecFixture(t *testing.T) *pulse.Pulse {
	t.Helper()
	fsys := afero.NewMemMapFs()
	writeVexprCohort(t, fsys, "v.pulse", 0, 40)
	writeVexprCohort(t, fsys, "w.pulse", 0, 27)
	p, err := pulse.New(pulse.Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// scoredVecAggs builds a slot's aggregations from a layout string:
// "s" = AGG_SUM qty labelled s, "m" = AGG_VEC_MEAN over b1,b2 labelled
// m (expanded when expand).
func scoredVecAggs(layout string, expand bool) []*types.Aggregation {
	out := make([]*types.Aggregation, 0, len(layout))
	for _, c := range layout {
		switch c {
		case 's':
			out = append(out, &types.Aggregation{Type: types.AGG_SUM, Field: "qty", Label: "s"})
		case 'm':
			a := &types.Aggregation{Type: types.AGG_VEC_MEAN, Fields: []string{"b1", "b2"}, Label: "m"}
			if expand {
				a.Params = vaggExpandParams()
			}
			out = append(out, a)
		}
	}
	return out
}

func scoredVecGroups() []*types.Group {
	return []*types.Group{{Type: types.GROUP_RANGE, Field: "qty", Params: json.RawMessage(`{"ranges":[[0,10],[10,20],[20,100]]}`)}}
}

func scoredVecCompose(layout string, expand bool, field any) *pulse.ComposedRequest {
	slot := func(label, cohort string) *types.Request {
		return &types.Request{Label: label, Cohort: &types.Cohort{Filename: cohort}, Groups: scoredVecGroups(),
			Aggregations: scoredVecAggs(layout, expand)}
	}
	spec := types.ComposeOverlaySpec{Name: "o", Kind: types.OverlayKindIndexVsRef, Reference: "a", Targets: []string{"b"}}
	if field != nil {
		spec.Params = map[string]any{"field": field}
	}
	return &pulse.ComposedRequest{Requests: []*types.Request{slot("a", "v.pulse"), slot("b", "w.pulse")},
		Overlays: []types.ComposeOverlaySpec{spec}}
}

func scoredVecLayers(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestComposeOverlay_ArrayValuedOnlyWhenScored: a slot [s, m] (or
// [m, s] with field s) scoring s runs, predict-clean, and its layer
// equals the single-aggregation slot's byte for byte; scoring m
// (default first, or by field) is refused PROCESSING_CONFIG
// array_valued with the Details it always carried, on both sides.
func TestComposeOverlay_ArrayValuedOnlyWhenScored(t *testing.T) {
	ctx := context.Background()
	p := scoredVecFixture(t)

	baseline, err := p.Compose(ctx, scoredVecCompose("s", false, nil))
	if err != nil {
		t.Fatalf("baseline compose: %v", err)
	}
	if len(baseline.Overlays) != 1 || baseline.Overlays[0].Payload.Series == nil {
		t.Fatalf("baseline overlay = %+v, want one SERIES layer", baseline.Overlays)
	}
	want := scoredVecLayers(t, baseline.Overlays)

	for _, expand := range []bool{false, true} {
		for _, c := range []struct {
			layout string
			field  any
		}{
			{"sm", nil},
			{"sm", "s"},
			{"ms", "s"},
		} {
			t.Run(fmt.Sprintf("runs/%s/field=%v/expand=%v", c.layout, c.field, expand), func(t *testing.T) {
				env, err := p.PredictCompose(ctx, scoredVecCompose(c.layout, expand, c.field))
				if err != nil {
					t.Fatal(err)
				}
				if len(env.Errors) != 0 {
					t.Fatalf("PredictCompose errors = %+v, want none", env.Errors)
				}
				out, err := p.Compose(ctx, scoredVecCompose(c.layout, expand, c.field))
				if err != nil {
					t.Fatalf("Compose: %v", err)
				}
				if got := scoredVecLayers(t, out.Overlays); got != want {
					t.Errorf("layers =\n%s\nwant (single-aggregation slot)\n%s", got, want)
				}
			})
		}
		for _, c := range []struct {
			layout string
			field  any
			ai     int
		}{
			{"ms", nil, 0},
			{"sm", "m", 1},
			{"m", nil, 0},
		} {
			t.Run(fmt.Sprintf("refused/%s/field=%v/expand=%v", c.layout, c.field, expand), func(t *testing.T) {
				wantDetails := map[string]any{"overlay_index": 0, "kind": string(types.OverlayKindIndexVsRef), "slot": "a",
					"host": fmt.Sprintf("requests[0].aggregations[%d]", c.ai), "aggregation": string(types.AGG_VEC_MEAN),
					"label": "m", "reason": "array_valued"}
				env, err := p.PredictCompose(ctx, scoredVecCompose(c.layout, expand, c.field))
				if err != nil {
					t.Fatal(err)
				}
				if len(env.Errors) == 0 || env.Errors[0].Code != string(perr.PROCESSING_CONFIG) ||
					!reflect.DeepEqual(env.Errors[0].Details, wantDetails) {
					t.Fatalf("PredictCompose = %+v, want PROCESSING_CONFIG %v first", env.Errors, wantDetails)
				}
				_, err = p.Compose(ctx, scoredVecCompose(c.layout, expand, c.field))
				ce := codeOf(t, err)
				if ce.Code != perr.PROCESSING_CONFIG || !reflect.DeepEqual(ce.Details, wantDetails) {
					t.Fatalf("Compose = %s %v, want PROCESSING_CONFIG %v", ce.Code, ce.Details, wantDetails)
				}
				if ce.Message != env.Errors[0].Message {
					t.Errorf("message drift: runtime %q, predict %q", ce.Message, env.Errors[0].Message)
				}
			})
		}
	}

	// An unknown field beside a vector aggregation is the field rule's
	// refusal on both sides, not array_valued.
	env, err := p.PredictCompose(ctx, scoredVecCompose("ms", false, "nope"))
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Errors) == 0 || env.Errors[0].Code != string(perr.PULSE_OVERLAY_FIELD_UNKNOWN) {
		t.Fatalf("PredictCompose unknown field = %+v, want PULSE_OVERLAY_FIELD_UNKNOWN first", env.Errors)
	}
	_, err = p.Compose(ctx, scoredVecCompose("ms", false, "nope"))
	if ce := codeOf(t, err); ce.Code != perr.PULSE_OVERLAY_FIELD_UNKNOWN {
		t.Fatalf("Compose unknown field = %s %v, want PULSE_OVERLAY_FIELD_UNKNOWN", ce.Code, ce.Details)
	}
}

// scoredVecChain: a one-stage chain (a vector stage may not feed
// another) whose overlay compares the stage with itself.
func scoredVecChain(layout string, expand bool, field any) *pulse.ChainRequest {
	zero := 0
	spec := &types.ChainOverlaySpec{Name: "o", Kind: types.OverlayKindDeltaVsStage, Ref: types.StageRef{Index: &zero}}
	if field != nil {
		spec.Params = map[string]any{"field": field}
	}
	return &pulse.ChainRequest{Cohort: &types.Cohort{Filename: "v.pulse"}, Stages: []*types.ChainStage{
		{Name: "profile", Request: &types.Request{Groups: scoredVecGroups(), Aggregations: scoredVecAggs(layout, expand)}},
	}, Overlays: []*types.ChainOverlaySpec{spec}}
}

// TestChainOverlay_ArrayValuedOnlyWhenScored: the whole-chain twin.
func TestChainOverlay_ArrayValuedOnlyWhenScored(t *testing.T) {
	ctx := context.Background()
	p := scoredVecFixture(t)

	baseline, err := p.ProcessChain(ctx, scoredVecChain("s", false, nil))
	if err != nil {
		t.Fatalf("baseline chain: %v", err)
	}
	if len(baseline.Overlays) != 1 || baseline.Overlays[0].Payload.Series == nil {
		t.Fatalf("baseline overlay = %+v, want one SERIES layer", baseline.Overlays)
	}
	want := scoredVecLayers(t, baseline.Overlays)

	for _, expand := range []bool{false, true} {
		for _, c := range []struct {
			layout string
			field  any
		}{
			{"sm", nil},
			{"sm", "s"},
			{"ms", "s"},
		} {
			t.Run(fmt.Sprintf("runs/%s/field=%v/expand=%v", c.layout, c.field, expand), func(t *testing.T) {
				env, err := p.PredictChain(ctx, scoredVecChain(c.layout, expand, c.field))
				if err != nil {
					t.Fatal(err)
				}
				if len(env.Errors) != 0 {
					t.Fatalf("PredictChain errors = %+v, want none", env.Errors)
				}
				out, err := p.ProcessChain(ctx, scoredVecChain(c.layout, expand, c.field))
				if err != nil {
					t.Fatalf("ProcessChain: %v", err)
				}
				if got := scoredVecLayers(t, out.Overlays); got != want {
					t.Errorf("layers =\n%s\nwant (single-aggregation stage)\n%s", got, want)
				}
			})
		}
		for _, c := range []struct {
			layout string
			field  any
			ai     int
		}{
			{"ms", nil, 0},
			{"sm", "m", 1},
			{"m", nil, 0},
		} {
			t.Run(fmt.Sprintf("refused/%s/field=%v/expand=%v", c.layout, c.field, expand), func(t *testing.T) {
				wantDetails := map[string]any{"overlay_index": 0, "kind": string(types.OverlayKindDeltaVsStage), "role": "target",
					"stage_index": 0, "stage_name": "profile", "aggregation_index": c.ai, "aggregation": string(types.AGG_VEC_MEAN),
					"label": "m", "reason": "array_valued"}
				env, err := p.PredictChain(ctx, scoredVecChain(c.layout, expand, c.field))
				if err != nil {
					t.Fatal(err)
				}
				if len(env.Errors) == 0 || env.Errors[0].Code != string(perr.PROCESSING_CONFIG) ||
					!reflect.DeepEqual(env.Errors[0].Details, wantDetails) {
					t.Fatalf("PredictChain = %+v, want PROCESSING_CONFIG %v first", env.Errors, wantDetails)
				}
				_, err = p.ProcessChain(ctx, scoredVecChain(c.layout, expand, c.field))
				ce := codeOf(t, err)
				if ce.Code != perr.PROCESSING_CONFIG || !reflect.DeepEqual(ce.Details, wantDetails) {
					t.Fatalf("ProcessChain = %s %v, want PROCESSING_CONFIG %v", ce.Code, ce.Details, wantDetails)
				}
			})
		}
	}

	// An unknown field beside a vector aggregation is the field rule's.
	env, err := p.PredictChain(ctx, scoredVecChain("ms", false, "nope"))
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Errors) == 0 || env.Errors[0].Code != string(perr.PULSE_OVERLAY_FIELD_UNKNOWN) {
		t.Fatalf("PredictChain unknown field = %+v, want PULSE_OVERLAY_FIELD_UNKNOWN first", env.Errors)
	}
	_, err = p.ProcessChain(ctx, scoredVecChain("ms", false, "nope"))
	if ce := codeOf(t, err); ce.Code != perr.PULSE_OVERLAY_FIELD_UNKNOWN {
		t.Fatalf("ProcessChain unknown field = %s %v, want PULSE_OVERLAY_FIELD_UNKNOWN", ce.Code, ce.Details)
	}
}

// scoredVecProcess: a grouped Process request whose series overlay
// (DELTA_VS_PRIOR, one per field in fields; nil = no params.field)
// scores the aggregation params.field names, else aggregations[0].
func scoredVecProcess(layout string, expand bool, fields ...any) *pulse.Request {
	req := &types.Request{Cohort: &types.Cohort{Filename: "v.pulse"}, Groups: scoredVecGroups(),
		Aggregations: scoredVecAggs(layout, expand)}
	for _, f := range fields {
		spec := types.OverlaySpec{Kind: types.OverlayKindDeltaVsPrior, Scope: types.OverlayScopeGroup}
		if f != nil {
			b, _ := json.Marshal(map[string]any{"field": f})
			spec.Params = b
		}
		req.Overlays = append(req.Overlays, spec)
	}
	return req
}

// predictCodedError predicts req over the cohort's bytes and returns
// the envelope's first error (nil when the request is predict-clean).
func predictCodedError(t *testing.T, p *pulse.Pulse, data []byte, req *pulse.Request) *descriptor.EnvelopeEntry {
	t.Helper()
	env, err := p.PredictBytes(context.Background(), data, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Errors) == 0 {
		return nil
	}
	return env.Errors[0]
}

// TestProcessOverlay_ArrayValuedOnlyWhenScored: the Process-host twin
// (E4-S10). A series overlay reads only the aggregation params.field
// names, so [m, s] scoring s runs predict-clean and its overlays equal
// the scalar-only request's byte for byte; scoring m — by field, or by
// default as aggregations[0] — is refused PROCESSING_CONFIG
// array_valued, identically on predict (first) and the runtime.
func TestProcessOverlay_ArrayValuedOnlyWhenScored(t *testing.T) {
	ctx := context.Background()
	fsys := afero.NewMemMapFs()
	writeVexprCohort(t, fsys, "v.pulse", 0, 40)
	data, err := afero.ReadFile(fsys, "v.pulse")
	if err != nil {
		t.Fatal(err)
	}
	p, err := pulse.New(pulse.Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}

	baseline, err := p.Process(ctx, scoredVecProcess("s", false, nil))
	if err != nil {
		t.Fatalf("baseline process: %v", err)
	}
	if len(baseline.Overlays) != 1 || baseline.Overlays[0].Payload.Series == nil || len(baseline.Overlays[0].Payload.Series.Entries) == 0 {
		t.Fatalf("baseline overlays = %+v, want one non-empty SERIES layer", baseline.Overlays)
	}
	want := scoredVecLayers(t, baseline.Overlays)

	for _, expand := range []bool{false, true} {
		for _, c := range []struct {
			layout string
			field  any
		}{
			{"sm", nil},
			{"sm", "s"},
			{"ms", "s"},
		} {
			t.Run(fmt.Sprintf("runs/%s/field=%v/expand=%v", c.layout, c.field, expand), func(t *testing.T) {
				if ce := predictCodedError(t, p, data, scoredVecProcess(c.layout, expand, c.field)); ce != nil {
					t.Fatalf("Predict = %s %v, want clean", ce.Code, ce.Details)
				}
				out, err := p.Process(ctx, scoredVecProcess(c.layout, expand, c.field))
				if err != nil {
					t.Fatalf("Process: %v", err)
				}
				if got := scoredVecLayers(t, out.Overlays); got != want {
					t.Errorf("overlays =\n%s\nwant (scalar-only request)\n%s", got, want)
				}
			})
		}
		for _, c := range []struct {
			layout string
			fields []any
			ai     int
		}{
			{"ms", []any{nil}, 0},
			{"sm", []any{"m"}, 1},
			{"m", []any{nil}, 0},
			// A second overlay scoring the scalar does not clear the
			// first's refusal.
			{"ms", []any{"s", nil}, 0},
		} {
			t.Run(fmt.Sprintf("refused/%s/fields=%v/expand=%v", c.layout, c.fields, expand), func(t *testing.T) {
				wantDetails := map[string]any{"aggregation": string(types.AGG_VEC_MEAN), "label": "m",
					"slot": fmt.Sprintf("aggregations[%d]", c.ai), "reason": "array_valued"}
				pe := predictCodedError(t, p, data, scoredVecProcess(c.layout, expand, c.fields...))
				if pe == nil || pe.Code != string(perr.PROCESSING_CONFIG) || !reflect.DeepEqual(pe.Details, wantDetails) {
					t.Fatalf("Predict = %+v, want PROCESSING_CONFIG %v", pe, wantDetails)
				}
				_, err := p.Process(ctx, scoredVecProcess(c.layout, expand, c.fields...))
				ce := codeOf(t, err)
				if ce.Code != perr.PROCESSING_CONFIG || !reflect.DeepEqual(ce.Details, wantDetails) {
					t.Fatalf("Process = %s %v, want PROCESSING_CONFIG %v", ce.Code, ce.Details, wantDetails)
				}
				if ce.Message != pe.Message {
					t.Errorf("message drift: runtime %q, predict %q", ce.Message, pe.Message)
				}
			})
		}
	}

	// An unknown field beside a vector aggregation is the field rule's
	// refusal on both sides, not array_valued.
	if pe := predictCodedError(t, p, data, scoredVecProcess("ms", false, "nope")); pe == nil || pe.Code != string(perr.PULSE_OVERLAY_FIELD_UNKNOWN) {
		t.Fatalf("Predict unknown field = %+v, want PULSE_OVERLAY_FIELD_UNKNOWN", pe)
	}
	_, err = p.Process(ctx, scoredVecProcess("ms", false, "nope"))
	if ce := codeOf(t, err); ce.Code != perr.PULSE_OVERLAY_FIELD_UNKNOWN {
		t.Fatalf("Process unknown field = %s %v, want PULSE_OVERLAY_FIELD_UNKNOWN", ce.Code, ce.Details)
	}
}
