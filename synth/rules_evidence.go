package synth

// RuleEvidence is the one slot on RuleSpec that DOES NOTHING.
//
// Read that first, because every other member of RuleSpec executes and
// this is exactly the place a later reader will assume execution. The
// rule pass (synth/rules_apply.go) never looks at it, compileRules never
// compiles it, validateRules never reads a field of it, and nothing in
// it can name a field, an expression or a value that changes a generated
// byte. It is carried so that a PROPOSED rule and the measurement that
// proposed it cannot be separated.
//
// # Why it lives on RuleSpec rather than beside it
//
// `profile create --suggest-rules` writes a file that
// `synth from-profile --rules` must consume WITHOUT MODIFICATION, and
// E1-S2 fixed that format as a BARE JSON ARRAY of rule objects. That
// rules out a sibling metadata block: wrapping the array in
// {"rules": [...], "evidence": [...]} is precisely the shape E2-S1's
// loader refuses, deliberately, because silently reading zero rules out
// of a wrapper object is the failure the eager-validation rule exists to
// remove. JSON has no comments, so the third option does not exist.
//
// A sidecar file was the remaining alternative and was rejected on the
// editing workflow: the file exists to be EDITED — an analyst deletes
// the candidates they do not believe and corrects the `when` of the ones
// they do — and evidence in a second document drifts from its rule on
// the first deletion, silently, in the direction of describing a rule
// that is no longer there. Carried on the rule, a deleted candidate
// takes its evidence with it.
//
// # Why the key is spelled `_evidence`
//
// The leading underscore is this repo's existing marker for a block that
// is documentation rather than payload — `examples/<dir>/*.json` carries
// `_meta` for the same reason, and the template renderer refuses a body
// that still has one attached. A reader scanning the file sees four
// executable keys spelled like verbs and one spelled like a note.
//
// # What it is NOT allowed to become
//
// A slot generation reads. If a future detector needs to influence
// generation it declares an ordinary executable slot and validates it;
// widening this one would make a document's meaning depend on a block
// authors are told they may delete freely.
//
// Hand-authoring it is harmless and unvalidated: an author who writes
// nonsense here gets a rule that behaves exactly as it would with the
// key absent. A rule carrying ONLY this key still fails
// PULSE_SYNTH_RULE_EMPTY, because it declares no action — see
// validateRule, which counts the four action slots and not this one.
type RuleEvidence struct {
	// Detector names the measurement that proposed the rule. One value
	// per detector kind, so a reader can tell a gating candidate apart
	// from the co-missing-block and exact-dependency candidates later
	// detectors add to the same file.
	Detector string `json:"detector"`

	// Note is the prose caveat, interpolated with this candidate's own
	// gate. It says what the measurement IS and what it is not, because
	// detection finds the STATISTICAL gate and a human knows the
	// SEMANTIC one, and those are routinely different fields that move
	// together.
	Note string `json:"note"`

	// GateField is the field whose levels split the targets' null rate.
	//
	// This and the four slots below are the GATING detector's own, and
	// carry `omitempty` so a co-missing candidate — which has no gate
	// field, no levels and no targets — does not write five keys spelled
	// like measurements with nothing measured in them. A gating
	// candidate populates every one of them by construction (a gate has
	// at least one level on each side and at least one target), so the
	// wire shape of a gating candidate is unchanged by the tags; the
	// keys that are shared with the co-missing detector deliberately do
	// NOT carry omitempty, because a real 0 there is a real measurement.
	GateField string `json:"gate_field,omitempty"`
	// GatedLevels and OpenLevels are that split, as the level spellings
	// the `when` predicate tests. The null pseudo-level is spelled
	// "(null)" here and reaches the predicate as isnull(field).
	GatedLevels []string `json:"gated_levels,omitempty"`
	OpenLevels  []string `json:"open_levels,omitempty"`

	// Levels carries the per-level support and the mean conditional null
	// rate across this candidate's targets — the shape of the split, at
	// a glance, before a reader descends into Targets.
	Levels []RuleEvidenceLevel `json:"levels,omitempty"`

	// Targets carries the per-target conditional null rates: what the
	// candidate actually measured, one row per field it proposes to
	// null.
	Targets []RuleEvidenceTarget `json:"targets,omitempty"`

	// Block carries the CO-MISSING detector's members: the fields a
	// null_together candidate names, each with the type, null count and
	// null rate that admitted it.
	//
	// It is a slot on the one evidence type rather than a second
	// evidence type because a reader of the candidate file walks one
	// shape and reads `detector` to know which half of it is populated —
	// and because the inertness contract (RuleEvidence's doc, and
	// TestRuleEvidence_IsInert) is a property of the whole struct, so a
	// parallel type would need its own copy of that guarantee and could
	// drift out of it silently.
	//
	// Agreement is each member's null-set overlap with the FIRST member
	// and is 1 for every member of an emitted block, by the admission
	// rule. It is carried anyway, and the constant is the point: it is
	// the detector's central claim — identical PATTERN, not merely an
	// identical rate — stated on the wire where a reader can check it
	// against the null counts beside it, rather than left implicit in
	// the fact that the candidate was emitted at all.
	Block []RuleEvidenceBlockMember `json:"block,omitempty"`

	// SourceField and SourceLevels are the EXACT-DEPENDENCY detector's
	// own: the single field every target of the candidate is a function
	// of, and the number of levels it was observed to take.
	//
	// It is deliberately NOT GateField reused. A gate field's levels
	// decide whether a target is ABSENT; a source field's levels decide
	// what a present target's VALUE is. They are different claims about
	// different halves of the record, and one key carrying both would
	// make `detector` the only thing standing between a reader and the
	// wrong reading.
	SourceField  string `json:"source_field,omitempty"`
	SourceLevels int    `json:"source_levels,omitempty"`

	// Dependency carries the measured lookup, one entry per target: the
	// source level to target value table the expression was READ OFF,
	// and the rendering form chosen.
	//
	// It is a slot on the one evidence type rather than a third evidence
	// type, exactly as Block is, so a reader walks one shape and reads
	// `detector` to know which part of it is populated — and so the
	// inertness contract (this type's own doc, and
	// TestRuleEvidence_IsInert) stays a property of the whole struct
	// rather than something a parallel type could drift out of.
	Dependency []RuleEvidenceDependency `json:"dependency,omitempty"`

	// RowsObserved is the cohort rows the detection saw (every row lands
	// in exactly one level, the null pseudo-level included).
	RowsObserved int `json:"rows_observed"`
	// RowsAffected is the rows the `when` would select — the row count
	// this rule would change. For a co-missing candidate, which carries
	// no `when`, it is the rows on which the block is null. For an
	// exact-dependency candidate, which also carries no `when`, it is
	// the CO-PRESENT rows: the rows the lookup was measured on, which
	// the admission rule (identical null patterns) makes exactly the
	// rows either field is present on.
	RowsAffected int `json:"rows_affected"`
	// GatedShare is RowsAffected / RowsObserved. It is the figure to
	// compare against each target's own null_rate in the profile
	// document: for a real gate the two agree, because a target null on
	// every gated row and present on every open one has exactly the
	// gated share as its marginal null rate. That agreement is how this
	// cohort's gate was first identified by hand.
	GatedShare float64 `json:"gated_share"`
	// MaxNullRateDeviation is the largest |null_rate - GatedShare| over
	// the targets: how far the agreement above falls short.
	//
	// The figure that identified the motivating cohort's gate BY HAND
	// was exactly this agreement — `1 - mean(aware)` = 0.2526093295989762
	// matching 50 fields' captured null_rate to ten digits — and the
	// obvious move is to make it the detector's lead ranking signal. It
	// is carried, and it is deliberately NOT the lead signal, because it
	// is ARITHMETICALLY IMPLIED by the split test rather than
	// independent of it: if P(null | gated) >= gateHighNullRate and
	// P(null | open) <= gateLowNullRate then the marginal null rate is
	// GatedShare x ~1 + (1 - GatedShare) x ~0, so it lies within the
	// thresholds of GatedShare for EVERY candidate the detector admits.
	// Ranking by it would be ranking by rounding error, and
	// TestSuggestRules_DeviationIsImpliedByTheSplitTest pins that.
	//
	// What it is genuinely for is CHECKING WITHOUT RE-MEASURING. A
	// reader holding the profile document compares GatedShare against
	// the targets' own null_rate, which is how the relationship was
	// found in the first place; and a deviation that is unexpectedly
	// large — several targets sitting near rather than at the open
	// boundary — is the signal that a candidate is association wearing
	// a gate's clothes.
	MaxNullRateDeviation float64 `json:"max_null_rate_deviation"`

	// MinLevelSupport is the thinnest level's row count, and ThinSupport
	// says whether it fell below minGateLevelSupport. A thin candidate
	// still SHIPS — suppressing it would hide the finding rather than
	// qualify it — so this is the qualification.
	//
	// A co-missing candidate has two arms rather than levels — the rows
	// where the block is null and the rows where it is present — and
	// this is the thinner of them, which asks the same question of the
	// same threshold: a block observed present on four rows is not a
	// measurement of a question block. An exact-dependency candidate's
	// is the thinnest SOURCE level: a mapping arm resting on four rows
	// is a coincidence, not a derivation.
	MinLevelSupport int  `json:"min_level_support"`
	ThinSupport     bool `json:"thin_support,omitempty"`
}

// RuleEvidenceLevel is one level of the gate field.
type RuleEvidenceLevel struct {
	// Level is the level's spelling, matching an entry of
	// RuleEvidence.GatedLevels or .OpenLevels.
	Level string `json:"level"`
	// Side is "gated" or "open".
	Side string `json:"side"`
	// N is the rows on which the gate held this level: the support
	// behind every conditional rate computed at it.
	N int `json:"n"`
	// MeanTargetNullRate is the mean of P(target null | this level)
	// across this candidate's targets. At a gated level it is ~1 and at
	// an open one ~0, by the admission rule.
	MeanTargetNullRate float64 `json:"mean_target_null_rate"`
}

// RuleEvidenceTarget is one field the candidate proposes to null, with
// the conditional rates that proposed it.
type RuleEvidenceTarget struct {
	Field string `json:"field"`
	// NullRate is the field's own marginal null rate over RowsObserved —
	// the same figure the profile document carries for it, so a reader
	// can check the candidate against the document without re-measuring.
	NullRate float64 `json:"null_rate"`
	// GatedNullRate and OpenNullRate are P(null | gated levels) and
	// P(null | open levels), pooled over the levels on each side.
	GatedNullRate float64 `json:"gated_null_rate"`
	OpenNullRate  float64 `json:"open_null_rate"`
}

// RuleEvidenceBlockMember is one field of a co-missing block.
type RuleEvidenceBlockMember struct {
	Field string `json:"field"`
	// Type is the field's `.pulse` type. It is carried because a block
	// mixing a u4 with three packed_bool flags is a derived-partition
	// shape and a block of twenty categoricals is a question battery,
	// and a reader deciding whether to believe a proposal wants that
	// without opening the profile document.
	Type string `json:"type"`
	// NullCount and NullRate are the member's own marginal. They are
	// identical across an emitted block by the admission rule, which is
	// what makes the member order arbitrary and what makes
	// max_null_rate_deviation 0.
	NullCount int     `json:"null_count"`
	NullRate  float64 `json:"null_rate"`
	// Agreement is the overlap of this member's null set with the FIRST
	// member's: of the rows where either is null, the share where both
	// are. 1 exactly when the two are null on the same rows.
	Agreement float64 `json:"agreement"`
}

// RuleEvidenceDependency is one target of an exact-dependency
// candidate, with the measured lookup that proposed it.
//
// The rendered EXPRESSION is deliberately absent: it is already the
// rule's own `set_expr` entry for this field, and a second copy would go
// stale the first time an analyst edits the one that executes — in the
// direction of describing a rule that is no longer there, which is the
// exact failure the whole `_evidence` design (see RuleEvidence) exists
// to avoid.
type RuleEvidenceDependency struct {
	Field string `json:"field"`
	// Type is the target's `.pulse` type, carried for Block's reason: a
	// reader deciding whether to believe a derived-flag proposal wants
	// to know whether the target is one bit or a small integer without
	// opening the profile document.
	Type string `json:"type"`
	// Form names the RENDERING judgement — which reading of the lookup
	// below the expression is. It is the one thing the expression itself
	// cannot say, and the thing most worth checking: `threshold` and
	// `threshold_chain` are TOTAL functions of the source (an
	// unobserved value lands in the nearest band), while `membership`,
	// `membership_complement` and `enumeration_chain` fall to a default
	// arm.
	Form string `json:"form"`
	// Mapping is the measured lookup itself, in the source's own level
	// order, with the rows behind each arm. Exceptions is 0 for every
	// emitted candidate BY THE ADMISSION RULE, and is carried anyway for
	// the reason Block's Agreement is: it is the detector's central
	// claim — this is a FUNCTION, not a tendency — stated on the wire
	// where a reader can check it against the supports beside it rather
	// than left implicit in the fact that the candidate was emitted.
	Mapping    []RuleEvidenceMapValue `json:"mapping"`
	Exceptions int                    `json:"exceptions"`
}

// RuleEvidenceMapValue is one arm of a measured lookup: a level of the
// source, the value the target took at it, and the rows behind it.
type RuleEvidenceMapValue struct {
	Level string `json:"level"`
	// Value is the target's value in its own JSON shape — a bool for a
	// packed_bool, a number for a u4 — rather than a uniform string, so
	// a reader can compare it against the profile document's own
	// marginal for the field without decoding a convention.
	Value any `json:"value"`
	N     int `json:"n"`
}
