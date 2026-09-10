package synth

import "fmt"

// claimTarget identifies one row-write target a drawRow post-processing
// stage can claim. Scalar targets (categorical-categorical,
// categorical-numeric, set-numeric's numeric side, correlation
// participants) key by field name alone (option left empty). A set_*
// field's individual declared Option (bit) keys by (field, option) —
// two different options on the SAME set_* field are two DIFFERENT
// targets, never one shared claim, because each option's transform
// writes only its own entry in the row's map[string]bool selection map.
type claimTarget struct {
	field  string
	option string
}

func (t claimTarget) String() string {
	if t.option == "" {
		return fmt.Sprintf("field %q", t.field)
	}
	return fmt.Sprintf("field %q option %q", t.field, t.option)
}

// conflictResolution is resolveConflicts' output: the six drawRow-stage
// relationship lists pruned of any later claimant whose target a
// higher-priority relationship already claimed, plus one warning string
// per excluded claimant.
type conflictResolution struct {
	catPairs     []CategoricalPairSpec
	catNumPairs  []CategoricalNumericPairSpec
	setSetPairs  []SetSetPairSpec
	setCatPairs  []SetCategoricalPairSpec
	setNumPairs  []SetNumericPairSpec
	correlations []CorrelationSpec
	// models are the Spec.Models entries that kept their target. A
	// model is claimed BEFORE any of the six stages above (see
	// resolveConflicts), so it can only lose to the captured-shape
	// pre-claim; everything else loses to it.
	models   []FieldModelSpec
	warnings []string
}

// resolveConflicts runs ONCE per Spec at generate() setup time (never
// per row — conflicts are static for a given spec, so per-row detection
// would be pure waste) and walks the exact priority order already
// implicit in drawRow's fixed stage sequence: catPairs -> catNumPairs ->
// setSetPairs -> setCatPairs -> setNumPairs -> corr. Each stage's
// transform unconditionally overwrites its target in drawRow, so without
// this pass, a target named by two relationships silently resolves to
// "whichever stage runs last" — this function decides, one time, which
// relationship naming a given target actually gets to run, and reports
// every one it drops instead of leaving the loss silent.
//
// A field carrying a linear model (Spec.Models, `profile create
// --fit-models`) is claimed FIRST, before any of the six stages: a
// model produces the field's value from ONE expression that already
// accounts for every predictor it was fitted on, so a later stage
// overwriting it would not layer extra structure on top — it would
// discard the model's whole account of the field and leave its
// surviving coefficients describing nothing that was actually drawn.
// This pre-claim is what retires the parallel numeric resample stages
// for a modelled field: a categorical-numeric or set-numeric pair
// naming it loses the claim and is reported exactly like any other
// exclusion. (A profile-derived spec carrying `models` already arrives
// with both numeric-target pair slots empty — see SpecFromProfile — so
// this fires only for a hand-authored spec that asks for both.)
//
// A field whose reconstructed distribution is a captured shape
// (DistMixture, `profile create --fit-shape`) is pre-claimed next,
// still before any of the six stages — the field's own independent
// sampler already draws its value before any conditional
// post-processing stage touches the row in drawRow, so nothing may
// overwrite it. This pre-claim is the single mechanism that replaces
// SpecFromProfile's two former special-cased silent skips
// (addCorrelation's DistMixture check, and the shape-vs-categorical-
// numeric skip): both are now ordinary claim conflicts against this
// pre-claim, reported exactly like any other exclusion below rather than
// dropped without a trace.
//
// The two orderings above are the E4-S1 change and the ONE line in this
// file that carries the story. The shape pre-claim used to run first
// and beat the model, on the reasoning that a captured mixture had no
// closed-form quantile a linear predictor could ride. That was true of
// the code, not of the construction: value = Q(Phi(mu + sigma*z))
// admits an arbitrary marginal in Q — it is exactly what a lognormal
// target already does — so once the mixture acquired a Q
// (synth/mixture_quantile.go) the exclusivity had nothing left holding
// it up. It cost the motivating cohort every conditioning relationship
// on the four fields whose shapes were most worth fitting.
//
// So the two COMPOSE and neither is a conflict: a shape-fitted field
// carrying a model is claimed by the model, draws through its model,
// and gets its fitted mixture as Q — no warning, because nothing was
// dropped. The shape pre-claim below is therefore expressed as an
// ordinary claim() rather than a direct map write: it still owns every
// DistMixture field the model claim did not take, so an UNMODELLED
// shape-fitted field keeps today's behaviour exactly (pairs and
// correlations naming it are still excluded, still with the "captured
// shape (--fit-shape)" wording), and it silently yields the ones it
// did. A pair naming a shape-fitted MODELLED field is still dropped —
// it was always going to be — but now names "linear model" as the
// owner, which is the truthful answer.
//
// Spec.Correlations is treated as ONE combined claimant across all of
// its participant fields — it resolves jointly via a single
// Cholesky-based draw, not one draw per pair, so losing one participant
// to an earlier claim must not drop the whole matrix. Any participant
// already claimed by an earlier stage (or by the shape-fit pre-claim) is
// excluded individually: every CorrelationSpec entry naming it is
// dropped, one warning names the excluded field, and the remaining
// participants still correlate — buildCorrelator rebuilds its Cholesky
// factor from whatever pairs survive.
func resolveConflicts(s *Spec) conflictResolution {
	var res conflictResolution
	claims := make(map[claimTarget]string, len(s.Fields))

	// claim registers desc as the target's owner iff nothing owns it yet.
	// Returns false (and leaves the existing owner in place) on conflict.
	claim := func(t claimTarget, desc string) bool {
		if _, taken := claims[t]; taken {
			return false
		}
		claims[t] = desc
		return true
	}

	conflict := func(t claimTarget, dropped string) {
		res.warnings = append(res.warnings, fmt.Sprintf(
			"conditional relationship conflict: %s is already claimed by %s; dropping %s",
			t, claims[t], dropped))
	}

	// modelClaimed records which targets the model pre-claim below won,
	// so the correlation arm can tell a model-owned field apart from a
	// pair-owned one and word its warning accordingly.
	modelClaimed := make(map[string]bool, len(s.Models))
	for _, m := range s.Models {
		t := claimTarget{field: m.Field}
		if claim(t, "linear model") {
			res.models = append(res.models, m)
			modelClaimed[m.Field] = true
		} else {
			conflict(t, "linear model")
		}
	}

	// The captured-shape pre-claim, running SECOND so a shape-fitted
	// field carrying a model composes with it instead of displacing it
	// — see this function's doc. A shape-fitted field with no model is
	// claimed here exactly as it always was, and everything downstream
	// behaves identically for it.
	for _, fs := range s.Fields {
		if fs.Distribution == DistMixture {
			claim(claimTarget{field: fs.Name}, "captured shape (--fit-shape)")
		}
	}

	for _, p := range s.CategoricalPairs {
		t := claimTarget{field: p.B}
		desc := fmt.Sprintf("categorical pair (%s -> %s)", p.A, p.B)
		if claim(t, desc) {
			res.catPairs = append(res.catPairs, p)
		} else {
			conflict(t, desc)
		}
	}

	for _, p := range s.CategoricalNumericPairs {
		t := claimTarget{field: p.B}
		desc := fmt.Sprintf("categorical-numeric pair (%s -> %s)", p.A, p.B)
		if claim(t, desc) {
			res.catNumPairs = append(res.catNumPairs, p)
		} else {
			conflict(t, desc)
		}
	}

	for _, p := range s.SetSetPairs {
		t := claimTarget{field: p.SetB, option: p.OptionB}
		desc := fmt.Sprintf("set-set pair (%s.%s -> %s.%s)", p.SetA, p.OptionA, p.SetB, p.OptionB)
		if claim(t, desc) {
			res.setSetPairs = append(res.setSetPairs, p)
		} else {
			conflict(t, desc)
		}
	}

	for _, p := range s.SetCategoricalPairs {
		t := claimTarget{field: p.Set, option: p.Option}
		desc := fmt.Sprintf("set-categorical pair (%s.%s <- %s)", p.Set, p.Option, p.Categorical)
		if claim(t, desc) {
			res.setCatPairs = append(res.setCatPairs, p)
		} else {
			conflict(t, desc)
		}
	}

	for _, p := range s.SetNumericPairs {
		t := claimTarget{field: p.Numeric}
		desc := fmt.Sprintf("set-numeric pair (%s.%s -> %s)", p.Set, p.Option, p.Numeric)
		if claim(t, desc) {
			res.setNumPairs = append(res.setNumPairs, p)
		} else {
			conflict(t, desc)
		}
	}

	if len(s.Correlations) > 0 {
		var participants []string
		seenParticipant := make(map[string]bool, len(s.Correlations)*2)
		for _, c := range s.Correlations {
			for _, f := range [2]string{c.A, c.B} {
				if !seenParticipant[f] {
					seenParticipant[f] = true
					participants = append(participants, f)
				}
			}
		}

		excluded := make(map[string]bool, len(participants))
		for _, f := range participants {
			t := claimTarget{field: f}
			if _, taken := claims[t]; !taken {
				continue
			}
			excluded[f] = true
			if modelClaimed[f] {
				// NOT an arbitration outcome, and deliberately worded so
				// it cannot be read as one — which is why it does not go
				// through conflict() and does not carry the
				// "conditional relationship conflict" prefix every real
				// claim loss carries.
				//
				// Spec.Correlations is a correlation between two fields'
				// VALUES, and the only way to realize one is to draw
				// both values from a shared copula — i.e. to overwrite
				// them. A modelled field's value is the model's to
				// produce, so there is no version of this request the
				// copula can honour without deleting the model's whole
				// account of the field.
				//
				// Rerouting the figure into the model's RESIDUAL instead
				// is equally wrong and more insidious: a value-scale
				// correlation between two fields sharing predictors
				// already contains those predictors' joint effect, so
				// applying it to the residual applies them a second time
				// (see synth/residual_corr.go). The residual scale has
				// its own measured section for exactly this reason, and
				// the remedy named below is the honest one rather than a
				// deferral — correlated residuals DID land, they simply
				// read a different number than this slot carries.
				res.warnings = append(res.warnings, fmt.Sprintf(
					"pairwise correlation naming %s is not applied: the field is drawn from its linear model, which owns its value, and a value-scale correlation would double-count the predictors the two fields share; capture residual correlations (`profile create --residual-correlations`) to correlate a modelled field. The remaining participants still correlate",
					t))
				continue
			}
			conflict(t, "pairwise correlation (excluding this field; remaining participants still correlate)")
		}
		for _, f := range participants {
			if !excluded[f] {
				claims[claimTarget{field: f}] = "pairwise correlation"
			}
		}
		for _, c := range s.Correlations {
			if excluded[c.A] || excluded[c.B] {
				continue
			}
			res.correlations = append(res.correlations, c)
		}
	}

	return res
}

// ResolveConflicts is the exported form of resolveConflicts: it prunes
// spec's captured conditional-pairing relationships down to exactly the
// subset generate() applies at row-draw time, in the same priority
// order (captured-shape pre-claim, then catPairs -> catNumPairs ->
// setSetPairs -> setCatPairs -> setNumPairs -> correlations).
//
// Spec.Models is arbitrated by the same pass but deliberately NOT
// returned here: this function's contract is the six PAIRWISE
// relationship lists a fidelity report scores, and a model is not a
// pairwise relationship. Its arbitration is still visible through this
// call's effect on the lists — a correlation or numeric-target pair the
// model pre-claim excluded is absent from what comes back.
//
// resolveConflicts is pure and deterministic in spec (conflicts are
// static for a given Spec — see resolveConflicts's own doc), so calling
// it again here against the same *Spec generate() was given reproduces
// the identical resolution without threading a return value through
// Result. Exists for callers building a fidelity report against a
// Spec's FULL captured relationships — e.g. writeSynthFidelityReport —
// that must restrict their comparison to relationships generation
// actually used: fidelity-checking a pair generation dropped would
// score a "delta" for a relationship that was never modeled, which is
// misleading rather than merely wasteful (see skills/synthetic-data.md,
// Fidelity report).
func ResolveConflicts(s *Spec) (
	catPairs []CategoricalPairSpec,
	catNumPairs []CategoricalNumericPairSpec,
	setCatPairs []SetCategoricalPairSpec,
	setNumPairs []SetNumericPairSpec,
	setSetPairs []SetSetPairSpec,
	correlations []CorrelationSpec,
) {
	r := resolveConflicts(s)
	return r.catPairs, r.catNumPairs, r.setCatPairs, r.setNumPairs, r.setSetPairs, r.correlations
}
