package windowparams

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func messages(refs []*Refusal) []string {
	out := make([]string, len(refs))
	for i, r := range refs {
		out[i] = r.Message
	}
	return out
}

// TestCheck_JudgingOrder: every broken rule is returned, in the order
// the package documents; First is the head (the engine's refusal).
func TestCheck_JudgingOrder(t *testing.T) {
	t.Parallel()

	neg := -1
	w := &types.Window{Type: types.WIN_MOVING_AVG,
		Frame:  &types.FrameSpec{Mode: "range", Preceding: &neg},
		Params: nil}
	want := []string{
		"order_by is required (at least one key)",
		"field is required",
		`frame.mode must be "rows" (got "range")`,
		"frame must have bounded preceding AND following",
		"frame.preceding must be >= 0",
	}
	got := messages(Check(w))
	if len(got) != len(want) {
		t.Fatalf("Check = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("refusal %d = %q, want %q", i, got[i], want[i])
		}
	}
	ce := First(w).Coded()
	if ce.Code != errors.PULSE_WINDOW_INVALID || ce.Message != "WIN_MOVING_AVG: "+want[0] {
		t.Errorf("First coded = %s %q", ce.Code, ce.Message)
	}
	if First(&types.Window{Type: types.WIN_ROW_NUMBER, OrderBy: []types.OrderKey{{Field: "t"}}}) != nil {
		t.Error("a valid row number slot was refused")
	}
	if Check(nil) != nil {
		t.Error("a nil window was refused")
	}
}

// TestCheck_ExtensionTypeNeutralRules: an untabled type is judged on
// order_by, its keys and a given frame only — never on field or frame
// presence.
func TestCheck_ExtensionTypeNeutralRules(t *testing.T) {
	t.Parallel()

	ext := types.WindowType("WIN_ACME_X")
	if refs := Check(&types.Window{Type: ext, OrderBy: []types.OrderKey{{Field: "t"}}}); refs != nil {
		t.Errorf("valid extension slot refused: %q", messages(refs))
	}
	got := messages(Check(&types.Window{Type: ext, OrderBy: []types.OrderKey{{Field: ""}}, Frame: &types.FrameSpec{Mode: "rows"}}))
	if len(got) != 1 || got[0] != "order_by[0] missing field" {
		t.Errorf("Check = %q", got)
	}
}

// TestLagAndPeriods: defaults, values and refusals of the two shared
// params shapes.
func TestLagAndPeriods(t *testing.T) {
	t.Parallel()

	lag := func(raw string) *types.Window {
		return &types.Window{Type: types.WIN_LAG, Params: json.RawMessage(raw)}
	}
	if s, ref := Lag(&types.Window{Type: types.WIN_LAG}); ref != nil || s.Offset != 1 || s.HasDefault {
		t.Errorf("default lag = %+v, %v", s, ref)
	}
	if s, ref := Lag(lag(`{"offset": 3, "default": 0}`)); ref != nil || s.Offset != 3 || !s.HasDefault || s.Default != 0.0 {
		t.Errorf("lag = %+v, %v", s, ref)
	}
	if s, ref := Lag(lag(`{"default": null}`)); ref != nil || s.HasDefault {
		t.Errorf("null default = %+v, %v", s, ref)
	}
	if _, ref := Lag(lag(`{"offset": -1}`)); ref == nil || ref.Message != "params.offset must be >= 0" {
		t.Errorf("negative offset = %v", ref)
	}
	if _, ref := Lag(lag(`{"offset": "x"}`)); ref == nil {
		t.Error("malformed lag params accepted")
	}
	per := func(raw string) *types.Window {
		return &types.Window{Type: types.WIN_DELTA, Params: json.RawMessage(raw)}
	}
	if n, ref := Periods(&types.Window{Type: types.WIN_DELTA}); ref != nil || n != 1 {
		t.Errorf("default periods = %d, %v", n, ref)
	}
	if n, ref := Periods(per(`{}`)); ref != nil || n != 1 {
		t.Errorf("empty periods = %d, %v", n, ref)
	}
	if n, ref := Periods(per(`{"periods": 4}`)); ref != nil || n != 4 {
		t.Errorf("periods = %d, %v", n, ref)
	}
	if _, ref := Periods(per(`{"periods": 0}`)); ref == nil || ref.Message != "params.periods must be >= 1" || ref.Error() != ref.Message {
		t.Errorf("zero periods = %v", ref)
	}
	if _, ref := Periods(per(`{"periods": "x"}`)); ref == nil {
		t.Error("malformed periods accepted")
	}
}

// TestTables: the frame matrix covers every built-in window exactly
// once (required, rejected, or neither only for none), and the field
// table names the value-bearing operators.
func TestTables(t *testing.T) {
	t.Parallel()

	for _, typ := range types.AllWindowTypes() {
		if FrameRequired(typ) == FrameRejected(typ) {
			t.Errorf("%s: frame required %v, rejected %v — every built-in takes a frame or refuses one", typ, FrameRequired(typ), FrameRejected(typ))
		}
	}
	for _, typ := range types.AllWindowTypes() {
		if FieldRequired(typ) == FieldForbidden(typ) {
			t.Errorf("%s: field required %v, forbidden %v — every built-in reads a field or refuses one", typ, FieldRequired(typ), FieldForbidden(typ))
		}
	}
	got := messages(Check(&types.Window{Type: types.WIN_RANK, Field: "x", OrderBy: []types.OrderKey{{Field: "t"}}}))
	if len(got) != 1 || got[0] != "field is not allowed: this operator reads only its order_by keys" {
		t.Errorf("rank with a field: Check = %q", got)
	}
}

// TestUnknownType_RenameHint: only a retired name carries the rename
// hint, and only when the instance offers its replacement.
func TestUnknownType_RenameHint(t *testing.T) {
	t.Parallel()

	msg, d := UnknownType(2, "WIN_ADSTOCK", nil)
	if msg != "window[2]: unknown window type WIN_ADSTOCK (renamed to WIN_DISTRIBUTED_LAG)" || d["renamed_to"] != "WIN_DISTRIBUTED_LAG" || d["window_index"] != 2 || d["type"] != "WIN_ADSTOCK" {
		t.Errorf("retired: %q %v", msg, d)
	}
	msg, d = UnknownType(0, "WIN_ADSTOCK", func(types.WindowType) bool { return false })
	if msg != "window[0]: unknown window type WIN_ADSTOCK" || d["renamed_to"] != nil {
		t.Errorf("retired, replacement hidden: %q %v", msg, d)
	}
	msg, d = UnknownType(0, "WIN_NOPE", nil)
	if msg != "window[0]: unknown window type WIN_NOPE" || len(d) != 2 {
		t.Errorf("never registered: %q %v", msg, d)
	}
}
