package synth

import (
	"sort"
	"strings"
)

// This file is the taxonomy for the free-text warnings every synth
// capture and generation path produces. It exists so a caller — today
// the `pulse profile create` / `pulse synth from-profile` / `pulse synth
// from-schema` terminal summary (internal/cli/warnings.go) — can report
// thousands of warnings as a handful of counted kinds instead of a
// scrolling dump.
//
// It lives HERE, beside the functions that build those strings, rather
// than in internal/cli, for one reason: a change to a message format is
// a change to this table, and the two must be edited together. A
// classifier in the CLI would drift the moment someone reworded a
// warning, and it would drift SILENTLY — the reworded line would simply
// start landing in the catch-all group.
//
// Nothing here changes what any warning slice CONTAINS. Profile.Warnings,
// Result.Warnings and FidelityReport.Warnings are untouched; this reads
// them.

// WarningGroup is one kind of warning together with every member of
// that kind, in the order they were produced.
//
// Attention is the whole point of the type. A capture on a wide cohort
// legitimately emits thousands of lines that describe EXPECTED outcomes
// — a thin pair that still ships, a target no candidate explained,
// a pair claim arbitrated away — and a summary that counts those as
// faults trains the reader to ignore the channel, which is how the
// v0.32.x model-drop defect survived a whole effort. Attention marks the
// kinds where something the caller asked for did not happen.
type WarningGroup struct {
	// Kind is the stable, field-free label for this group. It never
	// interpolates a field name, so the group count is a count of
	// findings of one shape.
	Kind string
	// Attention is true when this kind means a requested capture or
	// generation step did NOT happen. False marks an expected outcome:
	// a complete, correct result the caller may nonetheless want to know
	// about.
	Attention bool
	// Members holds every warning classified into this group, in
	// production order. Callers that render to a terminal are expected
	// to show a bounded prefix; the full list stays in whatever document
	// the command already writes.
	Members []string
}

// Count is the number of warnings in the group.
func (g WarningGroup) Count() int { return len(g.Members) }

// warningKind is one row of the classification table below.
type warningKind struct {
	kind      string
	attention bool
	match     func(string) bool
}

// hasAll reports whether w contains every one of subs.
func hasAll(w string, subs ...string) bool {
	for _, s := range subs {
		if !strings.Contains(w, s) {
			return false
		}
	}
	return true
}

// warningKinds is an ORDERED table — first match wins — because two
// rules deliberately overlap. `model for numeric field "x" not applied:
// model carries no predictors` matches both the zero-predictor rule and
// the generic not-applied rule, and it must land in the first: a
// zero-predictor model is a COMPLETE model (its own mean plus its own
// spread) and reporting it as a fault would put 50 non-faults into the
// headline count on the motivating cohort, which is exactly the
// signal-burying this taxonomy exists to prevent.
var warningKinds = []warningKind{
	// --- expected outcomes, ordered ahead of the fault rules they
	//     would otherwise be swallowed by ------------------------------
	{
		kind:      "model carries no predictors",
		attention: false,
		match: func(w string) bool {
			return strings.Contains(w, "carries no predictors")
		},
	},
	{
		kind:      "correlation not applied (the model owns the field)",
		attention: false,
		match: func(w string) bool {
			return hasAll(w, "pairwise correlation naming ", "is not applied")
		},
	},

	// --- a requested step did not happen ----------------------------
	{
		kind:      "model skipped",
		attention: true,
		match: func(w string) bool {
			return strings.HasPrefix(w, "model for numeric field ") &&
				strings.Contains(w, " skipped: ")
		},
	},
	{
		kind:      "model not applied",
		attention: true,
		match: func(w string) bool {
			return strings.HasPrefix(w, "model not applied: ") ||
				(strings.HasPrefix(w, "model for numeric field ") &&
					strings.Contains(w, " not applied: "))
		},
	},
	{
		kind:      "residual correlation dropped",
		attention: true,
		match: func(w string) bool {
			return strings.HasPrefix(w, "residual correlation(s) naming ")
		},
	},
	{
		kind:      "residual correlations not captured",
		attention: true,
		match: func(w string) bool {
			return strings.HasPrefix(w, "residual correlations requested without ")
		},
	},
	{
		kind:      "residual correlation pairs unmeasured",
		attention: true,
		match: func(w string) bool {
			return strings.HasPrefix(w, "residual correlations: ")
		},
	},
	{
		kind:      "correlation matrix completed by assumption",
		attention: true,
		match: func(w string) bool {
			return strings.Contains(w, "matrix completed by assumption")
		},
	},
	{
		kind:      "correlation matrix ridge-regularized",
		attention: true,
		match: func(w string) bool {
			return strings.Contains(w, "matrix is not positive definite")
		},
	},

	// --- arbitration and support caveats ----------------------------
	{
		kind:      "conditional relationship conflict",
		attention: false,
		match: func(w string) bool {
			return strings.HasPrefix(w, "conditional relationship conflict: ")
		},
	},
}

// thinSubjects are the subjects thinSupportWarning is called with, each
// becoming its own group so a reader can tell 2,900 thin categorical
// pairs apart from two thin numeric ones. The `+N further …` roll-ups
// those paths already emit are folded into the same group as the lines
// they summarise, since they are the same finding continued.
var thinSubjects = []string{
	"numeric pair",
	"categorical pair",
	"set pair",
	"residual pair",
	"model level",
}

// otherWarningKind is where an unrecognised warning lands, and it is
// deliberately marked Attention.
//
// The alternative — a benign default — is the exact shape of the defect
// that motivated this file: a `default:` arm returning a plausible value
// let a mis-serialised predictor kind disable 80% of a feature with no
// signal anywhere. An unclassified warning is a warning nobody has
// decided about, so it gets the reader's attention until someone adds a
// row above.
const otherWarningKind = "other"

// classifyWarning returns the group label and attention flag for one
// warning string.
func classifyWarning(w string) (string, bool) {
	for _, k := range warningKinds {
		if k.match(w) {
			return k.kind, k.attention
		}
	}
	for _, s := range thinSubjects {
		if strings.HasPrefix(w, "thin "+s+" ") {
			return "thin " + s, false
		}
		// The bounded-listing roll-up: "+69 further thin model level(s) …".
		if strings.HasPrefix(w, "+") && strings.Contains(w, "further thin "+s) {
			return "thin " + s, false
		}
	}
	return otherWarningKind, true
}

// GroupWarnings folds a flat warning slice into counted kind groups.
//
// Ordering is a property of the input, never of a map: groups needing
// attention come first so a truncated rendering keeps them, then by
// descending count, then by kind name. Members within a group keep
// production order.
//
// A nil or empty slice returns nil, so a caller can render
// unconditionally.
func GroupWarnings(warnings []string) []WarningGroup {
	if len(warnings) == 0 {
		return nil
	}
	at := make(map[string]int, len(warningKinds))
	var out []WarningGroup
	for _, w := range warnings {
		kind, attention := classifyWarning(w)
		i, seen := at[kind]
		if !seen {
			at[kind] = len(out)
			out = append(out, WarningGroup{Kind: kind, Attention: attention, Members: []string{w}})
			continue
		}
		out[i].Members = append(out[i].Members, w)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Attention != out[j].Attention {
			return out[i].Attention
		}
		if len(out[i].Members) != len(out[j].Members) {
			return len(out[i].Members) > len(out[j].Members)
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}

// CountWarningsNeedingAttention returns how many of the grouped warnings
// are in an Attention kind, and how many are expected outcomes. The two
// sum to the total, and separating them is what keeps a headline count
// meaningful on a cohort whose expected-outcome lines outnumber the real
// findings by three orders of magnitude.
func CountWarningsNeedingAttention(groups []WarningGroup) (attention, informational int) {
	for _, g := range groups {
		if g.Attention {
			attention += g.Count()
			continue
		}
		informational += g.Count()
	}
	return attention, informational
}
