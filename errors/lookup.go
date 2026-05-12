package errors

import (
	"sort"
	"strings"
)

// LookupResult is the depth-on-demand projection returned by Lookup,
// ByDomain, and Search. It carries the code identifier, its domain
// prefix, the canonical Message, and the materialised Fixup templates.
// FixupNotApplicable is mirrored so callers can distinguish "no fixup
// because internal bug" from "no fixup because none authored yet".
//
// LookupResult is a stable wire shape. The MCP `pulse_errors_lookup`
// tool and the `pulse errors lookup` / `pulse errors list` CLI leaves
// emit a LookupResult per code, with json field names that mirror the
// manifest's slim error_codes list.
type LookupResult struct {
	// Code is the canonical error code string (e.g.
	// "PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL").
	Code string `json:"code"`

	// Domain is the prefix segment (PULSE, SERVICE, PROCESSING,
	// ENCODING, DATA, CLI).
	Domain string `json:"domain"`

	// Message is the canonical human-readable description for the
	// code. Mirrors codeMetadata[c].Message.
	Message string `json:"message"`

	// Fixups is the materialised fixup template list. Nil when the
	// code is FixupNotApplicable.
	Fixups []Fixup `json:"fixups,omitempty"`

	// FixupNotApplicable is true for codes that have no mechanical
	// repair (typically *_INTERNAL invariants).
	FixupNotApplicable bool `json:"fixup_not_applicable,omitempty"`
}

// Domain returns the prefix segment of a Code (the substring before the
// first underscore). Returns the whole string when no underscore is
// present.
func Domain(c Code) string {
	s := string(c)
	if i := strings.IndexByte(s, '_'); i > 0 {
		return s[:i]
	}
	return s
}

// AllDomains returns every distinct domain prefix observed in
// AllCodes(), sorted alphabetically.
func AllDomains() []string {
	seen := make(map[string]struct{}, 8)
	for _, c := range allCodes {
		seen[Domain(c)] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for d := range seen {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// makeResult assembles a LookupResult from a Code and its Metadata.
// Returned Fixups slice is a defensive copy so callers cannot mutate
// the package's template registry.
func makeResult(c Code, m Metadata) LookupResult {
	r := LookupResult{
		Code:               string(c),
		Domain:             Domain(c),
		Message:            m.Message,
		FixupNotApplicable: m.FixupNotApplicable,
	}
	if !m.FixupNotApplicable && len(m.Fixups) > 0 {
		r.Fixups = make([]Fixup, len(m.Fixups))
		copy(r.Fixups, m.Fixups)
	}
	return r
}

// Lookup returns the metadata projection for a single code string.
// Case-sensitive exact match. Returns (LookupResult{}, false) when the
// code is unknown.
func Lookup(code string) (LookupResult, bool) {
	c, ok := codeIndex[code]
	if !ok {
		return LookupResult{}, false
	}
	m, ok := codeMetadata[c]
	if !ok {
		return LookupResult{}, false
	}
	return makeResult(c, m), true
}

// ByDomain returns every code's metadata in the given domain. Match is
// case-insensitive against the prefix segment. Returns a non-nil empty
// slice when no code matches (so JSON callers always see an array).
// Results are sorted by Code.
func ByDomain(domain string) []LookupResult {
	want := strings.ToUpper(strings.TrimSpace(domain))
	out := make([]LookupResult, 0)
	if want == "" {
		return out
	}
	for _, c := range allCodes {
		if strings.ToUpper(Domain(c)) != want {
			continue
		}
		m, ok := codeMetadata[c]
		if !ok {
			continue
		}
		out = append(out, makeResult(c, m))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}

// scoreSource ranks where a search substring matched. Lower values
// outrank higher ones: 0 = description hit, 1 = fixup hint hit.
const (
	matchInMessage = 0
	matchInFixup   = 1
	matchInCode    = 2
	matchNone      = 3
)

// matchScore returns the best (lowest) source where q appears in m. q
// is assumed already lowercase. Returns matchNone when nothing matches.
func matchScore(c Code, m Metadata, q string) int {
	if strings.Contains(strings.ToLower(m.Message), q) {
		return matchInMessage
	}
	for _, f := range m.Fixups {
		if strings.Contains(strings.ToLower(f.Hint), q) {
			return matchInFixup
		}
	}
	if strings.Contains(strings.ToLower(string(c)), q) {
		return matchInCode
	}
	return matchNone
}

// Search returns codes whose Message or Fixup hints contain q
// (case-insensitive substring). Results are ranked by where the match
// hit — description matches outrank fixup matches, fixup matches
// outrank code-name matches; ties resolve alphabetically by code.
// Returns a non-nil empty slice when nothing matches.
func Search(query string) []LookupResult {
	q := strings.ToLower(strings.TrimSpace(query))
	out := make([]LookupResult, 0)
	if q == "" {
		return out
	}
	type scored struct {
		res   LookupResult
		score int
	}
	var hits []scored
	for _, c := range allCodes {
		m, ok := codeMetadata[c]
		if !ok {
			continue
		}
		s := matchScore(c, m, q)
		if s == matchNone {
			continue
		}
		hits = append(hits, scored{res: makeResult(c, m), score: s})
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score < hits[j].score
		}
		return hits[i].res.Code < hits[j].res.Code
	})
	for _, h := range hits {
		out = append(out, h.res)
	}
	return out
}

// SortedCodeNames returns every registered code as a string slice,
// alphabetized. Used by the slim manifest's error_codes field.
func SortedCodeNames() []string {
	out := make([]string, len(allCodes))
	for i, c := range allCodes {
		out[i] = string(c)
	}
	sort.Strings(out)
	return out
}
