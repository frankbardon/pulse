package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
)

// U66 #713. An extension overlay kind opts into the engine's series
// params: HonoursField scores the aggregation params.field names (on a
// grouped request and on each Compose series slot) instead of the first
// one, HonoursPartitionBy runs its Series handler once per
// params.partition_by partition. Predict judges both exactly as the
// runtime does (one shared reading, the declaration reaching predict
// through the snapshot); unset, both keys are unknown params and the
// handler sees the first aggregation over the whole host — today's
// behaviour.

const (
	spOptedKind  = types.OverlayKind("OVERLAY_SPTEST_INDEX")
	spPlainKind  = types.OverlayKind("OVERLAY_SPTEST_PLAIN")
	spScalarKind = types.OverlayKind("OVERLAY_SPTEST_SCALAR")
)

// spIndexSeries indexes every host value to the host's first value and
// names the scored column (and the host's row count) in a warning: over
// a partition sub-host the base is the partition's first row.
func spIndexSeries(_ *types.OverlaySpec, h extend.SeriesOverlayHost) (extend.OverlayOutput, error) {
	base, _ := h.Value(0)
	s := &types.SeriesPayload{}
	for i := 0; i < h.Groups(); i++ {
		e := types.SeriesEntry{Key: h.GroupKey(i)}
		if v, ok := h.Value(i); ok && base != 0 {
			x := v / base
			e.Summary.Statistic = &x
		}
		s.Entries = append(s.Entries, e)
	}
	return extend.OverlayOutput{
		Payload:  types.OverlayPayload{Shape: types.OverlayShapeSeries, Series: s},
		Warnings: []types.OverlayWarning{{Code: "SPTEST_SCORED", Message: "scored " + h.ValueLabel(), Details: map[string]any{"label": h.ValueLabel()}}},
	}, nil
}

// spComposeEcho is the target slot's scored series value per group,
// with its value label in a warning.
func spComposeEcho(_ *types.ComposeOverlaySpec, h extend.ComposeOverlayHost) (extend.OverlayOutput, error) {
	ts := h.Target(0).Series()
	if ts == nil {
		return extend.OverlayOutput{}, fmt.Errorf("no series target")
	}
	s := &types.SeriesPayload{}
	for i := 0; i < ts.Groups(); i++ {
		e := types.SeriesEntry{Key: ts.GroupKey(i)}
		if v, ok := ts.Value(i); ok {
			x := v
			e.Summary.Statistic = &x
		}
		s.Entries = append(s.Entries, e)
	}
	return extend.OverlayOutput{
		Payload:  types.OverlayPayload{Shape: types.OverlayShapeSeries, Series: s},
		Warnings: []types.OverlayWarning{{Code: "SPTEST_SCORED", Message: "scored " + ts.ValueLabel()}},
	}, nil
}

// spRegistration is a Series + Compose kind with a declared params list
// (so an unknown key is refused), opted in as given.
func spRegistration(name types.OverlayKind, opted bool) pulse.OverlayRegistration {
	return pulse.OverlayRegistration{
		Name: name, Description: "index to the host's first row",
		Handler:            extend.OverlayHandler{Series: spIndexSeries, Compose: spComposeEcho},
		Shapes:             []types.OverlayShape{types.OverlayShapeSeries},
		Scopes:             []types.OverlayScope{types.OverlayScopeGroup},
		Params:             []pulse.ParamMeta{{Name: "scale", JSONType: "number", Description: "unused"}},
		HonoursField:       opted,
		HonoursPartitionBy: opted,
	}
}

// spScalarRegistration answers a SCALAR layer — legal on the whole
// host, misaligned with a partition.
func spScalarRegistration() pulse.OverlayRegistration {
	return pulse.OverlayRegistration{
		Name: spScalarKind, Description: "one figure",
		Handler: extend.OverlayHandler{Series: func(_ *types.OverlaySpec, h extend.SeriesOverlayHost) (extend.OverlayOutput, error) {
			x := float64(h.Groups())
			return extend.OverlayOutput{Payload: types.OverlayPayload{Shape: types.OverlayShapeScalar, Scalar: &x}}, nil
		}},
		Shapes:             []types.OverlayShape{types.OverlayShapeSeries, types.OverlayShapeScalar},
		Scopes:             []types.OverlayScope{types.OverlayScopeGroup},
		HonoursPartitionBy: true,
	}
}

func openSeriesParamsE2E(t *testing.T) (*pulse.Pulse, *types.Cohort) {
	t.Helper()
	return openE2E(t, pulse.Extensions{Overlays: []pulse.OverlayRegistration{
		spRegistration(spOptedKind, true), spRegistration(spPlainKind, false), spScalarRegistration()}})
}

// spRequest is SUM(qty) as s and COUNT(qty) as n by groups.
func spRequest(cohort *types.Cohort, twoGroupers bool, specs ...types.OverlaySpec) *types.Request {
	groups := []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}}
	if twoGroupers {
		groups = append(groups, &types.Group{Type: types.GROUP_DATE, Field: "day", Params: json.RawMessage(`{"component":"day"}`)})
	}
	return &types.Request{
		Cohort: cohort, Groups: groups, Overlays: specs,
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "qty", Label: "s"}, {Type: types.AGG_COUNT, Field: "qty", Label: "n"}},
	}
}

// spCompose is two series slots by region over [SUM as v, COUNT as w].
func spCompose(cohort *types.Cohort, specs ...types.ComposeOverlaySpec) *types.ComposedRequest {
	slot := func(label string) *types.Request {
		return &types.Request{Label: label, Cohort: cohort,
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "qty", Label: "v"}, {Type: types.AGG_COUNT, Field: "qty", Label: "w"}}}
	}
	return &types.ComposedRequest{Requests: []*types.Request{slot("a"), slot("b")}, Overlays: specs}
}

func spSpec(kind types.OverlayKind, params string) types.OverlaySpec {
	s := types.OverlaySpec{Name: "o", Kind: kind, Scope: types.OverlayScopeGroup}
	if params != "" {
		s.Params = json.RawMessage(params)
	}
	return s
}

func spStats(t *testing.T, layer types.OverlayLayer) []float64 {
	t.Helper()
	if layer.Payload.Series == nil {
		t.Fatalf("layer %s carries no series: %+v", layer.Kind, layer.Payload)
	}
	out := make([]float64, len(layer.Payload.Series.Entries))
	for i, e := range layer.Payload.Series.Entries {
		if e.Summary.Statistic == nil {
			t.Fatalf("entry %d has no figure", i)
		}
		out[i] = *e.Summary.Statistic
	}
	return out
}

func spPredictClean(t *testing.T, p *pulse.Pulse, req *types.Request) {
	t.Helper()
	pr, err := p.Predict(context.Background(), req)
	if err != nil || len(pr.Errors) != 0 {
		t.Fatalf("Predict refused an accepted spec: %v %+v", err, pr.Errors)
	}
}

// TestExtensions_OverlaySeriesFieldHonoured: params.field picks the
// scored aggregation of an opted-in kind on the grouped host and on each
// Compose series slot (ValueLabel and values follow it), predict
// accepts it; without params.field, and for a kind not opted in, the
// handler scores the first aggregation byte-identically.
func TestExtensions_OverlaySeriesFieldHonoured(t *testing.T) {
	t.Parallel()

	p, cohort := openSeriesParamsE2E(t)
	ctx := context.Background()

	req := spRequest(cohort, false, spSpec(spOptedKind, `{"field":"n"}`))
	resp := processE2E(t, p, req)
	spPredictClean(t, p, req)
	got := spStats(t, resp.Overlays[0])
	base := e2eFloat(t, resp.Data[0]["n"])
	for i, row := range resp.Data {
		if want := e2eFloat(t, row["n"]) / base; got[i] != want {
			t.Errorf("row %d = %v, want n / n[0] = %v", i, got[i], want)
		}
	}
	if w := resp.Warnings; len(w) != 1 || w[0].Message != "scored n" {
		t.Errorf("warnings = %+v, want the handler scoring n", w)
	}

	// Off: no field on the opted kind, and the kind not opted in, both
	// score the first aggregation — the same layer, byte for byte.
	opted := processE2E(t, p, spRequest(cohort, false, spSpec(spOptedKind, "")))
	plain := processE2E(t, p, spRequest(cohort, false, spSpec(spPlainKind, "")))
	a, _ := json.Marshal(opted.Overlays[0].Payload)
	b, _ := json.Marshal(plain.Overlays[0].Payload)
	if string(a) != string(b) || opted.Warnings[0].Message != "scored s" {
		t.Errorf("first-aggregation layers differ or misread:\n%s\n%s\n%+v", a, b, opted.Warnings)
	}

	creq := spCompose(cohort, types.ComposeOverlaySpec{Name: "c", Kind: spOptedKind, Scope: types.OverlayScopeGroup,
		Reference: "a", Targets: []string{"b"}, Params: map[string]any{"field": "w"}})
	out, err := p.Compose(ctx, creq)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	env, err := p.PredictCompose(ctx, creq)
	if err != nil || len(env.Errors) != 0 {
		t.Fatalf("PredictCompose refused an accepted spec: %v %+v", err, env.Errors)
	}
	cgot := spStats(t, out.Overlays[0])
	for i, row := range out.Responses[1].Data {
		if want := e2eFloat(t, row["w"]); cgot[i] != want {
			t.Errorf("compose row %d = %v, want w = %v", i, cgot[i], want)
		}
	}
	if w := out.Overlays[0].Warnings; len(w) != 1 || w[0].Message != "scored w" {
		t.Errorf("compose warnings = %+v, want the handler scoring w", w)
	}
	cplain, err := p.Compose(ctx, spCompose(cohort, types.ComposeOverlaySpec{Name: "c", Kind: spPlainKind, Scope: types.OverlayScopeGroup, Reference: "a", Targets: []string{"b"}}))
	if err != nil {
		t.Fatalf("Compose (plain): %v", err)
	}
	if w := cplain.Overlays[0].Warnings; len(w) != 1 || w[0].Message != "scored v" {
		t.Errorf("plain compose warnings = %+v, want the first aggregation v", w)
	}
}

// TestExtensions_OverlaySeriesPartitionHonoured: over two groupers an
// opted-in kind's handler runs once per partition (each row indexed to
// its own region's first row), `[]` walks every row once, and a kind
// not opted in sees the whole host — today's behaviour.
func TestExtensions_OverlaySeriesPartitionHonoured(t *testing.T) {
	t.Parallel()

	p, cohort := openSeriesParamsE2E(t)
	req := spRequest(cohort, true, spSpec(spOptedKind, ""))
	resp := processE2E(t, p, req)
	spPredictClean(t, p, req)
	got := spStats(t, resp.Overlays[0])
	first := map[any]float64{}
	parts := 0
	for i, row := range resp.Data {
		if _, ok := first[row["region"]]; !ok {
			first[row["region"]] = e2eFloat(t, row["s"])
			parts++
		}
		if want := e2eFloat(t, row["s"]) / first[row["region"]]; got[i] != want {
			t.Errorf("row %d (%v) = %v, want s / its region's first s = %v", i, row["region"], got[i], want)
		}
	}
	if parts < 2 || len(resp.Data) <= parts {
		t.Fatalf("fixture: %d rows over %d partitions, want several rows in each of several", len(resp.Data), parts)
	}
	if w := resp.Warnings; len(w) != 1 || w[0].Details["partitions"] != parts || w[0].Message != "scored s" {
		t.Errorf("warnings = %+v, want one SPTEST_SCORED raised by %d partitions", w, parts)
	}

	whole := func(kind types.OverlayKind, params string) []float64 {
		r := processE2E(t, p, spRequest(cohort, true, spSpec(kind, params)))
		return spStats(t, r.Overlays[0])
	}
	walk := make([]float64, len(resp.Data))
	for i, row := range resp.Data {
		walk[i] = e2eFloat(t, row["s"]) / e2eFloat(t, resp.Data[0]["s"])
	}
	if w := whole(spOptedKind, `{"partition_by":[]}`); !reflect.DeepEqual(w, walk) {
		t.Errorf("partition_by [] = %v, want one walk %v", w, walk)
	}
	if w := whole(spPlainKind, ""); !reflect.DeepEqual(w, walk) {
		t.Errorf("not opted in = %v, want one walk %v", w, walk)
	}
	if w := whole(spOptedKind, `{"partition_by":["region"],"field":"n"}`); slices.Equal(w, got) {
		t.Error("params.field ignored under an explicit partition")
	}
}

// TestExtensions_OverlaySeriesParamsPredictMatchesRuntime: every
// refusal the series params raise for an extension kind is predict's
// only error and the runtime's — code, message, details — on Process and
// Compose; a kind not opted in refuses both keys as unknown params.
func TestExtensions_OverlaySeriesParamsPredictMatchesRuntime(t *testing.T) {
	t.Parallel()

	p, cohort := openSeriesParamsE2E(t)
	ctx := context.Background()
	process := []struct {
		name string
		req  *types.Request
		code perr.Code
	}{
		{name: "field_unknown", req: spRequest(cohort, false, spSpec(spOptedKind, `{"field":"qty"}`)), code: perr.PULSE_OVERLAY_FIELD_UNKNOWN},
		{name: "partition_not_prefix", req: spRequest(cohort, true, spSpec(spOptedKind, `{"partition_by":["day"]}`)), code: perr.PULSE_OVERLAY_PARTITION_INVALID},
		{name: "partition_not_list", req: spRequest(cohort, true, spSpec(spOptedKind, `{"partition_by":"region"}`)), code: perr.PULSE_OVERLAY_PARTITION_INVALID},
		{name: "plain_field", req: spRequest(cohort, false, spSpec(spPlainKind, `{"field":"n"}`)), code: perr.PULSE_REQUEST_UNKNOWN_FIELD},
		{name: "plain_partition", req: spRequest(cohort, true, spSpec(spPlainKind, `{"partition_by":[]}`)), code: perr.PULSE_REQUEST_UNKNOWN_FIELD},
		{name: "field_scores_code", req: func() *types.Request {
			r := spRequest(cohort, false, spSpec(spOptedKind, `{"field":"m"}`))
			r.Aggregations = append(r.Aggregations, &types.Aggregation{Type: types.AGG_MODE, Field: "region", Label: "m"})
			r.Groups = []*types.Group{{Type: types.GROUP_DATE, Field: "day"}}
			return r
		}(), code: perr.PROCESSING_CONFIG},
	}
	for _, tc := range process {
		t.Run("process/"+tc.name, func(t *testing.T) {
			t.Parallel()
			pr, err := p.Predict(ctx, tc.req)
			if err != nil {
				t.Fatalf("Predict: %v", err)
			}
			if len(pr.Errors) != 1 {
				t.Fatalf("predict errors = %+v, want exactly one", pr.Errors)
			}
			_, err = p.Process(ctx, tc.req)
			sameRefusal(t, "process", pr.Errors[0], err, tc.code)
		})
	}

	compose := func(kind types.OverlayKind, params map[string]any) *types.ComposedRequest {
		return spCompose(cohort, types.ComposeOverlaySpec{Name: "c", Kind: kind, Scope: types.OverlayScopeGroup, Reference: "a", Targets: []string{"b"}, Params: params})
	}
	composes := []struct {
		name string
		req  *types.ComposedRequest
		code perr.Code
	}{
		{name: "field_unknown", req: compose(spOptedKind, map[string]any{"field": "qty"}), code: perr.PULSE_OVERLAY_FIELD_UNKNOWN},
		{name: "partition_compose_host", req: compose(spOptedKind, map[string]any{"partition_by": []any{}}), code: perr.PULSE_OVERLAY_PARTITION_INVALID},
		{name: "plain_field", req: compose(spPlainKind, map[string]any{"field": "w"}), code: perr.PULSE_REQUEST_UNKNOWN_FIELD},
		{name: "field_scores_code", req: func() *types.ComposedRequest {
			r := compose(spOptedKind, map[string]any{"field": "m"})
			for _, slot := range r.Requests {
				slot.Aggregations = append(slot.Aggregations, &types.Aggregation{Type: types.AGG_MODE, Field: "region", Label: "m"})
			}
			return r
		}(), code: perr.PROCESSING_CONFIG},
	}
	for _, tc := range composes {
		t.Run("compose/"+tc.name, func(t *testing.T) {
			t.Parallel()
			env, err := p.PredictCompose(ctx, tc.req)
			if err != nil {
				t.Fatalf("PredictCompose: %v", err)
			}
			if len(env.Errors) != 1 {
				t.Fatalf("predict errors = %+v, want exactly one", env.Errors)
			}
			_, err = p.Compose(ctx, tc.req)
			sameRefusal(t, "compose", env.Errors[0], err, tc.code)
		})
	}
}

// TestExtensions_OverlaySeriesPartitionOutputHeld: a partition's output
// must be a series with one entry per partition row; on the whole host
// (one partition) any declared shape stands.
func TestExtensions_OverlaySeriesPartitionOutputHeld(t *testing.T) {
	t.Parallel()

	p, cohort := openSeriesParamsE2E(t)
	if r := processE2E(t, p, spRequest(cohort, false, spSpec(spScalarKind, ""))); r.Overlays[0].Payload.Scalar == nil {
		t.Errorf("whole host = %+v, want the scalar layer", r.Overlays[0].Payload)
	}
	_, err := p.Process(context.Background(), spRequest(cohort, true, spSpec(spScalarKind, "")))
	ce := codeOf(t, err)
	if ce.Code != perr.PULSE_EXTENSION_OVERLAY_OUTPUT_MISMATCH || ce.Details["reason"] != "partition_misaligned" || ce.Details["entries"] != -1 {
		t.Errorf("partitioned scalar = %s %v, want partition_misaligned", ce.Code, ce.Details)
	}
}

// TestExtensions_OverlaySeriesParamsInManifest: the manifest lists
// field and partition_by for an opted-in kind beside its own params,
// and neither for a kind not opted in.
func TestExtensions_OverlaySeriesParamsInManifest(t *testing.T) {
	t.Parallel()

	p, _ := openSeriesParamsE2E(t)
	names := map[types.OverlayKind][]string{}
	for _, c := range p.Manifest(context.Background()).Extensions.Overlays {
		for _, prm := range c.Params {
			names[c.Kind] = append(names[c.Kind], prm.Name)
		}
	}
	if got := names[spOptedKind]; !reflect.DeepEqual(got, []string{"scale", "field", "partition_by"}) {
		t.Errorf("opted params = %v", got)
	}
	if got := names[spPlainKind]; !reflect.DeepEqual(got, []string{"scale"}) {
		t.Errorf("plain params = %v", got)
	}
	if got := names[spScalarKind]; !reflect.DeepEqual(got, []string{"partition_by"}) {
		t.Errorf("partition-only params = %v", got)
	}
}
