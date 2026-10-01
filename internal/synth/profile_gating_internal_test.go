package synth

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

// gateFixtureSchema is a survey-shaped cohort: a never-null boolean
// screener, a never-null ordinal that is its exact proxy, a forty-field
// perception block asked only of the screened-in, and three fields with
// NO gating relationship at all.
//
// The proxy is deliberate and is the point of the fixture, not an
// accident of it. On the motivating cohort detection finds `aware` while
// the analyst's own rule is `familiarity == 1`, and the two were
// measured as EXACTLY equivalent there — 96,326 rows each, zero
// exceptions either way. A fixture with one unambiguous gate would let a
// detector that picks arbitrarily between equivalent gates pass, and
// would make the "may be a proxy" caveat look like defensive prose
// rather than a measured property.
func gateFixtureSchema(t *testing.T, perception int) *encoding.Schema {
	t.Helper()
	region := encoding.NewDictionary()
	for _, v := range []string{"east", "west", "north"} {
		if _, err := region.Add(v); err != nil {
			t.Fatalf("region dict add: %v", err)
		}
	}
	fields := []encoding.Field{
		{Name: "aware", Type: encoding.FieldTypePackedBool},
		{Name: "familiarity", Type: encoding.FieldTypeU4},
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Dictionary: region},
	}
	for i := 0; i < perception; i++ {
		fields = append(fields, encoding.Field{
			Name: fmt.Sprintf("p%02d", i), Type: encoding.FieldTypeU4, Nullable: true,
		})
	}
	fields = append(fields,
		encoding.Field{Name: "noise", Type: encoding.FieldTypeF64, Nullable: true},
		encoding.Field{Name: "assoc", Type: encoding.FieldTypeU4, Nullable: true},
		encoding.Field{Name: "always", Type: encoding.FieldTypeF64},
	)
	return &encoding.Schema{Fields: fields}
}

// gateFixtureRows builds n rows where every value is an exact integer
// function of the row index — no float arithmetic anywhere, so the
// fixture bytes are architecture-independent by construction rather than
// by a barrier (synth/moments.go).
//
//   - aware  = 0 on one row in four (the gate)
//   - familiarity = 1 exactly when aware == 0, else 2..7
//   - p00..pNN null exactly when aware == 0
//   - noise  null on one row in SEVEN, INDEPENDENT of every gate
//     candidate: 7 is coprime with the gate's period (4), the ordinal's
//     (12) and the region cycle's (3), so every level sees the same
//     rate and no level can split.
//   - assoc  null at 10% / 30% / 50% by region — a genuine ASSOCIATION,
//     which is the thing the thresholds exist to refuse. `noise` cannot
//     be admitted at ANY threshold (its rate is the same at every
//     level), so it alone does not test them; this one is admitted the
//     moment the split test is loosened.
//   - region cycles over three values, gating nothing.
func gateFixtureRows(t *testing.T, schema *encoding.Schema, n, perception int) ([]map[string]any, []map[string]bool) {
	t.Helper()
	regions := []string{"east", "west", "north"}
	rows := make([]map[string]any, 0, n)
	nulls := make([]map[string]bool, 0, n)
	for i := 0; i < n; i++ {
		gated := i%4 == 0
		row := map[string]any{
			"region": regions[i%3],
			"always": float64(i % 11),
			"noise":  float64(i % 7),
		}
		rowNull := map[string]bool{}
		if gated {
			row["aware"] = 0.0
			row["familiarity"] = 1.0
		} else {
			row["aware"] = 1.0
			row["familiarity"] = float64(2 + i%6)
		}
		for p := 0; p < perception; p++ {
			name := fmt.Sprintf("p%02d", p)
			if gated {
				row[name] = 0.0
				rowNull[name] = true
			} else {
				row[name] = float64((i + p) % 8)
			}
		}
		// Independent of every gate candidate. The modulus is 7 rather
		// than 3 ON PURPOSE: 3 is the region cycle's period, so i%3
		// made `region == "east"` an EXACT gate on `noise` and the
		// fixture's own negative control was a positive. Found by the
		// test, which is what a negative control is for.
		if i%7 == 0 {
			rowNull["noise"] = true
		}
		// Association, not gating: the null rate genuinely MOVES with
		// region (10% / 30% / 50%) but never reaches ~1 or ~0. A rule
		// built from it would be applied unconditionally and be wrong on
		// most of the rows it selects, which is why the thresholds are
		// tight rather than merely non-trivial.
		switch i % 3 {
		case 0:
			rowNull["assoc"] = (i/3)%10 < 1
		case 1:
			rowNull["assoc"] = (i/3)%10 < 3
		default:
			rowNull["assoc"] = (i/3)%10 < 5
		}
		row["assoc"] = float64(i % 8)
		rows = append(rows, row)
		nulls = append(nulls, rowNull)
	}
	return rows, nulls
}

func gateFixtureProfile(t *testing.T, n, perception int) *Profile {
	t.Helper()
	schema := gateFixtureSchema(t, perception)
	rows, nulls := gateFixtureRows(t, schema, n, perception)
	data := encodeModelRows(t, schema, rows, nulls)
	prof, err := profileRecords(schema, bytes.NewReader(data), ProfileOptions{SuggestRules: true})
	if err != nil {
		t.Fatalf("profileRecords: %v", err)
	}
	return prof
}

func candidateByGate(cands []RuleSpec, gate string) (RuleSpec, bool) {
	for _, c := range cands {
		if c.Evidence != nil && c.Evidence.GateField == gate {
			return c, true
		}
	}
	return RuleSpec{}, false
}

// TestSuggestRules_KnownGateYieldsTheCorrectTargetList is acceptance
// criteria 2 and 3 in ONE test, because each is trivially satisfiable by
// abandoning the other: a detector proposing every pair satisfies "finds
// the gate" and a detector proposing nothing satisfies "no candidate for
// an ungated pair".
func TestSuggestRules_KnownGateYieldsTheCorrectTargetList(t *testing.T) {
	const perception = 40
	prof := gateFixtureProfile(t, 400, perception)
	cands := prof.RuleCandidates
	if len(cands) == 0 {
		t.Fatalf("no candidates; warnings = %v", prof.Warnings)
	}

	got, ok := candidateByGate(cands, "aware")
	if !ok {
		t.Fatalf("aware was not proposed as a gate; gates = %v", gateNames(cands))
	}
	want := make([]string, 0, perception)
	for i := 0; i < perception; i++ {
		want = append(want, fmt.Sprintf("p%02d", i))
	}
	if strings.Join(got.SetNull, ",") != strings.Join(want, ",") {
		t.Errorf("aware targets = %v, want %v", got.SetNull, want)
	}
	// round(), not bare ==, on EVERY numeric gate including a
	// packed_bool whose row value is already exactly 0.0 or 1.0. The
	// uniformity is the point: a file where one numeric gate is spelled
	// bare and another through round() teaches a reader that bare is
	// sometimes fine, and nothing in the file says which.
	if got.When != "round(aware) == 0" {
		t.Errorf("aware when = %q, want %q", got.When, "round(aware) == 0")
	}

	// The proxy is found too, and detection cannot tell the two apart —
	// which is exactly why the candidate note invites correction rather
	// than asserting cause.
	fam, ok := candidateByGate(cands, "familiarity")
	if !ok {
		t.Fatalf("familiarity (the exact proxy) was not proposed; gates = %v", gateNames(cands))
	}
	if len(fam.SetNull) != perception {
		t.Errorf("familiarity targets = %d, want %d", len(fam.SetNull), perception)
	}
	if fam.When != "round(familiarity) == 1" {
		t.Errorf("familiarity when = %q, want round() form", fam.When)
	}

	// The negative half: `noise` is null on one row in three regardless
	// of every gate candidate's level, and `region` gates nothing. No
	// candidate may name either as a target, and region may not be a
	// gate.
	for _, c := range cands {
		if c.Evidence.GateField == "region" {
			t.Errorf("region proposed as a gate, but its levels split nothing: %+v", c.Evidence.Levels)
		}
		for _, tgt := range c.SetNull {
			if tgt == "noise" || tgt == "always" {
				t.Errorf("gate %q proposed %q as a target, but its nulls are independent of every level",
					c.Evidence.GateField, tgt)
			}
			// The threshold's own job: `assoc`'s null rate moves with
			// region (10/30/50%) and is still not a gate. Loosening the
			// split test admits it — which is what makes this assertion
			// falsifiable, unlike `noise`, whose rate is flat and which
			// no threshold can admit.
			if tgt == "assoc" {
				t.Errorf("gate %q proposed %q as a target: a 10%%/30%%/50%% null rate is ASSOCIATION, "+
					"and a rule built from it is applied unconditionally and wrong on most rows it selects",
					c.Evidence.GateField, tgt)
			}
		}
	}
}

func gateNames(cands []RuleSpec) []string {
	out := make([]string, 0, len(cands))
	for _, c := range cands {
		if c.Evidence != nil {
			out = append(out, c.Evidence.GateField)
		}
	}
	return out
}

// TestSuggestRules_EvidenceCarriesRatesSupportAndRowsAffected is
// acceptance criterion 4: a candidate the analyst cannot evaluate is one
// they will accept blindly.
func TestSuggestRules_EvidenceCarriesRatesSupportAndRowsAffected(t *testing.T) {
	prof := gateFixtureProfile(t, 400, 40)
	got, ok := candidateByGate(prof.RuleCandidates, "aware")
	if !ok {
		t.Fatal("aware not proposed")
	}
	ev := got.Evidence

	if ev.Detector != gatingDetectorName {
		t.Errorf("detector = %q, want %q", ev.Detector, gatingDetectorName)
	}
	if ev.RowsObserved != 400 {
		t.Errorf("rows_observed = %d, want 400", ev.RowsObserved)
	}
	if ev.RowsAffected != 100 {
		t.Errorf("rows_affected = %d, want 100 (one row in four)", ev.RowsAffected)
	}
	if ev.GatedShare != 0.25 {
		t.Errorf("gated_share = %v, want 0.25", ev.GatedShare)
	}
	if len(ev.Levels) != 2 {
		t.Fatalf("levels = %d, want 2", len(ev.Levels))
	}
	for _, lv := range ev.Levels {
		switch lv.Level {
		case "0":
			if lv.Side != "gated" || lv.N != 100 || lv.MeanTargetNullRate != 1 {
				t.Errorf("gated level = %+v, want side=gated n=100 rate=1", lv)
			}
		case "1":
			if lv.Side != "open" || lv.N != 300 || lv.MeanTargetNullRate != 0 {
				t.Errorf("open level = %+v, want side=open n=300 rate=0", lv)
			}
		default:
			t.Errorf("unexpected level %q", lv.Level)
		}
	}
	if len(ev.Targets) != len(got.SetNull) {
		t.Fatalf("targets evidence = %d, rule targets = %d", len(ev.Targets), len(got.SetNull))
	}
	for _, tg := range ev.Targets {
		if tg.GatedNullRate != 1 || tg.OpenNullRate != 0 || tg.NullRate != 0.25 {
			t.Errorf("target %q = %+v, want gated=1 open=0 marginal=0.25", tg.Field, tg)
		}
	}
	if ev.MinLevelSupport != 100 || ev.ThinSupport {
		t.Errorf("support = %d thin=%v, want 100 / false", ev.MinLevelSupport, ev.ThinSupport)
	}
	if ev.Note == "" || !strings.Contains(ev.Note, "STATISTICAL") || !strings.Contains(ev.Note, "SEMANTIC") {
		t.Errorf("note does not invite correction: %q", ev.Note)
	}
}

// TestSuggestRules_DeviationIsImpliedByTheSplitTest pins the claim in
// RuleEvidence.MaxNullRateDeviation's doc: the `1 - P(gate)` agreement
// that identified the motivating cohort's gate by hand is NOT an
// independent signal, it is implied by the split test, so ranking by it
// would be ranking by rounding error.
//
// The bound is arithmetic: null_rate = gated_share x P(null|gated)
// + (1 - gated_share) x P(null|open), and the thresholds bound both
// conditional rates, so the deviation cannot exceed the wider threshold.
func TestSuggestRules_DeviationIsImpliedByTheSplitTest(t *testing.T) {
	prof := gateFixtureProfile(t, 400, 40)
	if len(prof.RuleCandidates) == 0 {
		t.Fatal("fixture must produce candidates for this test to mean anything")
	}
	bound := 1 - gateHighNullRate
	if gateLowNullRate > bound {
		bound = gateLowNullRate
	}
	for _, c := range prof.RuleCandidates {
		if c.Evidence.MaxNullRateDeviation > bound {
			t.Errorf("gate %q deviation = %v, exceeds the bound the thresholds imply (%v)",
				c.Evidence.GateField, c.Evidence.MaxNullRateDeviation, bound)
		}
	}
}

// TestSuggestRules_RidesTheExistingScan is the "no second pass" gate,
// the same direct measurement TestProfileModels_RidesTheExistingScan
// makes: profileRecords consumes a one-shot io.Reader, so a second pass
// is unrepresentable without re-reading bytes.
func TestSuggestRules_RidesTheExistingScan(t *testing.T) {
	schema := gateFixtureSchema(t, 40)
	rows, nulls := gateFixtureRows(t, schema, 400, 40)
	data := encodeModelRows(t, schema, rows, nulls)

	baseline := &countingReader{r: bytes.NewReader(data)}
	if _, err := profileRecords(schema, baseline, ProfileOptions{}); err != nil {
		t.Fatalf("baseline profileRecords: %v", err)
	}
	detected := &countingReader{r: bytes.NewReader(data)}
	prof, err := profileRecords(schema, detected, ProfileOptions{SuggestRules: true})
	if err != nil {
		t.Fatalf("detecting profileRecords: %v", err)
	}
	if len(prof.RuleCandidates) == 0 {
		t.Fatal("fixture must produce at least one candidate for this test to mean anything")
	}
	if detected.n != baseline.n {
		t.Errorf("detection read %d bytes, baseline read %d — it must ride the existing scan",
			detected.n, baseline.n)
	}
}

// TestSuggestRules_ThinSupportShipsWithItsSupport is acceptance
// criterion 7: a thin candidate is reported WITH its support rather than
// suppressed, and the warning listing is bounded thinnest-first.
func TestSuggestRules_ThinSupportShipsWithItsSupport(t *testing.T) {
	// Twelve rows: three gated, nine open. The gated level's support
	// (3) is far below minGateLevelSupport.
	prof := gateFixtureProfile(t, 12, 2)
	got, ok := candidateByGate(prof.RuleCandidates, "aware")
	if !ok {
		t.Fatalf("a thin candidate was SUPPRESSED rather than flagged; gates = %v, warnings = %v",
			gateNames(prof.RuleCandidates), prof.Warnings)
	}
	if !got.Evidence.ThinSupport {
		t.Errorf("thin_support = false at support %d (threshold %d)",
			got.Evidence.MinLevelSupport, minGateLevelSupport)
	}
	if got.Evidence.MinLevelSupport != 3 {
		t.Errorf("min_level_support = %d, want 3", got.Evidence.MinLevelSupport)
	}
	var thin []string
	for _, w := range prof.Warnings {
		if strings.HasPrefix(w, "thin gate level ") {
			thin = append(thin, w)
		}
	}
	if len(thin) == 0 {
		t.Errorf("a thin candidate shipped with no warning; warnings = %v", prof.Warnings)
	}
	for _, w := range thin {
		if !strings.Contains(w, "still ships") {
			t.Errorf("thin warning does not say the candidate still ships: %q", w)
		}
		kind, attention := classifyWarning(w)
		if kind != "thin gate level" || attention {
			t.Errorf("thin gate warning classified as %q / attention=%v, want an expected outcome",
				kind, attention)
		}
	}
}

// TestSuggestRules_BoundedThinListingRollsUp asserts the bounded,
// thinnest-first listing with a counted remainder — and that the
// remainder line lands in the SAME warning group as the lines it
// summarises, so the cap cannot split one finding across two counts.
func TestSuggestRules_BoundedThinListingRollsUp(t *testing.T) {
	var warnings []string
	thin := make([]gateCandidate, 0, maxThinGateWarnings+5)
	for i := 0; i < maxThinGateWarnings+5; i++ {
		thin = append(thin, gateCandidate{
			gate:       gateCandidateField{name: fmt.Sprintf("g%02d", i)},
			targets:    []int{0},
			minSupport: maxThinGateWarnings + 5 - i,
		})
	}
	appendGateThinWarnings(&warnings, thin)
	if len(warnings) != maxThinGateWarnings+1 {
		t.Fatalf("emitted %d lines, want %d + one roll-up", len(warnings), maxThinGateWarnings)
	}
	// Thinnest first: g24 has support 1.
	if !strings.Contains(warnings[0], "g24 x") {
		t.Errorf("listing is not thinnest-first; first line = %q", warnings[0])
	}
	rollup := warnings[len(warnings)-1]
	if !strings.HasPrefix(rollup, "+5 further") {
		t.Errorf("roll-up = %q, want a counted remainder of 5", rollup)
	}
	kind, _ := classifyWarning(rollup)
	if kind != "thin gate level" {
		t.Errorf("roll-up classified as %q, want the same group as the lines it summarises", kind)
	}
}

// TestSuggestRules_HighCardinalityFieldIsNotAGate pins maxGateLevels
// from BOTH sides: a field with exactly the cap's worth of levels is
// still considered, one level more is abandoned rather than truncated,
// and the abandonment is reported.
//
// The fixture's cardinality is expressed RELATIVE to the constant on
// purpose. An absolute 20 would stop exercising the cap the moment
// anyone raised it — silently, since the field would simply become a
// legitimate candidate and the test would go on passing. Found while
// falsifying: raising maxGateLevels to 64 under an absolute fixture
// left this test green. The cost is that the constant's numeric VALUE
// is not pinned here, which is correct — it is a tuning documented at
// its declaration, and a test asserting `maxGateLevels == 16` would be
// a tautology. What IS pinned is the behaviour at the boundary.
func TestSuggestRules_HighCardinalityFieldIsNotAGate(t *testing.T) {
	dict := encoding.NewDictionary()
	for i := 0; i < maxGateLevels+4; i++ {
		if _, err := dict.Add(fmt.Sprintf("v%02d", i)); err != nil {
			t.Fatalf("dict add: %v", err)
		}
	}
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "wide", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict},
		{Name: "target", Type: encoding.FieldTypeU4, Nullable: true},
	}}
	rows := make([]map[string]any, 0, 400)
	nulls := make([]map[string]bool, 0, 400)
	for i := 0; i < 400; i++ {
		level := i % (maxGateLevels + 4)
		row := map[string]any{"wide": fmt.Sprintf("v%02d", level), "target": float64(level % 8)}
		rowNull := map[string]bool{}
		// A perfect gate on the widest level — which must still not be
		// proposed, because the field was never a candidate.
		if level == 0 {
			rowNull["target"] = true
		}
		rows = append(rows, row)
		nulls = append(nulls, rowNull)
	}
	data := encodeModelRows(t, schema, rows, nulls)
	prof, err := profileRecords(schema, bytes.NewReader(data), ProfileOptions{SuggestRules: true})
	if err != nil {
		t.Fatalf("profileRecords: %v", err)
	}
	if len(prof.RuleCandidates) != 0 {
		t.Errorf("a %d-level field was proposed as a gate: %v", maxGateLevels+4, gateNames(prof.RuleCandidates))
	}
	var found bool
	for _, w := range prof.Warnings {
		if strings.HasPrefix(w, "rule suggestion: 1 field(s) were not considered as gates") {
			found = true
			kind, attention := classifyWarning(w)
			if kind != "rule candidate not considered" || !attention {
				t.Errorf("abandonment classified as %q / attention=%v", kind, attention)
			}
		}
	}
	if !found {
		t.Errorf("the abandoned candidate was not reported; warnings = %v", prof.Warnings)
	}

	// The other side of the boundary: exactly maxGateLevels levels is
	// still a candidate, so the cap refuses a branch that is too wide
	// rather than refusing wide fields generally.
	atCap := gateCardinalityFixture(t, maxGateLevels)
	if _, ok := candidateByGate(atCap.RuleCandidates, "wide"); !ok {
		t.Errorf("a field with exactly %d levels was refused; the cap must admit its own boundary",
			maxGateLevels)
	}
}

// gateCardinalityFixture profiles a cohort whose single gate candidate
// carries exactly `levels` distinct values, the lowest of which gates
// the only target.
func gateCardinalityFixture(t *testing.T, levels int) *Profile {
	t.Helper()
	dict := encoding.NewDictionary()
	for i := 0; i < levels; i++ {
		if _, err := dict.Add(fmt.Sprintf("v%03d", i)); err != nil {
			t.Fatalf("dict add: %v", err)
		}
	}
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "wide", Type: encoding.FieldTypeCategoricalU16, Dictionary: dict},
		{Name: "target", Type: encoding.FieldTypeU4, Nullable: true},
	}}
	rows := make([]map[string]any, 0, levels*20)
	nulls := make([]map[string]bool, 0, levels*20)
	for i := 0; i < levels*20; i++ {
		level := i % levels
		rowNull := map[string]bool{}
		if level == 0 {
			rowNull["target"] = true
		}
		rows = append(rows, map[string]any{
			"wide": fmt.Sprintf("v%03d", level), "target": float64(level % 8)})
		nulls = append(nulls, rowNull)
	}
	data := encodeModelRows(t, schema, rows, nulls)
	prof, err := profileRecords(schema, bytes.NewReader(data), ProfileOptions{SuggestRules: true})
	if err != nil {
		t.Fatalf("profileRecords: %v", err)
	}
	return prof
}

// TestSuggestRules_CoMissingGateIsCountedNotProposed pins the scope
// boundary: a gate whose ONLY gated level is the null pseudo-level is
// co-missingness between fields, not value gating, and belongs to a
// different detector. Every member of an N-field block reports the
// others this way, so proposing them restates one finding N times.
func TestSuggestRules_CoMissingGateIsCountedNotProposed(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "a", Type: encoding.FieldTypeU4, Nullable: true},
		{Name: "b", Type: encoding.FieldTypeU4, Nullable: true},
		{Name: "c", Type: encoding.FieldTypeU4, Nullable: true},
	}}
	rows := make([]map[string]any, 0, 300)
	nulls := make([]map[string]bool, 0, 300)
	for i := 0; i < 300; i++ {
		row := map[string]any{"a": float64(i % 5), "b": float64(i % 6), "c": float64(i % 7)}
		rowNull := map[string]bool{}
		if i%3 == 0 {
			rowNull["a"], rowNull["b"], rowNull["c"] = true, true, true
		}
		rows = append(rows, row)
		nulls = append(nulls, rowNull)
	}
	data := encodeModelRows(t, schema, rows, nulls)
	prof, err := profileRecords(schema, bytes.NewReader(data), ProfileOptions{SuggestRules: true})
	if err != nil {
		t.Fatalf("profileRecords: %v", err)
	}
	// NARROWED at E3-S2, not relaxed. The assertion was
	// "len(RuleCandidates) == 0" while this detector was the only one
	// writing to the slot; the co-missing detector now proposes exactly
	// this block, which is the whole point of it. What must still hold —
	// and is the half this test exists for — is that NO GATING candidate
	// is proposed for it.
	for _, c := range prof.RuleCandidates {
		if c.Evidence != nil && c.Evidence.Detector == gatingDetectorName {
			t.Errorf("a co-missing block was proposed as value gating: %v", c.Evidence.GateField)
		}
	}
	var found bool
	for _, w := range prof.Warnings {
		if strings.Contains(w, "relationship(s) not proposed") && strings.Contains(w, "ABSENCE") {
			found = true
		}
	}
	if !found {
		t.Errorf("the co-missing relationships were dropped silently; warnings = %v", prof.Warnings)
	}
	// And the other half of the hand-off: the finding this detector
	// counts is the finding E3-S2's detector proposes, so the two must
	// not BOTH stay silent about it.
	var block []string
	for _, c := range prof.RuleCandidates {
		if c.Evidence != nil && c.Evidence.Detector == comissingDetectorName {
			block = c.NullTogether
		}
	}
	if strings.Join(block, ",") != "a,b,c" {
		t.Errorf("the co-missing block was counted by one detector and proposed by neither: %v", block)
	}
}

// TestSuggestRules_PredicateSurvivesTheRuleCompiler asserts every
// emitted `when` compiles against the SAME environment validateRules
// compiles a rule's `when` against — the guarantee behind "consumed
// without modification", enforced at the source rather than at the
// loader the analyst feeds it to.
func TestSuggestRules_PredicateSurvivesTheRuleCompiler(t *testing.T) {
	prof := gateFixtureProfile(t, 400, 40)
	spec := &Spec{}
	for _, fp := range prof.Fields {
		// Nullable mirrors SpecFromProfile's own derivation
		// (NullRate > 0), which a set_null candidate depends on: a
		// non-nullable target is refused
		// (PULSE_SYNTH_RULE_FIELD_NOT_NULLABLE), and a gating candidate
		// exists only because the target HAS nulls, so dropping the flag
		// here would make the fixture describe a spec the derivation
		// never produces.
		spec.Fields = append(spec.Fields, FieldSpec{
			Name: fp.Name, Type: fp.Type, Nullable: fp.NullRate > 0})
	}
	spec.Rules = prof.RuleCandidates
	if len(spec.Rules) == 0 {
		t.Fatal("fixture must produce candidates")
	}
	if err := validateRules(spec); err != nil {
		t.Fatalf("a detected candidate does not survive rule validation: %v", err)
	}
}

// TestSuggestRules_UncompilablePredicateIsDroppedNotWritten covers a
// case the emitted file has to survive and no fixture in this package
// would otherwise reach: a cohort field whose name is a legal `.pulse`
// identifier and an expr-lang KEYWORD.
//
// `in` is the one that bites (expr's membership operator), and a survey
// cohort naming a column `in`, `not`, `and` or `let` is entirely
// ordinary. Without the guard the detector writes `round(in) == 0`,
// which parses as JSON, looks exactly like every other candidate, and
// is refused by the loader the analyst feeds it to — a file that is
// "consumed without modification" only if it is never consumed.
//
// The guard is the rule layer's OWN compiler (rowExprEnv /
// rowExprOptions), not a hand-rolled identifier check, so it cannot
// disagree with the validation the file will later face.
func TestSuggestRules_UncompilablePredicateIsDroppedNotWritten(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "in", Type: encoding.FieldTypePackedBool},
		{Name: "ok", Type: encoding.FieldTypePackedBool},
		{Name: "target", Type: encoding.FieldTypeU4, Nullable: true},
	}}
	rows := make([]map[string]any, 0, 400)
	nulls := make([]map[string]bool, 0, 400)
	for i := 0; i < 400; i++ {
		gated := i%4 == 0
		row := map[string]any{"in": 1.0, "ok": 1.0, "target": float64(i % 8)}
		rowNull := map[string]bool{}
		if gated {
			row["in"], row["ok"] = 0.0, 0.0
			rowNull["target"] = true
		}
		rows = append(rows, row)
		nulls = append(nulls, rowNull)
	}
	data := encodeModelRows(t, schema, rows, nulls)
	prof, err := profileRecords(schema, bytes.NewReader(data), ProfileOptions{SuggestRules: true})
	if err != nil {
		t.Fatalf("profileRecords: %v", err)
	}
	if _, ok := candidateByGate(prof.RuleCandidates, "in"); ok {
		t.Error("a candidate whose predicate cannot compile was written to the file")
	}
	// The sibling gate, identical in every way but its name, still
	// ships — so the guard drops a candidate rather than the pass.
	if _, ok := candidateByGate(prof.RuleCandidates, "ok"); !ok {
		t.Errorf("the compilable sibling was dropped too; gates = %v", gateNames(prof.RuleCandidates))
	}
	var reported bool
	for _, w := range prof.Warnings {
		if strings.Contains(w, "candidate(s) dropped") {
			reported = true
		}
	}
	if !reported {
		t.Errorf("the dropped candidate was not reported; warnings = %v", prof.Warnings)
	}

	spec := &Spec{Rules: prof.RuleCandidates}
	for i := range schema.Fields {
		// Nullable comes off the source schema for the same reason
		// SpecFromProfile derives it from the captured null rate: a
		// set_null candidate over a non-nullable field is refused.
		spec.Fields = append(spec.Fields, FieldSpec{
			Name:     schema.Fields[i].Name,
			Type:     schema.Fields[i].Type.String(),
			Nullable: schema.Fields[i].Nullable})
	}
	if err := validateRules(spec); err != nil {
		t.Fatalf("the written candidates do not survive rule validation: %v", err)
	}
}
