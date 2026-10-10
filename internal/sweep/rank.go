package sweep

import (
	"bytes"
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// The reason values PULSE_SWEEP_RANK_PATH carries under details.reason —
// on the refusal, and on the warning a slot left out of the ranking
// carries.
const (
	// RankReasonSyntax: `by` is not dotted segments — an empty segment
	// (a leading, trailing or doubled `.`). Refused by predict and the
	// runtime before any slot runs.
	RankReasonSyntax = "syntax"
	// RankReasonMissing: a segment names no key of an object, no list
	// element, or descends through a null. A refusal when the path
	// resolves in no sweep slot; a warning on the slot otherwise.
	RankReasonMissing = "missing"
	// RankReasonNotNumber: the path ends on a string, boolean, object or
	// list, or a segment descends into a scalar. Always a refusal.
	RankReasonNotNumber = "not_number"
	// RankReasonAmbiguous: a segment on a list matches more than one
	// element by `name` / `label`, or matches an element whose `name`
	// and `label` differ. Always a refusal.
	RankReasonAmbiguous = "ambiguous"
	// RankReasonNull: the path ends on null — an undefined figure (NaN
	// on the wire). A warning on the slot; never ranked last.
	RankReasonNull = "null"
)

// ParseRankPath splits a rank `by` path into its dotted segments,
// refusing an empty segment as PULSE_SWEEP_RANK_PATH (reason syntax).
// An empty `by` is Validate's PULSE_SWEEP_INVALID, not this.
func ParseRankPath(by string) ([]string, *errors.CodedError) {
	segs := strings.Split(by, ".")
	for i, s := range segs {
		if s == "" {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_SWEEP_RANK_PATH,
				"sweep rank path "+strconv.Quote(by)+" has an empty segment at position "+strconv.Itoa(i+1)+
					"; write dotted segments such as regressions.<name>.residual_std_err",
				map[string]any{"by": by, "reason": RankReasonSyntax, "segment_index": i})
		}
	}
	return segs, nil
}

// Rank orders the finished sweep slots by the number each one carries at
// r.By and returns the ranking (never nil). labels and responses are the
// SWEEP slots only, index-aligned, in expansion order; explicit
// `requests` are never ranked.
//
// Each slot is read in its wire form — the response marshalled as JSON
// (non-finite floats written as null) BEFORE any `return` shaping — so a
// path names exactly what a reader of an unshaped response sees. Path
// grammar: dotted segments; on an object a segment is a key, on a list
// it selects the one element whose `name` or `label` equals it (never
// an index); the path must end on a number.
//
// A slot whose path ends on null (an undefined figure), or whose path is
// missing while another slot resolves it, is left out of the ranking
// and gets a PULSE_SWEEP_RANK_PATH warning on its own Response naming
// the slot (details label, by, reason, segment). A nil slot response is
// skipped. The call is refused with PULSE_SWEEP_RANK_PATH, the first
// offending slot in expansion order under details.label and the failing
// segment under details.segment, when a path ends on a non-number or is
// ambiguous in any slot, or resolves in no slot.
//
// Ties keep expansion order; Rank is 1-based; Top trims the ranking
// only.
func Rank(r *types.SweepRank, labels []string, responses []*types.Response) ([]types.RankEntry, error) {
	if r == nil {
		return nil, nil
	}
	segs, perr := ParseRankPath(r.By)
	if perr != nil {
		return nil, perr
	}
	type miss struct {
		slot int
		seg  int
	}
	var (
		entries  []types.RankEntry
		nulls    []int
		misses   []miss
		resolved bool
	)
	for i, resp := range responses {
		if resp == nil {
			continue
		}
		raw, err := json.Marshal(resp)
		if err != nil {
			return nil, err
		}
		var doc any
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		if err := dec.Decode(&doc); err != nil {
			return nil, err
		}
		res := resolveRankPath(doc, segs)
		switch res.reason {
		case "":
			resolved = true
			entries = append(entries, types.RankEntry{Label: labels[i], Value: res.value})
		case RankReasonNull:
			resolved = true
			nulls = append(nulls, i)
		case RankReasonMissing:
			misses = append(misses, miss{slot: i, seg: res.seg})
		default:
			return nil, rankRefusal(r.By, labels[i], segs, res)
		}
	}
	if !resolved && len(misses) > 0 {
		m := misses[0]
		return nil, rankRefusal(r.By, labels[m.slot], segs, rankResolution{reason: RankReasonMissing, seg: m.seg})
	}
	for _, i := range nulls {
		warnExcluded(responses[i], r.By, labels[i], RankReasonNull, segs[len(segs)-1],
			"sweep slot "+strconv.Quote(labels[i])+" is left out of the ranking: its value at "+strconv.Quote(r.By)+" is null (undefined)")
	}
	for _, m := range misses {
		warnExcluded(responses[m.slot], r.By, labels[m.slot], RankReasonMissing, segs[m.seg],
			"sweep slot "+strconv.Quote(labels[m.slot])+" is left out of the ranking: its response has no "+
				strconv.Quote(segs[m.seg])+" on the path "+strconv.Quote(r.By))
	}

	desc := r.Order == types.SweepRankDesc
	sort.SliceStable(entries, func(a, b int) bool {
		if desc {
			return entries[a].Value > entries[b].Value
		}
		return entries[a].Value < entries[b].Value
	})
	if r.Top != nil && *r.Top < len(entries) {
		entries = entries[:*r.Top]
	}
	out := make([]types.RankEntry, len(entries))
	for i, e := range entries {
		e.Rank = i + 1
		out[i] = e
	}
	return out, nil
}

// rankResolution is one slot's answer for the path: the value, or the
// failing reason and segment index.
type rankResolution struct {
	value  float64
	reason string
	seg    int
	count  int // matching elements, for ambiguous
}

// resolveRankPath walks doc (a decoded wire response, numbers as
// json.Number) along segs.
func resolveRankPath(doc any, segs []string) rankResolution {
	cur := doc
	for k, seg := range segs {
		switch x := cur.(type) {
		case map[string]any:
			next, ok := x[seg]
			if !ok {
				return rankResolution{reason: RankReasonMissing, seg: k}
			}
			cur = next
		case []any:
			var match map[string]any
			n := 0
			for _, el := range x {
				m, ok := el.(map[string]any)
				if !ok {
					continue
				}
				if stringKey(m, "name") == seg || stringKey(m, "label") == seg {
					n++
					match = m
				}
			}
			switch {
			case n == 0:
				return rankResolution{reason: RankReasonMissing, seg: k}
			case n > 1:
				return rankResolution{reason: RankReasonAmbiguous, seg: k, count: n}
			}
			name, label := stringKey(match, "name"), stringKey(match, "label")
			if name != "" && label != "" && name != label {
				return rankResolution{reason: RankReasonAmbiguous, seg: k, count: 1}
			}
			cur = match
		case nil:
			return rankResolution{reason: RankReasonMissing, seg: k}
		default:
			return rankResolution{reason: RankReasonNotNumber, seg: k}
		}
	}
	last := len(segs) - 1
	switch x := cur.(type) {
	case json.Number:
		f, err := x.Float64()
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return rankResolution{reason: RankReasonNotNumber, seg: last}
		}
		return rankResolution{value: f}
	case nil:
		return rankResolution{reason: RankReasonNull, seg: last}
	default:
		return rankResolution{reason: RankReasonNotNumber, seg: last}
	}
}

// stringKey is m[key] when it is a string, else "".
func stringKey(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

// rankRefusal builds the PULSE_SWEEP_RANK_PATH refusal for one slot.
func rankRefusal(by, label string, segs []string, res rankResolution) *errors.CodedError {
	seg := segs[res.seg]
	details := map[string]any{"by": by, "label": label, "reason": res.reason, "segment": seg, "segment_index": res.seg}
	var why string
	switch res.reason {
	case RankReasonMissing:
		why = "segment " + strconv.Quote(seg) + " names nothing in any sweep slot's response (first: slot " + strconv.Quote(label) + ")"
	case RankReasonAmbiguous:
		details["count"] = res.count
		if res.count > 1 {
			why = "segment " + strconv.Quote(seg) + " matches " + strconv.Itoa(res.count) + " list elements by name or label in slot " + strconv.Quote(label)
		} else {
			why = "segment " + strconv.Quote(seg) + " matches a list element whose name and label differ in slot " + strconv.Quote(label)
		}
	default:
		why = "the value at segment " + strconv.Quote(seg) + " is not a number in slot " + strconv.Quote(label)
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_SWEEP_RANK_PATH,
		"sweep rank path "+strconv.Quote(by)+": "+why, details)
}

// warnExcluded records on resp that its slot is left out of the ranking.
func warnExcluded(resp *types.Response, by, label, reason, seg, msg string) {
	resp.Warnings = append(resp.Warnings, &types.ResponseWarning{
		Code:    string(errors.PULSE_SWEEP_RANK_PATH),
		Message: msg,
		Details: map[string]any{"by": by, "label": label, "reason": reason, "segment": seg},
	})
}
