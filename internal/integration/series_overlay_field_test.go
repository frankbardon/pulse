package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/seriesfield"
	"github.com/frankbardon/pulse/types"
)

// sfSpec is one process-host series overlay kind with its required ref
// and params, plus the grouper its host needs.
type sfSpec struct {
	spec  types.OverlaySpec
	group func() *types.Group
}

// sfSpecs is every series kind that scores one aggregation (the
// seriesfield set): the eight one-ordered-series kinds, YoY over the
// date grouper, and the three _TOTAL kinds.
func sfSpecs() []sfSpec {
	region := func() *types.Group { return mgRegion() }
	day := func() *types.Group { return mgDay() }
	out := make([]sfSpec, 0, 13)
	for _, s := range mgSeriesOverlaySpecs() {
		out = append(out, sfSpec{spec: s, group: region})
	}
	out = append(out,
		sfSpec{spec: types.OverlaySpec{Name: "yoy", Kind: types.OverlayKindYoY, Scope: types.OverlayScopeGroup,
			Ref: types.OverlayRef{YoY: &types.OverlayYoYRef{}}, Params: json.RawMessage(`{"frequency": "annual"}`)}, group: day},
		sfSpec{spec: types.OverlaySpec{Name: "it", Kind: types.OverlayKindIndexVsTotal, Scope: types.OverlayScopeGroup}, group: region},
		sfSpec{spec: types.OverlaySpec{Name: "st", Kind: types.OverlayKindShareOfTotal, Scope: types.OverlayScopeGroup}, group: region},
		sfSpec{spec: types.OverlaySpec{Name: "zt", Kind: types.OverlayKindZScoreVsTotal, Scope: types.OverlayScopeGroup}, group: region},
	)
	return out
}

// withField returns spec with params.field set to field (merged into
// any params the kind already carries).
func withField(t *testing.T, spec types.OverlaySpec, field any) types.OverlaySpec {
	t.Helper()
	m := map[string]any{}
	if len(spec.Params) > 0 {
		if err := json.Unmarshal(spec.Params, &m); err != nil {
			t.Fatal(err)
		}
	}
	m["field"] = field
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	spec.Params = b
	return spec
}

// sfRenderLayer renders a series layer entry by entry: key and
// statistic (nil = absent, NaN kept).
func sfRenderLayer(t *testing.T, resp *types.Response) string {
	t.Helper()
	if resp == nil || len(resp.Overlays) != 1 || resp.Overlays[0].Payload.Series == nil {
		t.Fatalf("want one series layer, got %+v", resp)
	}
	var b strings.Builder
	for _, e := range resp.Overlays[0].Payload.Series.Entries {
		st := "absent"
		if e.Summary.Statistic != nil {
			st = fmt.Sprint(*e.Summary.Statistic)
		}
		fmt.Fprintf(&b, "%v=%s;", e.Key, st)
	}
	return b.String()
}

// sfModes is the buffered, streaming and both parallel arms of the
// multi-grouper gate.
func sfModes() []mgMode {
	var out []mgMode
	for _, m := range mgModes {
		switch m.name {
		case "Buffered", "Streaming", "ShardArchive", "ParallelDecode":
			out = append(out, m)
		}
	}
	if len(out) != 4 {
		panic("sfModes: a gate mode was renamed")
	}
	return out
}

// TestSeriesOverlayField_SameInputFieldByLabel: two aggregations over
// one input field (a sum and a mean of y) are told apart by their
// default TYPE_field labels.
func TestSeriesOverlayField_SameInputFieldByLabel(t *testing.T) {
	t.Parallel()

	f := newMGFixture(t, pulse.Options{})
	sum := func() *types.Aggregation { return &types.Aggregation{Type: types.AGG_SUM, Field: "y"} }
	mean := func() *types.Aggregation { return &types.Aggregation{Type: types.AGG_AVERAGE, Field: "y"} }
	spec := types.OverlaySpec{Name: "it", Kind: types.OverlayKindIndexVsTotal, Scope: types.OverlayScopeGroup}
	run := func(spec types.OverlaySpec, aggs ...*types.Aggregation) string {
		req := mgRequest(f, []*types.Group{mgRegion()}, aggs...)
		req.Overlays = []types.OverlaySpec{spec}
		if env := predictEnvelope(t, f.p, f.fs, f.cohort, req); len(env.Errors) > 0 {
			t.Fatalf("predict: %+v", env.Errors)
		}
		resp, err := f.p.Process(context.Background(), req)
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		return sfRenderLayer(t, resp)
	}
	meanNamed := run(withField(t, spec, "AGG_AVERAGE_y"), sum(), mean())
	sumNamed := run(withField(t, spec, "AGG_SUM_y"), sum(), mean())
	if want := run(spec, mean()); meanNamed != want {
		t.Errorf("AGG_AVERAGE_y over [sum, mean]: %s, want mean alone %s", meanNamed, want)
	}
	if want := run(spec, sum()); sumNamed != want {
		t.Errorf("AGG_SUM_y over [sum, mean]: %s, want sum alone %s", sumNamed, want)
	}
	if meanNamed == sumNamed {
		t.Errorf("sum and mean of y scored identically: %s", sumNamed)
	}
}

// TestSeriesOverlayField_UnknownRefusedIdentically: a label naming no
// aggregation (or a non-string field) is refused
// PULSE_OVERLAY_FIELD_UNKNOWN with Details {index, kind, field,
// available} by predict and by every runtime arm, the Details equal.
func TestSeriesOverlayField_UnknownRefusedIdentically(t *testing.T) {
	t.Parallel()

	f := newMGFixture(t, pulse.Options{})
	for _, bad := range []any{"revenue", "AGG_SUM_y", 7.0} {
		for _, s := range sfSpecs() {
			for _, mode := range sfModes() {
				t.Run(fmt.Sprint(bad)+"/"+string(s.spec.Kind)+"/"+mode.name, func(t *testing.T) {
					req := mgRequest(f, []*types.Group{s.group()},
						&types.Aggregation{Type: types.AGG_COUNT, Field: "id", Label: "n"},
						&types.Aggregation{Type: types.AGG_SUM, Field: "y", Label: "orders"})
					// Index 1: the refused spec sits behind a valid one.
					req.Overlays = []types.OverlaySpec{
						{Name: "ok", Kind: types.OverlayKindIndexVsTotal, Scope: types.OverlayScopeGroup},
						withField(t, s.spec, bad),
					}
					resp, err := mode.run(t, f, req)
					if resp != nil && len(resp.Overlays) > 0 {
						t.Fatalf("a layer despite the unknown field: %+v", resp.Overlays)
					}
					ce := requireCodedError(t, err, perr.PULSE_OVERLAY_FIELD_UNKNOWN)
					// The mode may have appended aggregations; predict
					// judges the request the run executed.
					env := predictEnvelope(t, f.p, f.fs, f.cohort, req)
					var got map[string]any
					for _, e := range env.Errors {
						if e.Code == string(perr.PULSE_OVERLAY_FIELD_UNKNOWN) {
							got = e.Details
						}
					}
					if got == nil {
						t.Fatalf("predict did not refuse: %+v", env.Errors)
					}
					want := map[string]any{
						"index":     1,
						"kind":      string(s.spec.Kind),
						"field":     bad,
						"available": seriesfield.Labels(req.Aggregations),
					}
					if !reflect.DeepEqual(ce.Details, want) {
						t.Errorf("runtime details %#v, want %#v", ce.Details, want)
					}
					if !reflect.DeepEqual(got, ce.Details) {
						t.Errorf("predict details %#v != runtime %#v", got, ce.Details)
					}
				})
			}
		}
	}
}
