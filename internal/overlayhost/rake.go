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

// OVERLAY_RAKE params and its target-mismatch refusals, shared by
// predict (internal/descriptor) and runtime (internal/processing) so
// both raise the same code, message and details.

const (
	// RakeTargetsShares is the default params.targets_as: each axis's
	// targets are normalized by their sum and scaled to the host grand
	// total.
	RakeTargetsShares = "shares"
	// RakeTargetsCounts reads the targets as the fitted margins
	// themselves; the row and column totals must agree.
	RakeTargetsCounts = "counts"
	// RakeDefaultMaxIter is params.max_iter when omitted.
	RakeDefaultMaxIter = 100
	// RakeMaxMaxIter is the largest accepted params.max_iter.
	RakeMaxMaxIter = 100000
	// RakeDefaultTolerance is params.tolerance when omitted: the largest
	// relative margin error a converged fit may keep.
	RakeDefaultTolerance = 1e-8

	// RakeAxisRow / RakeAxisColumn name the axis in a key-mismatch
	// refusal.
	RakeAxisRow    = "row"
	RakeAxisColumn = "column"
)

// RakeParams is a decoded OVERLAY_RAKE params block.
type RakeParams struct {
	// RowTargets / ColTargets map a host key name (RakeKeyName) to its
	// raw target (a share or a count, per TargetsAs).
	RowTargets map[string]float64
	ColTargets map[string]float64
	// TargetsAs is RakeTargetsShares or RakeTargetsCounts.
	TargetsAs string
	MaxIter   int
	Tolerance float64
}

// RakeKeyName is the name a target map uses for a host axis key: a
// one-element string key is the string itself; any other key is its
// JSON encoding (the same identity OVERLAY_MARKOV aligns states by).
func RakeKeyName(key types.AxisKey) string { return MarkovStateName(key) }

// ParseRakeParams decodes spec.Params strictly: `row_targets` and
// `col_targets` (required, each a non-empty object of key → finite
// value >= 0 with a positive sum), `targets_as` ("shares" default |
// "counts"), `max_iter` (an integer 1–RakeMaxMaxIter, default 100) and
// `tolerance` (finite, > 0 and < 1, default 1e-8). Anything else is
// PULSE_OVERLAY_PARAM_MISSING with details {kind, param?, ...} plus
// index when index >= 0. Counts targets whose row and column totals
// differ by more than the tolerance (relative) are
// PULSE_OVERLAY_RAKE_TARGET_MISMATCH (RakeTotalsRefusal) — a params-only
// rule, so predict and runtime raise it alike. Whether a target key
// exists on the host is RakeKeyRefusal's call.
func ParseRakeParams(spec *types.OverlaySpec, index int) (RakeParams, *errors.CodedError) {
	out := RakeParams{TargetsAs: RakeTargetsShares, MaxIter: RakeDefaultMaxIter, Tolerance: RakeDefaultTolerance}
	var in struct {
		RowTargets json.RawMessage `json:"row_targets"`
		ColTargets json.RawMessage `json:"col_targets"`
		TargetsAs  json.RawMessage `json:"targets_as"`
		MaxIter    json.RawMessage `json:"max_iter"`
		Tolerance  json.RawMessage `json:"tolerance"`
	}
	paramread.Declare(paramread.Overlay, string(spec.Kind), &in)
	raw := bytes.TrimSpace(spec.Params)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return out, rakeParamRefusal(spec.Kind, index, "row_targets",
			"params row_targets and col_targets are required", nil)
	}
	if err := paramread.DecodeStrict(paramread.Overlay, string(spec.Kind), raw, &in); err != nil {
		return out, rakeParamRefusal(spec.Kind, index, "",
			"params must be an object with only row_targets, col_targets, targets_as, max_iter and tolerance: "+err.Error(), nil)
	}
	var cerr *errors.CodedError
	if out.RowTargets, cerr = parseRakeTargets(spec.Kind, "row_targets", in.RowTargets, index); cerr != nil {
		return out, cerr
	}
	if out.ColTargets, cerr = parseRakeTargets(spec.Kind, "col_targets", in.ColTargets, index); cerr != nil {
		return out, cerr
	}
	if len(in.TargetsAs) > 0 {
		var s string
		if err := json.Unmarshal(in.TargetsAs, &s); err != nil || (s != RakeTargetsShares && s != RakeTargetsCounts) {
			return out, rakeParamRefusal(spec.Kind, index, "targets_as",
				"targets_as must be \"shares\" or \"counts\", got "+string(in.TargetsAs),
				map[string]any{"targets_as": string(in.TargetsAs)})
		}
		out.TargetsAs = s
	}
	if len(in.MaxIter) > 0 {
		lit := string(bytes.TrimSpace(in.MaxIter))
		n, err := strconv.Atoi(lit)
		if err != nil || n < 1 || n > RakeMaxMaxIter {
			return out, rakeParamRefusal(spec.Kind, index, "max_iter",
				"max_iter must be an integer from 1 to "+strconv.Itoa(RakeMaxMaxIter)+", got "+lit,
				map[string]any{"max_iter": lit, "max": RakeMaxMaxIter})
		}
		out.MaxIter = n
	}
	if len(in.Tolerance) > 0 {
		var tol float64
		if err := json.Unmarshal(in.Tolerance, &tol); err != nil || !(tol > 0 && tol < 1) {
			return out, rakeParamRefusal(spec.Kind, index, "tolerance",
				"tolerance must be a number above 0 and below 1, got "+string(in.Tolerance),
				map[string]any{"tolerance": string(in.Tolerance)})
		}
		out.Tolerance = tol
	}
	if out.TargetsAs == RakeTargetsCounts {
		if cerr := RakeTotalsRefusal(spec.Kind, RakeTargetSum(out.RowTargets), RakeTargetSum(out.ColTargets), out.Tolerance, index); cerr != nil {
			return out, cerr
		}
	}
	return out, nil
}

func parseRakeTargets(kind types.OverlayKind, param string, raw json.RawMessage, index int) (map[string]float64, *errors.CodedError) {
	if len(raw) == 0 {
		return nil, rakeParamRefusal(kind, index, param, param+" is required (a key -> target map)", nil)
	}
	var targets map[string]float64
	if err := json.Unmarshal(raw, &targets); err != nil || len(targets) == 0 {
		return nil, rakeParamRefusal(kind, index, param,
			param+" must be a non-empty key -> number map, got "+string(raw),
			map[string]any{param: string(raw)})
	}
	for _, k := range sortedTargetKeys(targets) {
		v := targets[k]
		if k == "" || v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, rakeParamRefusal(kind, index, param,
				param+" values must be finite and >= 0 under non-empty keys; "+strconv.Quote(k)+" is not",
				map[string]any{"key": k})
		}
	}
	if sum := RakeTargetSum(targets); !(sum > 0) || math.IsInf(sum, 0) {
		return nil, rakeParamRefusal(kind, index, param, param+" must have a positive, finite sum", nil)
	}
	return targets, nil
}

func sortedTargetKeys(m map[string]float64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// RakeTargetSum sums a target map in sorted key order, so predict and
// runtime see the same rounding.
func RakeTargetSum(targets map[string]float64) float64 {
	var sum float64
	for _, k := range sortedTargetKeys(targets) {
		sum += targets[k]
	}
	return sum
}

func rakeParamRefusal(kind types.OverlayKind, index int, param, why string, extra map[string]any) *errors.CodedError {
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

// RakeTotalsRefusal refuses counts targets whose row total and column
// total differ by more than tolerance relative to the larger —
// PULSE_OVERLAY_RAKE_TARGET_MISMATCH, details {kind, reason:
// "totals_differ", row_total, column_total} plus index when >= 0. nil
// when they agree.
func RakeTotalsRefusal(kind types.OverlayKind, rowTotal, columnTotal, tolerance float64, index int) *errors.CodedError {
	if math.Abs(rowTotal-columnTotal) <= tolerance*math.Max(rowTotal, columnTotal) {
		return nil
	}
	details := map[string]any{"kind": string(kind), "reason": "totals_differ", "row_total": rowTotal, "column_total": columnTotal}
	if index >= 0 {
		details["index"] = index
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_RAKE_TARGET_MISMATCH,
		"overlay "+string(kind)+" counts targets need one grand total: the row targets sum to "+
			strconv.FormatFloat(rowTotal, 'g', -1, 64)+", the column targets to "+strconv.FormatFloat(columnTotal, 'g', -1, 64),
		details)
}

// RakeKeyRefusal refuses an axis whose target keys and host keys
// differ — PULSE_OVERLAY_RAKE_TARGET_MISMATCH, details {kind, reason:
// "keys", axis, target_only, host_only} (sorted; never null) plus index
// when >= 0. nil when both lists are empty. Predict passes the target
// keys a categorical dictionary cannot hold (host_only empty); runtime
// passes both sides of the observed host axis.
func RakeKeyRefusal(kind types.OverlayKind, axis string, targetOnly, hostOnly []string, index int) *errors.CodedError {
	if len(targetOnly) == 0 && len(hostOnly) == 0 {
		return nil
	}
	targetOnly = append([]string{}, targetOnly...)
	hostOnly = append([]string{}, hostOnly...)
	sort.Strings(targetOnly)
	sort.Strings(hostOnly)
	details := map[string]any{"kind": string(kind), "reason": "keys", "axis": axis, "target_only": targetOnly, "host_only": hostOnly}
	if index >= 0 {
		details["index"] = index
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_RAKE_TARGET_MISMATCH,
		"overlay "+string(kind)+" needs exactly one "+axis+" target per host "+axis+" key; the target keys and the host keys differ",
		details)
}
