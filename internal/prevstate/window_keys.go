package prevstate

import (
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/windowparams"
	"github.com/frankbardon/pulse/types"
)

// WindowKeys is view (KeySchemaWith's result over schema) plus one
// virtual field per lagged category of windows
// (windowparams.CategoricalLags: a WIN_LAG / WIN_LEAD of a categorical
// field, or of an earlier such lag, typed as its source on its
// dictionary, nullable), appended after view's fields — so a
// GROUP_CATEGORY key, its Group.Include and its dict_size resolve the
// lag's dictionary codes to the source's labels. A label view already
// names contributes nothing. view itself (the same pointer) when no
// window contributes, so a request lagging no category is
// byte-identical. Like KeySchema, a NAME-LOOKUP view, never a decode,
// stride or write schema.
func WindowKeys(view, schema *encoding.Schema, windows []*types.Window) *encoding.Schema {
	if view == nil || len(windows) == 0 {
		return view
	}
	lags := windowparams.CategoricalLags(windows, schema)
	if len(lags) == 0 {
		return view
	}
	var extra []encoding.Field
	for _, w := range windows {
		if w == nil {
			continue
		}
		l, ok := lags[windowparams.Label(w)]
		if !ok || view.Field(l.Label) != nil || hasField(extra, l.Label) {
			continue
		}
		extra = append(extra, l.Field)
	}
	if len(extra) == 0 {
		return view
	}
	out := *view
	out.Fields = append(append(make([]encoding.Field, 0, len(view.Fields)+len(extra)), view.Fields...), extra...)
	return &out
}
