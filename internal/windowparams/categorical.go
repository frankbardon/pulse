package windowparams

import (
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// CategoricalLag is one WIN_LAG / WIN_LEAD output copying a categorical
// column (mmm-optimize-depth E4-S1): its cells are the source's
// dictionary labels (a record row carries a categorical cell as its
// label), so the output is a categorical column on the source's
// dictionary — groupable by GROUP_CATEGORY, a crosstab axis, written by
// materialize as that categorical type with that dictionary.
type CategoricalLag struct {
	// Index is the window's position in Request.Windows.
	Index int
	// Label is the window's output column (Label).
	Label string
	// Source is the cohort field the chain copies (a lag of a lag names
	// the first lag's source).
	Source string
	// Field is the output's shape: the source's categorical type and
	// dictionary, named Label, nullable (a missing neighbour is null).
	Field encoding.Field
}

// IsCopyWindow reports whether t is a built-in copy window (WIN_LAG /
// WIN_LEAD): the only windows whose output is the source cell verbatim.
func IsCopyWindow(t types.WindowType) bool { return t == types.WIN_LAG || t == types.WIN_LEAD }

// CategoricalLags returns, in window order, every WIN_LAG / WIN_LEAD of
// windows whose field is a dictionary categorical of schema — or an
// EARLIER categorical lag's label, so a lag of a lag resolves — keyed by
// output label. A label naming a schema field (the shadow refusal's) or
// an earlier categorical lag contributes nothing. Nil when none: a
// request without one pays one pass over its windows.
func CategoricalLags(windows []*types.Window, schema *encoding.Schema) map[string]CategoricalLag {
	if schema == nil {
		return nil
	}
	var out map[string]CategoricalLag
	for i, w := range windows {
		if w == nil || !IsCopyWindow(w.Type) || w.Field == "" {
			continue
		}
		src, ok := categoricalSource(w.Field, schema, out)
		if !ok {
			continue
		}
		label := Label(w)
		if schema.Field(label) != nil {
			continue
		}
		if _, dup := out[label]; dup {
			continue
		}
		if out == nil {
			out = map[string]CategoricalLag{}
		}
		f := src.Field
		f.Name, f.Nullable = label, true
		out[label] = CategoricalLag{Index: i, Label: label, Source: src.Source, Field: f}
	}
	return out
}

// categoricalSource resolves a copy window's field: a dictionary
// categorical schema field, or an earlier categorical lag (lags).
func categoricalSource(field string, schema *encoding.Schema, lags map[string]CategoricalLag) (CategoricalLag, bool) {
	if f := schema.Field(field); f != nil {
		if !f.Type.IsCategorical() || f.Dictionary == nil {
			return CategoricalLag{}, false
		}
		return CategoricalLag{Source: f.Name, Field: *f}, true
	}
	l, ok := lags[field]
	return l, ok
}

// CategoricalDefaultRefusal judges window i's params.default when the
// window copies a categorical column (CategoricalLags over the same
// windows and schema): the default fills a cell of that categorical
// column, so it must be one of the source dictionary's labels, spelled
// exactly — a number, a bool or an unknown label is refused
// (PULSE_WINDOW_INVALID details.reason "default_not_a_label", with the
// field, its type and the given default). Nil for any other window, a
// valid label, an absent or null default, or a malformed params block
// (Check's refusal).
func CategoricalDefaultRefusal(windows []*types.Window, i int, schema *encoding.Schema) *Refusal {
	if i < 0 || i >= len(windows) {
		return nil
	}
	w := windows[i]
	if w == nil || !IsCopyWindow(w.Type) || w.Field == "" || schema == nil {
		return nil
	}
	spec, ref := Lag(w)
	if ref != nil || !spec.HasDefault {
		return nil
	}
	var earlier map[string]CategoricalLag
	if schema.Field(w.Field) == nil {
		earlier = CategoricalLags(windows[:i], schema)
	}
	src, ok := categoricalSource(w.Field, schema, earlier)
	if !ok {
		return nil
	}
	if s, isLabel := spec.Default.(string); isLabel {
		if _, found := src.Field.Dictionary.IDFor(s); found {
			return nil
		}
	}
	return &Refusal{Op: w.Type,
		Message: "params.default must be a label of categorical field " + w.Field + "'s dictionary (" + src.Field.Type.String() + "): the default fills a cell of the lagged category; name a dictionary entry exactly (pulse inspect lists them), or omit it for null",
		Details: map[string]any{"field": w.Field, "field_type": src.Field.Type.String(), "default": spec.Default, "reason": "default_not_a_label"}}
}
