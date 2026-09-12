package synth_test

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/synth"
)

// npsBlockSpec is the committed, shape-equivalent form of the real
// 122-field / 381,324-row survey profile's motivating block: a `u4` NPS
// score and three `packed_bool` band flags, all four nullable at the
// SAME declared null_rate 0.8260, exactly as `nps` / `promoter` /
// `passive` / `detractor` carry it there.
//
// `tail` is not part of the block and is the RNG probe: it is drawn
// AFTER every block member, so if the rule pass consumed a single draw
// on any row, every later row's tail would shift.
func npsBlockSpec(rows int, rates map[string]float64, rules []synth.RuleSpec) *synth.Spec {
	rate := func(name string, fallback float64) float64 {
		if r, ok := rates[name]; ok {
			return r
		}
		return fallback
	}
	flag := func(name string) synth.FieldSpec {
		return synth.FieldSpec{Name: name, Type: "packed_bool", Nullable: true,
			NullRate: rate(name, 0.8260), Distribution: synth.DistBernoulli,
			Params: map[string]any{"p": 0.3}}
	}
	return &synth.Spec{
		RowCount: rows,
		Fields: []synth.FieldSpec{
			{Name: "id", Type: "u32", Distribution: synth.DistMonotonicFrom,
				Params: map[string]any{"start": 1.0}},
			{Name: "aware", Type: "packed_bool", Distribution: synth.DistBernoulli,
				Params: map[string]any{"p": 0.5}},
			{Name: "nps", Type: "u4", Nullable: true, NullRate: rate("nps", 0.8260),
				Distribution: synth.DistUniform, Params: map[string]any{"min": 0.0, "max": 10.999}},
			flag("promoter"), flag("passive"), flag("detractor"),
			{Name: "tail", Type: "u4", Distribution: synth.DistUniform,
				Params: map[string]any{"min": 0.0, "max": 15.999}},
		},
		Rules: rules,
	}
}

var npsBlockFields = []string{"nps", "promoter", "passive", "detractor"}

// blockPresence returns, per row, how many of the block's fields carry a
// value, plus the count of rows on which ALL of them do.
func blockPresence(t *testing.T, data []byte) (perRow []int, allPresent int) {
	t.Helper()
	nulls := make(map[string][]bool, len(npsBlockFields))
	for _, name := range npsBlockFields {
		_, nulls[name] = readFieldRows(t, data, name)
	}
	rows := len(nulls["nps"])
	perRow = make([]int, rows)
	for i := 0; i < rows; i++ {
		for _, name := range npsBlockFields {
			if !nulls[name][i] {
				perRow[i]++
			}
		}
		if perRow[i] == len(npsBlockFields) {
			allPresent++
		}
	}
	return perRow, allPresent
}

// TestRules_NullTogetherSharesOneDecision is the story's headline claim
// and the committed form of the real-profile calibration. Three
// assertions over ONE pair of generated files, because each is trivially
// satisfiable by abandoning the others — a pass that nulls the whole
// block on every row satisfies co-missingness, one that nulls nothing
// satisfies the rate:
//
//  1. CO-MISSINGNESS. Every row carries all four block fields or none
//     of them. A partial block is the defect.
//  2. THE RATE IS THE FIRST FIELD'S COMPLEMENT, not the product of the
//     four. 0.174, not 0.174^4.
//  3. THE FIXTURE CAN SEE THE DEFECT. The same spec with the rule
//     removed produces the product — vanishingly few all-present rows —
//     so claim 2 is a measurement and not a tautology about a block
//     that was always present.
func TestRules_NullTogetherSharesOneDecision(t *testing.T) {
	const rows = 40000
	ruled, res, err := synth.SynthBytes(
		npsBlockSpec(rows, nil, []synth.RuleSpec{{NullTogether: npsBlockFields}}),
		synth.Options{Seed: 909})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	// All four members declare the same rate, so nothing is discarded
	// and nothing is warned about.
	if len(res.Warnings) != 0 {
		t.Fatalf("unexpected warnings on a block whose members agree: %v", res.Warnings)
	}

	perRow, allPresent := blockPresence(t, ruled)
	for i, n := range perRow {
		if n != 0 && n != len(npsBlockFields) {
			t.Fatalf("row %d: %d of %d block fields present — a null_together block is "+
				"all present or all absent", i, n, len(npsBlockFields))
		}
	}
	got := float64(allPresent) / float64(rows)
	// p = 0.174 at n = 40,000 has sd 0.0019; this band is ~4 sd.
	if got < 0.166 || got > 0.182 {
		t.Fatalf("block all-present rate = %.4f (%d/%d), want near the FIRST field's "+
			"null_rate complement 0.1740", got, allPresent, rows)
	}

	plain, _, err := synth.SynthBytes(npsBlockSpec(rows, nil, nil), synth.Options{Seed: 909})
	if err != nil {
		t.Fatalf("rules-free SynthBytes: %v", err)
	}
	_, plainAllPresent := blockPresence(t, plain)
	// 0.174^4 * 40,000 is ~36. Anything near the ruled figure means the
	// block was already co-missing and this test proves nothing.
	if plainAllPresent > rows/100 {
		t.Fatalf("the rules-free run already has %d/%d all-present rows; the fixture can no "+
			"longer see the independent-draw defect this rule exists to fix", plainAllPresent, rows)
	}
	if allPresent < 10*plainAllPresent {
		t.Fatalf("all-present rows: %d ruled vs %d rules-free — the rule did not change the "+
			"block's co-presence", allPresent, plainAllPresent)
	}
}

// TestRules_NullTogetherCopiesTheFirstFieldsRateAndSaysSo pins the
// RESOLUTION RULE together with its cost, in one test, because each half
// is meaningless alone: that the first field wins is only defensible if
// the members whose rate is discarded are named out loud.
//
// The block's gate declares 0.90 and its members 0.10 — a divergence no
// reading can call incidental — so the realized all-present rate must be
// the gate's complement 0.10, NOT the members' 0.90 and not any blend.
func TestRules_NullTogetherCopiesTheFirstFieldsRateAndSaysSo(t *testing.T) {
	const rows = 20000
	rates := map[string]float64{
		"nps": 0.90, "promoter": 0.10, "passive": 0.10, "detractor": 0.10,
	}
	data, res, err := synth.SynthBytes(
		npsBlockSpec(rows, rates, []synth.RuleSpec{{NullTogether: npsBlockFields}}),
		synth.Options{Seed: 4242})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}

	_, allPresent := blockPresence(t, data)
	got := float64(allPresent) / float64(rows)
	// p = 0.10 at n = 20,000 has sd 0.0021; ~4 sd band.
	if got < 0.092 || got > 0.109 {
		t.Fatalf("block all-present rate = %.4f, want the GATE's complement 0.1000 — "+
			"the members' own 0.90 rate must be ignored, not blended", got)
	}

	var found string
	for _, w := range res.Warnings {
		if strings.Contains(w, "null_together applies") {
			found = w
		}
	}
	if found == "" {
		t.Fatalf("no warning named the discarded null_rate; warnings = %v", res.Warnings)
	}
	for _, want := range []string{`rule 0`, `"nps"`, `"promoter"`, `"passive"`, `"detractor"`} {
		if !strings.Contains(found, want) {
			t.Fatalf("warning %q does not name %s", found, want)
		}
	}

	// ...and it is classified where a reader will see it, not in the
	// catch-all: the block applied, but a number the author wrote did
	// not.
	groups := synth.GroupWarnings(res.Warnings)
	var kind string
	for _, g := range groups {
		for _, m := range g.Members {
			if m == found {
				kind = g.Kind
				if !g.Attention {
					t.Fatalf("warning kind %q is not marked Attention", g.Kind)
				}
			}
		}
	}
	if kind == "" || kind == "other" {
		t.Fatalf("warning landed in kind %q, want its own classified kind", kind)
	}
}

// TestRules_NullTogetherAgreeingRatesAreSilent is the other half of the
// threshold's meaning at the END-TO-END surface: the warning must not
// fire on the ordinary block, or it is noise on every real spec. The
// boundary itself is pinned against the constant in
// synth/rules_null_block_internal_test.go.
func TestRules_NullTogetherAgreeingRatesAreSilent(t *testing.T) {
	rates := map[string]float64{
		// A coding difference / partial re-ask: within the threshold.
		"nps": 0.8260, "promoter": 0.8260, "passive": 0.8300, "detractor": 0.8190,
	}
	_, res, err := synth.SynthBytes(
		npsBlockSpec(500, rates, []synth.RuleSpec{{NullTogether: npsBlockFields}}),
		synth.Options{Seed: 7})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("a block whose members drift within the threshold must be silent: %v", res.Warnings)
	}
}

// TestRules_NullTogetherAndSetNullComposeByDeclarationOrder asserts the
// cross-rule interaction in BOTH directions over one fixture. The two
// orderings must differ, and each must differ in the documented way —
// a test that only checked "they differ" would pass on a pass that
// applied the rules in a fixed order of its own.
func TestRules_NullTogetherAndSetNullComposeByDeclarationOrder(t *testing.T) {
	const rows = 4000
	block := synth.RuleSpec{NullTogether: npsBlockFields}

	// (a) BLOCK THEN set_null over the GATE. The block is decided from
	// the gate's drawn state, then the gate alone is nulled — so the
	// last write wins and the block is broken on exactly the rows it had
	// decided were present.
	blockFirst, _, err := synth.SynthBytes(
		npsBlockSpec(rows, nil, []synth.RuleSpec{block, {SetNull: []string{"nps"}}}),
		synth.Options{Seed: 55})
	if err != nil {
		t.Fatalf("SynthBytes (block first): %v", err)
	}
	npsNulls := func(data []byte) []bool {
		_, n := readFieldRows(t, data, "nps")
		return n
	}
	for i, isNull := range npsNulls(blockFirst) {
		if !isNull {
			t.Fatalf("row %d: an unconditional set_null AFTER the block left nps carrying a value", i)
		}
	}
	perRow, allPresent := blockPresence(t, blockFirst)
	if allPresent != 0 {
		t.Fatalf("block-then-set_null: %d all-present rows, want 0 — the gate is nulled last", allPresent)
	}
	broken := 0
	for _, n := range perRow {
		if n == 3 {
			broken++
		}
	}
	if broken == 0 {
		t.Fatal("block-then-set_null produced no partially-present block; the later rule " +
			"must break the block it follows, and this fixture can no longer see it")
	}

	// (b) set_null over the GATE THEN the block. The block re-decides
	// from what the gate holds by then, which is null — so every member
	// follows it and the block is whole on every row.
	setNullFirst, _, err := synth.SynthBytes(
		npsBlockSpec(rows, nil, []synth.RuleSpec{{SetNull: []string{"nps"}}, block}),
		synth.Options{Seed: 55})
	if err != nil {
		t.Fatalf("SynthBytes (set_null first): %v", err)
	}
	perRow, allPresent = blockPresence(t, setNullFirst)
	if allPresent != 0 {
		t.Fatalf("set_null-then-block: %d all-present rows, want 0", allPresent)
	}
	for i, n := range perRow {
		if n != 0 {
			t.Fatalf("row %d: %d block fields present after set_null-then-block; the block "+
				"must follow the gate the earlier rule nulled", i, n)
		}
	}
}

// TestRules_NullTogetherIsTheRulesLastWord pins the WITHIN-rule order.
// The slots of one rule have no order an author can read — they are a
// JSON array and a Go map inside one object — so the order is fixed in
// applyNullTogether and asserted here rather than left to whichever
// loop happens to come first in the source.
//
// Both directions, because each is the useful composition of one slot
// with the block and each fails under the reversed order:
//
//  1. set_null over the GATE plus the block, in ONE rule, nulls the
//     whole block on every row. Reversed, the block would be decided
//     from the gate's DRAWN state and the set_null would then break it
//     apart on exactly the rows it had called present.
//  2. set over the GATE plus the block, in ONE rule, makes the whole
//     block present on every row. Reversed, the members would follow
//     the gate's drawn null and the literal would sit alone beside
//     three nulls.
//
// This is the falsification target for the phase order: moving
// applyNullTogether ahead of the per-field writes fails both halves.
func TestRules_NullTogetherIsTheRulesLastWord(t *testing.T) {
	const rows = 2000
	block := npsBlockFields

	nulled, _, err := synth.SynthBytes(
		npsBlockSpec(rows, nil, []synth.RuleSpec{{SetNull: []string{"nps"}, NullTogether: block}}),
		synth.Options{Seed: 17})
	if err != nil {
		t.Fatalf("SynthBytes (set_null + block): %v", err)
	}
	perRow, _ := blockPresence(t, nulled)
	for i, n := range perRow {
		if n != 0 {
			t.Fatalf("row %d: %d block fields present after set_null over the gate in the SAME "+
				"rule; the block is the rule's last word and must follow the nulled gate", i, n)
		}
	}

	present, _, err := synth.SynthBytes(
		npsBlockSpec(rows, nil, []synth.RuleSpec{{Set: map[string]any{"nps": 8.0}, NullTogether: block}}),
		synth.Options{Seed: 17})
	if err != nil {
		t.Fatalf("SynthBytes (set + block): %v", err)
	}
	perRow, allPresent := blockPresence(t, present)
	for i, n := range perRow {
		if n != len(block) {
			t.Fatalf("row %d: %d of %d block fields present after a set over the gate in the "+
				"SAME rule; a rule stating the gate HAS a value states the block does",
				i, n, len(block))
		}
	}
	if allPresent != rows {
		t.Fatalf("all-present rows = %d, want %d", allPresent, rows)
	}
}

// TestRules_NullTogetherConsumesNoRNG asserts the block copy takes no
// draw. `tail` is drawn after every block member on every row, so one
// stolen draw anywhere shifts every subsequent row's tail.
func TestRules_NullTogetherConsumesNoRNG(t *testing.T) {
	const rows = 2000
	ruled, _, err := synth.SynthBytes(
		npsBlockSpec(rows, nil, []synth.RuleSpec{{NullTogether: npsBlockFields}}),
		synth.Options{Seed: 31})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	plain, _, err := synth.SynthBytes(npsBlockSpec(rows, nil, nil), synth.Options{Seed: 31})
	if err != nil {
		t.Fatalf("rules-free SynthBytes: %v", err)
	}
	a := readF64Field(t, ruled, "tail")
	b := readF64Field(t, plain, "tail")
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("tail row %d: %v with the block rule vs %v without — the pass consumed RNG",
				i, a[i], b[i])
		}
	}
	// ...and the rule really fired, so the comparison is not vacuous.
	_, ruledAll := blockPresence(t, ruled)
	_, plainAll := blockPresence(t, plain)
	if ruledAll == plainAll {
		t.Fatalf("the block changed no row's co-presence (%d vs %d)", ruledAll, plainAll)
	}
}

// TestRules_NullTogetherIsLive replaces TestRules_InertSlotsStayInert,
// which E1-S4 narrowed to this one remaining slot. There are no inert
// slots left, so the claim is inverted rather than deleted: the same
// pinned rules-free spec must now generate DIFFERENT bytes, and the
// `when` of a null_together-only rule must be evaluated.
func TestRules_NullTogetherIsLive(t *testing.T) {
	spec := ruleFreeSpec()
	spec.Rules = []synth.RuleSpec{{NullTogether: []string{"perception", "nps"}}}
	data, res, err := synth.SynthBytes(spec, synth.Options{Seed: 4242})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got == preRuleApplySpecHash {
		t.Fatal("a null_together rule generated the rules-free bytes; the slot is still inert")
	}

	// ruleFreeSpec's `nps` is NOT nullable, so the block asks for a null
	// the file cannot record — the same silent-zero gotcha set_null has,
	// reported through the same warning kind.
	var warned bool
	for _, w := range res.Warnings {
		if strings.Contains(w, `rule 0 null_together names non-nullable field "nps"`) {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("no warning for a non-nullable block member; warnings = %v", res.Warnings)
	}
	// It shares set_null's kind — the finding has one shape whichever
	// slot asked for the null — so the message must not be matched on
	// its slot name. Searched rather than indexed: GroupWarnings sorts
	// attention-first then by count, and this spec emits two attention
	// kinds.
	var classified bool
	for _, g := range synth.GroupWarnings(res.Warnings) {
		if g.Kind == "rule cannot null a non-nullable field" {
			classified = true
		}
	}
	if !classified {
		t.Fatalf("warning not classified with set_null's kind: %+v", synth.GroupWarnings(res.Warnings))
	}

	// A null_together-only rule is REACHED, so its gate is evaluated:
	// `isnull` over an undeclared field compiles and refuses at row time
	// (E1-S1), which is the cheapest probe for "was this gate run".
	spec = ruleFreeSpec()
	spec.Rules = []synth.RuleSpec{
		{When: `isnull("no_such_field")`, NullTogether: []string{"perception", "nps"}},
	}
	if _, _, err := synth.SynthBytes(spec, synth.Options{Seed: 4242}); err == nil {
		t.Fatal("a rule carrying a live null_together must evaluate its when")
	}
}

// TestRules_NullTogetherRespectsItsGate asserts the block is not applied
// to a row its `when` did not select — the rows outside the gate must
// keep the independent per-field nulls they drew.
func TestRules_NullTogetherRespectsItsGate(t *testing.T) {
	const rows = 20000
	data, _, err := synth.SynthBytes(
		npsBlockSpec(rows, nil, []synth.RuleSpec{{When: "aware == 1", NullTogether: npsBlockFields}}),
		synth.Options{Seed: 88})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	aware := readF64Field(t, data, "aware")
	perRow, _ := blockPresence(t, data)
	var gatedPartial, openPartial int
	for i, n := range perRow {
		partial := n != 0 && n != len(npsBlockFields)
		if aware[i] == 1 {
			if partial {
				gatedPartial++
			}
			continue
		}
		if partial {
			openPartial++
		}
	}
	if gatedPartial != 0 {
		t.Fatalf("%d gated rows carry a partial block", gatedPartial)
	}
	if openPartial == 0 {
		t.Fatal("no non-gated row carries a partial block; the rule reached rows its when " +
			"did not select, or the fixture is degenerate")
	}
}
