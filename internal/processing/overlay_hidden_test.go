package processing

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/processing/regression"
	"github.com/frankbardon/pulse/types"
)

// The regression registry and the overlay route. A regression type or
// overlay kind the instance feature set hides must take every branch a
// never-registered one takes — not only fail with the same error, but
// get there by the same route (streaming vs buffered, gate vs no gate).
// The public parity harness (feature_parity_harness_test.go) compares
// end-to-end outcomes; these pin the routing decisions an outcome
// comparison cannot see, each against the decision a never-registered
// name gets and an unscoped control that proves the case is not
// vacuous.

const neverOverlay = "OVERLAY_NEVER_REGISTERED"

// hiddenOutcome renders a result for byte comparison: the code,
// message and details of a coded error, else the JSON of v.
func hiddenOutcome(t *testing.T, v any, err error) string {
	t.Helper()
	if err != nil {
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) {
			return "error: " + err.Error()
		}
		b, mErr := json.Marshal(map[string]any{"code": ce.Code, "message": ce.Message, "details": ce.Details})
		if mErr != nil {
			t.Fatalf("marshal error: %v", mErr)
		}
		return string(b)
	}
	b, mErr := json.Marshal(v)
	if mErr != nil {
		t.Fatalf("marshal result: %v", mErr)
	}
	return "ok: " + string(b)
}

// assertHiddenParity requires the hidden outcome to equal the
// never-registered one after substituting the names, and the unscoped
// control to differ from both (else the case never reaches the name).
func assertHiddenParity(t *testing.T, hidden, never, gotHidden, gotNever, control string) {
	t.Helper()
	if sub := strings.ReplaceAll(gotHidden, hidden, never); sub != gotNever {
		t.Errorf("hidden %s diverges from never-registered %s\nhidden: %s\nnever:  %s", hidden, never, gotHidden, gotNever)
	}
	if strings.ReplaceAll(control, hidden, never) == gotNever {
		t.Errorf("vacuous: unscoped %s behaves like a never-registered name\ncontrol: %s", hidden, control)
	}
}

func hiddenRouteSchema() *encoding.Schema {
	region := encoding.NewDictionary()
	for _, v := range []string{"north", "south"} {
		_, _ = region.Add(v)
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "age", Type: encoding.FieldTypeF64},
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Dictionary: region},
	}}
}

// TestExtensionRegistry_HiddenRegressionLookup: LookupRegression misses
// a hidden type, and regression.BuildWith over it raises the error a
// never-registered type raises.
func TestExtensionRegistry_HiddenRegressionLookup(t *testing.T) {
	var nilReg *ExtensionRegistry
	if _, ok := nilReg.LookupRegression(types.REG_OLS); !ok {
		t.Fatal("REG_OLS does not resolve on a nil registry; the case is vacuous")
	}
	scoped := nilReg.WithHidden(hiddenSet(string(types.REG_OLS)))
	if _, ok := scoped.LookupRegression(types.REG_OLS); ok {
		t.Error("hidden REG_OLS still resolves")
	}
	if _, ok := scoped.LookupRegression(types.REG_GLM); !ok {
		t.Error("REG_GLM stopped resolving when only REG_OLS is hidden")
	}
	build := func(rt types.RegressionType, r *ExtensionRegistry) string {
		spec := []*types.RegressionSpec{{Type: rt, Target: "age", Predictors: []string{"age"}}}
		engines, err := regression.BuildWith(spec, hiddenRouteSchema(), r.LookupRegression)
		return hiddenOutcome(t, len(engines), err)
	}
	assertHiddenParity(t, "REG_OLS", "REG_NEVER_REGISTERED",
		build(types.REG_OLS, scoped), build("REG_NEVER_REGISTERED", scoped), build(types.REG_OLS, nil))
}

// TestHiddenRouting_StreamAndMergeGates: CanStreamRequestWithExtensions
// (the processor's canStream: RegressionSpec.Streamable and
// types.OverlayStreamable) and CanMergeRequestWithExtensions (mergegate)
// route a hidden streamable regression type or overlay kind exactly like
// a never-registered one.
func TestHiddenRouting_StreamAndMergeGates(t *testing.T) {
	schema := hiddenRouteSchema()
	cases := []struct {
		name, hidden, never string
		req                 func(op string) *types.Request
	}{
		{"regression", string(types.REG_OLS), "REG_NEVER_REGISTERED", func(op string) *types.Request {
			return &types.Request{
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "age", Label: "s"}},
				Regressions:  []*types.RegressionSpec{{Type: types.RegressionType(op), Target: "age", Predictors: []string{"age"}}},
			}
		}},
		{"series_overlay", string(types.OverlayKindIndexVsPrior), neverOverlay, func(op string) *types.Request {
			return &types.Request{
				Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "age", Label: "s"}},
				Overlays:     []types.OverlaySpec{{Kind: types.OverlayKind(op), Scope: types.OverlayScopeGroup}},
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !CanStreamRequestWithExtensions(tc.req(tc.hidden), schema, nil) {
				t.Fatalf("unscoped %s does not stream; the case is vacuous", tc.hidden)
			}
			scoped := (*ExtensionRegistry)(nil).WithHidden(hiddenSet(tc.hidden))
			if got, want := CanStreamRequestWithExtensions(tc.req(tc.hidden), schema, scoped),
				CanStreamRequestWithExtensions(tc.req(tc.never), schema, scoped); got != want {
				t.Errorf("stream gate: hidden %s = %v, never-registered = %v", tc.hidden, got, want)
			}
			if got, want := CanMergeRequestWithExtensions(tc.req(tc.hidden), schema, scoped),
				CanMergeRequestWithExtensions(tc.req(tc.never), schema, scoped); got != want {
				t.Errorf("merge gate: hidden %s = %v, never-registered = %v", tc.hidden, got, want)
			}
		})
	}
}

// TestHiddenOverlayRoute_LevelWithinGate: the crosstab Level/Within
// gate keys on the route. A registered share kind refuses an
// out-of-range Level before dispatch; a never-registered kind passes it
// to the table miss — and so must a hidden one.
func TestHiddenOverlayRoute_LevelWithinGate(t *testing.T) {
	host := newFormulaHostWithMargins(2, 2, func(r, c int) float64 { return float64(r + c + 1) })
	run := func(kind string, exts *ExtensionRegistry) string {
		specs := []types.OverlaySpec{{Kind: types.OverlayKind(kind), Scope: types.OverlayScopeCell, Level: 5}}
		layers, _, err := ApplyOverlaysWithExtensions(specs, host, exts)
		return hiddenOutcome(t, layers, err)
	}
	hidden := string(types.OverlayKindShareOfRow)
	scoped := (*ExtensionRegistry)(nil).WithHidden(hiddenSet(hidden))
	assertHiddenParity(t, hidden, neverOverlay, run(hidden, scoped), run(neverOverlay, scoped), run(hidden, nil))
}

// TestHiddenOverlayRoute_YoYFrequencyPromotion: the SERIES fold
// promotes the grouper's frequency onto OVERLAY_YOY specs only; a
// hidden OVERLAY_YOY is left as authored, like any other kind.
func TestHiddenOverlayRoute_YoYFrequencyPromotion(t *testing.T) {
	req := func(kind types.OverlayKind) *types.Request {
		return &types.Request{
			Groups:   []*types.Group{{Type: types.GROUP_DATE, Field: "d", Params: json.RawMessage(`{"frequency":"month"}`)}},
			Overlays: []types.OverlaySpec{{Kind: kind, Scope: types.OverlayScopeGroup}},
		}
	}
	run := func(kind types.OverlayKind, exts *ExtensionRegistry) string {
		return hiddenOutcome(t, promoteYoYFrequencyFromGroupParams(req(kind), exts), nil)
	}
	hidden := string(types.OverlayKindYoY)
	scoped := (*ExtensionRegistry)(nil).WithHidden(hiddenSet(hidden))
	assertHiddenParity(t, hidden, neverOverlay,
		run(types.OverlayKindYoY, scoped), run(neverOverlay, scoped), run(types.OverlayKindYoY, nil))
}

// TestHiddenOverlayRoute_PairwiseSlabGate: the crosstab pairwise slab
// partition gate fires for a registered pairwise kind over a fan-out
// axis; a never-registered kind skips it and reaches the table miss on
// both crosstab exits — and so must a hidden one.
func TestHiddenOverlayRoute_PairwiseSlabGate(t *testing.T) {
	schema := partitionGateSchema(t)
	recs := partitionGateRecords(schema)
	hidden := string(types.OverlayKindPairwisePropZ)
	scoped := (*ExtensionRegistry)(nil).WithHidden(hiddenSet(hidden))
	run := func(kind string, exts *ExtensionRegistry, fused bool) string {
		req := partitionGateRequest(
			[]*types.Group{pgFlat("segment"), pgFan("brand")},
			[]*types.Group{pgFlat("wave")},
			types.OverlayScopeRow,
			`{"n_source":"n_within_distinct","n_within_depth":0}`,
		)
		req.Overlays[0].Kind = types.OverlayKind(kind)
		p := NewProcessorWithExtensions(schema, exts)
		var err error
		if fused {
			_, err = p.RunCrosstabFused(context.Background(), req, NewSliceIterator(recs))
		} else {
			_, err = p.RunCrosstab(context.Background(), req, recs)
		}
		return hiddenOutcome(t, nil, err)
	}
	for _, fused := range []bool{false, true} {
		name := map[bool]string{false: "buffered", true: "fused"}[fused]
		t.Run(name, func(t *testing.T) {
			assertHiddenParity(t, hidden, neverOverlay,
				run(hidden, scoped, fused), run(neverOverlay, scoped, fused), run(hidden, nil, fused))
		})
	}
}

// TestHiddenOverlayRoute_ComposePanelSlabGate: the Compose panel slab
// partition gate fires for a registered panel kind over a fan-out row
// axis; a never-registered kind skips it and folds to the stub layer —
// and so must a hidden one.
func TestHiddenOverlayRoute_ComposePanelSlabGate(t *testing.T) {
	fanOut := []types.GroupType{types.GROUP_CATEGORY, types.GROUP_SET_PER_ELEMENT}
	params := map[string]any{"n_source": types.PanelNSourceRowMarginValueWithin, "n_within_depth": 0}
	run := func(kind string, exts *ExtensionRegistry) string {
		ref, target := panelTwoDimSlots()
		spec := composeSpecMultiTargetPropZPanel([]string{"t1"}, nil)
		spec.Kind = types.OverlayKind(kind)
		spec.Params = params
		layers, _, err := ApplyComposeOverlaysWithRequests(
			[]types.ComposeOverlaySpec{spec},
			[]*types.Response{ref, target},
			[]string{"baseline", "t1"},
			[]*types.Request{panelPartitionRequest("baseline", fanOut...), panelPartitionRequest("t1", fanOut...)},
			exts)
		return hiddenOutcome(t, layers, err)
	}
	hidden := string(types.OverlayKindPropZPanel)
	scoped := (*ExtensionRegistry)(nil).WithHidden(hiddenSet(hidden))
	assertHiddenParity(t, hidden, neverOverlay, run(hidden, scoped), run(neverOverlay, scoped), run(hidden, nil))
}
