package overlayhost

import (
	"bytes"
	"encoding/json"
	"math"
	"sort"
	"strconv"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/types"
)

// OVERLAY_MARKOV params and its square-host rule, shared by predict
// (internal/descriptor) and runtime (internal/processing) so both raise
// the same code, message and details.

const (
	// MarkovMaxStep is the largest accepted params.steps entry.
	MarkovMaxStep = 1000
	// MarkovMaxStepCount caps how many params.steps entries one spec may
	// list: each one is a k × k part.
	MarkovMaxStepCount = 64
	// MarkovInitialMargin is the params.initial string naming the
	// observed row-margin distribution.
	MarkovInitialMargin = "margin"
)

// MarkovDefaultSteps is params.steps when omitted.
func MarkovDefaultSteps() []int { return []int{1, 3, 6, 12} }

// MarkovParams is a decoded OVERLAY_MARKOV params block.
type MarkovParams struct {
	// Steps lists the Pⁿ powers to emit (step_<n> parts), in the order
	// given; never empty after ParseMarkovParams.
	Steps []int
	// Absorbing lists the declared absorbing state names (nil when
	// omitted); the absorption_time part rides it.
	Absorbing []string
	// Initial is set when params.initial is present: InitialMargin
	// selects the observed row-margin distribution, otherwise
	// InitialShares holds the key → share map (raw, not normalized).
	Initial       bool
	InitialMargin bool
	InitialShares map[string]float64
}

// ParseMarkovParams decodes spec.Params strictly: only `steps` (a
// non-empty array of distinct integers 1–MarkovMaxStep, at most
// MarkovMaxStepCount of them), `absorbing` (a non-empty array of
// distinct non-empty state names) and `initial` ("margin", or a
// non-empty object of state → finite share >= 0 with a positive sum)
// are accepted. Anything else is PULSE_OVERLAY_PARAM_MISSING (the overlay
// param-validation code) with details {kind, param?, ...} plus index
// when index >= 0. Whether a named state exists is the runtime's call
// (MarkovStateRefusal): predict reads no record.
func ParseMarkovParams(spec *types.OverlaySpec, index int) (MarkovParams, *errors.CodedError) {
	out := MarkovParams{Steps: MarkovDefaultSteps()}
	var in struct {
		Steps     json.RawMessage `json:"steps"`
		Absorbing json.RawMessage `json:"absorbing"`
		Initial   json.RawMessage `json:"initial"`
	}
	paramread.Declare(paramread.Overlay, string(spec.Kind), &in)
	raw := bytes.TrimSpace(spec.Params)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return out, nil
	}
	if err := paramread.DecodeStrict(paramread.Overlay, string(spec.Kind), raw, &in); err != nil {
		return out, markovParamRefusal(spec.Kind, index, "",
			"params must be an object with only steps, absorbing and initial: "+err.Error(), nil)
	}
	if len(in.Steps) > 0 {
		steps, cerr := parseMarkovSteps(spec.Kind, in.Steps, index)
		if cerr != nil {
			return out, cerr
		}
		out.Steps = steps
	}
	if len(in.Absorbing) > 0 {
		names, cerr := parseAbsorbing(spec.Kind, in.Absorbing, index)
		if cerr != nil {
			return out, cerr
		}
		out.Absorbing = names
	}
	if len(in.Initial) > 0 {
		cerr := parseMarkovInitial(spec.Kind, in.Initial, index, &out)
		if cerr != nil {
			return out, cerr
		}
	}
	return out, nil
}

// parseAbsorbing decodes a params.absorbing value — a non-empty array
// of distinct non-empty state names — shared by OVERLAY_MARKOV and
// OVERLAY_RETENTION; anything else is PULSE_OVERLAY_PARAM_MISSING
// (param absorbing).
func parseAbsorbing(kind types.OverlayKind, raw json.RawMessage, index int) ([]string, *errors.CodedError) {
	var names []string
	if err := json.Unmarshal(raw, &names); err != nil || len(names) == 0 {
		return nil, markovParamRefusal(kind, index, "absorbing",
			"absorbing must be a non-empty array of state keys, got "+string(raw),
			map[string]any{"absorbing": string(raw)})
	}
	seen := map[string]bool{}
	for _, n := range names {
		if n == "" || seen[n] {
			return nil, markovParamRefusal(kind, index, "absorbing",
				"absorbing lists an empty or repeated state key "+strconv.Quote(n),
				map[string]any{"state": n})
		}
		seen[n] = true
	}
	return names, nil
}

func parseMarkovSteps(kind types.OverlayKind, raw json.RawMessage, index int) ([]int, *errors.CodedError) {
	var elems []json.RawMessage
	if err := json.Unmarshal(raw, &elems); err != nil || len(elems) == 0 || len(elems) > MarkovMaxStepCount {
		return nil, markovParamRefusal(kind, index, "steps",
			"steps must be a non-empty array of at most "+strconv.Itoa(MarkovMaxStepCount)+" integers, got "+string(raw),
			map[string]any{"steps": string(raw), "max_count": MarkovMaxStepCount})
	}
	steps := make([]int, len(elems))
	seen := map[int]bool{}
	for i, e := range elems {
		// A JSON number literal holding an integer in range — never a
		// string, a fraction or null.
		lit := string(bytes.TrimSpace(e))
		n, err := strconv.Atoi(lit)
		if err != nil || n < 1 || n > MarkovMaxStep || seen[n] {
			return nil, markovParamRefusal(kind, index, "steps",
				"each step must be a distinct integer from 1 to "+strconv.Itoa(MarkovMaxStep)+", got "+lit,
				map[string]any{"step": lit, "max_step": MarkovMaxStep})
		}
		seen[n] = true
		steps[i] = n
	}
	return steps, nil
}

func parseMarkovInitial(kind types.OverlayKind, raw json.RawMessage, index int, out *MarkovParams) *errors.CodedError {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if s != MarkovInitialMargin {
			return markovParamRefusal(kind, index, "initial",
				"initial must be \"margin\" or a state -> share map, got "+strconv.Quote(s),
				map[string]any{"initial": s})
		}
		out.Initial, out.InitialMargin = true, true
		return nil
	}
	var shares map[string]float64
	if err := json.Unmarshal(raw, &shares); err != nil || len(shares) == 0 {
		return markovParamRefusal(kind, index, "initial",
			"initial must be \"margin\" or a non-empty state -> share map, got "+string(raw),
			map[string]any{"initial": string(raw)})
	}
	var sum float64
	for k, v := range shares {
		if k == "" || v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
			return markovParamRefusal(kind, index, "initial",
				"initial shares must be finite and >= 0 under non-empty state keys; "+strconv.Quote(k)+" is not",
				map[string]any{"state": k})
		}
		sum += v
	}
	if sum <= 0 || math.IsInf(sum, 0) {
		return markovParamRefusal(kind, index, "initial",
			"initial shares must have a positive, finite sum (they are normalized to 1)", nil)
	}
	out.Initial, out.InitialShares = true, shares
	return nil
}

func markovParamRefusal(kind types.OverlayKind, index int, param, why string, extra map[string]any) *errors.CodedError {
	details := map[string]any{"kind": string(kind)}
	if param != "" {
		details["param"] = param
	}
	if index >= 0 {
		details["index"] = index
	}
	for k, v := range extra {
		details[k] = v
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_PARAM_MISSING,
		"overlay "+string(kind)+": "+why, details)
}

// MarkovStateName is the name a params key (absorbing, initial) uses
// for a host axis key, and the identity the row and column axes are
// aligned by: a one-element string key is the string itself; any other
// key is its JSON encoding (a number 3 → "3", a two-grouper key
// ["a","b"] → `["a","b"]`).
func MarkovStateName(key types.AxisKey) string {
	if len(key) == 1 {
		if s, ok := key[0].(string); ok {
			return s
		}
		b, err := json.Marshal(key[0])
		if err == nil {
			return string(b)
		}
	}
	b, err := json.Marshal(key)
	if err != nil {
		return ""
	}
	return string(b)
}

// MarkovArityRefusal refuses a host whose row and column axes carry a
// different number of groupers — their keys can never name the same
// states (PULSE_OVERLAY_MATRIX_NOT_SQUARE, details {kind,
// row_groupers, column_groupers} plus index when >= 0). Predict judges
// the request's crosstab axes, runtime the host's axis headers.
func MarkovArityRefusal(kind types.OverlayKind, rowGroupers, columnGroupers, index int) *errors.CodedError {
	if rowGroupers == columnGroupers {
		return nil
	}
	details := map[string]any{"kind": string(kind), "row_groupers": rowGroupers, "column_groupers": columnGroupers}
	if index >= 0 {
		details["index"] = index
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_MATRIX_NOT_SQUARE,
		"overlay "+string(kind)+" needs the same states on both crosstab axes; the rows have "+
			strconv.Itoa(rowGroupers)+" grouper(s), the columns "+strconv.Itoa(columnGroupers),
		details)
}

// MarkovKeySetRefusal refuses a host whose row and column key sets
// differ (runtime only: predict reads no record) once the handler has
// synthesized the column-only states declared in params.absorbing —
// PULSE_OVERLAY_MATRIX_NOT_SQUARE, details {kind, row_only,
// column_only} (sorted state names) plus index when >= 0. nil when both
// lists are empty.
func MarkovKeySetRefusal(kind types.OverlayKind, rowOnly, columnOnly []string, index int) *errors.CodedError {
	if len(rowOnly) == 0 && len(columnOnly) == 0 {
		return nil
	}
	rowOnly = append([]string{}, rowOnly...)
	columnOnly = append([]string{}, columnOnly...)
	sort.Strings(rowOnly)
	sort.Strings(columnOnly)
	details := map[string]any{"kind": string(kind), "row_only": rowOnly, "column_only": columnOnly}
	if index >= 0 {
		details["index"] = index
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_MATRIX_NOT_SQUARE,
		"overlay "+string(kind)+" needs the row and column key sets to be equal (states align by key); some states appear on one axis only",
		details)
}

// MarkovStateRefusal refuses a params key (absorbing, initial) that
// names no host state (runtime only) — PULSE_OVERLAY_PARAM_MISSING,
// details {kind, param, unknown (sorted), states} plus index when >= 0.
// nil when unknown is empty.
func MarkovStateRefusal(kind types.OverlayKind, param string, unknown, states []string, index int) *errors.CodedError {
	if len(unknown) == 0 {
		return nil
	}
	unknown = append([]string{}, unknown...)
	sort.Strings(unknown)
	return markovParamRefusal(kind, index, param,
		param+" names states the host crosstab does not carry",
		map[string]any{"unknown": unknown, "states": append([]string{}, states...)})
}
