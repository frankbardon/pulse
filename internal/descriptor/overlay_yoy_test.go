package descriptor

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// yoyHostReq returns a minimal grouped Process request whose first
// grouper is GROUP_DATE — the SERIES host the OVERLAY_YOY kind
// targets. Frequency lives on the OverlaySpec.Params (per-test fixture)
// so the host grouper's Params slot is intentionally empty unless the
// test promotes it.
func yoyHostReq() *types.Request {
	return &types.Request{
		Groups: []*types.Group{
			{Type: types.GROUP_DATE, Field: "order_date"},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "value"},
		},
	}
}

// TestValidateOverlay_YoYHappyPathAccepted asserts a well-formed
// OVERLAY_YOY overlay riding on a SERIES GROUP_DATE host passes the
// predict gate without surfacing any overlay-specific errors.
func TestValidateOverlay_YoYHappyPathAccepted(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := yoyHostReq()
	req.Overlays = []types.OverlaySpec{
		{
			Name:  "yoy",
			Kind:  types.OverlayKindYoY,
			Scope: types.OverlayScopeGroup,
			Ref: types.OverlayRef{
				YoY: &types.OverlayYoYRef{},
			},
			Params: json.RawMessage(`{"frequency": "monthly"}`),
		},
	}

	env := predictFromBytes(data, req, nil)
	for _, code := range []errors.Code{
		errors.PULSE_OVERLAY_KIND_UNKNOWN,
		errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
		errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED,
		errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE,
		errors.PULSE_OVERLAY_YOY_FREQUENCY_MISSING,
		errors.PULSE_OVERLAY_YOY_INCOMPATIBLE_FREQUENCY,
	} {
		if hasErrorCode(env, code) {
			t.Errorf("unexpected overlay error %s on OVERLAY_YOY happy-path request", code)
		}
	}
}

// TestValidateOverlay_YoYFrequencyRequired asserts that a YoY spec
// missing the frequency Param on both OverlaySpec.Params and
// Groups[0].Params fires PULSE_OVERLAY_YOY_FREQUENCY_MISSING. The
// story brief named this test explicitly.
func TestValidateOverlay_YoYFrequencyRequired(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := yoyHostReq()
	// Intentionally leave Groups[0].Params empty so the gate cannot fall
	// back to the host-grouper authoring slot.
	req.Overlays = []types.OverlaySpec{
		{
			Name:  "yoy",
			Kind:  types.OverlayKindYoY,
			Scope: types.OverlayScopeGroup,
			Ref: types.OverlayRef{
				YoY: &types.OverlayYoYRef{},
			},
			// Params intentionally absent.
		},
	}

	env := predictFromBytes(data, req, nil)
	if !hasErrorCode(env, errors.PULSE_OVERLAY_YOY_FREQUENCY_MISSING) {
		t.Fatalf("expected PULSE_OVERLAY_YOY_FREQUENCY_MISSING for missing frequency Param")
	}
}

// TestValidateOverlay_YoYFrequencyFromGroupParams pins the orchestrator
// fall-back: when the OverlaySpec.Params slot is empty but the host
// GROUP_DATE grouper carries a frequency Param the gate accepts the
// spec.
func TestValidateOverlay_YoYFrequencyFromGroupParams(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := yoyHostReq()
	req.Groups[0].Params = json.RawMessage(`{"component": "month", "frequency": "monthly"}`)
	req.Overlays = []types.OverlaySpec{
		{
			Name:  "yoy",
			Kind:  types.OverlayKindYoY,
			Scope: types.OverlayScopeGroup,
			Ref: types.OverlayRef{
				YoY: &types.OverlayYoYRef{},
			},
			// Params intentionally absent — the gate falls back to
			// Groups[0].Params.
		},
	}

	env := predictFromBytes(data, req, nil)
	for _, code := range []errors.Code{
		errors.PULSE_OVERLAY_YOY_FREQUENCY_MISSING,
		errors.PULSE_OVERLAY_YOY_INCOMPATIBLE_FREQUENCY,
	} {
		if hasErrorCode(env, code) {
			t.Errorf("unexpected overlay error %s on YoY frequency-from-group-params request", code)
		}
	}
}

// TestValidateOverlay_YoYIncompatibleFrequencyRejected asserts that a
// frequency value outside the supported set fires
// PULSE_OVERLAY_YOY_INCOMPATIBLE_FREQUENCY.
func TestValidateOverlay_YoYIncompatibleFrequencyRejected(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	for _, freq := range []string{"minutely", "yearly", "biennial", "decade"} {
		t.Run(freq, func(t *testing.T) {
			req := yoyHostReq()
			req.Overlays = []types.OverlaySpec{
				{
					Name:  "yoy",
					Kind:  types.OverlayKindYoY,
					Scope: types.OverlayScopeGroup,
					Ref: types.OverlayRef{
						YoY: &types.OverlayYoYRef{},
					},
					Params: json.RawMessage(`{"frequency": "` + freq + `"}`),
				},
			}

			env := predictFromBytes(data, req, nil)
			if !hasErrorCode(env, errors.PULSE_OVERLAY_YOY_INCOMPATIBLE_FREQUENCY) {
				t.Fatalf("frequency=%s: expected PULSE_OVERLAY_YOY_INCOMPATIBLE_FREQUENCY", freq)
			}
		})
	}
}

// TestValidateOverlay_YoYNonDateGrouperRejected asserts that a host
// whose first grouper is not GROUP_DATE fires
// PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE with Details naming the
// actual grouper type.
func TestValidateOverlay_YoYNonDateGrouperRejected(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	// siblingHostReq uses GROUP_CATEGORY over "region" — not a DATE host.
	req := siblingHostReq()
	req.Overlays = []types.OverlaySpec{
		{
			Name:  "yoy",
			Kind:  types.OverlayKindYoY,
			Scope: types.OverlayScopeGroup,
			Ref: types.OverlayRef{
				YoY: &types.OverlayYoYRef{},
			},
			Params: json.RawMessage(`{"frequency": "monthly"}`),
		},
	}

	env := predictFromBytes(data, req, nil)
	if !hasErrorCode(env, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE) {
		t.Fatalf("expected PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE for non-GROUP_DATE host")
	}
}

// TestValidateOverlay_YoYScopeUnsupported asserts every non-GROUP scope
// fires PULSE_OVERLAY_SCOPE_UNSUPPORTED.
func TestValidateOverlay_YoYScopeUnsupported(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	for _, scope := range []types.OverlayScope{
		types.OverlayScopeCell,
		types.OverlayScopeRow,
		types.OverlayScopeColumn,
		types.OverlayScopeMatrix,
		types.OverlayScopeTotal,
	} {
		t.Run(string(scope), func(t *testing.T) {
			req := yoyHostReq()
			req.Overlays = []types.OverlaySpec{
				{
					Kind:  types.OverlayKindYoY,
					Scope: scope,
					Ref: types.OverlayRef{
						YoY: &types.OverlayYoYRef{},
					},
					Params: json.RawMessage(`{"frequency": "monthly"}`),
				},
			}
			env := predictFromBytes(data, req, nil)
			if !hasErrorCode(env, errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED) {
				t.Fatalf("scope=%s: expected PULSE_OVERLAY_SCOPE_UNSUPPORTED", scope)
			}
		})
	}
}

// TestValidateOverlay_YoYRefRejected asserts non-YoY Ref pointers
// surface PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE.
func TestValidateOverlay_YoYRefRejected(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	cases := []struct {
		name string
		ref  types.OverlayRef
	}{
		{
			name: "Margin",
			ref:  types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow}},
		},
		{
			name: "Sibling",
			ref:  types.OverlayRef{Sibling: &types.OverlaySiblingRef{Field: "region", Value: "north"}},
		},
		{
			name: "Prior",
			ref:  types.OverlayRef{Prior: &types.OverlayPriorRef{}},
		},
		{
			name: "BaselineIndex",
			ref:  types.OverlayRef{BaselineIndex: &types.OverlayBaselineIndexRef{Position: 0}},
		},
		{
			name: "RollingMean",
			ref:  types.OverlayRef{RollingMean: &types.OverlayRollingMeanRef{}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := yoyHostReq()
			req.Overlays = []types.OverlaySpec{
				{
					Kind:   types.OverlayKindYoY,
					Scope:  types.OverlayScopeGroup,
					Ref:    tc.ref,
					Params: json.RawMessage(`{"frequency": "monthly"}`),
				},
			}
			env := predictFromBytes(data, req, nil)
			if !hasErrorCode(env, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE) {
				t.Fatalf("ref=%s: expected PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE", tc.name)
			}
		})
	}
}

// TestValidateOverlay_YoYMissingRefRejected asserts an empty Ref
// surfaces PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE.
func TestValidateOverlay_YoYMissingRefRejected(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := yoyHostReq()
	req.Overlays = []types.OverlaySpec{
		{
			Kind:   types.OverlayKindYoY,
			Scope:  types.OverlayScopeGroup,
			Ref:    types.OverlayRef{}, // empty
			Params: json.RawMessage(`{"frequency": "monthly"}`),
		},
	}
	env := predictFromBytes(data, req, nil)
	if !hasErrorCode(env, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE) {
		t.Fatalf("expected PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE for empty Ref")
	}
}

// TestValidateOverlay_YoYNoSeriesHostRejected asserts a request with no
// groupers OR an active Crosstab fires
// PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE (no SERIES host).
func TestValidateOverlay_YoYNoSeriesHostRejected(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	cases := []struct {
		name string
		req  *types.Request
	}{
		{
			name: "no Groups",
			req: &types.Request{
				Aggregations: []*types.Aggregation{
					{Type: types.AGG_SUM, Field: "value"},
				},
			},
		},
		{
			name: "Crosstab non-nil",
			req: &types.Request{
				Crosstab: crosstabHostSpec(),
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.req.Overlays = []types.OverlaySpec{
				{
					Kind:  types.OverlayKindYoY,
					Scope: types.OverlayScopeGroup,
					Ref: types.OverlayRef{
						YoY: &types.OverlayYoYRef{},
					},
					Params: json.RawMessage(`{"frequency": "monthly"}`),
				},
			}
			env := predictFromBytes(data, tc.req, nil)
			if !hasErrorCode(env, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE) {
				t.Fatalf("%s: expected PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE", tc.name)
			}
		})
	}
}

// TestValidateOverlay_YoYLevelWithinRejected asserts non-zero Level or
// Within values fire PULSE_OVERLAY_LEVEL_OUT_OF_RANGE — the kind is in
// the windowed implicit-margin family.
func TestValidateOverlay_YoYLevelWithinRejected(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	cases := []struct {
		name   string
		level  int
		within int
	}{
		{name: "Level=1", level: 1, within: 0},
		{name: "Within=1", level: 0, within: 1},
		{name: "both", level: 1, within: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := yoyHostReq()
			req.Overlays = []types.OverlaySpec{
				{
					Kind:  types.OverlayKindYoY,
					Scope: types.OverlayScopeGroup,
					Ref: types.OverlayRef{
						YoY: &types.OverlayYoYRef{},
					},
					Params: json.RawMessage(`{"frequency": "monthly"}`),
					Level:  tc.level,
					Within: tc.within,
				},
			}
			env := predictFromBytes(data, req, nil)
			if !hasErrorCode(env, errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE) {
				t.Fatalf("%s: expected PULSE_OVERLAY_LEVEL_OUT_OF_RANGE", tc.name)
			}
		})
	}
}

// TestValidateOverlay_YoYMultiGrouper: OVERLAY_YOY finds its GROUP_DATE
// grouper wherever it sits and partitions by every other key (U46
// E2-S3), so the date grouper outer or inner is accepted; zero or two
// date groupers are refused PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE
// with {groupers, date_groupers} (internal/seriespartition.YoY, the
// reading the runtime fold shares). The frequency fallback reads the
// date grouper's Params, not Groups[0]'s.
func TestValidateOverlay_YoYMultiGrouper(t *testing.T) {
	schema := overlayPredictSchema(t)
	data := buildTestPulseFile(t, schema)
	date := func(params string) *types.Group {
		g := &types.Group{Type: types.GROUP_DATE, Field: "order_date"}
		if params != "" {
			g.Params = json.RawMessage(params)
		}
		return g
	}
	shipped := &types.Group{Type: types.GROUP_DATE, Field: "ship_date"}
	region := func(params string) *types.Group {
		g := &types.Group{Type: types.GROUP_CATEGORY, Field: "region"}
		if params != "" {
			g.Params = json.RawMessage(params)
		}
		return g
	}
	yoyCodes := []errors.Code{
		errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
		errors.PULSE_OVERLAY_PARTITION_INVALID,
		errors.PULSE_OVERLAY_YOY_FREQUENCY_MISSING,
		errors.PULSE_OVERLAY_YOY_INCOMPATIBLE_FREQUENCY,
	}
	for _, c := range []struct {
		name   string
		groups []*types.Group
		params string
		code   errors.Code // "" = accepted
		want   map[string]any
	}{
		{"date outer", []*types.Group{date(""), region("")}, `{"frequency": "monthly"}`, "", nil},
		{"date inner", []*types.Group{region(""), date("")}, `{"frequency": "monthly"}`, "", nil},
		{"frequency from the inner date grouper", []*types.Group{region(""), date(`{"frequency": "monthly"}`)}, "", "", nil},
		// A category grouper takes no params: its frequency is refused
		// (U44 #301), never read as the YoY fallback.
		{"frequency on groups[0] is not the date grouper's", []*types.Group{region(`{"frequency": "monthly"}`), date("")}, "", errors.PULSE_REQUEST_UNKNOWN_FIELD,
			map[string]any{"path": "groups[0].params.frequency", "unknown_keys": []string{"frequency"},
				"suggestions": map[string]any{}, "valid_keys": []string{}}},
		{"no date grouper", []*types.Group{region(""), {Type: types.GROUP_CATEGORY, Field: "segment"}}, `{"frequency": "monthly"}`, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
			map[string]any{"index": 0, "kind": "OVERLAY_YOY", "groupers": 2, "date_groupers": 0, "required": "GROUP_DATE"}},
		{"two date groupers", []*types.Group{date(""), region(""), shipped}, `{"frequency": "monthly"}`, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
			map[string]any{"index": 0, "kind": "OVERLAY_YOY", "groupers": 3, "date_groupers": 2, "required": "GROUP_DATE"}},
		{"partition_by not the non-date keys", []*types.Group{region(""), date("")}, `{"frequency": "monthly", "partition_by": []}`, errors.PULSE_OVERLAY_PARTITION_INVALID,
			map[string]any{"index": 0, "kind": "OVERLAY_YOY", "partition_by": []string{}, "groups": []string{"region", "order_date"}, "reason": "not_non_date_keys", "field": "region"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			req := yoyHostReq()
			req.Groups = c.groups
			req.Overlays = []types.OverlaySpec{{
				Name: "yoy", Kind: types.OverlayKindYoY, Scope: types.OverlayScopeGroup,
				Ref: types.OverlayRef{YoY: &types.OverlayYoYRef{}},
			}}
			if c.params != "" {
				req.Overlays[0].Params = json.RawMessage(c.params)
			}
			env := predictFromBytes(data, req, nil)
			for _, code := range yoyCodes {
				if code != c.code && hasErrorCode(env, code) {
					t.Errorf("unexpected %s: %+v", code, env.Errors)
				}
			}
			if c.code == "" {
				return
			}
			for _, e := range env.Errors {
				if e.Code != string(c.code) {
					continue
				}
				if c.want != nil && !reflect.DeepEqual(e.Details, c.want) {
					t.Errorf("details %#v, want %#v", e.Details, c.want)
				}
				return
			}
			t.Fatalf("no %s: %+v", c.code, env.Errors)
		})
	}
}
