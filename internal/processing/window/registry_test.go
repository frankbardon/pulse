package window

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/windowparams"
	"github.com/frankbardon/pulse/types"
)

func TestApply_NoWindowsNoOp(t *testing.T) {
	t.Parallel()

	rows := []map[string]any{{"x": 1.0}, {"x": 2.0}}
	if err := Apply(context.Background(), rows, nil); err != nil {
		t.Fatalf("Apply with no windows returned error: %v", err)
	}
	if len(rows[0]) != 1 || len(rows[1]) != 1 {
		t.Errorf("rows mutated unexpectedly: %+v", rows)
	}
}

func TestApply_EmptyRowsNoOp(t *testing.T) {
	t.Parallel()

	rows := []map[string]any{}
	w := []*types.Window{
		{Type: types.WIN_LAG, Field: "x", OrderBy: []types.OrderKey{{Field: "x"}}},
	}
	if err := Apply(context.Background(), rows, w); err != nil {
		t.Fatalf("Apply with empty rows returned error: %v", err)
	}
}

func TestApply_UnknownTypeError(t *testing.T) {
	t.Parallel()

	rows := []map[string]any{{"x": 1.0}}
	w := []*types.Window{
		{Type: types.WindowType("WIN_NOPE"), Field: "x", OrderBy: []types.OrderKey{{Field: "x"}}},
	}
	err := Apply(context.Background(), rows, w)
	if err == nil {
		t.Fatal("expected error for unknown window type")
	}
	// Predict's refusal: PULSE_WINDOW_INVALID, the slot's index and type.
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_WINDOW_INVALID ||
		ce.Message != "window[0]: unknown window type WIN_NOPE" ||
		ce.Details["window_index"] != 0 || ce.Details["type"] != "WIN_NOPE" {
		t.Fatalf("err = %#v, want PULSE_WINDOW_INVALID window[0] WIN_NOPE", err)
	}
}

func TestWindowLabel(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		w    *types.Window
		want string
	}{
		{"explicit label", &types.Window{Type: types.WIN_LAG, Field: "rev", Label: "lag1"}, "lag1"},
		{"with field", &types.Window{Type: types.WIN_LAG, Field: "rev"}, "WIN_LAG_rev"},
		{"no field", &types.Window{Type: types.WIN_RANK}, "WIN_RANK"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := windowLabel(tc.w)
			if got != tc.want {
				t.Errorf("windowLabel = %q, want %q", got, tc.want)
			}
			// FR-8: the engine names a window by the one derivation
			// predict and the computed-column model read.
			if wp := windowparams.Label(tc.w); wp != got {
				t.Errorf("windowparams.Label = %q, engine windowLabel %q", wp, got)
			}
		})
	}
}

// TestFactories_RefuseSlotConfigAtConstruction: every registered
// factory judges the slot through the reading predict shares
// (internal/windowparams) before it builds a computer — a missing
// order_by, a missing value field, a frame on an operator that takes
// none and a missing frame on one that needs it are refused at
// construction with PULSE_WINDOW_INVALID and the "TYPE: rule" sentence,
// whatever the caller skipped.
func TestFactories_RefuseSlotConfigAtConstruction(t *testing.T) {
	t.Parallel()

	params := map[types.WindowType]json.RawMessage{
		types.WIN_EWMA:            json.RawMessage(`{"alpha": 0.5}`),
		types.WIN_DISTRIBUTED_LAG: json.RawMessage(`{"kernel": "geometric", "decay": 0.5, "max_lag": 3}`),
		types.WIN_SHARE:           json.RawMessage(`{"cumulative": true}`),
		types.WIN_LIFT:            json.RawMessage(`{"base": "y", "cumulative": true}`),
	}
	one, zero := 1, 0
	frame := func() *types.FrameSpec { return &types.FrameSpec{Mode: "rows", Preceding: &one, Following: &zero} }
	valid := func(typ types.WindowType) *types.Window {
		w := &types.Window{Type: typ, OrderBy: []types.OrderKey{{Field: "t"}}, Params: params[typ]}
		if windowparams.FieldRequired(typ) {
			w.Field = "x"
		}
		if windowparams.FrameRequired(typ) {
			w.Frame = frame()
		}
		return w
	}
	if len(RegisteredTypes()) != len(types.AllWindowTypes()) {
		t.Fatalf("registered %d window types, AllWindowTypes lists %d", len(RegisteredTypes()), len(types.AllWindowTypes()))
	}
	for _, typ := range types.AllWindowTypes() {
		factory, ok := Lookup(typ)
		if !ok {
			t.Errorf("%s: not registered", typ)
			continue
		}
		if _, err := factory(valid(typ), WindowOptions{}); err != nil {
			t.Errorf("%s: valid slot refused: %v", typ, err)
		}
		bad := map[string]*types.Window{}
		w := valid(typ)
		w.OrderBy = nil
		bad["no order_by"] = w
		if windowparams.FieldRequired(typ) {
			w = valid(typ)
			w.Field = ""
			bad["no field"] = w
		}
		if windowparams.FieldForbidden(typ) {
			w = valid(typ)
			w.Field = "x"
			bad["field given"] = w
		}
		w = valid(typ)
		if windowparams.FrameRequired(typ) {
			w.Frame = nil
			bad["no frame"] = w
		} else {
			w.Frame = frame()
			bad["frame given"] = w
		}
		for name, w := range bad {
			_, err := factory(w, WindowOptions{})
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_WINDOW_INVALID || !strings.HasPrefix(ce.Message, string(typ)+": ") {
				t.Errorf("%s %s: err = %v, want PULSE_WINDOW_INVALID %q…", typ, name, err, string(typ)+": ")
			}
		}
	}
}
