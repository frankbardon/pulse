package overlayhost

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/types"
)

// OVERLAY_SYMMETRY params, shared by predict (internal/descriptor) and
// runtime (internal/processing) so both raise the same code, message
// and details. Its square-host rule is OVERLAY_MARKOV's
// MarkovArityRefusal (the same grouper count on both axes); states
// align by MarkovStateName.

// SymmetryParams is a decoded OVERLAY_SYMMETRY params block.
type SymmetryParams struct {
	// Correct applies Edwards' continuity correction to a 2 × 2 table
	// (McNemar): (|n_12 − n_21| − 1)². Default true, R's
	// mcnemar.test(correct = TRUE); inert on a k × k table (Bowker) and
	// under Exact.
	Correct bool
	// Exact runs the exact test: on a 2 × 2 table the binomial McNemar,
	// p = binom.test(n_12, n_12 + n_21)$p.value (statdist.
	// BinomialTwoSidedP at 0.5) and statistic = n_12; on k × k the exact
	// conditional Bowker test (statdist.BowkerExactP), statistic = the
	// uncorrected χ². Default false (the χ² test, byte-identical);
	// refused on a probability-weighted host (SymmetryExactWeightRefusal)
	// and past the convolution's work cap (SymmetryExactTooLargeRefusal).
	Exact bool
	// ConfLevel is the coverage of the exact arm's discordant odds-ratio
	// interval, binom.test(n_12, n, conf.level = ConfLevel)$conf.int:
	// in (0, 1), default SymmetryDefaultConfLevel (binom.test's own).
	// Set without Exact it is refused (reason needs_exact): the
	// chi-square arm has no interval to cover; set on k × k it is refused
	// too (SymmetryExactConfLevelRefusal, reason needs_2x2).
	ConfLevel float64
	// ConfLevelSet records that params.conf_level was supplied (not the
	// default): only an explicit level is refused on k × k.
	ConfLevelSet bool
}

// SymmetryDefaultConfLevel is params.conf_level when absent:
// binom.test's default conf.level. Absent, the exact layer's figures
// are byte-identical to the fixed-95% interval shipped before the param.
const SymmetryDefaultConfLevel = 0.95

// ParseSymmetryParams decodes spec.Params strictly: only `correct` and
// `exact` (JSON booleans) and `conf_level` (a number in (0, 1), exact
// only) are accepted. Anything else is PULSE_OVERLAY_PARAM_MISSING with
// details {kind, param?, ...} plus index when index >= 0; a bad
// conf_level adds reason out_of_range (not a number in (0, 1), judged
// first) or needs_exact (set without exact true). Predict and the
// runtime handler both call it, so the refusals match.
func ParseSymmetryParams(spec *types.OverlaySpec, index int) (SymmetryParams, *errors.CodedError) {
	out := SymmetryParams{Correct: true, ConfLevel: SymmetryDefaultConfLevel}
	var in struct {
		Correct   json.RawMessage `json:"correct"`
		Exact     json.RawMessage `json:"exact"`
		ConfLevel json.RawMessage `json:"conf_level"`
	}
	paramread.Declare(paramread.Overlay, string(spec.Kind), &in)
	raw := bytes.TrimSpace(spec.Params)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return out, nil
	}
	if err := paramread.DecodeStrict(paramread.Overlay, string(spec.Kind), raw, &in); err != nil {
		return out, markovParamRefusal(spec.Kind, index, "",
			"params must be an object with only correct, exact and conf_level: "+err.Error(), nil)
	}
	for _, f := range []struct {
		name string
		raw  json.RawMessage
		dst  *bool
	}{{"correct", in.Correct, &out.Correct}, {"exact", in.Exact, &out.Exact}} {
		if len(f.raw) == 0 {
			continue
		}
		var b *bool
		if err := json.Unmarshal(f.raw, &b); err != nil || b == nil {
			return out, markovParamRefusal(spec.Kind, index, f.name,
				f.name+" must be true or false, got "+string(f.raw),
				map[string]any{f.name: string(f.raw)})
		}
		*f.dst = *b
	}
	if len(in.ConfLevel) == 0 {
		return out, nil
	}
	var level *float64
	if err := json.Unmarshal(in.ConfLevel, &level); err != nil || level == nil || !(*level > 0 && *level < 1) {
		return out, markovParamRefusal(spec.Kind, index, "conf_level",
			"conf_level must be a number in (0, 1), got "+string(in.ConfLevel),
			map[string]any{"reason": "out_of_range", "conf_level": string(in.ConfLevel)})
	}
	if !out.Exact {
		return out, markovParamRefusal(spec.Kind, index, "conf_level",
			"conf_level sets the exact odds-ratio interval and needs exact true; set \"exact\": true or drop conf_level",
			map[string]any{"reason": "needs_exact"})
	}
	out.ConfLevel, out.ConfLevelSet = *level, true
	return out, nil
}

// SymmetryExactConfLevelRefusal refuses an explicit params.conf_level
// with exact on a table of states other than 2: the level is the
// coverage of the 2 × 2 discordant odds-ratio interval, and the k × k
// exact Bowker test has no odds ratio. PULSE_OVERLAY_PARAM_MISSING
// {kind, param: "conf_level", reason: "needs_2x2", states, index?}. An
// absent conf_level is fine. The state count is the data's (row keys
// plus column-only keys), so only the runtime handler can raise it —
// predict reads no record, like the fewer-than-2-states refusal.
func SymmetryExactConfLevelRefusal(kind types.OverlayKind, p SymmetryParams, states, index int) *errors.CodedError {
	if !p.Exact || !p.ConfLevelSet || states == 2 {
		return nil
	}
	return markovParamRefusal(kind, index, "conf_level",
		"conf_level sets the 2 x 2 exact odds-ratio interval and the table has "+strconv.Itoa(states)+
			" states, where the exact Bowker test has no odds ratio; drop conf_level",
		map[string]any{"reason": "needs_2x2", "states": states})
}

// SymmetryExactTooLargeRefusal refuses params.exact on a k × k table
// whose exact Bowker convolution would enumerate more than limit terms
// (statdist.BowkerExactMaxTerms): PULSE_OVERLAY_PARAM_MISSING {kind,
// param: "exact", reason: "exact_too_large", states, terms, limit,
// index?}. Pulse never falls back to the chi-square silently; the
// counts are large enough for it, so the fixup is to drop exact.
// Runtime only: the support is the data's.
func SymmetryExactTooLargeRefusal(kind types.OverlayKind, states, terms, limit, index int) *errors.CodedError {
	return markovParamRefusal(kind, index, "exact",
		"the exact Bowker test on this "+strconv.Itoa(states)+"-state table needs more than "+strconv.Itoa(limit)+
			" enumeration terms; drop exact for Bowker's chi-square test, which these counts support",
		map[string]any{"reason": "exact_too_large", "states": states, "terms": terms, "limit": limit})
}

// SymmetryExactWeightRefusal refuses params.exact over a crosstab cell
// weighted under kind probability: the exact test needs integer counts,
// and a Kish-scaled Σw table is not one (the χ² test, which scales by
// n_eff, stays available). PULSE_WEIGHT_UNSUPPORTED with the
// frequency-only host details {slot, operator, host, kind,
// supported_kinds} plus param "exact" and index; predict
// (descriptor.validateOverlaySpec, the cell's resolved basis) and the
// runtime handler (the host's WeightBasis) raise it alike. Frequency
// weights are replication counts, so their Σw table is read as counts.
func SymmetryExactWeightRefusal(kind types.OverlayKind, p SymmetryParams, probability bool, index int) *errors.CodedError {
	if !p.Exact || !probability {
		return nil
	}
	slot := "overlays"
	details := map[string]any{"operator": string(kind), "host": "crosstab.cell", "param": "exact",
		"kind": string(types.WeightKindProbability), "supported_kinds": []string{string(types.WeightKindFrequency)}}
	if index >= 0 {
		slot += "[" + strconv.Itoa(index) + "]"
		details["index"] = index
	}
	details["slot"] = slot
	return errors.NewCodedErrorWithDetails(errors.PULSE_WEIGHT_UNSUPPORTED,
		slot+": "+string(kind)+" params.exact needs integer counts, and its host crosstab cell is weighted under kind \""+
			string(types.WeightKindProbability)+"\"; use a frequency weight (integer replication counts) on the crosstab cell, "+
			"set \"weight\": null on it, or drop exact for the chi-square test (scaled to the Kish n_eff)", details)
}

// SymmetryExactCountsRefusal refuses params.exact when a discordant
// pair n_ij / n_ji is not a whole count: binom.test's rule, a value
// more than 1e-7 from its rounding (an AGG_SUM host of fractional
// values) is refused, a closer one is rounded. PULSE_OVERLAY_
// NONNEGATIVE_REQUIRED {kind, param: "exact", reason: "non_integer",
// n12, n21, index?}, plus pair [from, to] (the two states) on a k × k
// table (pair non-nil; nil keeps the 2 × 2 refusal as it was);
// runtime only (data). It returns the rounded pair.
func SymmetryExactCountsRefusal(kind types.OverlayKind, n12, n21 float64, pair []string, index int) (int, int, *errors.CodedError) {
	r12, r21 := math.Round(n12), math.Round(n21)
	if math.Abs(n12-r12) <= 1e-7 && math.Abs(n21-r21) <= 1e-7 {
		return int(r12), int(r21), nil
	}
	details := map[string]any{"kind": string(kind), "param": "exact", "reason": "non_integer", "n12": n12, "n21": n21}
	if index >= 0 {
		details["index"] = index
	}
	names := "n_12 = " + strconv.FormatFloat(n12, 'g', -1, 64) + " and n_21 = " + strconv.FormatFloat(n21, 'g', -1, 64)
	if pair != nil {
		details["pair"] = pair
		names = "the " + pair[0] + " -> " + pair[1] + " count " + strconv.FormatFloat(n12, 'g', -1, 64) +
			" and its mirror " + strconv.FormatFloat(n21, 'g', -1, 64)
	}
	return 0, 0, errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_NONNEGATIVE_REQUIRED,
		"overlay "+string(kind)+": exact needs whole discordant counts, got "+names+"; use AGG_COUNT cells or drop exact", details)
}
