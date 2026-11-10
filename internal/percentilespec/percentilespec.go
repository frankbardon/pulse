// Package percentilespec holds the `percentile` param rule of
// AGG_PERCENTILE, shared by the runtime constructors
// (internal/processing — the plain, weighted and ordered-categorical
// arms) and predict (internal/descriptor), so the two refuse the same
// request with the same code, message and details by construction
// (U66 E9-S4).
//
// The percentile is a float in [0, 100]. Absent params read 50 (the
// median); a params object that omits the key, or sets it null, reads 0
// — the decode's zero value, kept exactly as the constructor has always
// read it. A value outside [0, 100] or a non-numeric one is
// PROCESSING_CONFIG.
//
// It is a leaf: it imports only the public errors / types packages and
// internal/paramread, never internal/processing or internal/service, so
// internal/descriptor may import it without breaking
// TestPredictNoExecutionImports.
package percentilespec

import (
	"encoding/json"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/types"
)

// Param is the params key.
const Param = "percentile"

// Default is the percentile absent params read (the median).
const Default = 50.0

// Params is the typed params decode both sides share, so a decode error
// quotes the same Go type on both.
type Params struct {
	// Percentile is the requested percentile, in [0, 100].
	Percentile float64 `json:"percentile"`
}

// Applies reports whether op reads the percentile param.
func Applies(op types.AggregationType) bool {
	return op == types.AGG_PERCENTILE
}

// Resolve decodes AGG_PERCENTILE's params and returns its percentile
// (Default when params are absent), or the PROCESSING_CONFIG refusal
// the runtime constructor and predict both raise: malformed params, or
// a percentile outside [0, 100].
func Resolve(raw json.RawMessage) (float64, *errors.CodedError) {
	name := string(types.AGG_PERCENTILE)
	paramread.Declare(paramread.Aggregator, name, (*Params)(nil))
	p := Default
	if len(raw) > 0 {
		var params Params
		if err := paramread.Decode(paramread.Aggregator, name, raw, &params); err != nil {
			return 0, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
				"invalid percentile params: "+err.Error(),
				map[string]any{"aggregation": name, "param": Param})
		}
		p = params.Percentile
	}
	if p < 0 || p > 100 {
		return 0, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
			"percentile must be between 0 and 100",
			map[string]any{"aggregation": name, "param": Param, "value": p})
	}
	return p, nil
}
