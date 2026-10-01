package service

import (
	"fmt"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// applyComposeLabelDefaults walks the ComposedRequest's slots and returns a
// new []*types.Request whose every entry carries a non-empty Label —
// caller-supplied values pass through verbatim; empty values are filled
// with the synthesized default `request_<index+1>` (1-based).
//
// The auto-default is applied to a shallow clone of each *Request so the
// caller's original pointer is never mutated. The cohort, slot lists, and
// every other slot share the underlying slices because validate-time
// dispatch only consumes Label and does not need a deep copy.
//
// After synthesis the helper rejects collisions where two slots resolve to
// the same final label with PULSE_COMPOSE_LABEL_COLLISION; this includes
// caller-supplied duplicates AND auto-defaults that collide with a
// caller-supplied "request_<n>" string in another slot.
func applyComposeLabelDefaults(composed *types.ComposedRequest) ([]*types.Request, error) {
	if composed == nil || len(composed.Requests) == 0 {
		return nil, nil
	}
	out := make([]*types.Request, len(composed.Requests))
	seen := make(map[string][]int, len(composed.Requests))
	for i, src := range composed.Requests {
		// Nil slots pass through unchanged — downstream dispatch already
		// rejects them in Process / ComposeParallel with a coded
		// validation error.
		if src == nil {
			out[i] = nil
			continue
		}
		// Clone-then-fill so the caller's pointer keeps an empty Label.
		clone := *src
		if clone.Label == "" {
			clone.Label = fmt.Sprintf("request_%d", i+1)
		}
		out[i] = &clone
		seen[clone.Label] = append(seen[clone.Label], i)
	}
	for label, idxs := range seen {
		if len(idxs) > 1 {
			return nil, errors.NewCodedErrorWithDetails(
				errors.PULSE_COMPOSE_LABEL_COLLISION,
				fmt.Sprintf("compose: label %q is used by %d slots", label, len(idxs)),
				map[string]any{
					"label":   label,
					"indices": idxs,
				},
			)
		}
	}
	return out, nil
}
