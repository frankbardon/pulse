package mcp

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// requestSlotKeys is the set of valid top-level JSON keys on a
// types.Request, derived once from the struct's json tags. Reflection
// keeps it in lockstep with the struct as slots are added.
var requestSlotKeys = jsonObjectKeys(&types.Request{})

// jsonObjectKeys returns the top-level JSON object keys declared by the
// struct pointed to by sample, read from each exported field's json
// tag. Fields tagged "-" or with an empty name are skipped.
func jsonObjectKeys(sample any) []string {
	t := reflect.TypeOf(sample)
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil || t.Kind() != reflect.Struct {
		return nil
	}
	keys := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		if name == "" {
			name = f.Name
		}
		keys = append(keys, name)
	}
	return keys
}

// checkUnknownRequestKeys decodes body as a JSON object and verifies
// every top-level key is a recognised types.Request slot. On any
// unknown key it returns a PULSE_REQUEST_UNKNOWN_FIELD CodedError whose
// message and details name the offending key(s), the nearest valid slot
// (Levenshtein), and the full valid-key list — turning the silent
// "unknown keys are dropped" failure into an actionable, first-try
// fixable error.
//
// Returns nil when body is empty, is not a JSON object (the typed
// decoder downstream surfaces the real parse error), or all keys are
// recognised.
func checkUnknownRequestKeys(body []byte) *perr.CodedError {
	if len(body) == 0 {
		return nil
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil
	}
	valid := make(map[string]struct{}, len(requestSlotKeys))
	for _, k := range requestSlotKeys {
		valid[k] = struct{}{}
	}
	var unknown []string
	for k := range raw {
		if _, ok := valid[k]; !ok {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)

	validList := append([]string(nil), requestSlotKeys...)
	sort.Strings(validList)

	suggestions := make(map[string]any, len(unknown))
	var parts []string
	for _, k := range unknown {
		if near := nearestKey(k, requestSlotKeys); near != "" {
			suggestions[k] = near
			parts = append(parts, `"`+k+`" (did you mean "`+near+`"?)`)
		} else {
			parts = append(parts, `"`+k+`"`)
		}
	}

	msg := "request contains unrecognized top-level key(s): " + strings.Join(parts, ", ") +
		". Unknown keys are ignored during decode, so the intended operation is silently dropped. Valid request keys: " +
		strings.Join(validList, ", ") + "."

	return perr.NewCodedErrorWithDetails(perr.PULSE_REQUEST_UNKNOWN_FIELD, msg, map[string]any{
		"unknown_keys": unknown,
		"suggestions":  suggestions,
		"valid_keys":   validList,
	})
}

// nearestKey returns the closest candidate to k by Levenshtein distance,
// or "" when no candidate is close enough. The acceptance threshold is
// edit distance < 4, further capped at the key length, so short or
// unrelated keys do not yield a misleading suggestion.
func nearestKey(k string, candidates []string) string {
	limit := min(4, len(k)) // accept edit distance up to 3, capped at key length
	best := ""
	bestDist := limit
	for _, c := range candidates {
		d := levenshtein(k, c)
		if d < bestDist {
			bestDist = d
			best = c
		}
	}
	return best
}

// levenshtein computes the edit distance between a and b using the
// standard two-row dynamic-programming table.
func levenshtein(a, b string) int {
	if a == b {
		return 0
	}
	ra, rb := []rune(a), []rune(b)
	if len(ra) == 0 {
		return len(rb)
	}
	if len(rb) == 0 {
		return len(ra)
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
			del := prev[j] + 1
			ins := curr[j-1] + 1
			sub := prev[j-1] + cost
			curr[j] = min(del, ins, sub)
		}
		prev, curr = curr, prev
	}
	return prev[len(rb)]
}

// checkUnknownKeysComposed validates each request inside a
// ComposedRequest body, tagging the offending request's index into the
// returned error's details.
func checkUnknownKeysComposed(body []byte) *perr.CodedError {
	var probe struct {
		Requests []json.RawMessage `json:"requests"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return nil
	}
	for i, r := range probe.Requests {
		if ce := checkUnknownRequestKeys(r); ce != nil {
			ce.Details["request_index"] = i
			return ce
		}
	}
	return nil
}

// checkUnknownKeysChain validates each stage's request inside a
// ChainRequest body, tagging the offending stage index (and name when
// set) into the returned error's details.
func checkUnknownKeysChain(body []byte) *perr.CodedError {
	var probe struct {
		Stages []struct {
			Name    string          `json:"name"`
			Request json.RawMessage `json:"request"`
		} `json:"stages"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return nil
	}
	for i, st := range probe.Stages {
		if len(st.Request) == 0 {
			continue
		}
		if ce := checkUnknownRequestKeys(st.Request); ce != nil {
			ce.Details["stage_index"] = i
			if st.Name != "" {
				ce.Details["stage_name"] = st.Name
			}
			return ce
		}
	}
	return nil
}
