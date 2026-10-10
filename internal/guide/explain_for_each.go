package guide

import (
	stderrors "errors"
	"strconv"

	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/limits"
	"github.com/frankbardon/pulse/internal/sweep"
	"github.com/frankbardon/pulse/types"
)

// itemFan maps a request's expanded feature, attribute and window lists
// back to the items as written, and states each for_each fan-out once,
// as a plain step right before the first item it adds.
type itemFan struct {
	e       *explainer
	written map[string][]int // list -> expanded index -> written index
	fans    map[string]sweep.FanOut
	told    map[string]bool
}

// expandItems expands r's for_each items with the ONE expansion every
// surface runs (descx.ExpandRequestItems), so Explain describes the items
// that run, not the template they come from. The derived-column limit is
// not Explain's to apply: predict's check reports it. A for_each that
// does not expand is a caveat, and the items are described as written.
func (e *explainer) expandItems(prefix string, r *types.Request) (*types.Request, *itemFan) {
	f := &itemFan{e: e, written: map[string][]int{}, fans: map[string]sweep.FanOut{}, told: map[string]bool{}}
	x, fan, _, err := descx.ExpandRequestItems(r, limits.Limits{})
	if err != nil {
		code := string(errors.PULSE_FOR_EACH_INVALID)
		var ce *errors.CodedError
		if stderrors.As(err, &ce) {
			code = string(ce.Code)
		}
		e.caveat("A for_each in " + prefixWords(prefix) + "does not expand (" + code + "), so its items are described as written; pulse errors lookup " + code + " says how to fix it.")
		return r, f
	}
	for _, fo := range fan {
		f.fans[fo.Path] = fo
	}
	f.index("features", len(r.Features), func(i int) bool { return r.Features[i] != nil && r.Features[i].ForEach != nil })
	f.index("attributes", len(r.Attributes), func(i int) bool { return r.Attributes[i] != nil && r.Attributes[i].ForEach != nil })
	f.index("windows", len(r.Windows), func(i int) bool { return r.Windows[i] != nil && r.Windows[i].ForEach != nil })
	return x, f
}

// index records, for one list, the written item each expanded item came
// from: a plain item once, a for_each item once per combination.
func (f *itemFan) index(list string, n int, fanned func(int) bool) {
	var out []int
	for i := range n {
		k := 1
		if fanned(i) {
			k = f.fans[list+"["+strconv.Itoa(i)+"]"].Count
		}
		for range k {
			out = append(out, i)
		}
	}
	f.written[list] = out
}

// slot is the written slot path of expanded item i of list, and the
// phrase naming the column a fanned-out item adds (its label, which tells
// the combinations apart; "" for a plain item). On the first item a
// for_each adds, it first states the fan-out.
func (f *itemFan) slot(prefix, list string, i int, r *role, label string) (string, string) {
	w := i
	if idx := f.written[list]; i < len(idx) {
		w = idx[i]
	}
	path := list + "[" + strconv.Itoa(w) + "]"
	fo, ok := f.fans[path]
	if !ok {
		return prefix + path, ""
	}
	if !f.told[path] {
		f.told[path] = true
		f.e.plainStep(prefix+path+".for_each", nil, fanSentence(prefix+path, fo, r))
	}
	if label == "" {
		return prefix + path, ""
	}
	return prefix + path, ", adding the column " + tick(label)
}

// fanSentence states one fan-out: "The for_each on `windows[1]` expands it
// into 3 window columns, taking `d` (3 values) and `decay` (3 values) in
// lockstep."
func fanSentence(path string, fo sweep.FanOut, r *role) string {
	axes := make([]string, len(fo.Axes))
	for i, ax := range fo.Axes {
		axes[i] = tick(ax.Name) + " (" + count(len(ax.Values), "value", "values") + ")"
	}
	how := ", one per combination of " + andList(axes)
	if fo.Mode == types.SweepModeZip {
		how = ", taking " + andList(axes) + " in lockstep"
	}
	return "The for_each on " + tick(path) + " expands it into " + count(fo.Count, r.one, r.many) + how + "."
}
