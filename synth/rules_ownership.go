package synth

import (
	"fmt"
	"math"
	"math/rand/v2"
	"sort"
)

// This file is the NULL-OWNERSHIP half of the rule layer: what
// `{"owns_nulls": true}` does to a field's own null draw, and the
// generation-time report on whether the claim held.
//
// # The defect it closes
//
// `set_null` states WHICH ROWS a field is absent on. It says nothing
// about HOW OFTEN, and until this flag existed it could not: the field's
// own `null_rate` kept firing underneath the gate, so a rule that was
// exactly right about the rows was wrong about the marginal. The two
// compose as `g + (1-g)*r` — gate fires, or the field's own draw says
// null — where `r` is a rate CAPTURED FROM THE SOURCE and therefore
// already inclusive of whatever the gate removed. Measured on the
// motivating 122-field survey profile, a gate that nulls 50 perception
// fields on `aware == 0` took `regard` from a captured 0.2526 to a
// generated 0.4418, with every gated row correct and every rate wrong.
//
// # Suppression, not subtraction
//
// An owned field's sampler still DRAWS its null — the same two rng calls
// in the same order — and the verdict is discarded
// (ruleOwnedNullSampler). Zeroing the rate by rebuilding the sampler
// without its nullable wrapper would drop one draw per row per owned
// field and move the whole seeded stream, which would make the change
// unmeasurable against a baseline and would break the one property that
// makes a gate auditable: with the draw retained, ownership moves the
// null MASK of the owned fields and NOTHING else in the file.
//
// # Why zero rather than the residual
//
// The arithmetically exact suppression is the residual rate conditional
// on the rule not firing, `(r - g) / (1 - g)`. It needs `g`, the rule's
// firing probability. A `when` is an arbitrary predicate over a row, so
// no Spec knows it; measuring it would need a generation pass whose own
// rows are drawn from the very rates being corrected, and the estimate
// would then depend on RowCount. Zero is the only completion that adds
// no invented structure — the same assume-and-RECORD posture
// buildCorrelator takes for the pairs a correlation list never named.
// The recording is ownershipWarnings below: the gap is measured on the
// rows that actually reached the file and reported, never silent.
//
// Where `g` IS knowable the residual is bounded rather than unknown.
// `profile create --suggest-rules` admits a (gate, target) pair only
// when the target is null on at least gateHighNullRate of gated rows and
// at most gateLowNullRate (0.02) of OPEN rows — and P(null | open) is
// exactly the residual. So every candidate detection can emit is within
// 0.02 of exact under a zeroed draw, which is the distance
// nullRateDivergenceThreshold already calls immaterial, and is why
// detection sets the flag on every gating candidate it writes.

// ruleOwnedNullSampler wraps a field's own sampler and DISCARDS its null
// verdict, leaving the draws it consumed exactly as they were.
//
// The forwarding of the value and the swallowing of the boolean are the
// whole type. It exists as a wrapper rather than as a `rate = 0` on
// nullableSampler because the draw count is the contract: nullableSampler
// takes one inner draw plus one rng.Float64() whatever the rate, and a
// spec that declares ownership must consume the identical per-row
// sequence to the same spec without it. That is what lets a calibration
// run diff an owned cohort against an unowned one cell by cell and
// attribute every difference to the null mask.
type ruleOwnedNullSampler struct{ inner sampler }

func (r ruleOwnedNullSampler) next(rng *rand.Rand) (any, bool) {
	v, _ := r.inner.next(rng)
	return v, false
}

// ruleOwnedNullFields returns the set of fields whose own null draw the
// rules have claimed: every SetNull entry of every rule declaring
// OwnsNulls.
//
// It is a UNION across rules and deliberately not an exclusive claim.
// resolveConflicts' claim() arbitrates who WRITES a value, and `set_null`
// writes none — it removes one — so it has never claimed a field away
// from its own generation (E2-S2) and must not start here. "Two gates
// can each account for this field's absence" is a coherent statement
// about a survey (a block asked only of aware respondents, itself
// skipped for a screened-out wave) and needs no arbitration: the draw is
// suppressed once and either gate can null the field.
//
// Called from two places with the same input — buildSchema, to wrap the
// samplers, and compileRules, to set up the accounting — so both derive
// ownership from one predicate and cannot drift.
func ruleOwnedNullFields(rules []RuleSpec) map[string]bool {
	var out map[string]bool
	for _, r := range rules {
		if !r.OwnsNulls {
			continue
		}
		for _, name := range r.SetNull {
			if out == nil {
				out = map[string]bool{}
			}
			out[name] = true
		}
	}
	return out
}

// ownedNullField is one field whose own null draw a rule discarded, held
// with everything the end-of-run report needs: the rules that claimed
// it, and the rate that was thrown away.
type ownedNullField struct {
	field string
	// rules are the DECLARATION-ORDER indices of the rules that claimed
	// this field. Plural because ownership is a union; the message names
	// all of them, since a reader given one index of two would go and
	// edit a rule that is not the only thing nulling the field.
	rules []int
	// declared is the field's own FieldSpec.NullRate — the number the
	// claim discarded, and the number the realised rate is measured
	// against.
	declared float64
}

// nullOwnershipDivergenceThreshold is how far an owned field's REALISED
// null rate may sit from the `null_rate` its claim discarded before the
// run says so.
//
// It is the same 0.02 as nullRateDivergenceThreshold and for the same
// reason — both answer "a declared rate was thrown away; how far off can
// the replacement be before a reader would notice" — but it is its own
// constant because the two are thrown away by different mechanisms and
// could legitimately move apart: that one bounds a COPY from a sibling
// field, measurable at compile time from two declarations, while this
// one bounds a GATE's firing rate, which is not knowable until rows
// exist. Sharing a constant because the numbers agree today is how two
// unrelated tunings get coupled forever (the minLevelObservations /
// MinPairObservations split is the same call).
//
// 0.02 is also not arbitrary here: it is exactly gateLowNullRate, the
// residual a detected gate is allowed to leave on its open rows, so a
// candidate `--suggest-rules` emits is inside this band by construction
// and a detection-derived run is silent unless generation itself has
// drifted.
const nullOwnershipDivergenceThreshold = 0.02

// ownershipNoiseStdErrs is how many standard errors of the DECLARED rate
// the gap must also exceed.
//
// Without it the threshold above fires on sampling noise for small runs:
// a 200-row generation of a field whose declared rate is 0.25 has a
// standard error of 0.031, so an exactly-correct gate misses by more
// than 0.02 about half the time and the warning becomes a coin toss that
// trains readers to ignore it. Two standard errors matches
// FidelityReport.Models' flagging rule ("exceeds BOTH the tolerance AND
// twice the refit's own standard error"), which exists for the identical
// reason. At the 40,000 rows a calibration run uses the term is 0.004
// and the absolute threshold is what binds.
const ownershipNoiseStdErrs = 2

// maxOwnedNullWarnings caps how many diverging owned fields are named
// before the rest collapse into one counted line. It matches
// maxNeverFiredRuleWarnings / maxThinLevelWarnings (20) because those
// lines share one warning slice and a reader who has learnt what a
// truncated listing looks like in one kind should not have to learn a
// second shape. A single gate over a 50-field perception block is one
// finding said fifty times.
const maxOwnedNullWarnings = 20

// buildOwnedNullFields resolves the ownership union into the
// declaration-ordered, per-field accounting rows compileRules hangs on
// the applier. `order` is the schema field order, so the report is
// stable across runs and independent of Go map iteration.
func buildOwnedNullFields(rules []RuleSpec, order []string, byName map[string]FieldSpec) []ownedNullField {
	owned := ruleOwnedNullFields(rules)
	if len(owned) == 0 {
		return nil
	}
	claims := make(map[string][]int, len(owned))
	for i, r := range rules {
		if !r.OwnsNulls {
			continue
		}
		for _, name := range r.SetNull {
			if len(claims[name]) > 0 && claims[name][len(claims[name])-1] == i {
				// One rule naming a field twice in set_null claims it
				// once; the duplicate is the author's typo and not a
				// second owner.
				continue
			}
			claims[name] = append(claims[name], i)
		}
	}
	out := make([]ownedNullField, 0, len(owned))
	for _, name := range order {
		if !owned[name] {
			continue
		}
		f, ok := byName[name]
		if !ok {
			continue
		}
		out = append(out, ownedNullField{field: name, rules: claims[name], declared: f.NullRate})
	}
	return out
}

// noteOwnedNulls records, for the row just drawn, whether each owned
// field ended up null. Called at the END of apply, so it sees the rule
// pass's final state — which is the only state that matters: the file
// records the mask as the last rule left it.
func (a *ruleApplier) noteOwnedNulls(nullMask map[string]bool) {
	for i := range a.owned {
		a.rowOwnedNull[i] = nullMask[a.owned[i].field]
	}
}

// commitOwnedNulls folds the just-ACCEPTED row into the owned-field
// totals, for the reason commitRow does the same for firings: a
// constraint-rejected row was re-drawn and left nothing in the file, so
// counting it would make the reported rate undividable by the row count.
func (a *ruleApplier) commitOwnedNulls() {
	if a == nil {
		return
	}
	for i, isNull := range a.rowOwnedNull {
		if isNull {
			a.ownedNull[i]++
		}
	}
}

// ownershipWarnings reports every owned field whose realised null rate
// missed the `null_rate` its claim discarded.
//
// This is the honest half of the zero-rather-than-residual decision. The
// claim says "this rule is the only source of absence for this field";
// the run can check it, and two distinct failures share one shape:
//
//   - the gate explains only PART of the field's missingness, so the
//     realised rate comes out below the declared one (the residual the
//     suppression zeroed was not actually zero), and
//   - the claim was made on a field the rule never nulls at all, so the
//     realised rate is 0 against a declared 0.25 and ownership has done
//     nothing but delete a quarter of the field's absences.
//
// One message covers both because the number tells them apart, and
// because a reader acts on the same three things either way: widen the
// `when`, add the co-missing block the gate does not capture, or drop
// the flag and take the double count back.
//
// A gate that produces MORE absence than the field declared is reported
// by the same test — `{"owns_nulls": true}` over a field whose captured
// rate is 0 is a rule inventing missingness the source does not have,
// which is exactly as worth saying.
//
// Ordered worst-first and bounded, matching the thin-support listings:
// the tail of a 50-field block is the same finding continued, and the
// first twenty are the ones with something to say.
func (a *ruleApplier) ownershipWarnings(rows int) []string {
	if a == nil || len(a.owned) == 0 || rows <= 0 {
		return nil
	}
	type gap struct {
		i        int
		realised float64
		delta    float64
	}
	var bad []gap
	for i := range a.owned {
		realised := float64(a.ownedNull[i]) / float64(rows)
		delta := math.Abs(realised - a.owned[i].declared)
		if delta <= nullOwnershipDivergenceThreshold {
			continue
		}
		// The noise term uses the DECLARED rate rather than the realised
		// one: the question is whether a run of this size could have
		// produced this gap while the claim was exactly right, and under
		// that hypothesis the declared rate is the true one.
		p := a.owned[i].declared
		if se := math.Sqrt(p * (1 - p) / float64(rows)); delta <= ownershipNoiseStdErrs*se {
			continue
		}
		bad = append(bad, gap{i: i, realised: realised, delta: delta})
	}
	if len(bad) == 0 {
		return nil
	}
	sort.SliceStable(bad, func(x, y int) bool {
		if bad[x].delta != bad[y].delta {
			return bad[x].delta > bad[y].delta
		}
		return a.owned[bad[x].i].field < a.owned[bad[y].i].field
	})
	shown := bad
	if len(shown) > maxOwnedNullWarnings {
		shown = shown[:maxOwnedNullWarnings]
	}
	out := make([]string, 0, len(shown)+1)
	for _, g := range shown {
		o := a.owned[g.i]
		out = append(out, ownedNullDivergenceWarning(o, g.realised, rows))
	}
	if rest := len(bad) - len(shown); rest > 0 {
		out = append(out, fmt.Sprintf(
			"+%d further owned field(s) whose realised null rate misses the null_rate owns_nulls discarded; "+
				"listing suppressed to keep the warning list readable — the %d shown are the widest gaps",
			rest, len(shown)))
	}
	return out
}

// ownedNullDivergenceWarning renders one owned field's gap.
//
// It carries the discarded rate, the realised rate, the row count the
// realised rate is out of, and the rule indices that made the claim —
// everything needed to decide without re-reading the cohort, and the
// indices because a rule has no name of its own.
func ownedNullDivergenceWarning(o ownedNullField, realised float64, rows int) string {
	return fmt.Sprintf("%s the nulls of field %q: its own null_rate %.4g was discarded, "+
		"but the rule(s) nulled it on %.4g of %d generated row(s) — "+
		"the gate accounts for only part of the field's missingness; "+
		"widen its when, add the co-missing null_together block it does not capture, "+
		"or drop owns_nulls to restore the field's own draw",
		ownedNullClaimant(o.rules), o.field, o.declared, realised, rows)
}

// ownedNullClaimant renders the claiming rule indices as the subject of
// the sentence above. Singular and plural are spelled differently
// because a field owned by two rules is a materially different situation
// — editing one of them does not fix the gap — and the reader has to see
// that from the line.
func ownedNullClaimant(idx []int) string {
	if len(idx) == 1 {
		return fmt.Sprintf("rule %d owns", idx[0])
	}
	parts := make([]string, 0, len(idx))
	for _, i := range idx {
		parts = append(parts, fmt.Sprint(i))
	}
	return "rules " + joinComma(parts) + " own"
}

func joinComma(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ", "
		}
		out += p
	}
	return out
}
