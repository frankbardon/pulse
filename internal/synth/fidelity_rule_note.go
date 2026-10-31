package synth

import (
	"fmt"
	"strings"
)

// This file explains a flagged marginal on a field a structural rule
// writes (FR-18).
//
// The rule pass runs LAST and reaches GENERATED rows only — the source
// rows of a top-up are copied straight through — so a rule that sets,
// rewrites or nulls a field moves the synthetic marginal and never the
// source one. The report then shows a difference that looks like a
// generation fault and is often the rule doing exactly what it was
// written to do (a planted window, a derived band, a gated null). The
// note names the rules so a reader can tell the two apart; it does not
// decide which one it is.
//
// # What counts as flagged
//
// An effect size of at least "small" on Cohen's bands — the same
// magnitude the report tells a reader to read instead of the p-value.
// A set_* field is flagged when any one option is. A rejected test at
// "very small" is the large-n trivial difference and gets no note.
//
// # What counts as written
//
// A field named in any of a rule's `set`, `set_expr`, `set_null` slots,
// or in its `null_together` block after the first member (the first
// member's null state is the one copied, so it is read, not written).
// A gated rule counts like an ungated one: the note says which rules
// can move the field, and a rule that never fired is already its own
// warning. The listing is in declaration order, slots walked in
// ruleFieldSlots order, so the text never depends on a map seed.

// ruleNoteMagnitudes are the effect-size magnitudes that flag a marginal.
var ruleNoteMagnitudes = map[string]bool{"small": true, "medium": true, "large": true}

// AttachRuleNotes sets RuleNote on every flagged marginal (fields[] and
// set_fields[]) whose field a rule in spec writes. A spec without rules
// leaves the report untouched.
func AttachRuleNotes(report *FidelityReport, spec *Spec) {
	if report == nil || spec == nil || len(spec.Rules) == 0 {
		return
	}
	writers := ruleWriters(spec.Rules)
	if len(writers) == 0 {
		return
	}
	for _, e := range report.Fields {
		if e.EffectSize == nil || !ruleNoteMagnitudes[e.EffectSize.Magnitude] {
			continue
		}
		if w := writers[e.Field]; len(w) > 0 {
			e.RuleNote = ruleNoteText(w)
		}
	}
	for _, e := range report.SetFields {
		w := writers[e.Field]
		if len(w) == 0 {
			continue
		}
		for _, o := range e.Options {
			if o.EffectSize != nil && ruleNoteMagnitudes[o.EffectSize.Magnitude] {
				e.RuleNote = ruleNoteText(w)
				break
			}
		}
	}
}

// ruleWriters maps each field a rule writes to the rules writing it,
// each spelled ruleClaimDesc's way, in declaration order and without
// repeats.
func ruleWriters(rules []RuleSpec) map[string][]string {
	out := map[string][]string{}
	add := func(field, desc string) {
		for _, have := range out[field] {
			if have == desc {
				return
			}
		}
		out[field] = append(out[field], desc)
	}
	for i, r := range rules {
		for _, name := range sortedKeys(r.Set) {
			add(name, ruleClaimDesc(i, "set"))
		}
		for _, name := range sortedKeys(r.SetExpr) {
			add(name, ruleClaimDesc(i, "set_expr"))
		}
		for _, name := range r.SetNull {
			add(name, ruleClaimDesc(i, "set_null"))
		}
		if len(r.NullTogether) > 1 {
			for _, name := range r.NullTogether[1:] {
				add(name, ruleClaimDesc(i, "null_together"))
			}
		}
	}
	return out
}

// ruleNoteText is the single spelling of the note.
func ruleNoteText(writers []string) string {
	return fmt.Sprintf("written by %s; the rule pass runs last on generated rows only (source rows are copied through), so this difference may be the rule's effect rather than a generation fault",
		strings.Join(writers, ", "))
}
