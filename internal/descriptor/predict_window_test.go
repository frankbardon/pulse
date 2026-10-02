package descriptor

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// windowTestSchema returns a schema rich enough to drive predict-window tests:
// numeric (revenue), date (ts), categorical (region), packed_bool (flag),
// datetime (at), decimal128 (amt) and a non-orderable set_u8 (tags).
func windowTestSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "revenue", Type: encoding.FieldTypeF64, Description: "Daily revenue in USD"},
			{Name: "ts", Type: encoding.FieldTypeDate, Description: "Date of observation row"},
			{Name: "region", Type: encoding.FieldTypeCategoricalU8, Description: "Region code for tenant", Dictionary: makeDictionary(t, "us", "eu", "apac")},
			{Name: "flag", Type: encoding.FieldTypePackedBool, Description: "Bit-packed boolean indicator"},
			{Name: "at", Type: encoding.FieldTypeDateTime, Description: "Event timestamp in epoch seconds"},
			{Name: "amt", Type: encoding.FieldTypeDecimal128, Precision: 18, Scale: 2, Description: "Exact amount in USD cents"},
			{Name: "tags", Type: encoding.FieldTypeSetU8, Description: "Selected tag memberships", Dictionary: makeDictionary(t, "a", "b")},
		},
	}
}

// envHasErrorCode reports whether env.Errors contains an entry with the given code.
func envHasErrorCode(env *descriptor.Envelope, code errors.Code) bool {
	for _, e := range env.Errors {
		if e.Code == string(code) {
			return true
		}
	}
	return false
}

// envHasErrorContaining reports whether any error message contains substring.
func envHasErrorContaining(env *descriptor.Envelope, substr string) bool {
	for _, e := range env.Errors {
		if strings.Contains(e.Message, substr) {
			return true
		}
	}
	return false
}

func ptrInt(v int) *int { return &v }

func TestPredictWindow_ValidLag(t *testing.T) {
	schema := windowTestSchema(t)
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Windows: []*types.Window{
			{
				Type:    types.WIN_LAG,
				Field:   "revenue",
				OrderBy: []types.OrderKey{{Field: "ts"}},
			},
		},
	}

	env := predictFromBytes(data, req, nil)
	for _, e := range env.Errors {
		if e.Code == string(errors.PULSE_WINDOW_INVALID) {
			t.Errorf("unexpected window error: %+v", e)
		}
	}
}

func TestPredictWindow_UnknownType(t *testing.T) {
	schema := windowTestSchema(t)
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Windows: []*types.Window{
			{
				Type:    types.WindowType("WIN_BOGUS"),
				Field:   "revenue",
				OrderBy: []types.OrderKey{{Field: "ts"}},
			},
		},
	}

	env := predictFromBytes(data, req, nil)
	if !envHasErrorCode(env, errors.PULSE_WINDOW_INVALID) {
		t.Fatalf("expected PULSE_WINDOW_INVALID, got %+v", env.Errors)
	}
}

func TestPredictWindow_MissingOrderBy(t *testing.T) {
	schema := windowTestSchema(t)
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Windows: []*types.Window{
			{Type: types.WIN_LAG, Field: "revenue"},
		},
	}

	env := predictFromBytes(data, req, nil)
	if !envHasErrorContaining(env, "order_by is required") {
		t.Fatalf("expected order_by-required error, got %+v", env.Errors)
	}
}

func TestPredictWindow_OrderByUnknownField(t *testing.T) {
	schema := windowTestSchema(t)
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Windows: []*types.Window{
			{Type: types.WIN_RANK, OrderBy: []types.OrderKey{{Field: "nope"}}},
		},
	}

	env := predictFromBytes(data, req, nil)
	if !envHasErrorContaining(env, "order_by field nope does not exist") {
		t.Fatalf("expected order_by unknown field error, got %+v", env.Errors)
	}
}

func TestPredictWindow_OrderByNonOrderable(t *testing.T) {
	schema := windowTestSchema(t)
	data := buildTestPulseFile(t, schema)

	cases := []struct {
		name  string
		field string
	}{
		{"set_u8", "tags"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := &types.Request{
				Windows: []*types.Window{
					{Type: types.WIN_RANK, OrderBy: []types.OrderKey{{Field: tc.field}}},
				},
			}
			env := predictFromBytes(data, req, nil)
			if !envHasErrorContaining(env, "is not orderable") {
				t.Fatalf("expected non-orderable error for %s, got %+v", tc.field, env.Errors)
			}
		})
	}
}

// TestPredictWindow_OrderByOrderableAccepted: every type the window
// comparator orders at runtime — categorical by dictionary label,
// packed_bool as 0/1, datetime by signed epoch seconds, decimal128 by
// value — is accepted by predict (IsOrderableType).
func TestPredictWindow_OrderByOrderableAccepted(t *testing.T) {
	schema := windowTestSchema(t)
	data := buildTestPulseFile(t, schema)
	for _, field := range []string{"revenue", "ts", "region", "flag", "at", "amt"} {
		req := &types.Request{
			Windows: []*types.Window{
				{Type: types.WIN_RANK, OrderBy: []types.OrderKey{{Field: field}}},
			},
		}
		if env := predictFromBytes(data, req, nil); envHasErrorContaining(env, "is not orderable") {
			t.Fatalf("%s order_by refused: %+v", field, env.Errors)
		}
	}
}

// TestIsOrderableType_RefusesExactlySets pins the rule over every
// registered field type (type bytes 0..19): only the six set_* rungs
// are refused.
func TestIsOrderableType_RefusesExactlySets(t *testing.T) {
	refused := map[string]bool{"set_u8": true, "set_u16": true, "set_u32": true, "set_u64": true, "set_u128": true, "set_u256": true}
	n := 0
	for b := 0; b < 256; b++ {
		ft := encoding.FieldType(b)
		if !ft.IsKnown() {
			continue
		}
		n++
		if got, want := IsOrderableType(ft), !refused[ft.String()]; got != want {
			t.Errorf("IsOrderableType(%s) = %v, want %v", ft, got, want)
		}
	}
	if n != 20 {
		t.Fatalf("walked %d known field types, want 20 — extend the table", n)
	}
}

func TestPredictWindow_PartitionByUnknownField(t *testing.T) {
	schema := windowTestSchema(t)
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Windows: []*types.Window{
			{
				Type:        types.WIN_LAG,
				Field:       "revenue",
				PartitionBy: []string{"nope"},
				OrderBy:     []types.OrderKey{{Field: "ts"}},
			},
		},
	}
	env := predictFromBytes(data, req, nil)
	if !envHasErrorContaining(env, "partition_by field nope") {
		t.Fatalf("expected partition_by unknown field error, got %+v", env.Errors)
	}
}

func TestPredictWindow_FrameOnNonFrameOp(t *testing.T) {
	schema := windowTestSchema(t)
	data := buildTestPulseFile(t, schema)

	for _, wt := range []types.WindowType{
		types.WIN_LAG, types.WIN_LEAD, types.WIN_ROW_NUMBER, types.WIN_RANK,
		types.WIN_DENSE_RANK, types.WIN_PCT_CHANGE,
	} {
		t.Run(string(wt), func(t *testing.T) {
			w := &types.Window{
				Type:    wt,
				Field:   "revenue",
				OrderBy: []types.OrderKey{{Field: "ts"}},
				Frame:   &types.FrameSpec{Mode: "rows", Preceding: ptrInt(1)},
			}
			if wt == types.WIN_ROW_NUMBER || wt == types.WIN_RANK || wt == types.WIN_DENSE_RANK {
				w.Field = ""
			}
			req := &types.Request{Windows: []*types.Window{w}}
			env := predictFromBytes(data, req, nil)
			if !envHasErrorContaining(env, "frame is not allowed") {
				t.Fatalf("expected frame-not-allowed error for %s, got %+v", wt, env.Errors)
			}
		})
	}
}

func TestPredictWindow_FrameMissingOnFrameOp(t *testing.T) {
	schema := windowTestSchema(t)
	data := buildTestPulseFile(t, schema)

	for _, wt := range []types.WindowType{
		types.WIN_RUNNING_SUM, types.WIN_RUNNING_AVG, types.WIN_MOVING_AVG, types.WIN_EWMA,
	} {
		t.Run(string(wt), func(t *testing.T) {
			w := &types.Window{
				Type:    wt,
				Field:   "revenue",
				OrderBy: []types.OrderKey{{Field: "ts"}},
			}
			if wt == types.WIN_EWMA {
				w.Params = json.RawMessage(`{"alpha": 0.5}`)
			}
			req := &types.Request{Windows: []*types.Window{w}}
			env := predictFromBytes(data, req, nil)
			if !envHasErrorContaining(env, "frame is required") {
				t.Fatalf("expected frame-required error for %s, got %+v", wt, env.Errors)
			}
		})
	}
}

func TestPredictWindow_FrameModeNotRows(t *testing.T) {
	schema := windowTestSchema(t)
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Windows: []*types.Window{
			{
				Type:    types.WIN_RUNNING_SUM,
				Field:   "revenue",
				OrderBy: []types.OrderKey{{Field: "ts"}},
				Frame:   &types.FrameSpec{Mode: "range", Preceding: ptrInt(1)},
			},
		},
	}
	env := predictFromBytes(data, req, nil)
	if !envHasErrorContaining(env, `frame.mode must be "rows"`) {
		t.Fatalf("expected mode!=rows error, got %+v", env.Errors)
	}
}

func TestPredictWindow_MovingAvgUnboundedFrame(t *testing.T) {
	schema := windowTestSchema(t)
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Windows: []*types.Window{
			{
				Type:    types.WIN_MOVING_AVG,
				Field:   "revenue",
				OrderBy: []types.OrderKey{{Field: "ts"}},
				Frame:   &types.FrameSpec{Mode: "rows", Preceding: ptrInt(3)}, // missing Following
			},
		},
	}
	env := predictFromBytes(data, req, nil)
	if !envHasErrorContaining(env, "MOVING_AVG") {
		t.Fatalf("expected MOVING_AVG bounded-frame error, got %+v", env.Errors)
	}
}

func TestPredictWindow_EwmaAlphaBounds(t *testing.T) {
	schema := windowTestSchema(t)
	data := buildTestPulseFile(t, schema)

	cases := []struct {
		name   string
		params json.RawMessage
		expect string
	}{
		{"missing alpha", json.RawMessage(`{}`), "alpha is required"},
		{"alpha = 0", json.RawMessage(`{"alpha": 0}`), "alpha must be in (0, 1]"},
		{"alpha > 1", json.RawMessage(`{"alpha": 1.5}`), "alpha must be in (0, 1]"},
		{"alpha < 0", json.RawMessage(`{"alpha": -0.1}`), "alpha must be in (0, 1]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := &types.Request{
				Windows: []*types.Window{
					{
						Type:    types.WIN_EWMA,
						Field:   "revenue",
						OrderBy: []types.OrderKey{{Field: "ts"}},
						Frame:   &types.FrameSpec{Mode: "rows"},
						Params:  tc.params,
					},
				},
			}
			env := predictFromBytes(data, req, nil)
			if !envHasErrorContaining(env, tc.expect) {
				t.Fatalf("expected %q, got %+v", tc.expect, env.Errors)
			}
		})
	}
}

func TestPredictWindow_NonNumericField(t *testing.T) {
	schema := windowTestSchema(t)
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Windows: []*types.Window{
			{
				Type:    types.WIN_LAG,
				Field:   "region",
				OrderBy: []types.OrderKey{{Field: "ts"}},
			},
		},
	}
	env := predictFromBytes(data, req, nil)
	if !envHasErrorContaining(env, "must be numeric") {
		t.Fatalf("expected non-numeric field error, got %+v", env.Errors)
	}
}

func TestPredictWindow_MissingField(t *testing.T) {
	schema := windowTestSchema(t)
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Windows: []*types.Window{
			{
				Type:    types.WIN_LAG,
				OrderBy: []types.OrderKey{{Field: "ts"}},
			},
		},
	}
	env := predictFromBytes(data, req, nil)
	// Missing field uses SERVICE_VALIDATION (consistency with existing validators).
	if !envHasErrorContaining(env, "field is required") {
		t.Fatalf("expected field-required error, got %+v", env.Errors)
	}
}

func TestPredictWindow_LabelCollisionRefused(t *testing.T) {
	schema := windowTestSchema(t)
	data := buildTestPulseFile(t, schema)

	// Both an aggregation and a window output the same label.
	req := &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_AVERAGE, Field: "revenue", Label: "x"},
		},
		Windows: []*types.Window{
			{
				Type:    types.WIN_LAG,
				Field:   "revenue",
				Label:   "x",
				OrderBy: []types.OrderKey{{Field: "ts"}},
			},
		},
	}
	// A collision is the shared field rule's shadow REFUSAL (an error,
	// which the runtime raises too), no longer a predict-only warning.
	env := predictFromBytes(data, req, nil)
	if !envHasErrorContaining(env, "window[0] label x shadows an existing column") {
		t.Fatalf("expected label-shadow error, got warnings=%+v errors=%+v", env.Warnings, env.Errors)
	}
	for _, w := range env.Warnings {
		if strings.Contains(w.Message, "collides") {
			t.Fatalf("stale collision warning alongside the refusal: %+v", w)
		}
	}
}

func TestPredictWindow_RankWithoutFrameValid(t *testing.T) {
	schema := windowTestSchema(t)
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Windows: []*types.Window{
			{
				Type:    types.WIN_RANK,
				OrderBy: []types.OrderKey{{Field: "revenue", Desc: true}},
			},
		},
	}
	env := predictFromBytes(data, req, nil)
	for _, e := range env.Errors {
		if e.Code == string(errors.PULSE_WINDOW_INVALID) {
			t.Fatalf("unexpected error: %+v", e)
		}
	}
}

func TestPredictWindow_LagWithOffsetParams(t *testing.T) {
	schema := windowTestSchema(t)
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Windows: []*types.Window{
			{
				Type:    types.WIN_LAG,
				Field:   "revenue",
				OrderBy: []types.OrderKey{{Field: "ts"}},
				Params:  json.RawMessage(`{"offset": -1}`),
			},
		},
	}
	env := predictFromBytes(data, req, nil)
	if !envHasErrorContaining(env, "params.offset must be >= 0") {
		t.Fatalf("expected negative offset error, got %+v", env.Errors)
	}
}

func TestPredictSort_SchemaField(t *testing.T) {
	schema := windowTestSchema(t)
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Sort: []types.OrderKey{{Field: "ts"}},
	}
	env := predictFromBytes(data, req, nil)
	for _, e := range env.Errors {
		if strings.Contains(e.Message, "sort[") {
			t.Errorf("unexpected sort error: %+v", e)
		}
	}
}

func TestPredictSort_UnknownField(t *testing.T) {
	schema := windowTestSchema(t)
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Sort: []types.OrderKey{{Field: "nope"}},
	}
	env := predictFromBytes(data, req, nil)
	if !envHasErrorContaining(env, "is not produced by the pipeline") {
		t.Fatalf("expected unknown-field error, got %+v", env.Errors)
	}
}

func TestPredictSort_AggregationLabel(t *testing.T) {
	schema := windowTestSchema(t)
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_AVERAGE, Field: "revenue", Label: "avg_rev"},
		},
		Groups: []*types.Group{
			{Type: types.GROUP_CATEGORY, Field: "region"},
		},
		Sort: []types.OrderKey{{Field: "avg_rev", Desc: true}},
	}
	env := predictFromBytes(data, req, nil)
	for _, e := range env.Errors {
		if strings.Contains(e.Message, "sort[") {
			t.Errorf("unexpected sort error: %+v", e)
		}
	}
}

func TestPredictSort_WindowLabel(t *testing.T) {
	schema := windowTestSchema(t)
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Windows: []*types.Window{
			{Type: types.WIN_LAG, Field: "revenue", Label: "rev_lag", OrderBy: []types.OrderKey{{Field: "ts"}}},
		},
		Sort: []types.OrderKey{{Field: "rev_lag"}},
	}
	env := predictFromBytes(data, req, nil)
	for _, e := range env.Errors {
		if strings.Contains(e.Message, "sort[") {
			t.Errorf("unexpected sort error: %+v", e)
		}
	}
}

func TestPredictSort_MissingFieldName(t *testing.T) {
	schema := windowTestSchema(t)
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Sort: []types.OrderKey{{Field: ""}},
	}
	env := predictFromBytes(data, req, nil)
	if !envHasErrorContaining(env, "field is required") {
		t.Fatalf("expected field-required error, got %+v", env.Errors)
	}
}

// TestPredictAttrRank_MigrationHint verifies that submitting an ATTR_RANK
// request returns a SERVICE_VALIDATION error with replacement=WIN_RANK
// in the details payload.
func TestPredictAttrRank_MigrationHint(t *testing.T) {
	schema := windowTestSchema(t)
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Attributes: []*types.Attribute{
			{Type: types.AttributeType("ATTR_RANK"), Field: "revenue"},
		},
	}
	env := predictFromBytes(data, req, nil)
	if !envHasErrorContaining(env, "ATTR_RANK was removed") {
		t.Fatalf("expected migration hint, got %+v", env.Errors)
	}
	// Locate the entry and verify replacement key.
	var found bool
	for _, e := range env.Errors {
		if strings.Contains(e.Message, "ATTR_RANK was removed") {
			if rep, ok := e.Details["replacement"]; !ok || rep != "WIN_RANK" {
				t.Errorf("details.replacement = %v, want WIN_RANK", rep)
			}
			found = true
		}
	}
	if !found {
		t.Errorf("migration hint not found in errors: %+v", env.Errors)
	}
}

func TestPredictWindow_PctChangePeriodsParams(t *testing.T) {
	schema := windowTestSchema(t)
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Windows: []*types.Window{
			{
				Type:    types.WIN_PCT_CHANGE,
				Field:   "revenue",
				OrderBy: []types.OrderKey{{Field: "ts"}},
				Params:  json.RawMessage(`{"periods": 0}`),
			},
		},
	}
	env := predictFromBytes(data, req, nil)
	if !envHasErrorContaining(env, "params.periods must be >= 1") {
		t.Fatalf("expected periods bound error, got %+v", env.Errors)
	}
}

// --- WIN_DELTA -------------------------------------------------------------
//
// WIN_DELTA shares WIN_PCT_CHANGE's predict contract: no frame, a required
// numeric Field, and periods >= 1. Each of the three maps in predict_window.go
// is a separate registration, so each is asserted separately — missing one is a
// silent predict gap, not a build failure.

func TestPredictWindow_DeltaFrameRejected(t *testing.T) {
	schema := windowTestSchema(t)
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Windows: []*types.Window{
			{
				Type:    types.WIN_DELTA,
				Field:   "revenue",
				OrderBy: []types.OrderKey{{Field: "ts"}},
				Frame:   &types.FrameSpec{Mode: "rows", Preceding: ptrInt(1)},
			},
		},
	}
	env := predictFromBytes(data, req, nil)
	if !envHasErrorContaining(env, "frame is not allowed") {
		t.Fatalf("expected frame-not-allowed error for WIN_DELTA, got %+v", env.Errors)
	}
}

func TestPredictWindow_DeltaFieldRequired(t *testing.T) {
	schema := windowTestSchema(t)
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Windows: []*types.Window{
			{
				Type:    types.WIN_DELTA,
				OrderBy: []types.OrderKey{{Field: "ts"}},
			},
		},
	}
	env := predictFromBytes(data, req, nil)
	if !envHasErrorContaining(env, "field is required") {
		t.Fatalf("expected field-required error for WIN_DELTA, got %+v", env.Errors)
	}
}

func TestPredictWindow_DeltaNumericFieldRequired(t *testing.T) {
	schema := windowTestSchema(t)
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Windows: []*types.Window{
			{
				Type:    types.WIN_DELTA,
				Field:   "region",
				OrderBy: []types.OrderKey{{Field: "ts"}},
			},
		},
	}
	env := predictFromBytes(data, req, nil)
	if !envHasErrorContaining(env, "must be numeric") {
		t.Fatalf("expected non-numeric field error for WIN_DELTA, got %+v", env.Errors)
	}
}

func TestPredictWindow_DeltaPeriodsParams(t *testing.T) {
	schema := windowTestSchema(t)
	data := buildTestPulseFile(t, schema)

	for _, tc := range []struct {
		name    string
		params  string
		wantErr string
	}{
		{name: "zero", params: `{"periods": 0}`, wantErr: "params.periods must be >= 1"},
		{name: "negative", params: `{"periods": -2}`, wantErr: "params.periods must be >= 1"},
		{name: "malformed", params: `{"periods": "two"}`, wantErr: "malformed params"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := &types.Request{
				Windows: []*types.Window{
					{
						Type:    types.WIN_DELTA,
						Field:   "revenue",
						OrderBy: []types.OrderKey{{Field: "ts"}},
						Params:  json.RawMessage(tc.params),
					},
				},
			}
			env := predictFromBytes(data, req, nil)
			if !envHasErrorContaining(env, tc.wantErr) {
				t.Fatalf("expected %q, got %+v", tc.wantErr, env.Errors)
			}
			// The message must name WIN_DELTA, not the operator it shares a case with.
			if !envHasErrorContaining(env, "(WIN_DELTA)") {
				t.Fatalf("expected the error to name WIN_DELTA, got %+v", env.Errors)
			}
		})
	}
}

func TestPredictWindow_DeltaValid(t *testing.T) {
	schema := windowTestSchema(t)
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Windows: []*types.Window{
			{
				Type:    types.WIN_DELTA,
				Field:   "revenue",
				OrderBy: []types.OrderKey{{Field: "ts"}},
				Params:  json.RawMessage(`{"periods": 2}`),
			},
		},
	}
	env := predictFromBytes(data, req, nil)
	if len(env.Errors) != 0 {
		t.Fatalf("expected a clean predict for WIN_DELTA, got %+v", env.Errors)
	}
}
