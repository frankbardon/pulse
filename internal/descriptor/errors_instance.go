package descriptor

import (
	"sort"
	"strings"

	"github.com/frankbardon/pulse/errors"
)

// The instance-scoped error surface. Every function takes the instance
// and is the identity over the errors package for a nil / unscoped one,
// so the default instance lists and renders exactly what errors.Lookup /
// ByDomain / Search do. On a scoped instance:
//
//   - a code is listed iff errorCodeVisible (some owner enabled, or
//     shared) — a hidden code is "not found" / absent everywhere;
//   - every Fixup whose hint or example names a hidden feature (or the
//     MCP tool of one) is stripped at render time; a code may be left
//     with no fixups;
//   - search ranks over the rendered (stripped) text only, so it never
//     matches on hidden-only text.
//
// A visible code's Message is never rewritten: TestErrorMessagesName
// OnlyOwners keeps every Message free of a feature that can be hidden
// while the code stays listed.

// ErrorCodeVisible reports whether inst lists code c.
func ErrorCodeVisible(inst *InstanceSnapshot, c errors.Code) bool {
	if !inst.Scoped() {
		return true
	}
	return errorCodeVisible(c, inst.Enabled)
}

// ErrorLookup is errors.Lookup as inst sees it.
func ErrorLookup(inst *InstanceSnapshot, code string) (errors.LookupResult, bool) {
	r, ok := errors.Lookup(code)
	if !ok || !inst.Scoped() {
		return r, ok
	}
	if !errorCodeVisible(errors.Code(r.Code), inst.Enabled) {
		return errors.LookupResult{}, false
	}
	return stripHiddenFixups(r, hiddenProseNames(inst)), true
}

// ErrorsByDomain is errors.ByDomain as inst sees it.
func ErrorsByDomain(inst *InstanceSnapshot, domain string) []errors.LookupResult {
	all := errors.ByDomain(domain)
	if !inst.Scoped() {
		return all
	}
	hidden := hiddenProseNames(inst)
	out := make([]errors.LookupResult, 0, len(all))
	for _, r := range all {
		if errorCodeVisible(errors.Code(r.Code), inst.Enabled) {
			out = append(out, stripHiddenFixups(r, hidden))
		}
	}
	return out
}

// Search ranks, mirroring errors.Search: a Message hit outranks a fixup
// hint hit, which outranks a code-name hit; ties sort by code.
const (
	searchInMessage = iota
	searchInFixup
	searchInCode
	searchNone
)

// ErrorsSearch is errors.Search as inst sees it: it scores the rendered
// result of every visible code, never the stripped fixups.
func ErrorsSearch(inst *InstanceSnapshot, query string) []errors.LookupResult {
	if !inst.Scoped() {
		return errors.Search(query)
	}
	q := strings.ToLower(strings.TrimSpace(query))
	out := make([]errors.LookupResult, 0)
	if q == "" {
		return out
	}
	hidden := hiddenProseNames(inst)
	type scored struct {
		res   errors.LookupResult
		score int
	}
	var hits []scored
	for _, c := range errors.AllCodes() {
		r, ok := errors.Lookup(string(c))
		if !ok || !errorCodeVisible(c, inst.Enabled) {
			continue
		}
		r = stripHiddenFixups(r, hidden)
		if s := searchScore(r, q); s != searchNone {
			hits = append(hits, scored{res: r, score: s})
		}
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

func searchScore(r errors.LookupResult, q string) int {
	if strings.Contains(strings.ToLower(r.Message), q) {
		return searchInMessage
	}
	for _, f := range r.Fixups {
		if strings.Contains(strings.ToLower(f.Hint), q) {
			return searchInFixup
		}
	}
	if strings.Contains(strings.ToLower(r.Code), q) {
		return searchInCode
	}
	return searchNone
}

// stripHiddenFixups drops every fixup naming a hidden token in its hint
// or a string example (whole-token match, as the manifest prose scrub).
// r.Fixups is already a defensive copy, so filtering in a fresh slice
// leaves the errors package's table untouched.
func stripHiddenFixups(r errors.LookupResult, hidden map[string]struct{}) errors.LookupResult {
	if len(hidden) == 0 || len(r.Fixups) == 0 {
		return r
	}
	kept := make([]errors.Fixup, 0, len(r.Fixups))
	for _, f := range r.Fixups {
		if !fixupNamesHidden(f, hidden) {
			kept = append(kept, f)
		}
	}
	if len(kept) == 0 {
		kept = nil
	}
	r.Fixups = kept
	return r
}

func fixupNamesHidden(f errors.Fixup, hidden map[string]struct{}) bool {
	if mentionsHidden(f.Hint, hidden) {
		return true
	}
	for _, e := range f.Examples {
		if s, ok := e.(string); ok && mentionsHidden(s, hidden) {
			return true
		}
	}
	return false
}

// errorCodeNamesFor / errorDomainsFor are the manifest's error lists for
// the instance whose offer predicate is on: the visible codes,
// alphabetized, and the domains that keep at least one visible code.
func errorCodeNamesFor(on func(string) bool) []string {
	all := errorCodeNames()
	out := make([]string, 0, len(all))
	for _, n := range all {
		if errorCodeVisible(errors.Code(n), on) {
			out = append(out, n)
		}
	}
	return out
}

func errorDomainsFor(codes []string) []string {
	seen := map[string]bool{}
	for _, n := range codes {
		seen[errors.Domain(errors.Code(n))] = true
	}
	out := make([]string, 0, len(seen))
	for d := range seen {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}
