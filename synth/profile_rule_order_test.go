package synth_test

import (
	"bytes"
	"sort"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/synth"
)

// orderSourceSpec builds a cohort carrying ALL THREE detectable
// relationships plus both shapes of the ordering hazard, generated
// through the rule layer itself so "recovered" and "reproducible" mean
// the same thing (the pattern blockSourceSpec and depSourceSpec set).
//
//	blockmate + screener  — one co-missing block, blockmate FIRST
//	screener              — gates `answer` at level 1 AND at null
//	level -> derived      — an exact dependency, derived = level >= 3
//	derived               — gates `opinion` at level 0
//
// The two hazards are the two directions the preference order got wrong:
// a BLOCK holding a gate's source (`screener`), and a DEPENDENCY writing
// a gate's source (`derived`). blockmate is declared first so it is the
// block's gate field — with `screener` first the block would copy
// screener's own null onto blockmate and could not move the gate's
// input, which is exactly the fixture that silently stops testing the
// thing.
func orderSourceSpec(rows int) *synth.Spec {
	u4 := func(name string, nullable bool, nullRate float64, mean, min, max float64) synth.FieldSpec {
		return synth.FieldSpec{
			Name: name, Type: "u4", Nullable: nullable, NullRate: nullRate,
			Distribution: synth.DistNormal,
			Params:       map[string]any{"mean": mean, "std": 0.9, "min": min, "max": max},
		}
	}
	return &synth.Spec{
		RowCount: rows,
		Fields: []synth.FieldSpec{
			u4("blockmate", true, 0.30, 2, 1, 3),
			u4("screener", true, 0.30, 2, 1, 3),
			// `answer` and `opinion` draw NO null of their own: the gate
			// thresholds are 0.98/0.02, so a target carrying even a 5%
			// MCAR rate of its own lands between them at the gate's open
			// levels and is classified as ungated. Their nulls come from
			// the rules alone.
			u4("answer", true, 0, 4, 1, 7),
			u4("level", false, 0, 2.5, 1, 4),
			{Name: "derived", Type: "packed_bool", Distribution: synth.DistBernoulli,
				Params: map[string]any{"p": 0.5}},
			u4("opinion", true, 0, 4, 1, 7),
		},
		Rules: []synth.RuleSpec{
			{NullTogether: []string{"blockmate", "screener"}},
			{SetExpr: map[string]string{"derived": "round(level) >= 3"}},
			{When: "isnull(screener) || round(screener) == 1", SetNull: []string{"answer"}},
			{When: "round(derived) == 0", SetNull: []string{"opinion"}},
		},
	}
}

// orphans counts rows where `gate` is null (or holds `gatedValue`) while
// `target` still carries a value — the incoherence a gate exists to
// remove, stated as a row count rather than a rate so a shortfall is
// reported as the number it is.
type orphanCount struct {
	gateNullTargetPresent int
	gateZeroTargetPresent int
}

func countOrphans(t *testing.T, data []byte, gate, target string) orphanCount {
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
	var out orphanCount
	for {
		if err := rr.ReadRecord(values, nulls); err != nil {
			break
		}
		if nulls[target] {
			continue
		}
		switch {
		case nulls[gate]:
			out.gateNullTargetPresent++
		case values[gate] == 0:
			out.gateZeroTargetPresent++
		}
	}
	return out
}

// detectorPreferenceOrder is the order E3-S1/S2/S3 emitted: gating,
// then co-missing blocks, then exact dependencies, each detector's own
// list in its own order. It is the NEGATIVE CONTROL — the order the fix
// replaced — reconstructed from the emitted candidates by a stable sort,
// so the control and the subject are the same proposals in two orders
// and nothing else differs.
func detectorPreferenceOrder(cands []synth.RuleSpec) []synth.RuleSpec {
	rank := map[string]int{"gating": 0, "co_missing": 1, "dependency": 2}
	out := append([]synth.RuleSpec(nil), cands...)
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := 3, 3
		if out[i].Evidence != nil {
			ri = rank[out[i].Evidence.Detector]
		}
		if out[j].Evidence != nil {
			rj = rank[out[j].Evidence.Detector]
		}
		return ri < rj
	})
	return out
}

// TestSuggestRules_EmittedOrderLeavesNoOrphanRows is the epic's warrant,
// and it is deliberately ONE test with a positive and a negative half
// because each is trivially satisfiable by abandoning the other: a file
// that nulls every target leaves no orphan, and an order that changes
// nothing cannot be shown to matter.
//
// Positive: the candidates AS EMITTED, applied unmodified, leave zero
// rows where a gate's field is absent and its target still answered.
// Negative: the SAME candidates in the detector preference order — the
// order this fix replaced — leave a four-figure count of them.
func TestSuggestRules_EmittedOrderLeavesNoOrphanRows(t *testing.T) {
	const rows = 6000

	src, _, err := synth.SynthBytes(orderSourceSpec(rows), synth.Options{Seed: 71})
	if err != nil {
		t.Fatalf("SynthBytes(source cohort): %v", err)
	}
	prof, err := synth.ProfileBytes(src, synth.ProfileOptions{
		TopK: 8, IncludeStats: true, SuggestRules: true, Seed: 1,
	})
	if err != nil {
		t.Fatalf("ProfileBytes: %v", err)
	}

	// THE FIXTURE GUARD. Every assertion below is vacuous if the
	// fixture stops producing the candidate it is about, and a vacuous
	// assertion reads as coverage. E2-S1, E3-S1 and E3-S2 each shipped a
	// fixture that quietly stopped exercising its own slot, in a
	// different form each time; this one fails loudly instead.
	var (
		gateOnScreener, gateOnDerived, block, dep int
		blockFirstMember                          string
	)
	for _, c := range prof.RuleCandidates {
		if c.Evidence == nil {
			t.Fatalf("candidate without evidence: %+v", c)
		}
		switch c.Evidence.Detector {
		case "gating":
			switch c.Evidence.GateField {
			case "screener":
				gateOnScreener++
			case "derived":
				gateOnDerived++
			}
		case "co_missing":
			block++
			if len(c.NullTogether) > 0 && blockFirstMember == "" {
				blockFirstMember = c.NullTogether[0]
			}
		case "dependency":
			dep++
		}
	}
	if gateOnScreener == 0 || gateOnDerived == 0 || block == 0 || dep == 0 {
		t.Fatalf("fixture stopped exercising a candidate kind: gate(screener)=%d gate(derived)=%d "+
			"block=%d dependency=%d; warnings = %v",
			gateOnScreener, gateOnDerived, block, dep, prof.Warnings)
	}
	if blockFirstMember != "blockmate" {
		t.Fatalf("block gate field is %q, want \"blockmate\" — with screener first the block cannot "+
			"move the gate's own input and this fixture tests nothing",
			blockFirstMember)
	}

	// ONE deletion, made for both arms alike, and the reason is a
	// finding rather than a convenience. The detector also proposes the
	// direct gate `level` -> `opinion`: `derived` IS round(level) >= 3,
	// so every gate on `derived` is equivalent to one on `level`, and
	// `level` is written by nothing, so that proxy gate fires on the
	// final value and nulls `opinion` on exactly the rows the hazard
	// would have left answered. The dependency-to-gate hazard is
	// therefore INVISIBLE while the proxy is present — measured the same
	// way on the motivating cohort, where gate(`familiarity`) masks
	// gate(`aware`) after `aware` is rewritten, and the violation costs
	// 0 rows until the analyst deletes the proxy. Deleting a proposal is
	// exactly what the candidate file is for, so the fixture makes that
	// edit rather than pretending the hazard is unreachable.
	candidates := make([]synth.RuleSpec, 0, len(prof.RuleCandidates))
	deleted := 0
	for _, c := range prof.RuleCandidates {
		if c.Evidence.Detector == "gating" && c.Evidence.GateField == "level" {
			deleted++
			continue
		}
		candidates = append(candidates, c)
	}
	if deleted != 1 {
		t.Fatalf("deleted %d proxy gate(s) on \"level\", want exactly 1 — the fixture no longer carries "+
			"the proxy that makes this deletion meaningful", deleted)
	}

	gen := func(rules []synth.RuleSpec) []byte {
		spec, _ := synth.SpecFromProfile(prof, rows)
		spec.Rules = rules
		data, _, err := synth.SynthBytes(spec, synth.Options{Seed: 4242})
		if err != nil {
			t.Fatalf("SynthBytes(generated cohort): %v", err)
		}
		return data
	}

	emitted := gen(candidates)
	control := gen(detectorPreferenceOrder(candidates))

	cases := []struct {
		name         string
		gate, target string
		// which arm of orphanCount carries this hazard.
		null bool
	}{
		{name: "a block holding a gate's source", gate: "screener", target: "answer", null: true},
		{name: "a dependency writing a gate's source", gate: "derived", target: "opinion"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := countOrphans(t, emitted, tc.gate, tc.target)
			was := countOrphans(t, control, tc.gate, tc.target)
			g, w := got.gateZeroTargetPresent, was.gateZeroTargetPresent
			if tc.null {
				g, w = got.gateNullTargetPresent, was.gateNullTargetPresent
			}
			if g != 0 {
				t.Errorf("emitted order leaves %d orphan row(s) of %d: %q is absent and %q is still answered",
					g, rows, tc.gate, tc.target)
			}
			// The negative control. Without it the first assertion
			// passes on any order at all, including one that could never
			// have produced the defect.
			if w == 0 {
				t.Errorf("the detector preference order leaves no orphan either, so this fixture cannot "+
					"distinguish the two orders and the assertion above is vacuous (gate %q, target %q)",
					tc.gate, tc.target)
			}
			t.Logf("gate %q -> %q: emitted order %d orphan row(s), detector preference order %d of %d",
				tc.gate, tc.target, g, w, rows)
		})
	}
}

// TestSuggestRules_EmittedOrderIsMinimallyPerturbed pins the other half
// of the rule: only the candidates a dependency FORCES to move, move. A
// walk that reversed the file, or always put blocks first, would satisfy
// the orphan test above and destroy the reason the preference order
// exists at all — gates are the largest structural claims and the ones
// most in need of the analyst's judgement.
//
// The property asserted is the one the walk actually guarantees: a
// candidate that jumped AHEAD of a candidate the detectors ranked before
// it did so because something emitted after it depends on it. That is
// false for every wholesale shuffle and true for a just-in-time hoist.
func TestSuggestRules_EmittedOrderIsMinimallyPerturbed(t *testing.T) {
	const rows = 6000
	src, _, err := synth.SynthBytes(orderSourceSpec(rows), synth.Options{Seed: 71})
	if err != nil {
		t.Fatalf("SynthBytes(source cohort): %v", err)
	}
	prof, err := synth.ProfileBytes(src, synth.ProfileOptions{
		TopK: 8, IncludeStats: true, SuggestRules: true, Seed: 1,
	})
	if err != nil {
		t.Fatalf("ProfileBytes: %v", err)
	}
	if len(prof.RuleCandidates) < 4 {
		t.Fatalf("fixture produced %d candidate(s), too few to say anything about order; warnings = %v",
			len(prof.RuleCandidates), prof.Warnings)
	}

	emitted := prof.RuleCandidates
	detRank := map[string]int{}
	for i, c := range detectorPreferenceOrder(emitted) {
		detRank[candKey(c)] = i
	}
	deps := candDependencies(emitted)

	jumped := 0
	for i := range emitted {
		ahead := false
		for j := i + 1; j < len(emitted); j++ {
			if detRank[candKey(emitted[i])] > detRank[candKey(emitted[j])] {
				ahead = true
				break
			}
		}
		if !ahead {
			continue
		}
		jumped++
		// Something later must need it. Anything else is a shuffle.
		needed := false
		for j := i + 1; j < len(emitted); j++ {
			if deps[candKey(emitted[j])][candKey(emitted[i])] {
				needed = true
				break
			}
		}
		if !needed {
			t.Errorf("candidate %d (%s) was hoisted ahead of a candidate the detectors ranked before it, "+
				"and nothing emitted after it reads a field it writes", i, candKey(emitted[i]))
		}
	}
	// The fixture guard: with nothing hoisted the loop above is vacuous.
	if jumped == 0 {
		t.Fatal("no candidate was hoisted, so this fixture carries no ordering constraint and the " +
			"assertion above means nothing")
	}
	t.Logf("%d of %d candidate(s) hoisted", jumped, len(emitted))
}

// candKey addresses a candidate independently of its position.
func candKey(c synth.RuleSpec) string {
	k := c.When + "|"
	for _, f := range c.SetNull {
		k += f + ","
	}
	k += "|"
	for _, f := range c.NullTogether {
		k += f + ","
	}
	k += "|"
	for _, f := range sortedExprKeys(c.SetExpr) {
		k += f + "=" + c.SetExpr[f] + ","
	}
	return k
}

func sortedExprKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// candDependencies computes, for each candidate, the candidates it reads
// a written field from. It re-derives the read and write sets from the
// rule slots rather than calling the production helper, so the test can
// disagree with the implementation.
func candDependencies(cands []synth.RuleSpec) map[string]map[string]bool {
	writes := make([]map[string]bool, len(cands))
	written := map[string]bool{}
	for i, c := range cands {
		w := map[string]bool{}
		for _, f := range c.SetNull {
			w[f] = true
		}
		for _, f := range c.NullTogether {
			w[f] = true
		}
		for f := range c.SetExpr {
			w[f] = true
		}
		for f := range c.Set {
			w[f] = true
		}
		writes[i] = w
		for f := range w {
			written[f] = true
		}
	}
	out := map[string]map[string]bool{}
	for i, c := range cands {
		reads := map[string]bool{}
		for _, src := range append([]string{c.When}, valuesOf(c.SetExpr)...) {
			for f := range written {
				if containsIdent(src, f) {
					reads[f] = true
				}
			}
		}
		deps := map[string]bool{}
		for j := range cands {
			if i == j {
				continue
			}
			for f := range reads {
				if writes[j][f] {
					deps[candKey(cands[j])] = true
					break
				}
			}
		}
		out[candKey(c)] = deps
	}
	return out
}

func valuesOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for _, k := range sortedExprKeys(m) {
		out = append(out, m[k])
	}
	return out
}

// containsIdent reports whether src mentions field as a whole
// identifier.
func containsIdent(src, field string) bool {
	for i := 0; i+len(field) <= len(src); i++ {
		if src[i:i+len(field)] != field {
			continue
		}
		if i > 0 && identByte(src[i-1]) {
			continue
		}
		if j := i + len(field); j < len(src) && identByte(src[j]) {
			continue
		}
		return true
	}
	return false
}

func identByte(c byte) bool {
	return c == '_' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
