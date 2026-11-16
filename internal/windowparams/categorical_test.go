package windowparams

import (
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

func catSchema() *encoding.Schema {
	d := encoding.NewDictionary()
	for _, s := range []string{"a", "b"} {
		_, _ = d.Add(s)
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "t", Type: encoding.FieldTypeU16},
		{Name: "x", Type: encoding.FieldTypeF64},
		{Name: "c", Type: encoding.FieldTypeCategoricalU8, Dictionary: d},
		{Name: "taken", Type: encoding.FieldTypeF64},
	}}
}

func copyWin(t types.WindowType, field, label, params string) *types.Window {
	w := &types.Window{Type: t, Field: field, Label: label, OrderBy: []types.OrderKey{{Field: "t"}}}
	if params != "" {
		w.Params = []byte(params)
	}
	return w
}

// TestCategoricalLags: a copy window over a dictionary categorical — or
// over an EARLIER categorical lag — is a lagged category on the source's
// type and dictionary, nullable; a numeric lag, a value window, a label
// shadowing a schema field and a forward reference are not.
func TestCategoricalLags(t *testing.T) {
	t.Parallel()

	s := catSchema()
	windows := []*types.Window{
		copyWin(types.WIN_LEAD, "lag2", "fwd", ""), // forward reference: lag2 is later
		copyWin(types.WIN_LAG, "c", "lag1", ""),
		copyWin(types.WIN_LAG, "lag1", "lag2", ""),
		copyWin(types.WIN_LAG, "x", "lagx", ""),
		copyWin(types.WIN_DELTA, "c", "d", ""),
		copyWin(types.WIN_LAG, "c", "taken", ""),
	}
	got := CategoricalLags(windows, s)
	if len(got) != 2 {
		t.Fatalf("lags %v, want lag1 and lag2", got)
	}
	for _, label := range []string{"lag1", "lag2"} {
		l, ok := got[label]
		if !ok || l.Source != "c" || l.Field.Name != label || !l.Field.Nullable ||
			l.Field.Type != encoding.FieldTypeCategoricalU8 || l.Field.Dictionary != s.Field("c").Dictionary {
			t.Fatalf("%s = %+v", label, l)
		}
	}
	if got["lag2"].Index != 2 {
		t.Fatalf("lag2 index %d, want 2", got["lag2"].Index)
	}
	if CategoricalLags(windows[3:5], s) != nil || CategoricalLags(windows, nil) != nil {
		t.Fatal("no categorical lag must be nil")
	}
}

// TestCategoricalDefaultRefusal: a categorical copy's default must be a
// dictionary label; a numeric copy's default is not judged here.
func TestCategoricalDefaultRefusal(t *testing.T) {
	t.Parallel()

	s := catSchema()
	for _, c := range []struct {
		windows []*types.Window
		i       int
		refused bool
	}{
		{[]*types.Window{copyWin(types.WIN_LAG, "c", "l", `{"default": "a"}`)}, 0, false},
		{[]*types.Window{copyWin(types.WIN_LAG, "c", "l", `{"default": null}`)}, 0, false},
		{[]*types.Window{copyWin(types.WIN_LAG, "c", "l", "")}, 0, false},
		{[]*types.Window{copyWin(types.WIN_LAG, "c", "l", `{"default": "z"}`)}, 0, true},
		{[]*types.Window{copyWin(types.WIN_LEAD, "c", "l", `{"default": 1}`)}, 0, true},
		{[]*types.Window{copyWin(types.WIN_LAG, "x", "l", `{"default": "z"}`)}, 0, false},
		{[]*types.Window{copyWin(types.WIN_LAG, "c", "l", ""), copyWin(types.WIN_LEAD, "l", "m", `{"default": "B"}`)}, 1, true},
		{[]*types.Window{copyWin(types.WIN_LAG, "c", "l", ""), copyWin(types.WIN_LEAD, "l", "m", `{"default": "b"}`)}, 1, false},
	} {
		ref := CategoricalDefaultRefusal(c.windows, c.i, s)
		if (ref != nil) != c.refused {
			t.Fatalf("%s default %s: refusal %v, want refused=%v", c.windows[c.i].Field, c.windows[c.i].Params, ref, c.refused)
		}
		if ref != nil && (ref.Details["reason"] != "default_not_a_label" || ref.Coded().Code != "PULSE_WINDOW_INVALID") {
			t.Fatalf("refusal %+v", ref)
		}
	}
}
