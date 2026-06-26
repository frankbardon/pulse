package descriptor

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// windowTestSchema returns a schema rich enough to drive predict-window tests:
// numeric (revenue), date (ts), categorical (region), and a non-orderable
// packed_bool (flag).
func windowTestSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "revenue", Type: encoding.FieldTypeF64, Description: "Daily revenue in USD"},
			{Name: "ts", Type: encoding.FieldTypeDate, Description: "Date of observation row"},
			{Name: "region", Type: encoding.FieldTypeCategoricalU8, Description: "Region code for tenant", Dictionary: makeDictionary(t, "us", "eu", "apac")},
			{Name: "flag", Type: encoding.FieldTypePackedBool, Description: "Bit-packed boolean indicator"},
		},
	}
}

// envHasErrorCode reports whether env.Errors contains an entry with the given code.
func envHasErrorCode(env *Envelope, code errors.Code) bool {
	for _, e := range env.Errors {
		if e.Code == string(code) {
			return true
		}
	}
	return false
}

// envHasErrorContaining reports whether any error message contains substring.
func envHasErrorContaining(env *Envelope, substr string) bool {
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

	env := PredictFromBytes(data, req, nil)
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

	env := PredictFromBytes(data, req, nil)
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

	env := PredictFromBytes(data, req, nil)
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

	env := PredictFromBytes(data, req, nil)
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
		{"categorical", "region"},
		{"packed_bool", "flag"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := &types.Request{
				Windows: []*types.Window{
					{Type: types.WIN_RANK, OrderBy: []types.OrderKey{{Field: tc.field}}},
				},
			}
			env := PredictFromBytes(data, req, nil)
			if !envHasErrorContaining(env, "is not orderable") {
				t.Fatalf("expected non-orderable error for %s, got %+v", tc.field, env.Errors)
			}
		})
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
	env := PredictFromBytes(data, req, nil)
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
			env := PredictFromBytes(data, req, nil)
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
			env := PredictFromBytes(data, req, nil)
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
	env := PredictFromBytes(data, req, nil)
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
	env := PredictFromBytes(data, req, nil)
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
			env := PredictFromBytes(data, req, nil)
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
	env := PredictFromBytes(data, req, nil)
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
	env := PredictFromBytes(data, req, nil)
	// Missing field uses SERVICE_VALIDATION (consistency with existing validators).
	if !envHasErrorContaining(env, "field is required") {
		t.Fatalf("expected field-required error, got %+v", env.Errors)
	}
}

func TestPredictWindow_LabelCollision(t *testing.T) {
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
	env := PredictFromBytes(data, req, nil)
	found := false
	for _, w := range env.Warnings {
		if w.Code == string(errors.PULSE_WINDOW_INVALID) && strings.Contains(w.Message, "collides") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected label-collision warning, got warnings=%+v errors=%+v", env.Warnings, env.Errors)
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
	env := PredictFromBytes(data, req, nil)
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
	env := PredictFromBytes(data, req, nil)
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
	env := PredictFromBytes(data, req, nil)
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
	env := PredictFromBytes(data, req, nil)
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
	env := PredictFromBytes(data, req, nil)
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
	env := PredictFromBytes(data, req, nil)
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
	env := PredictFromBytes(data, req, nil)
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
	env := PredictFromBytes(data, req, nil)
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
	env := PredictFromBytes(data, req, nil)
	if !envHasErrorContaining(env, "params.periods must be >= 1") {
		t.Fatalf("expected periods bound error, got %+v", env.Errors)
	}
}
