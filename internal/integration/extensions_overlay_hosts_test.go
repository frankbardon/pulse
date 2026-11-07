package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// An embedder-registered overlay kind runs on the SERIES host (a grouped
// request's Request.Overlays) and on ComposedRequest.Overlays when its
// registration sets those handlers (U67 E2-S2), lands one layer like a
// built-in, and every spec of it — including one over a host or slot it
// does not declare (chain, facet) — is refused at predict exactly as at
// runtime.

const (
	overlayTrendKind types.OverlayKind = "OVERLAY_ACME_TREND"
	overlayRatioKind types.OverlayKind = "OVERLAY_ACME_RATIO"
)

// composeScale reads a Compose spec's params.scale (default 1).
func composeScale(spec *types.ComposeOverlaySpec) float64 {
	if v, ok := spec.Params["scale"].(float64); ok {
		return v
	}
	return 1
}

// validateComposeScale is validateOverlayScale over a Compose spec.
func validateComposeScale(spec *types.ComposeOverlaySpec) error {
	raw, _ := json.Marshal(spec.Params)
	return validateOverlayScale(&types.OverlaySpec{Params: raw})
}

// trendSeries indexes each group's value to the first group's, × scale,
// one entry per host group, plus a warning naming the scored column.
func trendSeries(spec *types.OverlaySpec, host extend.SeriesOverlayHost) (extend.OverlayOutput, error) {
	base, ok := host.Value(0)
	if !ok || base == 0 {
		return extend.OverlayOutput{}, fmt.Errorf("no base value")
	}
	scale := overlayScale(spec)
	s := &types.SeriesPayload{}
	for i := 0; i < host.Groups(); i++ {
		e := types.SeriesEntry{Key: host.GroupKey(i)}
		if v, ok := host.Value(i); ok {
			x := v / base * scale
			e.Summary.Statistic = &x
		}
		s.Entries = append(s.Entries, e)
	}
	return extend.OverlayOutput{
		Payload:  types.OverlayPayload{Shape: types.OverlayShapeSeries, Series: s},
		Warnings: []types.OverlayWarning{{Code: "ACME_TREND_SCORED", Message: "scored " + host.ValueLabel() + " by " + fmt.Sprint(host.GroupFields())}},
	}, nil
}

// ratioCompose is target / reference × scale over whichever shape the
// slots share, plus one warning naming both slots.
func ratioCompose(spec *types.ComposeOverlaySpec, host extend.ComposeOverlayHost) (extend.OverlayOutput, error) {
	ref, tgt := host.Reference(), host.Target(0)
	if tgt == nil || host.Target(host.Targets()) != nil {
		return extend.OverlayOutput{}, fmt.Errorf("want one target")
	}
	scale := composeScale(spec)
	note := []types.OverlayWarning{{Code: "ACME_RATIO_SLOTS", Message: tgt.Label() + " over " + ref.Label(),
		Details: map[string]any{"reference_index": ref.Index(), "target_index": tgt.Index()}}}
	switch ref.Shape() {
	case types.OverlayShapeMatrix:
		rm, tm := ref.Matrix(), tgt.Matrix()
		if rm == nil || tm == nil || ref.Series() != nil {
			return extend.OverlayOutput{}, fmt.Errorf("matrix slot without its matrix view")
		}
		m := &types.MatrixPayload{RowHeader: tm.RowHeader(), ColumnHeader: tm.ColumnHeader(), CellLabel: "ratio"}
		for r := 0; r < tm.Rows(); r++ {
			m.RowKeys = append(m.RowKeys, tm.RowKey(r))
			row := make([]types.MatrixCell, tm.Columns())
			for c := range row {
				a, aok := rm.Cell(r, c)
				b, bok := tm.Cell(r, c)
				if aok && bok && a != 0 {
					row[c] = types.MatrixCell{Value: b / a * scale, Present: true}
				}
			}
			m.Cells = append(m.Cells, row)
		}
		for c := 0; c < tm.Columns(); c++ {
			m.ColumnKeys = append(m.ColumnKeys, tm.ColumnKey(c))
		}
		return extend.OverlayOutput{Payload: types.OverlayPayload{Shape: types.OverlayShapeMatrix, Matrix: m}, Warnings: note}, nil
	case types.OverlayShapeSeries:
		rs, ts := ref.Series(), tgt.Series()
		if rs == nil || ts == nil || ref.Matrix() != nil {
			return extend.OverlayOutput{}, fmt.Errorf("series slot without its series view")
		}
		s := &types.SeriesPayload{}
		for i := 0; i < ts.Groups(); i++ {
			e := types.SeriesEntry{Key: ts.GroupKey(i)}
			a, aok := rs.Value(i)
			b, bok := ts.Value(i)
			if aok && bok && a != 0 {
				x := b / a * scale
				e.Summary.Statistic = &x
			}
			s.Entries = append(s.Entries, e)
		}
		return extend.OverlayOutput{Payload: types.OverlayPayload{Shape: types.OverlayShapeSeries, Series: s}, Warnings: note}, nil
	}
	a, aok := ref.Scalar()
	b, bok := tgt.Scalar()
	if !aok || !bok || a == 0 {
		return extend.OverlayOutput{}, fmt.Errorf("no scalar values")
	}
	x := b / a * scale
	return extend.OverlayOutput{Payload: types.OverlayPayload{Shape: types.OverlayShapeScalar, Scalar: &x}, Warnings: note}, nil
}

// trendRegistration runs on the SERIES host and on Compose.
func trendRegistration() pulse.OverlayRegistration {
	return pulse.OverlayRegistration{
		Name: overlayTrendKind, Description: "index to the first group, or target over reference",
		Handler: extend.OverlayHandler{Series: trendSeries, Compose: ratioCompose},
		Shapes:  []types.OverlayShape{types.OverlayShapeSeries, types.OverlayShapeMatrix, types.OverlayShapeScalar},
		Scopes:  []types.OverlayScope{types.OverlayScopeGroup, types.OverlayScopeCell, types.OverlayScopeTotal},
		Params:  []pulse.ParamMeta{{Name: "scale", JSONType: "number", Description: "multiplier"}},
		// Request and Compose specs share the scale rules.
		Validate:        validateOverlayScale,
		ValidateCompose: validateComposeScale,
	}
}

// ratioRegistration runs on Compose only.
func ratioRegistration() pulse.OverlayRegistration {
	return pulse.OverlayRegistration{
		Name: overlayRatioKind, Description: "target over reference",
		Handler: extend.OverlayHandler{Compose: ratioCompose},
		Shapes:  []types.OverlayShape{types.OverlayShapeSeries, types.OverlayShapeMatrix, types.OverlayShapeScalar},
		Scopes:  []types.OverlayScope{types.OverlayScopeCell},
	}
}

func openHostsE2E(t *testing.T) (*pulse.Pulse, *types.Cohort) {
	t.Helper()
	return openE2E(t, pulse.Extensions{Overlays: []pulse.OverlayRegistration{
		shareRegistration(shareOverlay, nil), trendRegistration(), ratioRegistration()}})
}

// overlaySeriesRequest is SUM(qty) by region with the specs.
func overlaySeriesRequest(cohort *types.Cohort, specs ...types.OverlaySpec) *types.Request {
	return &types.Request{
		Cohort:       cohort,
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "qty", Label: "s"}, {Type: types.AGG_COUNT, Field: "qty", Label: "n"}},
		Overlays:     specs,
	}
}

// composeSlots is two slots of one shape over the cohort: "a" counts,
// "b" sums qty — matrix (by region crosstab), series (by region) or
// scalar (ungrouped).
func composeSlots(cohort *types.Cohort, shape types.OverlayShape) []*types.Request {
	slot := func(label string, agg types.AggregationType) *types.Request {
		a := &types.Aggregation{Type: agg, Field: "qty", Label: "v"}
		r := &types.Request{Label: label, Cohort: cohort}
		switch shape {
		case types.OverlayShapeMatrix:
			r.Crosstab = &types.CrosstabSpec{Rows: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}}, Cell: a}
		case types.OverlayShapeSeries:
			r.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}}
			r.Aggregations = []*types.Aggregation{a}
		default:
			r.Aggregations = []*types.Aggregation{a}
		}
		return r
	}
	return []*types.Request{slot("a", types.AGG_COUNT), slot("b", types.AGG_SUM)}
}

func composeWith(cohort *types.Cohort, shape types.OverlayShape, specs ...types.ComposeOverlaySpec) *types.ComposedRequest {
	return &types.ComposedRequest{Requests: composeSlots(cohort, shape), Overlays: specs}
}

// TestExtensions_OverlaySeriesRunsLikeBuiltin: on a grouped request the
// SERIES handler folds over the first aggregation, its layer and
// warnings land like a built-in's, the host rows are untouched, and
// predict accepts the request.
func TestExtensions_OverlaySeriesRunsLikeBuiltin(t *testing.T) {
	t.Parallel()

	p, cohort := openHostsE2E(t)
	spec := types.OverlaySpec{Kind: overlayTrendKind, Scope: types.OverlayScopeGroup, Params: json.RawMessage(`{"scale":100}`)}
	req := overlaySeriesRequest(cohort, spec)
	resp := processE2E(t, p, req)
	if len(resp.Overlays) != 1 {
		t.Fatalf("overlays = %+v, want one layer", resp.Overlays)
	}
	layer := resp.Overlays[0]
	if layer.Name != "acme_trend" || layer.Kind != overlayTrendKind || layer.Scope != types.OverlayScopeGroup {
		t.Errorf("layer identity = %q %q %q", layer.Name, layer.Kind, layer.Scope)
	}
	got := layer.Payload.Series
	if layer.Payload.Shape != types.OverlayShapeSeries || got == nil || len(got.Entries) != len(resp.Data) || len(resp.Data) < 2 {
		t.Fatalf("payload = %+v over %d host rows", layer.Payload, len(resp.Data))
	}
	base := e2eFloat(t, resp.Data[0]["s"])
	for i, row := range resp.Data {
		if !reflect.DeepEqual(got.Entries[i].Key, types.AxisKey{row["region"]}) {
			t.Errorf("entry %d key = %v, want [%v]", i, got.Entries[i].Key, row["region"])
		}
		want := e2eFloat(t, row["s"]) / base * 100
		if st := got.Entries[i].Summary.Statistic; st == nil || *st != want {
			t.Errorf("entry %d = %v, want %v", i, st, want)
		}
	}
	var lifted bool
	for _, w := range resp.Warnings {
		if w.Code == "ACME_TREND_SCORED" && w.Message == "scored s by [region]" {
			lifted = true
		}
	}
	if !lifted {
		t.Errorf("warnings = %+v, want the handler's ACME_TREND_SCORED", resp.Warnings)
	}

	free := overlaySeriesRequest(cohort)
	baseResp := processE2E(t, p, free)
	a, _ := json.Marshal(baseResp.Data)
	b, _ := json.Marshal(resp.Data)
	if string(a) != string(b) {
		t.Errorf("the overlay changed the host rows:\n%s\nvs overlay-free\n%s", b, a)
	}

	pr, err := p.Predict(context.Background(), req)
	if err != nil || len(pr.Errors) != 0 {
		t.Fatalf("predict refused an accepted spec: %v %+v", err, pr.Errors)
	}
}

// TestExtensions_OverlayComposeRunsLikeBuiltin: on ComposedRequest.Overlays
// the Compose handler reads the reference and target slots through the
// view of their shape — crosstab, series, scalar — its layer lands in
// ComposedResponse.Overlays, its warnings in that layer's Warnings, the
// slot responses are untouched, and predict-compose accepts the request.
func TestExtensions_OverlayComposeRunsLikeBuiltin(t *testing.T) {
	t.Parallel()

	p, cohort := openHostsE2E(t)
	ctx := context.Background()
	for _, shape := range []types.OverlayShape{types.OverlayShapeMatrix, types.OverlayShapeSeries, types.OverlayShapeScalar} {
		t.Run(string(shape), func(t *testing.T) {
			t.Parallel()
			specs := []types.ComposeOverlaySpec{
				{Kind: overlayRatioKind, Scope: types.OverlayScopeCell, Reference: "a", Targets: []string{"b"}},
				{Name: "scaled", Kind: overlayTrendKind, Scope: types.OverlayScopeCell, Reference: "a", Targets: []string{"b"}, Params: map[string]any{"scale": 10.0}},
			}
			req := composeWith(cohort, shape, specs...)
			out, err := p.Compose(ctx, req)
			if err != nil {
				t.Fatalf("Compose: %v", err)
			}
			if len(out.Overlays) != 2 {
				t.Fatalf("overlays = %+v, want two layers", out.Overlays)
			}
			for i, layer := range out.Overlays {
				wantName := []string{"acme_ratio", "scaled"}[i]
				if layer.Name != wantName || layer.Kind != specs[i].Kind || layer.Scope != types.OverlayScopeCell || layer.Payload.Shape != shape {
					t.Errorf("layer %d = %q %q %q %q, want %s over %s", i, layer.Name, layer.Kind, layer.Scope, layer.Payload.Shape, wantName, shape)
				}
				if len(layer.Warnings) != 1 || layer.Warnings[0].Code != "ACME_RATIO_SLOTS" || layer.Warnings[0].Message != "b over a" ||
					layer.Warnings[0].Details["overlay_index"] != i || layer.Warnings[0].Details["target_index"] != 1 {
					t.Errorf("layer %d warnings = %+v, want its own ACME_RATIO_SLOTS at overlay_index %d", i, layer.Warnings, i)
				}
			}
			scale := []float64{1, 10}
			ref, tgt := out.Responses[0], out.Responses[1]
			for i, layer := range out.Overlays {
				switch shape {
				case types.OverlayShapeMatrix:
					m := layer.Payload.Matrix
					for r := range tgt.Crosstab.Matrix.Cells {
						want := tgt.Crosstab.Matrix.Cells[r][0].Scalar() / ref.Crosstab.Matrix.Cells[r][0].Scalar() * scale[i]
						if m == nil || m.Cells[r][0].Scalar() != want || !reflect.DeepEqual(m.RowKeys[r], tgt.Crosstab.Matrix.RowKeys[r]) {
							t.Fatalf("layer %d cell %d = %+v, want %v", i, r, m, want)
						}
					}
				case types.OverlayShapeSeries:
					s := layer.Payload.Series
					for g, row := range tgt.Data {
						want := e2eFloat(t, row["v"]) / e2eFloat(t, ref.Data[g]["v"]) * scale[i]
						if s == nil || s.Entries[g].Summary.Statistic == nil || *s.Entries[g].Summary.Statistic != want {
							t.Fatalf("layer %d entry %d = %+v, want %v", i, g, s, want)
						}
					}
				default:
					want := e2eFloat(t, tgt.Data[0]["v"]) / e2eFloat(t, ref.Data[0]["v"]) * scale[i]
					if v := layer.Payload.Scalar; v == nil || *v != want {
						t.Fatalf("layer %d scalar = %v, want %v", i, v, want)
					}
				}
			}

			free, err := p.Compose(ctx, composeWith(cohort, shape))
			if err != nil {
				t.Fatalf("overlay-free Compose: %v", err)
			}
			a, _ := json.Marshal(free.Responses)
			b, _ := json.Marshal(out.Responses)
			if string(a) != string(b) {
				t.Errorf("the overlays changed the slot responses:\n%s\nvs overlay-free\n%s", b, a)
			}

			env, err := p.PredictCompose(ctx, req)
			if err != nil || len(env.Errors) != 0 {
				t.Fatalf("PredictCompose refused an accepted spec: %v %+v", err, env.Errors)
			}
		})
	}
}

// TestExtensions_OverlayHostsPredictMatchesRuntime is the per-host
// parity gate: a spec over a host the kind does not declare (crosstab,
// series, Compose, chain, facet) and every refusal of the declaration on
// the SERIES and Compose hosts is predict's only error and the runtime's
// error — code, message and details identical.
func TestExtensions_OverlayHostsPredictMatchesRuntime(t *testing.T) {
	t.Parallel()

	p, cohort := openHostsE2E(t)
	ctx := context.Background()
	series := func(mut func(*types.OverlaySpec)) *types.Request {
		s := types.OverlaySpec{Name: "tr", Kind: overlayTrendKind, Scope: types.OverlayScopeGroup}
		if mut != nil {
			mut(&s)
		}
		return overlaySeriesRequest(cohort, s)
	}
	seriesParams := func(raw string) *types.Request {
		return series(func(s *types.OverlaySpec) { s.Params = json.RawMessage(raw) })
	}
	process := []struct {
		name string
		req  *types.Request
		code perr.Code
	}{
		{name: "compose_only_on_crosstab", req: overlayCrosstabRequest(cohort, types.OverlaySpec{Kind: overlayRatioKind, Scope: types.OverlayScopeCell}), code: perr.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE},
		{name: "compose_only_on_series", req: overlaySeriesRequest(cohort, types.OverlaySpec{Kind: overlayRatioKind, Scope: types.OverlayScopeCell}), code: perr.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE},
		{name: "series_only_on_crosstab", req: overlayCrosstabRequest(cohort, types.OverlaySpec{Kind: overlayTrendKind, Scope: types.OverlayScopeCell}), code: perr.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE},
		{name: "series_ref", req: series(func(s *types.OverlaySpec) { s.Ref.Margin = &types.OverlayMarginRef{Axis: types.MarginAxisRow} }), code: perr.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE},
		{name: "series_scope", req: series(func(s *types.OverlaySpec) { s.Scope = types.OverlayScopeRow }), code: perr.PULSE_OVERLAY_SCOPE_UNSUPPORTED},
		{name: "series_within", req: series(func(s *types.OverlaySpec) { s.Within = 1 }), code: perr.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE},
		{name: "series_param_unknown", req: seriesParams(`{"scael":2}`), code: perr.PULSE_REQUEST_UNKNOWN_FIELD},
		{name: "series_validate_uncoded", req: seriesParams(`{"scale":-2}`), code: perr.PULSE_EXTENSION_SPEC_REFUSED},
		{name: "series_validate_coded", req: seriesParams(`{"scale":0}`), code: perr.PROCESSING_CONFIG},
		{name: "series_validate_panic", req: seriesParams(`{"scale":99}`), code: perr.PULSE_EXTENSION_SPEC_REFUSED},
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

	compose := func(shape types.OverlayShape, mut func(*types.ComposeOverlaySpec)) *types.ComposedRequest {
		s := types.ComposeOverlaySpec{Name: "tr", Kind: overlayTrendKind, Scope: types.OverlayScopeCell, Reference: "a", Targets: []string{"b"}}
		if mut != nil {
			mut(&s)
		}
		return composeWith(cohort, shape, s)
	}
	composeParams := func(params map[string]any) *types.ComposedRequest {
		return compose(types.OverlayShapeMatrix, func(s *types.ComposeOverlaySpec) { s.Params = params })
	}
	composes := []struct {
		name string
		req  *types.ComposedRequest
		code perr.Code
	}{
		{name: "matrix_only_on_compose", req: compose(types.OverlayShapeMatrix, func(s *types.ComposeOverlaySpec) { s.Kind = overlayShareKind }), code: perr.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE},
		{name: "scope", req: compose(types.OverlayShapeMatrix, func(s *types.ComposeOverlaySpec) { s.Scope = types.OverlayScopeRow }), code: perr.PULSE_OVERLAY_SCOPE_UNSUPPORTED},
		{name: "level", req: compose(types.OverlayShapeMatrix, func(s *types.ComposeOverlaySpec) { s.Level = 1 }), code: perr.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE},
		{name: "param_unknown", req: composeParams(map[string]any{"scael": 2.0}), code: perr.PULSE_REQUEST_UNKNOWN_FIELD},
		{name: "validate_uncoded", req: composeParams(map[string]any{"scale": -2.0}), code: perr.PULSE_EXTENSION_SPEC_REFUSED},
		{name: "validate_coded", req: composeParams(map[string]any{"scale": 0.0}), code: perr.PROCESSING_CONFIG},
		{name: "validate_panic", req: composeParams(map[string]any{"scale": 99.0}), code: perr.PULSE_EXTENSION_SPEC_REFUSED},
		{name: "reference_unknown", req: compose(types.OverlayShapeSeries, func(s *types.ComposeOverlaySpec) { s.Reference = "zz" }), code: perr.PULSE_OVERLAY_REFERENCE_UNKNOWN},
		{name: "shape_divergent", req: func() *types.ComposedRequest {
			r := compose(types.OverlayShapeMatrix, nil)
			r.Requests[1] = composeSlots(cohort, types.OverlayShapeSeries)[1]
			return r
		}(), code: perr.PULSE_OVERLAY_SLOT_SHAPE_DIVERGENT},
	}
	for _, tc := range composes {
		t.Run("compose/"+tc.name, func(t *testing.T) {
			t.Parallel()
			env, err := p.PredictCompose(ctx, tc.req)
			if err != nil {
				t.Fatalf("PredictCompose: %v", err)
			}
			if len(env.Errors) != 1 {
				t.Fatalf("predict-compose errors = %+v, want exactly one", env.Errors)
			}
			_, err = p.Compose(ctx, tc.req)
			sameRefusal(t, "compose", env.Errors[0], err, tc.code)
		})
	}

	t.Run("chain", func(t *testing.T) {
		t.Parallel()
		zero, one := 0, 1
		for _, kind := range []types.OverlayKind{overlayShareKind, overlayTrendKind, overlayRatioKind} {
			mk := func() *types.ChainRequest {
				return &types.ChainRequest{Cohort: cohort, Stages: []*types.ChainStage{
					{Name: "s0", Request: &types.Request{Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}}, Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "qty", Label: "v"}}}},
					{Name: "s1", Request: &types.Request{Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}}, Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "v", Label: "v"}}}},
				}, Overlays: []*types.ChainOverlaySpec{{Name: "o", Kind: kind, Scope: types.OverlayScopeGroup, Ref: types.StageRef{Index: &zero}, Target: types.StageRef{Index: &one}}}}
			}
			env, err := p.PredictChain(ctx, mk())
			if err != nil || len(env.Errors) != 1 {
				t.Fatalf("%s predict-chain = %v %+v, want one error", kind, err, env)
			}
			_, err = p.ProcessChain(ctx, mk())
			sameRefusal(t, "chain "+string(kind), env.Errors[0], err, perr.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE)
			if h := env.Errors[0].Details["host"]; h != "chain" {
				t.Errorf("%s details.host = %v, want chain", kind, h)
			}
		}
	})

	t.Run("facet", func(t *testing.T) {
		t.Parallel()
		for _, kind := range []types.OverlayKind{overlayShareKind, overlayTrendKind, overlayRatioKind} {
			mk := func() *types.FacetRequest {
				return &types.FacetRequest{Cohort: cohort, Fields: []string{"region"},
					Overlays: []types.OverlaySpec{{Name: "o", Kind: kind, Scope: types.OverlayScopeGroup}}}
			}
			env, err := p.PredictFacet(ctx, mk())
			if err != nil || len(env.Errors) != 1 {
				t.Fatalf("%s predict-facet = %v %+v, want one error", kind, err, env)
			}
			_, err = p.FacetSchema(ctx, mk())
			sameRefusal(t, "facet "+string(kind), env.Errors[0], err, perr.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE)
			if h := env.Errors[0].Details["host"]; h != "facet" {
				t.Errorf("%s details.host = %v, want facet", kind, h)
			}
		}
	})
}

// TestExtensions_OverlaySlotRefusalWording pins the wrong-slot refusal
// an extension kind gets: the built-in wording, naming the slots its
// handlers declare.
func TestExtensions_OverlaySlotRefusalWording(t *testing.T) {
	t.Parallel()

	p, cohort := openHostsE2E(t)
	_, err := p.Process(context.Background(), overlayCrosstabRequest(cohort, types.OverlaySpec{Kind: overlayRatioKind, Scope: types.OverlayScopeCell}))
	ce := requireCodedError(t, err, perr.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE)
	want := map[string]any{"index": 0, "kind": string(overlayRatioKind), "host": "request", "hosts": []string{"compose"}}
	if ce.Message != "overlay OVERLAY_ACME_RATIO does not run on Request.Overlays; attach it to ComposedRequest.Overlays" || !reflect.DeepEqual(ce.Details, want) {
		t.Errorf("refusal = %q %v, want details %v", ce.Message, ce.Details, want)
	}
}

// TestExtensions_OverlayValidateRunsOncePerSpec: a Process run calls a
// kind's Validate hook once per spec on each Process host — the host
// dispatcher re-judges the declaration without re-running the hook.
func TestExtensions_OverlayValidateRunsOncePerSpec(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	counting := func(spec *types.OverlaySpec) error { calls.Add(1); return nil }
	share := shareRegistration(shareOverlay, counting)
	trend := trendRegistration()
	trend.Validate = counting
	p, cohort := openE2E(t, pulse.Extensions{Overlays: []pulse.OverlayRegistration{share, trend}})
	for _, tc := range []struct {
		name string
		req  *types.Request
	}{
		{"crosstab", overlayCrosstabRequest(cohort, types.OverlaySpec{Kind: overlayShareKind, Scope: types.OverlayScopeCell})},
		{"series", overlaySeriesRequest(cohort, types.OverlaySpec{Kind: overlayTrendKind, Scope: types.OverlayScopeGroup})},
	} {
		calls.Store(0)
		resp := processE2E(t, p, tc.req)
		if len(resp.Overlays) != 1 {
			t.Fatalf("%s: overlays = %+v", tc.name, resp.Overlays)
		}
		if n := calls.Load(); n != 1 {
			t.Errorf("%s: Validate ran %d times for one spec, want 1", tc.name, n)
		}
	}
}

// TestExtensions_OverlayHostsProbed: pulse.New probes the SERIES and
// Compose handlers too — a panic names its host, an undeclared shape
// and a ValidateCompose panic fail registration, an error is tolerated,
// and a Compose-only or SERIES-only kind registers.
func TestExtensions_OverlayHostsProbed(t *testing.T) {
	t.Parallel()

	one := 1.0
	scalarCompose := func(*types.ComposeOverlaySpec, extend.ComposeOverlayHost) (extend.OverlayOutput, error) {
		return extend.OverlayOutput{Payload: types.OverlayPayload{Shape: types.OverlayShapeScalar, Scalar: &one}}, nil
	}
	cases := []struct {
		name    string
		mut     func(*pulse.OverlayRegistration)
		code    perr.Code
		details map[string]any
	}{
		{name: "ok", mut: func(*pulse.OverlayRegistration) {}},
		{name: "series_only", mut: func(r *pulse.OverlayRegistration) { r.Handler.Compose, r.ValidateCompose = nil, nil }},
		{name: "compose_only", mut: func(r *pulse.OverlayRegistration) { r.Handler.Series, r.Validate = nil, nil }},
		{name: "errors_tolerated", mut: func(r *pulse.OverlayRegistration) {
			r.Handler.Series = func(*types.OverlaySpec, extend.SeriesOverlayHost) (extend.OverlayOutput, error) {
				return extend.OverlayOutput{}, fmt.Errorf("needs real data")
			}
			r.Handler.Compose = func(*types.ComposeOverlaySpec, extend.ComposeOverlayHost) (extend.OverlayOutput, error) {
				return extend.OverlayOutput{}, fmt.Errorf("needs real data")
			}
		}},
		{name: "series_panic", mut: func(r *pulse.OverlayRegistration) {
			r.Handler.Series = func(*types.OverlaySpec, extend.SeriesOverlayHost) (extend.OverlayOutput, error) { panic("boom") }
		}, code: perr.PULSE_EXTENSION_FACTORY_PANIC,
			details: map[string]any{"category": "overlay", "name": string(overlayTrendKind), "stage": "handler", "host": "series", "panic": "boom"}},
		{name: "compose_panic", mut: func(r *pulse.OverlayRegistration) {
			r.Handler.Compose = func(*types.ComposeOverlaySpec, extend.ComposeOverlayHost) (extend.OverlayOutput, error) {
				panic("bang")
			}
		}, code: perr.PULSE_EXTENSION_FACTORY_PANIC,
			details: map[string]any{"category": "overlay", "name": string(overlayTrendKind), "stage": "handler", "host": "compose", "panic": "bang"}},
		{name: "compose_undeclared_shape", mut: func(r *pulse.OverlayRegistration) {
			r.Shapes = []types.OverlayShape{types.OverlayShapeSeries, types.OverlayShapeMatrix}
			r.Handler.Compose = scalarCompose
		}, code: perr.PULSE_EXTENSION_OVERLAY_OUTPUT_MISMATCH,
			details: map[string]any{"category": "overlay", "name": string(overlayTrendKind), "reason": "undeclared_shape",
				"shape": "scalar", "shapes": []string{"series", "matrix"}}},
		{name: "validate_compose_panic", mut: func(r *pulse.OverlayRegistration) {
			r.ValidateCompose = func(*types.ComposeOverlaySpec) error { panic("never") }
		}, code: perr.PULSE_EXTENSION_FACTORY_PANIC,
			details: map[string]any{"category": "overlay", "name": string(overlayTrendKind), "stage": "validate",
				"validate_code": string(perr.PULSE_EXTENSION_SPEC_REFUSED), "panic": "never",
				"validate_error": "overlay OVERLAY_ACME_TREND Validate panicked: never"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reg := trendRegistration()
			tc.mut(&reg)
			_, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), Extensions: pulse.Extensions{Overlays: []pulse.OverlayRegistration{reg}}})
			if tc.code == "" {
				if err != nil {
					t.Fatalf("pulse.New: %v", err)
				}
				return
			}
			ce := requireCodedError(t, err, tc.code)
			if !reflect.DeepEqual(ce.Details, tc.details) {
				t.Errorf("details = %v, want %v", ce.Details, tc.details)
			}
		})
	}
}
