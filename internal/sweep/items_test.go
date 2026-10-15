package sweep

import (
	"encoding/json"
	stderrors "errors"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func decodeRequest(t *testing.T, raw string) *types.Request {
	t.Helper()
	var r types.Request
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		t.Fatal(err)
	}
	return &r
}

func mustExpandItems(t *testing.T, r *types.Request) (*types.Request, []FanOut) {
	t.Helper()
	out, fan, err := ExpandItems(r, nil)
	if err != nil {
		t.Fatalf("ExpandItems: %v", err)
	}
	return out, fan
}

func requireForEachInvalid(t *testing.T, err error, reason, field string) *errors.CodedError {
	t.Helper()
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) {
		t.Fatalf("want PULSE_FOR_EACH_INVALID/%s, got %v", reason, err)
	}
	if ce.Code != errors.PULSE_FOR_EACH_INVALID || ce.Details["reason"] != reason {
		t.Fatalf("got %s reason=%v (%s), want PULSE_FOR_EACH_INVALID/%s", ce.Code, ce.Details["reason"], ce.Message, reason)
	}
	if field != "" && ce.Details["field"] != field {
		t.Fatalf("field = %v, want %s (%s)", ce.Details["field"], field, ce.Message)
	}
	return ce
}

func windowLabels(ws []*types.Window) []string {
	out := make([]string, len(ws))
	for i, w := range ws {
		out[i] = w.Label
	}
	return out
}

// The adstock idiom: one WIN_EWMA item fanned out over channel x decay,
// grid order (first axis slowest), `{{}}` in field and label, `$var`
// splicing the decay into params type-preserving, plain items untouched
// and kept in place.
func TestExpandItems_GridInPlace(t *testing.T) {
	r := decodeRequest(t, `{
		"cohort": {"filename": "x.pulse"},
		"windows": [
			{"type": "WIN_LAG", "field": "tv_spend_k", "label": "lag", "order_by": [{"field": "week"}]},
			{"for_each": {"axes": [{"name": "ch", "values": ["tv", "search"]}, {"name": "decay", "values": [0.3, 0.50, 0.7]}]},
			 "type": "WIN_EWMA", "field": "{{ch}}_spend_k", "label": "{{ch}}_ad{{decay}}",
			 "partition_by": ["region"], "order_by": [{"field": "week"}], "frame": {"mode": "rows"},
			 "params": {"decay": {"$var": "decay"}}},
			{"type": "WIN_LEAD", "field": "tv_spend_k", "label": "lead", "order_by": [{"field": "week"}]}
		]
	}`)
	before, _ := json.Marshal(r)
	out, fan := mustExpandItems(t, r)
	want := []string{"lag", "tv_ad0.3", "tv_ad0.50", "tv_ad0.7", "search_ad0.3", "search_ad0.50", "search_ad0.7", "lead"}
	if got := windowLabels(out.Windows); !reflect.DeepEqual(got, want) {
		t.Fatalf("labels = %v, want %v", got, want)
	}
	if out.Windows[0] != r.Windows[0] || out.Windows[7] != r.Windows[2] {
		t.Fatal("plain items must be the caller's pointers, in place")
	}
	w := out.Windows[5]
	if w.Field != "search_spend_k" || w.Type != types.WIN_EWMA || w.ForEach != nil ||
		!reflect.DeepEqual(w.PartitionBy, []string{"region"}) || w.Frame == nil || w.Frame.Mode != "rows" {
		t.Fatalf("rendered item = %+v", w)
	}
	if string(w.Params) != `{"decay":0.50}` {
		t.Fatalf("params = %s, want the number spliced as written", w.Params)
	}
	if len(fan) != 1 || fan[0].Path != "windows[1]" || fan[0].Count != 6 || fan[0].Mode != types.SweepModeGrid {
		t.Fatalf("fan-outs = %+v", fan)
	}
	after, _ := json.Marshal(r)
	if string(before) != string(after) {
		t.Fatal("ExpandItems modified its input")
	}
}

// The zip label-axis idiom: an integer label axis walked in lockstep
// with the decay axis, so `tv_ad10` carries decay 0.1.
func TestExpandItems_ZipLabelAxis(t *testing.T) {
	r := decodeRequest(t, `{"windows": [{
		"for_each": {"axes": [{"name": "d", "values": [10, 30, 50, 70]}, {"name": "decay", "values": [0.1, 0.3, 0.5, 0.7]}], "mode": "zip"},
		"type": "WIN_EWMA", "field": "tv_spend_k", "label": "tv_ad{{d}}", "order_by": [{"field": "week"}],
		"params": {"decay": {"$var": "decay"}}}]}`)
	out, fan := mustExpandItems(t, r)
	if got := windowLabels(out.Windows); !reflect.DeepEqual(got, []string{"tv_ad10", "tv_ad30", "tv_ad50", "tv_ad70"}) {
		t.Fatalf("labels = %v", got)
	}
	if string(out.Windows[2].Params) != `{"decay":0.5}` {
		t.Fatalf("params = %s", out.Windows[2].Params)
	}
	if fan[0].Mode != types.SweepModeZip || fan[0].Count != 4 {
		t.Fatalf("fan-out = %+v", fan[0])
	}
}

// Features and attributes fan out the same way; a for_each-free request
// comes back as the same pointer with no fan-outs.
func TestExpandItems_FeaturesAttributesAndPlain(t *testing.T) {
	r := decodeRequest(t, `{
		"features": [{"for_each": {"axes": [{"name": "f", "values": ["a", "b"]}]}, "type": "FEAT_LOG", "field": "{{f}}", "label": "log_{{f}}"}],
		"attributes": [{"for_each": {"axes": [{"name": "f", "values": ["a", "b", "c"]}]}, "type": "ATTR_ZSCORE", "field": "{{f}}", "label": "z_{{f}}"}]
	}`)
	out, fan := mustExpandItems(t, r)
	if len(out.Features) != 2 || out.Features[1].Label != "log_b" || len(out.Attributes) != 3 || out.Attributes[2].Field != "c" {
		t.Fatalf("features = %+v attributes = %+v", out.Features, out.Attributes)
	}
	if len(fan) != 2 || fan[0].Path != "features[0]" || fan[1].Path != "attributes[0]" {
		t.Fatalf("fan-outs = %+v", fan)
	}
	if got := DerivedCount(r); got != 5 {
		t.Fatalf("DerivedCount = %d, want 5", got)
	}

	plain := decodeRequest(t, `{"windows": [{"type": "WIN_LAG", "field": "x", "order_by": [{"field": "t"}]}]}`)
	var seen int
	got, fan, err := ExpandItems(plain, func(n int) error { seen = n; return nil })
	if err != nil || got != plain || fan != nil {
		t.Fatalf("plain request: got %p want %p, fan %v, err %v", got, plain, fan, err)
	}
	if seen != 1 {
		t.Fatalf("preflight saw %d, want 1 (plain items count too)", seen)
	}
}

// The preflight sees the expanded count (plain + expanded) before
// anything renders, and its error comes back unchanged.
func TestExpandItems_PreflightBeforeRender(t *testing.T) {
	r := decodeRequest(t, `{"windows": [
		{"type": "WIN_LAG", "field": "x", "order_by": [{"field": "t"}]},
		{"for_each": {"axes": [{"name": "n", "values": [1, 2, 3]}, {"name": "m", "values": [1, 2]}]},
		 "type": "WIN_LAG", "field": "x", "label": "l{{n}}_{{m}}", "order_by": [{"field": "t"}]}]}`)
	sentinel := stderrors.New("over")
	var seen int
	out, fan, err := ExpandItems(r, func(n int) error { seen = n; return sentinel })
	if err != sentinel || seen != 7 || out != nil || fan != nil {
		t.Fatalf("err = %v seen = %d out = %v, want the preflight error with 7 and nothing built", err, seen, out)
	}
}

// A rendered item carrying an unknown key is PULSE_REQUEST_UNKNOWN_FIELD
// (the shared strict decoder), its details.path rooted at the item and
// the combination named; a mistyped value stays item_decode.
func TestRenderItem_StrictDecode(t *testing.T) {
	p := pendingItem{
		path: "windows[3]",
		spec: &types.ForEach{Axes: []types.SweepAxis{{Name: "x", Values: []any{"a"}}}},
		body: json.RawMessage(`{"type": "WIN_LAG", "bogus": "{{x}}", "frame": {"mode": "rows", "precedng": 1}}`),
	}
	_, err := renderItem[types.Window](p, map[string]any{"x": "a"}, 0)
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_REQUEST_UNKNOWN_FIELD {
		t.Fatalf("want PULSE_REQUEST_UNKNOWN_FIELD, got %v", err)
	}
	if ce.Details["path"] != "windows[3].bogus" || ce.Details["index"] != 0 {
		t.Fatalf("details = %v", ce.Details)
	}

	p.body = json.RawMessage(`{"type": "WIN_LAG", "frame": {"mode": "rows", "precedng": 1}}`)
	_, err = renderItem[types.Window](p, map[string]any{"x": "a"}, 0)
	if !stderrors.As(err, &ce) || ce.Details["path"] != "windows[3].frame.precedng" ||
		ce.Details["suggestions"].(map[string]any)["precedng"] != "preceding" {
		t.Fatalf("nested: %v", err)
	}

	p.body = json.RawMessage(`{"type": "WIN_LAG", "field": 3}`)
	_, err = renderItem[types.Window](p, map[string]any{"x": "a"}, 0)
	requireForEachInvalid(t, err, ReasonItemDecode, "windows[3]")
}

func TestExpandItems_Faults(t *testing.T) {
	cases := []struct {
		name, body, reason, field string
	}{
		{"axes empty", `{"windows": [{"for_each": {"axes": []}, "type": "WIN_LAG", "field": "x", "order_by": []}]}`,
			ReasonAxesEmpty, "windows[0].for_each.axes"},
		{"bad name", `{"features": [{"for_each": {"axes": [{"name": "1x", "values": [1]}]}, "type": "FEAT_LOG", "field": "{{x}}"}]}`,
			ReasonAxisNameInvalid, "features[0].for_each.axes[0].name"},
		{"duplicate", `{"features": [{"for_each": {"axes": [{"name": "x", "values": [1]}, {"name": "x", "values": [2]}]}, "type": "FEAT_LOG", "field": "{{x}}"}]}`,
			ReasonAxisNameDuplicate, "features[0].for_each.axes[1].name"},
		{"values empty", `{"attributes": [{"for_each": {"axes": [{"name": "x", "values": []}]}, "type": "ATTR_ZSCORE", "field": "{{x}}"}]}`,
			ReasonValuesEmpty, "attributes[0].for_each.axes[0].values"},
		{"not scalar", `{"windows": [{"for_each": {"axes": [{"name": "x", "values": [[1]]}]}, "type": "WIN_LAG", "field": "{{x}}", "order_by": []}]}`,
			ReasonValueNotScalar, "windows[0].for_each.axes[0].values[0]"},
		{"mode", `{"windows": [{"for_each": {"axes": [{"name": "x", "values": [1]}], "mode": "cross"}, "type": "WIN_LAG", "field": "{{x}}", "order_by": []}]}`,
			ReasonModeUnknown, "windows[0].for_each.mode"},
		{"zip lengths", `{"windows": [{"for_each": {"axes": [{"name": "x", "values": [1, 2]}, {"name": "y", "values": [1]}], "mode": "zip"}, "type": "WIN_LAG", "field": "{{x}}{{y}}", "order_by": []}]}`,
			ReasonZipLengthMismatch, "windows[0].for_each.axes[1].values"},
		{"unreferenced", `{"windows": [{"for_each": {"axes": [{"name": "x", "values": [1]}, {"name": "y", "values": [1]}]}, "type": "WIN_LAG", "field": "{{x}}", "order_by": []}]}`,
			ReasonAxisUnreferenced, "windows[0].for_each.axes[1].name"},
		{"unknown placeholder", `{"windows": [{"for_each": {"axes": [{"name": "x", "values": [1]}]}, "type": "WIN_LAG", "field": "{{x}}", "label": "{{z}}", "order_by": []}]}`,
			ReasonPlaceholderUnknown, "windows[0].label"},
		{"bad syntax", `{"windows": [{"for_each": {"axes": [{"name": "x", "values": [1]}]}, "type": "WIN_LAG", "field": "{{x}}", "label": "{{", "order_by": []}]}`,
			ReasonPlaceholderInvalid, "windows[0].label"},
		{"unknown key", `{"windows": [{"for_each": {"axes": [{"name": "x", "values": [1]}]}, "type": "WIN_LAG", "field": "x", "order_by": [], "params": {"{{x}}": 1}}]}`,
			ReasonPlaceholderInvalid, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := ExpandItems(decodeRequest(t, c.body), nil)
			requireForEachInvalid(t, err, c.reason, c.field)
		})
	}
}

// A Go caller can put a mistyped value where strict decode refuses it:
// the decode fault names the unknown or mistyped key.
func TestExpandItems_StrictDecode(t *testing.T) {
	r := &types.Request{Windows: []*types.Window{{
		Type: types.WIN_EWMA, Field: "x", Label: "e{{d}}",
		Params:  json.RawMessage(`{"decay": {"$var": "d"}}`),
		ForEach: &types.ForEach{Axes: []types.SweepAxis{{Name: "d", Values: []any{0.5}}}},
	}}}
	out, _ := mustExpandItems(t, r)
	if out.Windows[0].Label != "e0.5" || string(out.Windows[0].Params) != `{"decay":0.5}` {
		t.Fatalf("Go-built item = %+v", out.Windows[0])
	}

}

// A compose sweep body carrying an item for_each is refused before the
// sweep's own placeholder scan.
func TestExpand_NestedForEachInSweep(t *testing.T) {
	c := decodeComposed(t, `{"sweep": {"axes": [{"name": "a", "values": [1]}], "request": {
		"label_unused": "{{a}}",
		"windows": [{"for_each": {"axes": [{"name": "d", "values": [1]}]}, "type": "WIN_LAG", "field": "x", "label": "{{d}}", "order_by": []}]}}}`)
	_, err := Expand(c, nil)
	requireForEachInvalid(t, err, ReasonNestedInSweep, "sweep.request.windows[0].for_each")
}
