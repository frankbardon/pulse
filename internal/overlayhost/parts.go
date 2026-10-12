package overlayhost

import (
	"sort"
	"strconv"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// DimAxisKeyPrefix is the prefix of the synthetic dimension AxisKeys a
// coordinate-style overlay part (points × dimensions) keys its columns
// with: "dim_1", "dim_2", … in dimension order. A dimension is not a
// host coordinate, so it gets a stable synthetic key instead of
// borrowing a host axis tuple.
const DimAxisKeyPrefix = "dim_"

// DimAxisKeys returns the n synthetic dimension keys ["dim_1"], …,
// ["dim_n"] — each a single-element AxisKey, so a renderer reads them
// exactly like a one-grouper host axis. n <= 0 returns an empty,
// non-nil slice (a matrix payload with no columns still marshals its
// column_keys as []).
func DimAxisKeys(n int) []types.AxisKey {
	if n < 0 {
		n = 0
	}
	out := make([]types.AxisKey, n)
	for i := range out {
		out[i] = types.AxisKey{DimAxisKeyPrefix + strconv.Itoa(i+1)}
	}
	return out
}

// PartsRefusal judges a handler-built layer's OverlayPayload.Parts:
// every part has a non-empty name, is non-nil, declares one of the
// three payload shapes and carries no Parts of its own (parts never
// nest). A layer with no parts passes. The violation is the handler's,
// never the caller's — no request can author a part — so it is
// PROCESSING_INTERNAL, raised by every overlay dispatcher right after a
// handler returns. Parts are judged in name order so the refusal is
// deterministic.
func PartsRefusal(layer *types.OverlayLayer, index int) *errors.CodedError {
	if layer == nil || len(layer.Payload.Parts) == 0 {
		return nil
	}
	names := make([]string, 0, len(layer.Payload.Parts))
	for name := range layer.Payload.Parts {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		part := layer.Payload.Parts[name]
		reason := ""
		switch {
		case name == "":
			reason = "a part needs a name"
		case part == nil:
			reason = "a part cannot be nil"
		case len(part.Parts) > 0:
			reason = "parts never nest: a part cannot carry parts"
		case part.Shape != types.OverlayShapeScalar && part.Shape != types.OverlayShapeSeries && part.Shape != types.OverlayShapeMatrix:
			reason = "a part's shape must be scalar, series or matrix"
		}
		if reason == "" {
			continue
		}
		return errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
			"overlay "+string(layer.Kind)+" built a malformed part "+strconv.Quote(name)+": "+reason,
			map[string]any{
				"index":  index,
				"kind":   string(layer.Kind),
				"part":   name,
				"reason": reason,
			})
	}
	return nil
}
