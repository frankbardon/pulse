package vectors

import (
	"bytes"
	"encoding/json"
	"math"
	"slices"
	"strconv"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/types"
)

// ScaleKey is a rating battery's declared scale range [Lo, Hi] and its
// reverse-keyed member positions — the one definition MAT_RELIABILITY
// and ATTR_SCALE_SCORE share. The range is DECLARED (params or a kind
// "scale" vector's bounds), never inferred from the data, and a value
// outside it is never clamped: MAT_RELIABILITY refuses the run,
// ATTR_SCALE_SCORE reads the value as missing and counts it.
type ScaleKey struct {
	// Lo and Hi are the declared range, Lo < Hi, both finite.
	Lo, Hi float64
	// Reverse are the reverse-keyed member positions, ascending.
	Reverse []int
}

// OutOfRange reports whether the present value v lies outside [Lo, Hi]
// (NaN — a missing value — never does).
func (k ScaleKey) OutOfRange(v float64) bool {
	return !math.IsNaN(v) && (v < k.Lo || v > k.Hi)
}

// FirstOutOfRange returns the first position of x holding a present
// value outside [Lo, Hi], or −1.
func (k ScaleKey) FirstOutOfRange(x []float64) int {
	for i, v := range x {
		if k.OutOfRange(v) {
			return i
		}
	}
	return -1
}

// ApplyReverse flips each reverse-keyed present member of x in place:
// x' = Lo + Hi − x. A NaN stays NaN.
func (k ScaleKey) ApplyReverse(x []float64) {
	for _, i := range k.Reverse {
		if v := x[i]; !math.IsNaN(v) {
			x[i] = k.Lo + k.Hi - v
		}
	}
}

// ATTR_SCALE_SCORE params.stat values.
const (
	// ScaleStatMean is the default: the mean of the valid items.
	ScaleStatMean = "mean"
	// ScaleStatSum is the PRORATED sum: the valid-item mean × the item
	// count (there is no raw sum — a skipped item never reads as 0).
	ScaleStatSum = "sum"
)

// ScaleStats returns params.stat's values, default first.
func ScaleStats() []string { return []string{ScaleStatMean, ScaleStatSum} }

// scaleScoreParams is ATTR_SCALE_SCORE's wire params (strict decode).
type scaleScoreParams struct {
	Stat     *string      `json:"stat"`
	MinValid *json.Number `json:"min_valid"`
	Reverse  []string     `json:"reverse"`
	ScaleMin *float64     `json:"scale_min"`
	ScaleMax *float64     `json:"scale_max"`
}

// ScaleScoreConfig is ATTR_SCALE_SCORE's params judged against its
// resolved members.
type ScaleScoreConfig struct {
	// Stat is ScaleStatMean (default) or ScaleStatSum.
	Stat string
	// MinValid is the fewest valid (present, in-range) items a row
	// needs for a score; below it the score is null. Default: every
	// item.
	MinValid int
	// HasScale reports a declared range (params, else the kind "scale"
	// vector's bounds); Key carries it and the reverse positions.
	HasScale bool
	Key      ScaleKey
}

// ScaleScoreParams judges ATTR_SCALE_SCORE's params against members
// (the resolved vector or inline fields): stat ("mean" default, or
// "sum"), min_valid (an integer in [1, item count], default the item
// count), scale_min / scale_max (both or neither, finite, min < max;
// without them the kind "scale" vector's declared bounds stand in) and
// reverse (member LABELS, each once; needs a range from either source —
// the range is never inferred from the data). Unknown keys are refused.
// Refusals are SERVICE_VALIDATION naming the slot at. Predict (the
// field-reference walk) and the runtime (Processor.prepare) both call
// it.
func ScaleScoreParams(at string, raw json.RawMessage, members Resolved) (ScaleScoreConfig, *errors.CodedError) {
	p := len(members.Members)
	cfg := ScaleScoreConfig{Stat: ScaleStatMean, MinValid: p}
	var params scaleScoreParams
	paramread.Declare(paramread.Attribute, string(types.ATTR_SCALE_SCORE), &params)
	raw = bytes.TrimSpace(raw)
	if len(raw) > 0 && !bytes.Equal(raw, []byte("null")) {
		if err := paramread.DecodeStrict(paramread.Attribute, string(types.ATTR_SCALE_SCORE), raw, &params); err != nil {
			return ScaleScoreConfig{}, attributeInvalid(at, "bad_params",
				at+" ATTR_SCALE_SCORE params: "+err.Error()+" (keys: stat, min_valid, reverse, scale_min, scale_max)", nil)
		}
	}
	if params.Stat != nil {
		switch v := *params.Stat; v {
		case ScaleStatMean, ScaleStatSum:
			cfg.Stat = v
		default:
			return ScaleScoreConfig{}, attributeInvalid(at, "bad_params", at+" params.stat must be \"mean\" or \"sum\"",
				map[string]any{"param": "stat", "value": v, "valid": ScaleStats()})
		}
	}
	if params.MinValid != nil {
		k, ok := positiveInt(*params.MinValid)
		if !ok || k > p {
			return ScaleScoreConfig{}, attributeInvalid(at, "bad_params",
				at+" params.min_valid must be an integer from 1 to the item count ("+strconv.Itoa(p)+")",
				map[string]any{"param": "min_valid", "value": params.MinValid.String(), "members": p})
		}
		cfg.MinValid = k
	}
	switch {
	case params.ScaleMin == nil && params.ScaleMax == nil:
		if members.HasScale {
			// Explicit params win; otherwise the kind "scale" vector's
			// declared bounds are the battery's range.
			cfg.HasScale, cfg.Key.Lo, cfg.Key.Hi = true, members.ScaleMin, members.ScaleMax
		}
	case params.ScaleMin == nil || params.ScaleMax == nil:
		return ScaleScoreConfig{}, attributeInvalid(at, "bad_params", at+" params.scale_min and params.scale_max are set together",
			map[string]any{"param": "scale_min"})
	default:
		lo, hi := *params.ScaleMin, *params.ScaleMax
		if math.IsNaN(lo) || math.IsInf(lo, 0) || math.IsNaN(hi) || math.IsInf(hi, 0) || !(lo < hi) {
			return ScaleScoreConfig{}, attributeInvalid(at, "bad_params", at+" params.scale_min must be below params.scale_max",
				map[string]any{"param": "scale_min", "scale_min": lo, "scale_max": hi})
		}
		cfg.HasScale, cfg.Key.Lo, cfg.Key.Hi = true, lo, hi
	}
	if len(params.Reverse) == 0 {
		return cfg, nil
	}
	if !cfg.HasScale {
		return ScaleScoreConfig{}, attributeInvalid(at, "reverse_needs_scale",
			at+" params.reverse needs the battery's scale range: set params.scale_min and params.scale_max, or scale_min / scale_max on its kind \"scale\" vector (the range is never inferred from the data)",
			map[string]any{"param": "reverse", "reverse": append([]string(nil), params.Reverse...)})
	}
	labels := members.Labels
	if len(labels) != p {
		labels = members.Members
	}
	seen := make(map[string]bool, len(params.Reverse))
	for _, r := range params.Reverse {
		if seen[r] {
			return ScaleScoreConfig{}, attributeInvalid(at, "bad_params", at+" params.reverse names "+strconv.Quote(r)+" twice",
				map[string]any{"param": "reverse", "value": r})
		}
		seen[r] = true
		pos := slices.Index(labels, r)
		if pos < 0 {
			return ScaleScoreConfig{}, attributeInvalid(at, "bad_params", at+" params.reverse names "+strconv.Quote(r)+", which is not a member label",
				map[string]any{"param": "reverse", "value": r, "labels": append([]string(nil), labels...)})
		}
		cfg.Key.Reverse = append(cfg.Key.Reverse, pos)
	}
	slices.Sort(cfg.Key.Reverse)
	return cfg, nil
}

// CanonicalParams is cfg as the params of an inline-fields slot over
// members (labels = member names): every default written out, the
// declared range explicit (so a kind "scale" vector's bounds survive
// the rewrite to literal fields) and reverse spelled by member NAME.
// Processor.prepare hands it to the factory beside the literal member
// list; ScaleScoreParams over those members decodes it back to cfg.
func (cfg ScaleScoreConfig) CanonicalParams(members []string) json.RawMessage {
	out := struct {
		Stat     string   `json:"stat"`
		MinValid int      `json:"min_valid"`
		Reverse  []string `json:"reverse,omitempty"`
		ScaleMin *float64 `json:"scale_min,omitempty"`
		ScaleMax *float64 `json:"scale_max,omitempty"`
	}{Stat: cfg.Stat, MinValid: cfg.MinValid}
	if cfg.HasScale {
		lo, hi := cfg.Key.Lo, cfg.Key.Hi
		out.ScaleMin, out.ScaleMax = &lo, &hi
	}
	for _, i := range cfg.Key.Reverse {
		out.Reverse = append(out.Reverse, members[i])
	}
	b, err := json.Marshal(out)
	if err != nil {
		// Unreachable: finite floats, strings and ints always marshal.
		return nil
	}
	return b
}

// ScaleScoreSlotParams resolves attribute a's (an ATTR_SCALE_SCORE
// slot) params against its members and returns them canonical
// (CanonicalParams); ok is false when they do not judge (the
// field-reference pass has refused that request).
func ScaleScoreSlotParams(a *types.Attribute, members Resolved) (json.RawMessage, bool) {
	cfg, err := ScaleScoreParams(attributePath(0), a.Params, members)
	if err != nil {
		return nil, false
	}
	return cfg.CanonicalParams(members.Members), true
}
