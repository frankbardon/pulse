package synth_test

import (
	"bytes"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/fs"
	"github.com/frankbardon/pulse/synth"
)

// depSourceSpec builds a cohort that genuinely CARRIES the motivating
// relationship, by generating it through the rule layer itself: `score`
// is a 0..10 integer, `promoter` / `passive` / `detractor` are its three
// bands, and all four share one null decision.
//
// Generating the source through the rules rather than hand-encoding rows
// is the same choice blockSourceSpec and suggestSourceSpec make: the
// claim under test is a ROUND TRIP, and a fixture built by the mechanism
// the round trip ends in is the one shape where "recovered" and
// "reproducible" mean the same thing.
//
// `decoy` is the negative control at the public surface — an ordinary
// flag correlated with nothing, drawn independently — and `region` /
// `west` add a categorical-sourced dependency beside the numeric one.
func depSourceSpec(rows int) *synth.Spec {
	numeric := func(name string, lo, hi float64) synth.FieldSpec {
		return synth.FieldSpec{
			Name: name, Type: "u4", Nullable: true, NullRate: 0.25,
			Distribution: synth.DistNormal,
			Params:       map[string]any{"mean": 5.0, "std": 3.0, "min": lo, "max": hi},
		}
	}
	flag := func(name string) synth.FieldSpec {
		return synth.FieldSpec{
			Name: name, Type: "packed_bool", Nullable: true, NullRate: 0.25,
			Distribution: synth.DistBernoulli, Params: map[string]any{"p": 0.4},
		}
	}
	spec := &synth.Spec{
		RowCount: rows,
		Fields: []synth.FieldSpec{
			numeric("score", 0, 10),
			flag("promoter"), flag("passive"), flag("detractor"),
			{Name: "region", Type: "categorical_u8", Distribution: synth.DistWeightedCategorical,
				Params: map[string]any{
					"values":  []any{"east", "north", "south", "west"},
					"weights": []any{0.3, 0.3, 0.2, 0.2},
				}},
			{Name: "west", Type: "packed_bool", Distribution: synth.DistBernoulli,
				Params: map[string]any{"p": 0.3}},
			flag("decoy"),
		},
		Rules: []synth.RuleSpec{
			{When: "!isnull(score)", SetExpr: map[string]string{"score": "round(score)"}},
			{
				SetExpr: map[string]string{
					"promoter":  "score >= 9",
					"passive":   "score >= 7 && score < 9",
					"detractor": "score < 7",
				},
				NullTogether: []string{"score", "promoter", "passive", "detractor"},
			},
			{SetExpr: map[string]string{"west": `region == "west"`}},
		},
	}
	return spec
}

func depFixtureCandidates(t *testing.T) *synth.Profile {
	t.Helper()
	data, _, err := synth.SynthBytes(depSourceSpec(8000), synth.Options{Seed: 91})
	if err != nil {
		t.Fatalf("SynthBytes(source cohort): %v", err)
	}
	prof, err := synth.ProfileBytes(data, synth.ProfileOptions{
		TopK: 8, IncludeStats: true, SuggestRules: true, Seed: 1,
	})
	if err != nil {
		t.Fatalf("ProfileBytes: %v", err)
	}
	return prof
}

func depCandidateRules(prof *synth.Profile) []synth.RuleSpec {
	var out []synth.RuleSpec
	for _, c := range prof.RuleCandidates {
		if c.Evidence != nil && c.Evidence.Detector == "dependency" {
			out = append(out, c)
		}
	}
	return out
}

// bandCoherence counts, over generated rows where `score` is present,
// how many carry the flag assignment the SOURCE relationship implies —
// and how many carry a flag at all while the score is absent, which is
// the other half of "reproduces the source mapping".
func bandCoherence(t *testing.T, data []byte) (scored, agree, orphanFlag int) {
	t.Helper()
	r := bytes.NewReader(data)
	if err := encoding.ReadHeader(r); err != nil {
		t.Fatalf("read header: %v", err)
	}
	schema, err := encoding.ReadSchema(r)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	rr := encoding.NewRecordReader(r, schema)
	values := map[string]float64{}
	nulls := map[string]bool{}
	for {
		if err := rr.ReadRecord(values, nulls); err != nil {
			break
		}
		if nulls["score"] {
			for _, f := range []string{"promoter", "passive", "detractor"} {
				if !nulls[f] {
					orphanFlag++
					break
				}
			}
			continue
		}
		scored++
		s := values["score"]
		want := map[string]bool{
			"promoter":  s >= 9,
			"passive":   s >= 7 && s < 9,
			"detractor": s < 7,
		}
		ok := true
		for f, w := range want {
			if nulls[f] || (values[f] != 0) != w {
				ok = false
			}
		}
		if ok {
			agree++
		}
	}
	return scored, agree, orphanFlag
}

// TestSuggestDeps_WrittenFileGeneratesUnmodified is the story's central
// acceptance criterion — the candidate's expression, fed through
// `--rules`, reproduces the source mapping exactly on generated rows —
// asserted BY GENERATION rather than by inspecting the string.
//
// Both halves in ONE test, because each is trivially satisfiable by
// abandoning the other: a rule nulling every flag satisfies "no row
// disagrees", and the negative control is what makes the first half
// capable of failing at all.
func TestSuggestDeps_WrittenFileGeneratesUnmodified(t *testing.T) {
	prof := depFixtureCandidates(t)
	if len(depCandidateRules(prof)) == 0 {
		t.Fatalf("no dependency candidates; warnings = %v", prof.Warnings)
	}

	cfg := fs.NewMemMap()
	if err := synth.WriteRuleCandidates(cfg.Fs(), prof.RuleCandidates, "/candidates.json"); err != nil {
		t.Fatalf("WriteRuleCandidates: %v", err)
	}

	spec, _ := synth.SpecFromProfile(roundTripProfile(t, prof), 6000)
	if err := synth.ApplyRulesFile(cfg.Fs(), spec, "/candidates.json"); err != nil {
		t.Fatalf("the written candidate file was not consumed unmodified: %v", err)
	}
	ruled, _, err := synth.SynthBytes(spec, synth.Options{Seed: 606})
	if err != nil {
		t.Fatalf("SynthBytes(ruled): %v", err)
	}
	base, _ := synth.SpecFromProfile(roundTripProfile(t, prof), 6000)
	unruled, _, err := synth.SynthBytes(base, synth.Options{Seed: 606})
	if err != nil {
		t.Fatalf("SynthBytes(unruled): %v", err)
	}

	scored, agree, orphan := bandCoherence(t, ruled)
	if scored == 0 {
		t.Fatal("no scored rows generated")
	}
	if agree != scored {
		t.Errorf("the source mapping was reproduced on %d of %d scored rows", agree, scored)
	}
	if orphan != 0 {
		t.Errorf("%d row(s) carry a band flag with no score; the null_together riding the SAME rule "+
			"is what prevents that", orphan)
	}

	baseScored, baseAgree, baseOrphan := bandCoherence(t, unruled)
	if baseAgree == baseScored && baseOrphan == 0 {
		t.Fatalf("the negative control is already coherent (%d of %d scored, %d orphan) — the first half "+
			"of this test cannot fail", baseAgree, baseScored, baseOrphan)
	}
	t.Logf("ruled: %d/%d scored rows coherent, %d orphan; unruled: %d/%d, %d orphan",
		agree, scored, orphan, baseAgree, baseScored, baseOrphan)
}

// TestSuggestDeps_CategoricalDependencyFiresOnEveryWireRow covers the
// categorical arm end to end, and with it the claim that the emitted
// expression reads the value the FILE holds rather than the sampler's
// float.
func TestSuggestDeps_CategoricalDependencyFiresOnEveryWireRow(t *testing.T) {
	prof := depFixtureCandidates(t)
	var found bool
	for _, c := range depCandidateRules(prof) {
		if c.Evidence.SourceField == "region" {
			found = true
			if got, want := c.SetExpr["west"], `region == "west"`; got != want {
				t.Errorf("set_expr[west] = %q, want %q", got, want)
			}
		}
	}
	if !found {
		t.Fatalf("no categorical-sourced candidate; warnings = %v", prof.Warnings)
	}

	cfg := fs.NewMemMap()
	if err := synth.WriteRuleCandidates(cfg.Fs(), prof.RuleCandidates, "/c.json"); err != nil {
		t.Fatalf("WriteRuleCandidates: %v", err)
	}
	spec, _ := synth.SpecFromProfile(roundTripProfile(t, prof), 4000)
	if err := synth.ApplyRulesFile(cfg.Fs(), spec, "/c.json"); err != nil {
		t.Fatalf("ApplyRulesFile: %v", err)
	}
	data, _, err := synth.SynthBytes(spec, synth.Options{Seed: 12})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}

	r := bytes.NewReader(data)
	if err := encoding.ReadHeader(r); err != nil {
		t.Fatalf("read header: %v", err)
	}
	schema, err := encoding.ReadSchema(r)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	var dict *encoding.Dictionary
	for i := range schema.Fields {
		if schema.Fields[i].Name == "region" {
			dict = schema.Fields[i].Dictionary
		}
	}
	if dict == nil {
		t.Fatal("no region dictionary on the generated cohort")
	}
	rr := encoding.NewRecordReader(r, schema)
	values := map[string]float64{}
	nulls := map[string]bool{}
	rows, bad := 0, 0
	for {
		if err := rr.ReadRecord(values, nulls); err != nil {
			break
		}
		rows++
		want := dict.Resolve(uint32(values["region"])) == "west"
		if (values["west"] != 0) != want {
			bad++
		}
	}
	if rows == 0 {
		t.Fatal("no rows generated")
	}
	if bad != 0 {
		t.Errorf("the categorical dependency disagreed on %d of %d wire rows", bad, rows)
	}
}

// TestSuggestDeps_ComposeWithTheOtherDetectorsInOneFile: the three
// detectors write into ONE file, which `--rules` consumes unmodified,
// and the emitted order is the applied order.
func TestSuggestDeps_ComposeWithTheOtherDetectorsInOneFile(t *testing.T) {
	prof := depFixtureCandidates(t)
	kinds := map[string]int{}
	lastDep := -1
	firstNonDep := -1
	for i, c := range prof.RuleCandidates {
		if c.Evidence == nil {
			t.Fatalf("candidate %d carries no evidence", i)
		}
		kinds[c.Evidence.Detector]++
		if c.Evidence.Detector == "dependency" {
			lastDep = i
		} else if firstNonDep < 0 {
			firstNonDep = i
		}
	}
	if kinds["dependency"] == 0 {
		t.Fatalf("no dependency candidates; kinds = %v", kinds)
	}
	if kinds["co_missing"] == 0 {
		t.Fatalf("the fixture produced no co-missing candidate, so this test cannot show the two compose; "+
			"kinds = %v", kinds)
	}
	// Dependency candidates come LAST: they carry their own
	// null_together and must re-resolve the block AFTER any null-state
	// rule has decided the source's own null state. That is the
	// PREFERENCE order, and it holds here because no gate in this
	// fixture reads a field a dependency writes; where one does, the
	// writer-before-reader rule hoists the dependency ahead of it
	// (TestSuggestRules_EmittedOrderLeavesNoOrphanRows).
	for i, c := range prof.RuleCandidates {
		if c.Evidence.Detector != "dependency" && i > lastDep {
			t.Errorf("a %s candidate is written after a dependency candidate (index %d > %d)",
				c.Evidence.Detector, i, lastDep)
		}
	}

	cfg := fs.NewMemMap()
	if err := synth.WriteRuleCandidates(cfg.Fs(), prof.RuleCandidates, "/all.json"); err != nil {
		t.Fatalf("WriteRuleCandidates: %v", err)
	}
	spec, _ := synth.SpecFromProfile(roundTripProfile(t, prof), 4000)
	if err := synth.ApplyRulesFile(cfg.Fs(), spec, "/all.json"); err != nil {
		t.Fatalf("the combined candidate file was not consumed unmodified: %v", err)
	}
	if len(spec.Rules) != len(prof.RuleCandidates) {
		t.Errorf("loaded %d rules from a %d-candidate file", len(spec.Rules), len(prof.RuleCandidates))
	}
	data, _, err := synth.SynthBytes(spec, synth.Options{Seed: 44})
	if err != nil {
		t.Fatalf("SynthBytes(combined): %v", err)
	}
	scored, agree, orphan := bandCoherence(t, data)
	if scored == 0 || agree != scored || orphan != 0 {
		t.Errorf("with every detector's candidates applied: %d of %d scored rows coherent, %d orphan",
			agree, scored, orphan)
	}

	// THE ORDER HAS TEETH, and this is where. For candidates AS
	// DETECTED both orders generate the same thing, so an index
	// comparison alone asserts a preference rather than a property. The
	// file exists to be EDITED, and the shape that separates the two
	// orders is a hand-written rule that nulls the SOURCE and not its
	// derived fields — which is the ordinary way an analyst narrows one.
	// Dependency-last REPAIRS it: the derivation's own `null_together`
	// re-reads the source after the edit. Dependency-first leaves every
	// gated row carrying a band flag with no score.
	handEdit := synth.RuleSpec{When: `region == "north"`, SetNull: []string{"score"}}
	run := func(depLast bool) (int, int) {
		t.Helper()
		var rules []synth.RuleSpec
		var deps []synth.RuleSpec
		for _, c := range prof.RuleCandidates {
			if c.Evidence.Detector == "dependency" {
				deps = append(deps, c)
				continue
			}
			rules = append(rules, c)
		}
		rules = append(rules, handEdit)
		if depLast {
			rules = append(rules, deps...)
		} else {
			rules = append(deps, rules...)
		}
		s2, _ := synth.SpecFromProfile(roundTripProfile(t, prof), 4000)
		s2.Rules = rules
		out, _, err := synth.SynthBytes(s2, synth.Options{Seed: 44})
		if err != nil {
			t.Fatalf("SynthBytes(depLast=%v): %v", depLast, err)
		}
		sc, ag, orph := bandCoherence(t, out)
		if sc == 0 {
			t.Fatalf("no scored rows (depLast=%v)", depLast)
		}
		return ag - sc, orph
	}
	lastGap, lastOrphan := run(true)
	firstGap, firstOrphan := run(false)
	if lastGap != 0 || lastOrphan != 0 {
		t.Errorf("dependency-LAST did not repair the hand-narrowed gate: gap=%d orphan=%d",
			lastGap, lastOrphan)
	}
	if firstOrphan <= lastOrphan {
		t.Errorf("dependency-FIRST produced %d orphan rows against %d for dependency-last; the two "+
			"orders must differ on a hand-edited file or the chosen order is decorative",
			firstOrphan, lastOrphan)
	}
	t.Logf("hand-narrowed gate: dep-last orphan=%d, dep-first orphan=%d", lastOrphan, firstOrphan)
	_ = firstGap
}

// depWideSourceSpec is the FU-16 fixture: a cohort built so the two
// dependency renderings the motivating cohort never reaches are forced.
//
// Neither `membership_complement` nor `enumeration_chain` fires on the
// real 381,324-row survey cohort — every dependency it carries is
// numeric-sourced, so both arms shipped with unit assertions on render()
// and no worked example of the detector producing one. They are reached
// here by the two structures that select them:
//
//   - `nonwest` is a boolean of a FOUR-level categorical that is true on
//     THREE of them, so the false side is the smaller one and the
//     rendering is its negated complement rather than a three-term
//     disjunction;
//   - `tier` is a MULTI-VALUED numeric of a categorical, which has no
//     threshold reading at all (a categorical has no order to band), so
//     it renders as an equality chain.
//
// Every field is non-nullable on purpose: null-pattern admission is
// answered from the detector's own counts when neither side is ever
// null, so the fixture needs no co-null accumulator and the two
// renderings are the only variables in it. `decoy` is the negative
// control — a flag determined by nothing.
func depWideSourceSpec(rows int) *synth.Spec {
	return &synth.Spec{
		RowCount: rows,
		Fields: []synth.FieldSpec{
			{Name: "region", Type: "categorical_u8", Distribution: synth.DistWeightedCategorical,
				Params: map[string]any{
					"values":  []any{"east", "north", "south", "west"},
					"weights": []any{0.3, 0.25, 0.25, 0.2},
				}},
			{Name: "plan", Type: "categorical_u8", Distribution: synth.DistWeightedCategorical,
				Params: map[string]any{
					"values":  []any{"basic", "plus", "pro"},
					"weights": []any{0.2, 0.3, 0.5},
				}},
			{Name: "nonwest", Type: "packed_bool", Distribution: synth.DistBernoulli,
				Params: map[string]any{"p": 0.5}},
			{Name: "tier", Type: "u4", Distribution: synth.DistNormal,
				Params: map[string]any{"mean": 2.0, "std": 1.0, "min": 1.0, "max": 3.0}},
			{Name: "decoy", Type: "packed_bool", Distribution: synth.DistBernoulli,
				Params: map[string]any{"p": 0.4}},
		},
		Rules: []synth.RuleSpec{
			{SetExpr: map[string]string{"nonwest": `region != "west"`}},
			{SetExpr: map[string]string{"tier": `plan == "basic" ? 1 : (plan == "plus" ? 2 : 3)`}},
		},
	}
}

// TestSuggestDeps_ComplementAndEnumerationArmsFireEndToEnd is FU-16's
// close: both rendering arms are reached by the DETECTOR (not by a
// render() unit fixture), each is pinned to the exact expression it
// emits, and each is then fed back through `--rules` and shown to
// reproduce its mapping on every generated wire row.
//
// The two are asserted in ONE test because they share a fixture and a
// round trip; splitting them would double a 4,000-row generation to
// state one property twice.
func TestSuggestDeps_ComplementAndEnumerationArmsFireEndToEnd(t *testing.T) {
	src, _, err := synth.SynthBytes(depWideSourceSpec(4000), synth.Options{Seed: 31})
	if err != nil {
		t.Fatalf("SynthBytes(source cohort): %v", err)
	}
	prof, err := synth.ProfileBytes(src, synth.ProfileOptions{
		TopK: 8, IncludeStats: true, SuggestRules: true, Seed: 1,
	})
	if err != nil {
		t.Fatalf("ProfileBytes: %v", err)
	}

	formOf := map[string]string{}
	exprOf := map[string]string{}
	for _, c := range depCandidateRules(prof) {
		for target, expr := range c.SetExpr {
			exprOf[target] = expr
		}
		for _, d := range c.Evidence.Dependency {
			formOf[d.Field] = d.Form
		}
	}

	// The exact strings are pinned rather than pattern-matched: the
	// whole point of recording a worked example is that a reader can see
	// what the arm produces, and a substring test would pass against a
	// chain missing an arm.
	for _, want := range []struct{ target, form, expr string }{
		{"nonwest", "membership_complement", `!(region == "west")`},
		{"tier", "enumeration_chain", `plan == "basic" ? 1 : (plan == "plus" ? 2 : (3))`},
	} {
		if got := formOf[want.target]; got != want.form {
			t.Errorf("%s: form = %q, want %q (candidates = %v)", want.target, got, want.form, exprOf)
		}
		if got := exprOf[want.target]; got != want.expr {
			t.Errorf("%s: set_expr = %q, want %q", want.target, got, want.expr)
		}
	}
	if _, ok := exprOf["decoy"]; ok {
		t.Errorf("the negative control was proposed as derived: %q", exprOf["decoy"])
	}

	// The round trip: the emitted file, unmodified, reproduces both
	// mappings on generated rows.
	cfg := fs.NewMemMap()
	if err := synth.WriteRuleCandidates(cfg.Fs(), prof.RuleCandidates, "/c.json"); err != nil {
		t.Fatalf("WriteRuleCandidates: %v", err)
	}
	spec, _ := synth.SpecFromProfile(roundTripProfile(t, prof), 4000)
	if err := synth.ApplyRulesFile(cfg.Fs(), spec, "/c.json"); err != nil {
		t.Fatalf("ApplyRulesFile: %v", err)
	}
	data, _, err := synth.SynthBytes(spec, synth.Options{Seed: 77})
	if err != nil {
		t.Fatalf("SynthBytes(generated): %v", err)
	}

	r := bytes.NewReader(data)
	if err := encoding.ReadHeader(r); err != nil {
		t.Fatalf("read header: %v", err)
	}
	schema, err := encoding.ReadSchema(r)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	dictOf := map[string]*encoding.Dictionary{}
	for i := range schema.Fields {
		if d := schema.Fields[i].Dictionary; d != nil {
			dictOf[schema.Fields[i].Name] = d
		}
	}
	if dictOf["region"] == nil || dictOf["plan"] == nil {
		t.Fatalf("generated cohort lost a source dictionary: %v", dictOf)
	}
	tierOf := map[string]float64{"basic": 1, "plus": 2, "pro": 3}
	rr := encoding.NewRecordReader(r, schema)
	values := map[string]float64{}
	nulls := map[string]bool{}
	rows, badComplement, badEnumeration := 0, 0, 0
	seenPlans := map[string]int{}
	for {
		if err := rr.ReadRecord(values, nulls); err != nil {
			break
		}
		rows++
		region := dictOf["region"].Resolve(uint32(values["region"]))
		plan := dictOf["plan"].Resolve(uint32(values["plan"]))
		seenPlans[plan]++
		if (values["nonwest"] != 0) != (region != "west") {
			badComplement++
		}
		if values["tier"] != tierOf[plan] {
			badEnumeration++
		}
	}
	if rows == 0 {
		t.Fatal("no rows generated")
	}
	if len(seenPlans) != 3 {
		t.Fatalf("the generated cohort carries %d plan level(s), so the chain's arms are not all exercised: %v",
			len(seenPlans), seenPlans)
	}
	if badComplement != 0 {
		t.Errorf("membership_complement disagreed on %d of %d wire rows", badComplement, rows)
	}
	if badEnumeration != 0 {
		t.Errorf("enumeration_chain disagreed on %d of %d wire rows", badEnumeration, rows)
	}
	t.Logf("FU-16 worked example: %d rows, plan levels %v; "+
		"nonwest := %s (membership_complement); tier := %s (enumeration_chain)",
		rows, seenPlans, exprOf["nonwest"], exprOf["tier"])
}
