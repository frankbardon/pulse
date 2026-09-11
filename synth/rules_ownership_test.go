package synth_test

import (
	stderrors "errors"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/fs"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/synth"
)

// ownershipSpec is the committed, shape-equivalent form of the real
// 122-field survey profile's motivating gate: a never-null screener
// (`aware`) and a perception field (`regard`) that is asked only of the
// respondents the screener admits.
//
// `regard`'s declared null_rate is the MARGINAL the profiler captured,
// so it already includes every row the gate removed — which is the whole
// arithmetic this file is about. `tail` is drawn after `regard` and is
// the RNG probe: if suppression changed the number of draws the row
// consumes, every later row's tail would shift.
func ownershipSpec(rows int, awareP, regardNullRate float64, rules []synth.RuleSpec) *synth.Spec {
	return &synth.Spec{
		RowCount: rows,
		Fields: []synth.FieldSpec{
			{Name: "id", Type: "u32", Distribution: synth.DistMonotonicFrom,
				Params: map[string]any{"start": 1.0}},
			{Name: "aware", Type: "packed_bool", Distribution: synth.DistBernoulli,
				Params: map[string]any{"p": awareP}},
			{Name: "wave", Type: "packed_bool", Distribution: synth.DistBernoulli,
				Params: map[string]any{"p": 0.5}},
			{Name: "regard", Type: "u4", Nullable: true, NullRate: regardNullRate,
				Distribution: synth.DistUniform, Params: map[string]any{"min": 0.0, "max": 10.999}},
			{Name: "tail", Type: "u4", Distribution: synth.DistUniform,
				Params: map[string]any{"min": 0.0, "max": 15.999}},
		},
		Rules: rules,
	}
}

func gateRule(owns bool, when string, targets ...string) synth.RuleSpec {
	return synth.RuleSpec{When: when, SetNull: targets, OwnsNulls: owns}
}

// nullRateOf returns the share of rows on which `name` is absent.
func nullRateOf(t *testing.T, data []byte, name string) float64 {
	t.Helper()
	_, nulls := readFieldRows(t, data, name)
	if len(nulls) == 0 {
		t.Fatalf("field %q: no rows", name)
	}
	n := 0
	for _, isNull := range nulls {
		if isNull {
			n++
		}
	}
	return float64(n) / float64(len(nulls))
}

// TestOwnsNulls_KeepsTheGateExactAndTheMarginalCaptured is FU-01's
// headline claim. Both halves are asserted over ONE pair of generated
// files because each is trivially satisfiable by abandoning the other —
// a rule that nulls nothing keeps the marginal, and a rule that nulls
// every gated row and leaves the field's own draw alone keeps the gate:
//
//  1. THE GATE IS EXACT. `regard` is absent on every gated row and
//     present on every open one — the property `set_null` already had,
//     and which suppression must not cost.
//  2. THE MARGINAL IS THE CAPTURED ONE. `regard`'s generated null rate
//     is the gate's own firing rate, which is the number the profiler
//     measured, rather than g + (1-g)*r.
//  3. THE FIXTURE CAN SEE THE DEFECT. The identical spec WITHOUT the
//     flag reproduces the double count, so claim 2 is a measurement and
//     not a tautology about a field that was never doubly nulled.
func TestOwnsNulls_KeepsTheGateExactAndTheMarginalCaptured(t *testing.T) {
	const (
		rows    = 20000
		awareP  = 0.75 // so P(aware == 0) = 0.25
		capture = 0.25 // the marginal the gate fully explains
	)
	owned, _, err := synth.SynthBytes(
		ownershipSpec(rows, awareP, capture, []synth.RuleSpec{gateRule(true, "aware == 0", "regard")}),
		synth.Options{Seed: 4242})
	if err != nil {
		t.Fatalf("owned: %v", err)
	}
	unowned, _, err := synth.SynthBytes(
		ownershipSpec(rows, awareP, capture, []synth.RuleSpec{gateRule(false, "aware == 0", "regard")}),
		synth.Options{Seed: 4242})
	if err != nil {
		t.Fatalf("unowned: %v", err)
	}

	aware, _ := readFieldRows(t, owned, "aware")
	_, regardNull := readFieldRows(t, owned, "regard")
	gated, gatedNull, openRows, openNull := 0, 0, 0, 0
	for i := range aware {
		if aware[i] == 0 {
			gated++
			if regardNull[i] {
				gatedNull++
			}
			continue
		}
		openRows++
		if regardNull[i] {
			openNull++
		}
	}
	if gated == 0 || openRows == 0 {
		t.Fatalf("degenerate fixture: gated=%d open=%d", gated, openRows)
	}
	if gatedNull != gated {
		t.Errorf("claim 1: gate no longer exact: %d of %d gated rows null", gatedNull, gated)
	}
	if openNull != 0 {
		t.Errorf("claim 1: %d of %d OPEN rows null; owns_nulls must suppress the field's own draw, "+
			"not leave it firing", openNull, openRows)
	}

	got := nullRateOf(t, owned, "regard")
	if math.Abs(got-capture) > 0.01 {
		t.Errorf("claim 2: owned null rate %.4f, want the captured %.4f (+-0.01)", got, capture)
	}

	doubled := nullRateOf(t, unowned, "regard")
	want := capture + (1-capture)*capture // g + (1-g)*r
	if math.Abs(doubled-want) > 0.015 {
		t.Errorf("claim 3: unowned null rate %.4f, want the double count %.4f (+-0.015); "+
			"the fixture cannot see the defect", doubled, want)
	}
	if doubled-got < 0.1 {
		t.Errorf("claim 3: owned %.4f and unowned %.4f are not far enough apart for claim 2 to mean anything",
			got, doubled)
	}
}

// TestOwnsNulls_MovesTheNullMaskAndNothingElse pins the SUPPRESSION
// mechanism rather than its effect. The owned field still draws its
// null — same calls, same order — and only the verdict is discarded, so
// every other field's value and null sequence must be identical between
// an owned run and an unowned one at the same seed.
//
// Rebuilding the sampler without its nullable wrapper would zero the
// rate just as well and drop one rng.Float64() per row, shifting every
// later draw; `tail` and `wave` are drawn after `regard` precisely so
// that shift is visible here.
func TestOwnsNulls_MovesTheNullMaskAndNothingElse(t *testing.T) {
	const rows = 5000
	rules := func(owns bool) []synth.RuleSpec {
		return []synth.RuleSpec{gateRule(owns, "aware == 0", "regard")}
	}
	owned, _, err := synth.SynthBytes(ownershipSpec(rows, 0.75, 0.25, rules(true)), synth.Options{Seed: 7})
	if err != nil {
		t.Fatalf("owned: %v", err)
	}
	unowned, _, err := synth.SynthBytes(ownershipSpec(rows, 0.75, 0.25, rules(false)), synth.Options{Seed: 7})
	if err != nil {
		t.Fatalf("unowned: %v", err)
	}
	for _, name := range []string{"id", "aware", "wave", "tail"} {
		aVals, aNulls := readFieldRows(t, owned, name)
		bVals, bNulls := readFieldRows(t, unowned, name)
		if len(aVals) != len(bVals) {
			t.Fatalf("field %q: row counts differ (%d vs %d)", name, len(aVals), len(bVals))
		}
		for i := range aVals {
			if aVals[i] != bVals[i] || aNulls[i] != bNulls[i] {
				t.Fatalf("field %q row %d: owned (%v,%v) != unowned (%v,%v); "+
					"suppression moved the seeded stream", name, i, aVals[i], aNulls[i], bVals[i], bNulls[i])
			}
		}
	}
	// And the owned field's VALUE is untouched wherever neither run
	// nulls it: suppression removes absences, it does not redraw.
	rVals, rNulls := readFieldRows(t, owned, "regard")
	uVals, uNulls := readFieldRows(t, unowned, "regard")
	compared := 0
	for i := range rVals {
		if rNulls[i] || uNulls[i] {
			continue
		}
		compared++
		if rVals[i] != uVals[i] {
			t.Fatalf("regard row %d: owned %v != unowned %v", i, rVals[i], uVals[i])
		}
	}
	if compared == 0 {
		t.Fatal("no commonly-present rows: the comparison asserted nothing")
	}
}

// TestOwnsNulls_AbsentFlagIsTheUnchangedPath is the byte-identity half.
// A rule that does NOT declare ownership must leave every sampler
// exactly as a rules-free spec built it, so the whole file matches a run
// with the rule removed apart from the nulls the rule itself adds.
func TestOwnsNulls_AbsentFlagIsTheUnchangedPath(t *testing.T) {
	const rows = 4000
	ruled, _, err := synth.SynthBytes(
		ownershipSpec(rows, 0.75, 0.25, []synth.RuleSpec{gateRule(false, "aware == 0", "regard")}),
		synth.Options{Seed: 99})
	if err != nil {
		t.Fatalf("ruled: %v", err)
	}
	bare, _, err := synth.SynthBytes(ownershipSpec(rows, 0.75, 0.25, nil), synth.Options{Seed: 99})
	if err != nil {
		t.Fatalf("bare: %v", err)
	}
	for _, name := range []string{"id", "aware", "wave", "tail"} {
		aVals, aNulls := readFieldRows(t, ruled, name)
		bVals, bNulls := readFieldRows(t, bare, name)
		for i := range aVals {
			if aVals[i] != bVals[i] || aNulls[i] != bNulls[i] {
				t.Fatalf("field %q row %d differs between a set_null-without-ownership run and a rules-free one",
					name, i)
			}
		}
	}
	// The un-owned run still double counts, which is what makes the
	// comparison above a statement about the FLAG rather than about a
	// rule that happens to do nothing.
	if got := nullRateOf(t, ruled, "regard"); got < 0.35 {
		t.Errorf("un-owned null rate %.4f: expected the double count (~0.4375)", got)
	}
}

// TestOwnsNulls_RefusedWithNoSetNull pins the one new spec-parse fault.
// An ownership claim with no referent is silent if it is merely ignored:
// the fields keep the double count and nothing says the flag did
// nothing.
func TestOwnsNulls_RefusedWithNoSetNull(t *testing.T) {
	spec := ownershipSpec(100, 0.75, 0.25, []synth.RuleSpec{
		{When: "aware == 0", Set: map[string]any{"wave": 1.0}, OwnsNulls: true},
	})
	_, _, err := synth.SynthBytes(spec, synth.Options{Seed: 1})
	if err == nil {
		t.Fatal("expected a refusal for owns_nulls with no set_null")
	}
	var coded *errors.CodedError
	if !stderrors.As(err, &coded) {
		t.Fatalf("error is not coded: %v", err)
	}
	if coded.Code != errors.PULSE_SYNTH_RULE_OWNERSHIP_INVALID {
		t.Errorf("code = %s, want PULSE_SYNTH_RULE_OWNERSHIP_INVALID", coded.Code)
	}
	if got := coded.Details[errors.DetailSynthRuleSlot]; got != "owns_nulls" {
		t.Errorf("details slot = %v, want owns_nulls", got)
	}
	if got := coded.Details[errors.DetailSynthRule]; got != 0 {
		t.Errorf("details rule = %v, want 0", got)
	}
	// A rule declaring it WITH a set_null field is accepted, so the
	// refusal is about the empty claim and not about the flag.
	if _, _, err := synth.SynthBytes(
		ownershipSpec(100, 0.75, 0.25, []synth.RuleSpec{gateRule(true, "aware == 0", "regard")}),
		synth.Options{Seed: 1}); err != nil {
		t.Fatalf("a populated ownership claim must be accepted: %v", err)
	}
}

// TestOwnsNulls_ReportsAClaimTheRunDidNotBearOut is the honest half of
// the zero-rather-than-residual decision. Three shapes over the same
// threshold, and the SILENT one is asserted beside the two loud ones —
// without it the test passes for an implementation that warns on
// everything, which is the same as not warning at all.
func TestOwnsNulls_ReportsAClaimTheRunDidNotBearOut(t *testing.T) {
	const rows = 20000
	cases := []struct {
		name     string
		awareP   float64
		capture  float64
		when     string
		warn     bool
		contains []string
	}{
		{
			name: "the gate explains all of it", awareP: 0.75, capture: 0.25,
			when: "aware == 0", warn: false,
		},
		{
			// Captured 0.50, gate accounts for 0.25 of it: the residual
			// the suppression zeroed was not zero.
			name: "the gate explains only part of it", awareP: 0.75, capture: 0.50,
			when: "aware == 0", warn: true,
			contains: []string{`the nulls of field "regard"`, "null_rate 0.5 was discarded", "of 20000 generated row(s)"},
		},
		{
			// A claim over a gate that never fires: ownership has done
			// nothing but delete the field's absences.
			name: "the gate never fires", awareP: 1.0, capture: 0.25,
			when: "aware == 0", warn: true,
			contains: []string{`the nulls of field "regard"`, "rule 0 owns"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, res, err := synth.SynthBytes(
				ownershipSpec(rows, tc.awareP, tc.capture, []synth.RuleSpec{gateRule(true, tc.when, "regard")}),
				synth.Options{Seed: 4242})
			if err != nil {
				t.Fatalf("synth: %v", err)
			}
			var hit []string
			for _, w := range res.Warnings {
				if strings.Contains(w, " the nulls of field ") {
					hit = append(hit, w)
				}
			}
			if tc.warn && len(hit) == 0 {
				t.Fatalf("expected an ownership divergence warning; got %v", res.Warnings)
			}
			if !tc.warn {
				if len(hit) != 0 {
					t.Fatalf("an exactly-accounted gate must stay silent; got %v", hit)
				}
				return
			}
			for _, want := range tc.contains {
				if !strings.Contains(hit[0], want) {
					t.Errorf("warning %q does not carry %q", hit[0], want)
				}
			}
			// It reaches the terminal summary as something needing
			// attention, never the expected-outcome count and never the
			// unrecognised catch-all.
			groups := synth.GroupWarnings(hit)
			if len(groups) != 1 {
				t.Fatalf("expected one warning kind, got %d: %+v", len(groups), groups)
			}
			if !groups[0].Attention {
				t.Errorf("kind %q must need attention", groups[0].Kind)
			}
			if groups[0].Kind == "other" {
				t.Error("the warning fell into the unrecognised catch-all")
			}
		})
	}
}

// TestOwnsNulls_TwoRulesMayOwnOneField pins the union. Ownership is not
// routed through resolveConflicts' exclusive claim(), because `set_null`
// removes a value rather than supplying one and "two gates can each
// account for this field's absence" needs no arbitration.
//
// The two gates here are DISJOINT and together account for the captured
// rate exactly, so the union is measurable (neither gate alone reaches
// it) and the run stays silent — a warning would mean the union was not
// applied.
func TestOwnsNulls_TwoRulesMayOwnOneField(t *testing.T) {
	const rows = 20000
	// aware=0 on 0.25 of rows; wave=0 on 0.5 of rows, independently.
	// P(aware == 0 or (aware == 1 and wave == 0)) = 0.25 + 0.75*0.5 = 0.625.
	spec := ownershipSpec(rows, 0.75, 0.625, []synth.RuleSpec{
		gateRule(true, "aware == 0", "regard"),
		gateRule(true, "aware == 1 && wave == 0", "regard"),
	})
	data, res, err := synth.SynthBytes(spec, synth.Options{Seed: 4242})
	if err != nil {
		t.Fatalf("synth: %v", err)
	}
	if got := nullRateOf(t, data, "regard"); math.Abs(got-0.625) > 0.015 {
		t.Errorf("null rate %.4f, want the union 0.625 (+-0.015)", got)
	}
	for _, w := range res.Warnings {
		if strings.Contains(w, " the nulls of field ") {
			t.Errorf("two owners accounting for the rate exactly must stay silent; got %q", w)
		}
	}

	// Suppression happens ONCE, not twice: a second claim over the same
	// field cannot remove absences the first already removed.
	single := ownershipSpec(rows, 0.75, 0.625, []synth.RuleSpec{
		gateRule(true, "aware == 0 || wave == 0", "regard"),
	})
	sdata, _, err := synth.SynthBytes(single, synth.Options{Seed: 4242})
	if err != nil {
		t.Fatalf("single: %v", err)
	}
	if got := nullRateOf(t, sdata, "regard"); math.Abs(got-0.625) > 0.015 {
		t.Errorf("one-rule equivalent null rate %.4f, want 0.625 (+-0.015)", got)
	}

	// When two owners together MISS the captured rate, the line names
	// both, because editing one of them does not close the gap.
	short := ownershipSpec(rows, 0.75, 0.95, []synth.RuleSpec{
		gateRule(true, "aware == 0", "regard"),
		gateRule(true, "aware == 1 && wave == 0", "regard"),
	})
	_, shres, err := synth.SynthBytes(short, synth.Options{Seed: 4242})
	if err != nil {
		t.Fatalf("short: %v", err)
	}
	found := false
	for _, w := range shres.Warnings {
		if strings.Contains(w, " the nulls of field ") {
			found = true
			if !strings.Contains(w, "rules 0, 1 own") {
				t.Errorf("warning %q must name both claiming rules", w)
			}
		}
	}
	if !found {
		t.Fatalf("expected a divergence warning; got %v", shres.Warnings)
	}
}

// TestOwnsNulls_SilentOnSamplingNoise pins the second half of the
// divergence test: the gap must also clear two standard errors of the
// declared rate.
//
// Without that term the 0.02 absolute threshold alone fires on ordinary
// binomial noise — at 200 rows and a declared 0.25 the standard error is
// 0.031 — and a warning that fires on a correct spec about half the time
// is a warning readers learn to skip. The test asserts BOTH halves so it
// cannot pass by accident: at least one of these seeds has a gap the
// absolute threshold alone WOULD have reported, and none of them warns.
func TestOwnsNulls_SilentOnSamplingNoise(t *testing.T) {
	const (
		rows    = 100
		capture = 0.25
	)
	wouldHaveFired := 0
	for _, seed := range []int64{1, 3, 7, 11, 42, 77, 99, 512, 1234, 4242} {
		data, res, err := synth.SynthBytes(
			ownershipSpec(rows, 0.75, capture, []synth.RuleSpec{gateRule(true, "aware == 0", "regard")}),
			synth.Options{Seed: seed})
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		gap := math.Abs(nullRateOf(t, data, "regard") - capture)
		t.Logf("seed %d gap %.4f", seed, gap)
		if gap > 0.02 {
			wouldHaveFired++
		}
		for _, w := range res.Warnings {
			if strings.Contains(w, " the nulls of field ") {
				t.Errorf("seed %d: an exactly-accounted gate warned on sampling noise: %q", seed, w)
			}
		}
	}
	if wouldHaveFired == 0 {
		t.Fatal("no seed produced a gap past the absolute threshold, so the noise term was never exercised")
	}
}

// TestSuggestRules_GatingCandidatesOwnTheirNulls closes the loop for
// detection. Three claims over one round trip, because the first alone
// is satisfiable by writing a key nothing reads:
//
//  1. Every GATING candidate carries the flag and no other detector's
//     candidate does — a co-missing block already discards its
//     non-gate members' rates by copying the gate's decision.
//  2. The generated cohort keeps each gated target's CAPTURED null
//     rate, rather than the double count.
//  3. The gate is still exact on the same file.
func TestSuggestRules_GatingCandidatesOwnTheirNulls(t *testing.T) {
	prof, _ := suggestFixtureProfile(t)

	gating, other := 0, 0
	for _, c := range prof.RuleCandidates {
		if c.Evidence == nil {
			t.Fatal("a candidate carries no evidence")
		}
		if c.Evidence.Detector == "gating" {
			gating++
			if !c.OwnsNulls {
				t.Errorf("gating candidate on %q does not own its nulls", c.Evidence.GateField)
			}
			continue
		}
		other++
		if c.OwnsNulls {
			t.Errorf("%s candidate carries owns_nulls; only a gate claims a rate",
				c.Evidence.Detector)
		}
	}
	if gating == 0 {
		t.Fatal("no gating candidate emitted; claim 1 asserted nothing")
	}
	if other == 0 {
		t.Log("no non-gating candidate emitted; the negative half of claim 1 asserted nothing")
	}

	cfg := fs.NewMemMap()
	if err := synth.WriteRuleCandidates(cfg.Fs(), prof.RuleCandidates, "/candidates.json"); err != nil {
		t.Fatalf("WriteRuleCandidates: %v", err)
	}
	spec, _ := synth.SpecFromProfile(roundTripProfile(t, prof), 8000)
	captured := map[string]float64{}
	for _, f := range spec.Fields {
		captured[f.Name] = f.NullRate
	}
	if err := synth.ApplyRulesFile(cfg.Fs(), spec, "/candidates.json"); err != nil {
		t.Fatalf("ApplyRulesFile: %v", err)
	}
	data, _, err := synth.SynthBytes(spec, synth.Options{Seed: 4242})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	for i := 1; i <= suggestTargets; i++ {
		name := fmt.Sprintf("q%d", i)
		want := captured[name]
		if want < 0.1 {
			t.Fatalf("fixture degenerate: %s captured null_rate %.4f", name, want)
		}
		got := nullRateOf(t, data, name)
		if math.Abs(got-want) > 0.02 {
			t.Errorf("claim 2: %s generated null rate %.4f, captured %.4f; "+
				"the double count is %.4f", name, got, want, want+(1-want)*want)
		}
	}
	gated, allNull := gatedRowStats(t, data)
	if gated == 0 {
		t.Fatal("no gated rows; claim 3 asserted nothing")
	}
	if allNull != gated {
		t.Errorf("claim 3: the gate fired on %d of %d gated rows", allNull, gated)
	}
}
