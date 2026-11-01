// Package ddofspec holds the `ddof` param rule of AGG_STDDEV and
// AGG_VARIANCE (delta degrees of freedom: the variance divides the sum
// of squared deviations m2 by n − ddof), shared by the runtime
// constructors (internal/processing) and predict (internal/descriptor)
// so the two refuse the same request with the same code, message and
// details by construction.
//
// ddof is 0 (the default, the population figure m2 / n — every path
// byte-identical to an absent param) or 1 (the sample figure
// m2 / (n − 1); weighted, AGG_WELFORD's m2_w / (Σw − 1)). Any other
// value is PROCESSING_CONFIG.
//
// It is a leaf: it imports only the public errors / types packages and
// internal/paramread, never internal/processing or internal/service, so
// internal/descriptor may import it without breaking
// TestPredictNoExecutionImports.
package ddofspec

import (
	"encoding/json"
	"math"
	"strconv"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/types"
)

// Param is the params key.
const Param = "ddof"

// Params is the typed params decode both sides share, so a decode error
// quotes the same Go type on both. A float, so 1.0 reads as 1 and 0.5
// is refused by the value rule rather than by the decoder.
type Params struct {
	// DDOF is the delta degrees of freedom: 0 (population) or 1
	// (sample). Absent or null = 0.
	DDOF *float64 `json:"ddof"`
}

// Applies reports whether op reads the ddof param.
func Applies(op types.AggregationType) bool {
	return op == types.AGG_STDDEV || op == types.AGG_VARIANCE
}

// Resolve decodes op's params and returns its ddof (0 when absent), or
// the PROCESSING_CONFIG refusal the runtime constructor and predict both
// raise: malformed params, or a ddof other than 0 or 1.
func Resolve(op types.AggregationType, raw json.RawMessage) (int, *errors.CodedError) {
	name := string(op)
	paramread.Declare(paramread.Aggregator, name, (*Params)(nil))
	var p Params
	if err := paramread.DecodeOptional(paramread.Aggregator, name, raw, &p); err != nil {
		return 0, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
			name+": invalid params: "+err.Error(),
			map[string]any{"aggregation": name, "param": Param})
	}
	if p.DDOF == nil {
		return 0, nil
	}
	switch *p.DDOF {
	case 0:
		return 0, nil
	case 1:
		return 1, nil
	}
	return 0, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
		name+": params.ddof must be 0 (population, m2 / n) or 1 (sample, m2 / (n - 1)), got "+
			strconv.FormatFloat(*p.DDOF, 'g', -1, 64),
		map[string]any{"aggregation": name, "param": Param, "value": *p.DDOF})
}

// Variance is m2 / (n − ddof), where n is the row count (unweighted) or
// Σw (weighted). Undefined (NaN, JSON null) when n − ddof ≤ 0 — no row
// under ddof 0, one row (or Σw ≤ 1) under ddof 1 — never 0. Under
// ddof 0 it is exactly the m2 / n every path divided by before the
// param existed.
func Variance(m2, n float64, ddof int) float64 {
	d := n
	if ddof != 0 {
		d = n - float64(ddof)
	}
	if !(d > 0) {
		return math.NaN()
	}
	return m2 / d
}
