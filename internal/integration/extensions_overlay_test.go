package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// An embedder-registered OVERLAY_<NS>_<NAME> kind (OverlayRegistration,
// U67 E2-S1) runs on a crosstab host like a built-in — one
// types.OverlayLayer in Response.Overlays, the host result untouched —
// and every spec of it is judged by its declaration (host shape, Ref,
// Scope, Level / Within, params, Validate) at predict exactly as at
// runtime, byte-identically.

const overlayShareKind types.OverlayKind = "OVERLAY_ACME_SHARE"

// overlayScale reads params.scale (default 1).
func overlayScale(spec *types.OverlaySpec) float64 {
	var p struct {
		Scale *float64 `json:"scale"`
	}
	if len(spec.Params) > 0 && json.Unmarshal(spec.Params, &p) == nil && p.Scale != nil {
		return *p.Scale
	}
	return 1
}

// validateOverlayScale refuses params.scale by value: negative — an
// uncoded error; zero — a wrapped coded one; 99 — a panic.
func validateOverlayScale(spec *types.OverlaySpec) error {
	switch s := overlayScale(spec); {
	case s < 0:
		return fmt.Errorf("params.scale must be non-negative, got %g", s)
	case s == 0:
		return fmt.Errorf("wrapped: %w", perr.NewCodedErrorWithDetails(perr.PROCESSING_CONFIG,
			"overlay params.scale must not be zero", map[string]any{"key": "scale"}))
	case s == 99:
		panic("scale is 99")
	}
	return nil
}

// shareOverlay is cell / grand total × params.scale over the crosstab,
// with the host's row keys and headers, plus a min/max summary.
func shareOverlay(spec *types.OverlaySpec, host extend.MatrixOverlayHost) (extend.OverlayOutput, error) {
	grand, ok := host.GrandTotal()
	if !ok || grand == 0 {
		return extend.OverlayOutput{}, fmt.Errorf("no grand total")
	}
	scale := overlayScale(spec)
	m := &types.MatrixPayload{RowHeader: host.RowHeader(), ColumnHeader: host.ColumnHeader(), CellLabel: "share"}
	var lo, hi *float64
	for r := 0; r < host.Rows(); r++ {
		m.RowKeys = append(m.RowKeys, host.RowKey(r))
		row := make([]types.MatrixCell, host.Columns())
		for c := range row {
			if v, ok := host.Cell(r, c); ok {
				s := v / grand * scale
				row[c] = types.MatrixCell{Value: s, Present: true}
				if lo == nil || s < *lo {
					lo = &s
				}
				if hi == nil || s > *hi {
					hi = &s
				}
			}
		}
		m.Cells = append(m.Cells, row)
	}
	for c := 0; c < host.Columns(); c++ {
		m.ColumnKeys = append(m.ColumnKeys, host.ColumnKey(c))
	}
	return extend.OverlayOutput{
		Payload: types.OverlayPayload{Shape: types.OverlayShapeMatrix, Matrix: m},
		Summary: &types.OverlaySummary{Min: lo, Max: hi},
	}, nil
}

// shareRegistration is the crosstab share kind with validate.
func shareRegistration(handler extend.MatrixOverlayFunc, validate extend.SpecValidator[types.OverlaySpec]) pulse.OverlayRegistration {
	return pulse.OverlayRegistration{
		Name: overlayShareKind, Description: "share of the grand total, scaled",
		Handler:  extend.OverlayHandler{Matrix: handler},
		Shapes:   []types.OverlayShape{types.OverlayShapeMatrix},
		Scopes:   []types.OverlayScope{types.OverlayScopeCell},
		Refs:     []extend.OverlayRefKind{extend.OverlayRefNone, extend.OverlayRefMargin},
		Params:   []pulse.ParamMeta{{Name: "scale", JSONType: "number", Description: "multiplier"}},
		Validate: validate,
	}
}

// overlayCrosstabRequest is COUNT by region with one spec.
func overlayCrosstabRequest(cohort *types.Cohort, spec types.OverlaySpec) *types.Request {
	return &types.Request{
		Cohort: cohort,
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "qty", Label: "n"},
			Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
		},
		Overlays: []types.OverlaySpec{spec},
	}
}

// TestExtensions_OverlayMatrixRunsLikeBuiltin: the layer lands in
// Response.Overlays with the engine-stamped identity and the handler's
// payload, the host crosstab is byte-identical to the overlay-free run,
// and predict accepts the request.
func TestExtensions_OverlayMatrixRunsLikeBuiltin(t *testing.T) {
	t.Parallel()

	p, cohort := openE2E(t, pulse.Extensions{Overlays: []pulse.OverlayRegistration{shareRegistration(shareOverlay, validateOverlayScale)}})
	spec := types.OverlaySpec{Kind: overlayShareKind, Scope: types.OverlayScopeCell, Params: json.RawMessage(`{"scale":100}`)}
	req := overlayCrosstabRequest(cohort, spec)
	resp := processE2E(t, p, req)
	if len(resp.Overlays) != 1 {
		t.Fatalf("overlays = %+v, want one layer", resp.Overlays)
	}
	layer := resp.Overlays[0]
	if layer.Name != "acme_share" || layer.Kind != overlayShareKind || layer.Scope != types.OverlayScopeCell {
		t.Errorf("layer identity = %q %q %q, want acme_share %s cell", layer.Name, layer.Kind, layer.Scope, overlayShareKind)
	}
	host := resp.Crosstab.Matrix
	got := layer.Payload.Matrix
	if layer.Payload.Shape != types.OverlayShapeMatrix || got == nil || len(got.Cells) != len(host.Cells) || len(host.Cells) != 4 {
		t.Fatalf("payload = %+v over %d host rows", layer.Payload, len(host.Cells))
	}
	grand := host.GrandTotal.Scalar()
	for r := range host.Cells {
		if want := host.Cells[r][0].Scalar() / grand * 100; got.Cells[r][0].Scalar() != want {
			t.Errorf("cell[%d] = %v, want %v", r, got.Cells[r][0].Scalar(), want)
		}
		if !reflect.DeepEqual(got.RowKeys[r], host.RowKeys[r]) {
			t.Errorf("row key %d = %v, want host %v", r, got.RowKeys[r], host.RowKeys[r])
		}
	}
	if layer.Summary == nil || layer.Summary.Min == nil || layer.Summary.Max == nil {
		t.Errorf("summary = %+v, want min / max", layer.Summary)
	}

	free := overlayCrosstabRequest(cohort, spec)
	free.Overlays = nil
	base := processE2E(t, p, free)
	a, _ := json.Marshal(base.Crosstab)
	b, _ := json.Marshal(resp.Crosstab)
	if string(a) != string(b) {
		t.Errorf("the overlay changed the host crosstab:\n%s\nvs overlay-free\n%s", b, a)
	}

	pr, err := p.Predict(context.Background(), req)
	if err != nil || len(pr.Errors) != 0 {
		t.Fatalf("predict refused an accepted spec: %v %+v", err, pr.Errors)
	}

	// A Compose slot's own Request.Overlays is the same crosstab host.
	slot := overlayCrosstabRequest(cohort, spec)
	slot.Label = "a"
	composed, err := p.Compose(context.Background(), &types.ComposedRequest{Requests: []*types.Request{slot}})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if got := composed.Responses[0].Overlays; len(got) != 1 || !reflect.DeepEqual(got[0].Payload, layer.Payload) {
		t.Errorf("compose slot overlays = %+v, want the Process layer", got)
	}
	env, err := p.PredictCompose(context.Background(), &types.ComposedRequest{Requests: []*types.Request{slot}})
	if err != nil || len(env.Errors) != 0 {
		t.Fatalf("PredictCompose refused the slot overlay: %v %+v", err, env.Errors)
	}
}

// TestExtensions_OverlayPredictMatchesRuntime is the parity gate: every
// refusal of the declaration — host shape, multi-measure host, Ref,
// Scope, Level / Within, params, Validate (uncoded, coded, panic) — is
// predict's only error and Process's error, code, message and details
// identical.
func TestExtensions_OverlayPredictMatchesRuntime(t *testing.T) {
	t.Parallel()

	p, cohort := openE2E(t, pulse.Extensions{Overlays: []pulse.OverlayRegistration{shareRegistration(shareOverlay, validateOverlayScale)}})
	base := func(mut func(*types.OverlaySpec)) types.OverlaySpec {
		s := types.OverlaySpec{Kind: overlayShareKind, Scope: types.OverlayScopeCell, Name: "sh"}
		if mut != nil {
			mut(&s)
		}
		return s
	}
	params := func(raw string) func(*types.OverlaySpec) {
		return func(s *types.OverlaySpec) { s.Params = json.RawMessage(raw) }
	}
	crosstab := func(mut func(*types.OverlaySpec)) *types.Request { return overlayCrosstabRequest(cohort, base(mut)) }
	cases := []struct {
		name string
		req  *types.Request
		code perr.Code
	}{
		{name: "series_host", req: &types.Request{Cohort: cohort,
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "score", Label: "n"}},
			Overlays:     []types.OverlaySpec{base(nil)}}, code: perr.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE},
		{name: "scalar_host", req: &types.Request{Cohort: cohort,
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "score", Label: "n"}},
			Overlays:     []types.OverlaySpec{base(nil)}}, code: perr.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE},
		{name: "multi_measure", req: func() *types.Request {
			r := crosstab(nil)
			r.Crosstab.Cell = nil
			r.Crosstab.Cells = []*types.Aggregation{{Type: types.AGG_COUNT, Field: "qty", Label: "n"}, {Type: types.AGG_SUM, Field: "score", Label: "s"}}
			return r
		}(), code: perr.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE},
		{name: "ref_unread", req: crosstab(func(s *types.OverlaySpec) { s.Ref.Prior = &types.OverlayPriorRef{} }), code: perr.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE},
		{name: "ref_two_arms", req: crosstab(func(s *types.OverlaySpec) {
			s.Ref.Margin = &types.OverlayMarginRef{Axis: types.MarginAxisRow}
			s.Ref.Prior = &types.OverlayPriorRef{}
		}), code: perr.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE},
		{name: "ref_bad_axis", req: crosstab(func(s *types.OverlaySpec) { s.Ref.Margin = &types.OverlayMarginRef{Axis: "diagonal"} }), code: perr.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE},
		{name: "scope", req: crosstab(func(s *types.OverlaySpec) { s.Scope = types.OverlayScopeRow }), code: perr.PULSE_OVERLAY_SCOPE_UNSUPPORTED},
		{name: "level", req: crosstab(func(s *types.OverlaySpec) { s.Level = 1 }), code: perr.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE},
		{name: "param_unknown", req: crosstab(params(`{"scael":2}`)), code: perr.PULSE_REQUEST_UNKNOWN_FIELD},
		{name: "param_type", req: crosstab(params(`{"scale":"big"}`)), code: perr.PULSE_REQUEST_PARAM_INVALID},
		{name: "validate_uncoded", req: crosstab(params(`{"scale":-2}`)), code: perr.PULSE_EXTENSION_SPEC_REFUSED},
		{name: "validate_coded", req: crosstab(params(`{"scale":0}`)), code: perr.PROCESSING_CONFIG},
		{name: "validate_panic", req: crosstab(params(`{"scale":99}`)), code: perr.PULSE_EXTENSION_SPEC_REFUSED},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := p.Process(context.Background(), tc.req)
			ce := requireCodedError(t, err, tc.code)
			pr, perrv := p.Predict(context.Background(), tc.req)
			if perrv != nil {
				t.Fatalf("Predict: %v", perrv)
			}
			if len(pr.Errors) != 1 {
				t.Fatalf("predict errors = %+v, want exactly runtime's %s %q %v", pr.Errors, ce.Code, ce.Message, ce.Details)
			}
			if got := pr.Errors[0]; got.Code != string(ce.Code) || got.Message != ce.Message || !reflect.DeepEqual(got.Details, ce.Details) {
				t.Errorf("predict = %s %q %v\nruntime = %s %q %v", got.Code, got.Message, got.Details, ce.Code, ce.Message, ce.Details)
			}
		})
	}

	t.Run("validate_details", func(t *testing.T) {
		t.Parallel()
		_, err := p.Process(context.Background(), crosstab(params(`{"scale":-2}`)))
		ce := requireCodedError(t, err, perr.PULSE_EXTENSION_SPEC_REFUSED)
		want := map[string]any{"category": "overlay", "type": string(overlayShareKind), "slot": "overlays", "name": "sh"}
		if ce.Message != "overlay OVERLAY_ACME_SHARE refused its spec: params.scale must be non-negative, got -2" || !reflect.DeepEqual(ce.Details, want) {
			t.Errorf("Validate refusal = %q %v, want details %v", ce.Message, ce.Details, want)
		}
	})
}

// TestExtensions_OverlayRuntimeOutputHeld: a handler output its Shapes
// do not declare is refused on the run too (the probe host passes),
// PULSE_EXTENSION_OVERLAY_OUTPUT_MISMATCH with the spec index.
func TestExtensions_OverlayRuntimeOutputHeld(t *testing.T) {
	t.Parallel()

	// Matrix on the 2-row probe host, a scalar on any larger host.
	sneaky := func(spec *types.OverlaySpec, host extend.MatrixOverlayHost) (extend.OverlayOutput, error) {
		if host.Rows() > 2 {
			v := 1.0
			return extend.OverlayOutput{Payload: types.OverlayPayload{Shape: types.OverlayShapeScalar, Scalar: &v}}, nil
		}
		return shareOverlay(spec, host)
	}
	p, cohort := openE2E(t, pulse.Extensions{Overlays: []pulse.OverlayRegistration{shareRegistration(sneaky, nil)}})
	_, err := p.Process(context.Background(), overlayCrosstabRequest(cohort, types.OverlaySpec{Kind: overlayShareKind, Scope: types.OverlayScopeCell}))
	ce := requireCodedError(t, err, perr.PULSE_EXTENSION_OVERLAY_OUTPUT_MISMATCH)
	if ce.Details["reason"] != "undeclared_shape" || ce.Details["index"] != 0 || ce.Details["shape"] != "scalar" {
		t.Errorf("details = %v, want reason undeclared_shape, index 0, shape scalar", ce.Details)
	}
}

// TestExtensions_OverlayProbed: pulse.New probes the handler over the
// synthetic host — a panic (handler or Validate), an undeclared shape
// and an arm mismatch fail registration; an error is tolerated; a
// well-behaved kind registers.
func TestExtensions_OverlayProbed(t *testing.T) {
	t.Parallel()

	one := 1.0
	returning := func(payload types.OverlayPayload) extend.MatrixOverlayFunc {
		return func(*types.OverlaySpec, extend.MatrixOverlayHost) (extend.OverlayOutput, error) {
			return extend.OverlayOutput{Payload: payload}, nil
		}
	}
	cases := []struct {
		name     string
		handler  extend.MatrixOverlayFunc
		validate extend.SpecValidator[types.OverlaySpec]
		code     perr.Code
		details  map[string]any
	}{
		{name: "ok", handler: shareOverlay, validate: validateOverlayScale},
		{name: "error_tolerated", handler: func(*types.OverlaySpec, extend.MatrixOverlayHost) (extend.OverlayOutput, error) {
			return extend.OverlayOutput{}, fmt.Errorf("needs real data")
		}},
		{name: "handler_panic", handler: func(*types.OverlaySpec, extend.MatrixOverlayHost) (extend.OverlayOutput, error) { panic("boom") },
			code:    perr.PULSE_EXTENSION_FACTORY_PANIC,
			details: map[string]any{"category": "overlay", "name": string(overlayShareKind), "stage": "handler", "panic": "boom"}},
		{name: "validate_panic", handler: shareOverlay, validate: func(*types.OverlaySpec) error { panic("never") },
			code: perr.PULSE_EXTENSION_FACTORY_PANIC,
			details: map[string]any{"category": "overlay", "name": string(overlayShareKind), "stage": "validate",
				"validate_code": string(perr.PULSE_EXTENSION_SPEC_REFUSED), "panic": "never",
				"validate_error": "overlay OVERLAY_ACME_SHARE Validate panicked: never"}},
		{name: "undeclared_shape", handler: returning(types.OverlayPayload{Shape: types.OverlayShapeScalar, Scalar: &one}),
			code: perr.PULSE_EXTENSION_OVERLAY_OUTPUT_MISMATCH,
			details: map[string]any{"category": "overlay", "name": string(overlayShareKind), "reason": "undeclared_shape",
				"shape": "scalar", "shapes": []string{"matrix"}}},
		{name: "arm_mismatch", handler: returning(types.OverlayPayload{Shape: types.OverlayShapeMatrix, Scalar: &one}),
			code: perr.PULSE_EXTENSION_OVERLAY_OUTPUT_MISMATCH,
			details: map[string]any{"category": "overlay", "name": string(overlayShareKind), "reason": "shape_arm_mismatch",
				"shape": "matrix", "shapes": []string{"matrix"}}},
		{name: "engine_owned", handler: returning(types.OverlayPayload{Shape: types.OverlayShapeMatrix,
			Matrix: &types.MatrixPayload{}, PAdjusted: &types.MatrixPayload{}}),
			code: perr.PULSE_EXTENSION_OVERLAY_OUTPUT_MISMATCH,
			details: map[string]any{"category": "overlay", "name": string(overlayShareKind), "reason": "engine_owned",
				"shape": "matrix", "shapes": []string{"matrix"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(),
				Extensions: pulse.Extensions{Overlays: []pulse.OverlayRegistration{shareRegistration(tc.handler, tc.validate)}}})
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

// TestExtensions_OverlayRegistrationRefused: the naming policy
// (namespace required, reserved namespaces, built-in collisions,
// duplicates) and the declaration's shape are enforced at pulse.New.
func TestExtensions_OverlayRegistrationRefused(t *testing.T) {
	t.Parallel()

	reg := func(mut func(*pulse.OverlayRegistration)) []pulse.OverlayRegistration {
		r := shareRegistration(shareOverlay, nil)
		mut(&r)
		return []pulse.OverlayRegistration{r}
	}
	cases := []struct {
		name   string
		regs   []pulse.OverlayRegistration
		code   perr.Code
		reason string
	}{
		{name: "no_namespace", regs: reg(func(r *pulse.OverlayRegistration) { r.Name = "OVERLAY_HEAT" }), code: perr.PULSE_EXTENSION_NAME_INVALID},
		{name: "lower_case", regs: reg(func(r *pulse.OverlayRegistration) { r.Name = "OVERLAY_acme_heat" }), code: perr.PULSE_EXTENSION_NAME_INVALID},
		{name: "wrong_prefix", regs: reg(func(r *pulse.OverlayRegistration) { r.Name = "AGG_ACME_HEAT" }), code: perr.PULSE_EXTENSION_NAME_INVALID},
		{name: "reserved", regs: reg(func(r *pulse.OverlayRegistration) { r.Name = "OVERLAY_PULSE_HEAT" }), code: perr.PULSE_EXTENSION_NAME_RESERVED},
		{name: "builtin", regs: reg(func(r *pulse.OverlayRegistration) { r.Name = types.OverlayKindShareOfRow }), code: perr.PULSE_EXTENSION_NAME_COLLISION},
		{name: "duplicate", regs: append(reg(func(*pulse.OverlayRegistration) {}), reg(func(*pulse.OverlayRegistration) {})...), code: perr.PULSE_EXTENSION_DUPLICATE},
		{name: "missing_handler", regs: reg(func(r *pulse.OverlayRegistration) { r.Handler = extend.OverlayHandler{} }), code: perr.PULSE_EXTENSION_PARAM_INVALID, reason: "missing_handler"},
		{name: "no_shapes", regs: reg(func(r *pulse.OverlayRegistration) { r.Shapes = nil }), code: perr.PULSE_EXTENSION_PARAM_INVALID, reason: "invalid_shape"},
		{name: "bad_scope", regs: reg(func(r *pulse.OverlayRegistration) { r.Scopes = []types.OverlayScope{types.OverlayScopeGroup} }), code: perr.PULSE_EXTENSION_PARAM_INVALID, reason: "invalid_scope"},
		{name: "bad_ref", regs: reg(func(r *pulse.OverlayRegistration) { r.Refs = []extend.OverlayRefKind{"prior"} }), code: perr.PULSE_EXTENSION_PARAM_INVALID, reason: "invalid_ref"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), Extensions: pulse.Extensions{Overlays: tc.regs}})
			ce := requireCodedError(t, err, tc.code)
			if tc.reason != "" && ce.Details["reason"] != tc.reason {
				t.Errorf("reason = %v, want %s (%v)", ce.Details["reason"], tc.reason, ce.Details)
			}
		})
	}
}
