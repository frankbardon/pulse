package strictjson

import (
	"sort"
	"strings"

	perr "github.com/frankbardon/pulse/errors"
)

// UnknownFieldError builds THE PULSE_REQUEST_UNKNOWN_FIELD refusal for
// the unrecognised keys unknown of the object at objectPath inside a
// request ("" = the request root; "sort[0]", "crosstab.cell", ...),
// against that object's valid keys. Nil when unknown is empty.
//
// details: unknown_keys (sorted), suggestions (unknown key → nearest
// valid key, when one is close), valid_keys (sorted) and, below the
// root, path — the location of the first offending key
// (`sort[0].direction`). The message names the object and both lists.
// The strict decoder and the instance slot gate (descx.SlotRefusal)
// both build it here, so an unknown key and a hidden slot cannot drift.
func UnknownFieldError(objectPath string, unknown, valid []string) *perr.CodedError {
	return UnknownFieldErrorAliased(objectPath, unknown, valid, nil)
}

// UnknownFieldErrorAliased is UnknownFieldError with known misnomers:
// an unknown key found in aliases is suggested as its alias (a valid
// key no edit distance would reach — GROUP_DATE params.unit is
// params.component), ahead of the nearest-key guess.
func UnknownFieldErrorAliased(objectPath string, unknown, valid []string, aliases map[string]string) *perr.CodedError {
	if len(unknown) == 0 {
		return nil
	}
	unknown = append([]string(nil), unknown...)
	sort.Strings(unknown)
	validList := append(make([]string, 0, len(valid)), valid...) // [] (never null) for an object with no keys
	sort.Strings(validList)

	suggestions := make(map[string]any, len(unknown))
	parts := make([]string, 0, len(unknown))
	for _, k := range unknown {
		near := aliases[k]
		if near == "" {
			near = NearestKey(k, valid)
		}
		if near != "" {
			suggestions[k] = near
			parts = append(parts, `"`+k+`" (did you mean "`+near+`"?)`)
		} else {
			parts = append(parts, `"`+k+`"`)
		}
	}

	where, validWhere := "top-level key(s): ", "Valid request keys: "
	if objectPath != "" {
		where, validWhere = "key(s) in "+objectPath+": ", "Valid keys there: "
	}
	msg := "request contains unrecognized " + where + strings.Join(parts, ", ") +
		". An unknown key is refused, never ignored, so a misspelled or misplaced key cannot silently drop the intended operation. " +
		validWhere + strings.Join(validList, ", ") + "."

	details := map[string]any{
		"unknown_keys": unknown,
		"suggestions":  suggestions,
		"valid_keys":   validList,
	}
	if objectPath != "" {
		details["path"] = joinPath(objectPath, unknown[0])
	}
	return perr.NewCodedErrorWithDetails(perr.PULSE_REQUEST_UNKNOWN_FIELD, msg, details)
}

// NearestKey returns the closest candidate to k by Levenshtein distance,
// or "" when no candidate is close enough. The acceptance threshold is
// edit distance < 4, further capped at the key length, so short or
// unrelated keys do not yield a misleading suggestion. Ties keep the
// earliest candidate.
func NearestKey(k string, candidates []string) string {
	limit := min(4, len(k))
	best := ""
	bestDist := limit
	for _, c := range candidates {
		if d := Levenshtein(k, c); d < bestDist {
			bestDist = d
			best = c
		}
	}
	return best
}

// Levenshtein is the rune-level edit distance between a and b.
func Levenshtein(a, b string) int {
	if a == b {
		return 0
	}
	ra := []rune(a)
	rb := []rune(b)
	if len(ra) == 0 {
		return len(rb)
	}
	if len(rb) == 0 {
		return len(ra)
	}
	// Keep rb the shorter row so the buffer stays minimal.
	if len(ra) < len(rb) {
		ra, rb = rb, ra
	}
	prev := make([]int, len(rb)+1)
	curr := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		curr[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			curr[j] = min(prev[j]+1, curr[j-1]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[len(rb)]
}
