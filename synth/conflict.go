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
	warnings     []string
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
// A field whose reconstructed distribution is a captured shape
// (DistMixture, `profile create --fit-shape`) is pre-claimed under
// "captured shape (--fit-shape)" before any of the six stages below run
// — the field's own independent sampler already draws its value before
// any conditional post-processing stage touches the row in drawRow, so
// nothing may overwrite it. This pre-claim is the single mechanism that
// replaces SpecFromProfile's two former special-cased silent skips
// (addCorrelation's DistMixture check, and the shape-vs-categorical-
// numeric skip): both are now ordinary claim conflicts against this
// pre-claim, reported exactly like any other exclusion below rather than
// dropped without a trace.
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

	for _, fs := range s.Fields {
		if fs.Distribution == DistMixture {
			claims[claimTarget{field: fs.Name}] = "captured shape (--fit-shape)"
		}
	}

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
			if _, taken := claims[t]; taken {
				excluded[f] = true
				conflict(t, "pairwise correlation (excluding this field; remaining participants still correlate)")
			}
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
