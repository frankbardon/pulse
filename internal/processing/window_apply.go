package processing

import (
	"context"

	"github.com/frankbardon/pulse/internal/fieldaccept"
	"github.com/frankbardon/pulse/internal/processing/window"
	"github.com/frankbardon/pulse/types"
)

// applyWindows runs req's windows over rows, after refusing every
// window whose value field the accepted-types table (internal/fieldaccept)
// does not accept — a categorical's dictionary index, a set's bitmask,
// decimal128, packed_bool, datetime. The refusal is predict's
// (validateWindows reports the same *errors.CodedError) and is raised
// before any row is windowed, empty input included. A type the
// instance does not resolve is left to ApplyWithExt's unknown-window
// error, as predict reports an unknown type before its field.
func (p *Processor) applyWindows(ctx context.Context, rows []map[string]any, windows []*types.Window) error {
	for _, w := range windows {
		if w == nil {
			continue
		}
		if _, ok := p.exts.LookupWindow(w.Type); !ok {
			continue
		}
		if cerr := fieldaccept.Check(string(w.Type), fieldaccept.SlotField, w.Field, p.schema); cerr != nil {
			return cerr
		}
	}
	return window.ApplyWithExt(ctx, rows, windows, p.exts.LookupWindow)
}
