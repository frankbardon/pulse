package synth_test

import (
	"bytes"
	"encoding/json"
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/synth"
)

// npsBandSpec is the committed, shape-equivalent form of the real-profile
// calibration: a `u4` NPS score that is null on ~83% of rows and three
// `packed_bool` flags carrying the SAME null rate — the exact shape
// `nps` / `promoter` / `passive` / `detractor` have on the motivating
// 122-field, 381,324-row survey profile (null_rate 0.8260 on all four).
//
// The rule set is deliberately TWO rules rather than one, and that is
// the fixture's second job. `int(nps)` normalises the row's
// pre-rounding float to the integer the WIRE will carry, and the bands
// must be classified from THAT — so they cannot live in the same rule as
// the normalisation, because every expression in one rule sees the row
// as it was before the rule ran (see ruleApplier.apply's SNAPSHOT
// paragraph). Written as one rule the bands would classify the float,
// and a draw of 8.7 would be `passive` while the wire carried a 9.
//
// Both rules are gated on `!isnull(nps)`: a `set_expr` CLEARS the target's
// null mask (a rule stating a value is stating the field has one), so an
// ungated normalisation would un-null 83% of the column, and an ungated
// classification would read the drawn-but-not-written value behind a
// null and flag a respondent who was never asked.
func npsBandSpec(rows int, rules []synth.RuleSpec) *synth.Spec {
	flag := func(name string) synth.FieldSpec {
		return synth.FieldSpec{Name: name, Type: "packed_bool", Nullable: true,
			NullRate: 0.8260, Distribution: synth.DistBernoulli,
			Params: map[string]any{"p": 0.3}}
	}
	return &synth.Spec{
		RowCount: rows,
		Fields: []synth.FieldSpec{
			{Name: "id", Type: "u32", Distribution: synth.DistMonotonicFrom,
				Params: map[string]any{"start": 1.0}},
			{Name: "nps", Type: "u4", Nullable: true, NullRate: 0.8260,
				Distribution: synth.DistUniform, Params: map[string]any{"min": 0.0, "max": 10.999}},
			flag("promoter"), flag("passive"), flag("detractor"),
		},
		Rules: rules,
	}
}

// npsBandRules is the story's motivating example, verbatim in shape.
func npsBandRules() []synth.RuleSpec {
	return []synth.RuleSpec{
		{When: "!isnull(nps)", SetExpr: map[string]string{"nps": "int(nps)"}},
		{When: "!isnull(nps)", SetExpr: map[string]string{
			"promoter":  "nps >= 9",
			"passive":   "nps >= 7 && nps < 9",
			"detractor": "nps < 7",
		}},
	}
}

// TestRules_SetExprThreeBandNPS is the story's central end-to-end claim
// and the committed form of the real-profile calibration. Four
// assertions over ONE generated file, because each is trivially
// satisfiable by abandoning the others — a pass that sets every flag
// satisfies "a flag is set", one that sets none satisfies "no flag
// outside the gate":
//
//  1. EXACTLY ONE flag is set on every row carrying an nps. The three
//     bands partition the reals, so two flags or none is a coercion or
//     an ordering fault, not a data outcome.
//  2. The flag that is set MATCHES the wire nps. This is the bool ->
//     packed_bool cell of the coercion matrix asserted on the FILE, and
//     it is exact only because the normalisation rule ran first.
//  3. Every flag on a carrying row is non-null, because a set_expr
//     clears the mask.
//  4. On a row where nps is NULL the flags are byte-for-byte what the
//     rules-free run at the same seed produced — the rule reached no row
//     its `when` did not select.
func TestRules_SetExprThreeBandNPS(t *testing.T) {
	const rows = 4000
	ruled, res, err := synth.SynthBytes(npsBandSpec(rows, npsBandRules()), synth.Options{Seed: 909})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", res.Warnings)
	}
	plain, _, err := synth.SynthBytes(npsBandSpec(rows, nil), synth.Options{Seed: 909})
	if err != nil {
		t.Fatalf("rules-free SynthBytes: %v", err)
	}

	npsV, npsN := readFieldRows(t, ruled, "nps")
	flags := map[string][]float64{}
	flagNulls := map[string][]bool{}
	plainFlags := map[string][]float64{}
	plainFlagNulls := map[string][]bool{}
	for _, name := range []string{"promoter", "passive", "detractor"} {
		flags[name], flagNulls[name] = readFieldRows(t, ruled, name)
		plainFlags[name], plainFlagNulls[name] = readFieldRows(t, plain, name)
	}

	var carrying, missing int
	bandCount := map[string]int{}
	for i := range npsV {
		if npsN[i] {
			missing++
			for _, name := range []string{"promoter", "passive", "detractor"} {
				if flags[name][i] != plainFlags[name][i] || flagNulls[name][i] != plainFlagNulls[name][i] {
					t.Fatalf("row %d: %s moved on a row with no nps (%v/%v vs %v/%v)",
						i, name, flags[name][i], flagNulls[name][i],
						plainFlags[name][i], plainFlagNulls[name][i])
				}
			}
			continue
		}
		carrying++
		set := ""
		n := 0
		for _, name := range []string{"promoter", "passive", "detractor"} {
			if flagNulls[name][i] {
				t.Fatalf("row %d: %s is null on a row carrying nps = %v; "+
					"a set_expr states the field HAS a value and must clear the mask",
					i, name, npsV[i])
			}
			if flags[name][i] == 1 {
				n++
				set = name
			}
		}
		if n != 1 {
			t.Fatalf("row %d: nps = %v has %d flags set, want exactly 1 "+
				"(promoter %v / passive %v / detractor %v)", i, npsV[i], n,
				flags["promoter"][i], flags["passive"][i], flags["detractor"][i])
		}
		want := "detractor"
		switch {
		case npsV[i] >= 9:
			want = "promoter"
		case npsV[i] >= 7:
			want = "passive"
		}
		if set != want {
			t.Fatalf("row %d: nps = %v flagged %s, want %s — the band was classified "+
				"from a value other than the one on the wire", i, npsV[i], set, want)
		}
		bandCount[set]++
	}
	if carrying == 0 || missing == 0 {
		t.Fatalf("fixture degenerate: %d carrying / %d missing rows, want both", carrying, missing)
	}
	for _, band := range []string{"promoter", "passive", "detractor"} {
		if bandCount[band] == 0 {
			t.Fatalf("fixture degenerate: band %s never fired (%v)", band, bandCount)
		}
	}
	// The declared null rate must survive: if it did not, "the flags did
	// not move on a missing row" would be a claim about no rows.
	if rate := float64(missing) / float64(len(npsV)); rate < 0.79 || rate > 0.86 {
		t.Fatalf("nps missing rate = %.4f, want near the declared 0.8260", rate)
	}

	// ...and the NORMALISATION RULE IS LOAD-BEARING, pinned here because
	// dropping it is silent: the partition property still holds (exactly
	// one flag per row, always), only the band disagrees with the number
	// beside it. A row's `nps` is the sampler's PRE-ROUNDING float while
	// the wire carries round(f), so `f >= 9` and `round(f) >= 9` differ
	// on every draw in [8.5, 9). Measured on the motivating 122-field
	// profile at 20,000 rows: 476 of 3,486 carrying rows (13.7%) land in
	// a band that contradicts their own printed score.
	unnormalised, _, err := synth.SynthBytes(npsBandSpec(rows, npsBandRules()[1:]), synth.Options{Seed: 909})
	if err != nil {
		t.Fatalf("unnormalised SynthBytes: %v", err)
	}
	uNps, uNull := readFieldRows(t, unnormalised, "nps")
	uPromoter, _ := readFieldRows(t, unnormalised, "promoter")
	mismatch := 0
	for i := range uNps {
		if uNull[i] {
			continue
		}
		if (uNps[i] >= 9) != (uPromoter[i] == 1) {
			mismatch++
		}
	}
	if mismatch == 0 {
		t.Fatal("dropping the int(nps) normalisation produced no band/wire disagreement; " +
			"the fixture can no longer see the pre-rounding gotcha this test exists to pin")
	}
}

// TestRules_SetExprSnapshotMakesIntraRuleOrderUnobservable is THE TEST
// E1-S3 COULD NOT WRITE. It reported honestly that intra-rule assignment
// order was unfalsifiable — a Go map cannot name one field twice, so two
// assignments in one rule always targeted different fields and nothing
// could observe which ran first. `set_expr` READS the row, which makes
// the order observable, and E1-S4's answer is SNAPSHOT semantics: every
// expression in one rule sees the row as it stood before the rule ran.
//
// Three claims, all over one rule, and each falsifies a different wrong
// implementation:
//
//  1. A SWAP works. Under evaluate-and-assign-per-key in the sorted
//     order the compiled slices carry, `a` would take the old `b` and
//     then `b` would take the NEW `a` — both ending up equal to the old
//     `b`. A real swap is only possible if both read the snapshot.
//  2. A sibling `set` LITERAL is not visible to a `set_expr` in the same
//     rule. The literal still lands; the expression reads the drawn
//     value.
//  3. SELF-reference works and is not refused. `{"nps": "nps + 1"}` —
//     a field derived from its own drawn value — is the most natural
//     thing an author writes, and it is why refusing intra-rule reads
//     outright was rejected as the third option.
func TestRules_SetExprSnapshotMakesIntraRuleOrderUnobservable(t *testing.T) {
	base := func(rules []synth.RuleSpec) *synth.Spec {
		return &synth.Spec{
			RowCount: 200,
			Fields: []synth.FieldSpec{
				{Name: "a", Type: "f64", Distribution: synth.DistUniform,
					Params: map[string]any{"min": 1.0, "max": 2.0}},
				{Name: "b", Type: "f64", Distribution: synth.DistUniform,
					Params: map[string]any{"min": 10.0, "max": 20.0}},
			},
			Rules: rules,
		}
	}
	drawnA := readF64Field(t, mustSynth(t, base(nil), 5), "a")
	drawnB := readF64Field(t, mustSynth(t, base(nil), 5), "b")

	// 1 — the swap.
	swapped := mustSynth(t, base([]synth.RuleSpec{{
		SetExpr: map[string]string{"a": "b", "b": "a"},
	}}), 5)
	gotA := readF64Field(t, swapped, "a")
	gotB := readF64Field(t, swapped, "b")
	for i := range gotA {
		if gotA[i] != drawnB[i] || gotB[i] != drawnA[i] {
			t.Fatalf("row %d: a=%v b=%v, want the swap a=%v b=%v — "+
				"the two expressions did not both read the pre-rule row",
				i, gotA[i], gotB[i], drawnB[i], drawnA[i])
		}
	}

	// 2 — a sibling `set` literal is invisible to a sibling `set_expr`.
	mixed := mustSynth(t, base([]synth.RuleSpec{{
		Set:     map[string]any{"a": 99.0},
		SetExpr: map[string]string{"b": "a"},
	}}), 5)
	gotA = readF64Field(t, mixed, "a")
	gotB = readF64Field(t, mixed, "b")
	for i := range gotA {
		if gotA[i] != 99 {
			t.Fatalf("row %d: a = %v, want the literal 99", i, gotA[i])
		}
		if gotB[i] != drawnA[i] {
			t.Fatalf("row %d: b = %v, want the DRAWN a %v — a set_expr must not "+
				"see its own rule's set literal", i, gotB[i], drawnA[i])
		}
	}

	// 3 — self-reference.
	incremented := mustSynth(t, base([]synth.RuleSpec{{
		SetExpr: map[string]string{"a": "a + 1"},
	}}), 5)
	gotA = readF64Field(t, incremented, "a")
	for i := range gotA {
		if gotA[i] != drawnA[i]+1 {
			t.Fatalf("row %d: a = %v, want the drawn %v plus 1", i, gotA[i], drawnA[i])
		}
	}
}

// TestRules_SetExprReadsEarlierRuleButNotLater is the CROSS-rule half,
// which is the ordering an author can actually read off the document and
// therefore the one the rule layer exposes. Worked example:
//
//	[ {"set":      {"a": 5}},      // rule 0 writes a = 5
//	  {"set_expr": {"b": "a"}},    // rule 1 sees 5
//	  {"set":      {"a": 9}} ]     // rule 2 overwrites a; b stays 5
//
// b == 5 and a == 9. Correct and surprising, which is why it is pinned.
func TestRules_SetExprReadsEarlierRuleButNotLater(t *testing.T) {
	spec := &synth.Spec{
		RowCount: 50,
		Fields: []synth.FieldSpec{
			{Name: "a", Type: "u8", Distribution: synth.DistUniform,
				Params: map[string]any{"min": 100.0, "max": 200.0}},
			{Name: "b", Type: "u8", Distribution: synth.DistUniform,
				Params: map[string]any{"min": 100.0, "max": 200.0}},
		},
		Rules: []synth.RuleSpec{
			{Set: map[string]any{"a": 5.0}},
			{SetExpr: map[string]string{"b": "a"}},
			{Set: map[string]any{"a": 9.0}},
		},
	}
	data := mustSynth(t, spec, 3)
	for i, v := range readF64Field(t, data, "b") {
		if v != 5 {
			t.Fatalf("row %d: b = %v, want 5 — a set_expr must see what an EARLIER rule wrote", i, v)
		}
	}
	for i, v := range readF64Field(t, data, "a") {
		if v != 9 {
			t.Fatalf("row %d: a = %v, want 9 (last write wins)", i, v)
		}
	}
}

// TestRules_SetExprCoercesOnTheWireForEveryTargetClass asserts the
// coercion matrix's admitted cells against the DECODED FILE rather than
// against the row map, because the row map is not the contract: a Go bool
// left in the row would be rescued by writeFieldValueForField's toBool
// on a packed_bool and turned into a silent 0 on a u8, and only the file
// can tell the two apart.
//
// One rule, one row shape, every class: bool -> packed_bool, bool -> u8
// (the cell the story calls out by name), number -> u4, int -> u16,
// string -> categorical_u8, []string -> set_u8 (split(), the shape only
// an expression can produce).
func TestRules_SetExprCoercesOnTheWireForEveryTargetClass(t *testing.T) {
	spec := &synth.Spec{
		RowCount: 40,
		Fields: []synth.FieldSpec{
			// Every source is a `constant` so the assertions below are
			// exact numbers rather than tolerances: the claim is about the
			// coercion, not about a draw.
			{Name: "src", Type: "u8", Distribution: synth.DistConstant,
				Params: map[string]any{"value": 3.0}},
			{Name: "flag", Type: "packed_bool", Distribution: synth.DistConstant,
				Params: map[string]any{"value": false}},
			{Name: "flagnum", Type: "u8", Distribution: synth.DistConstant,
				Params: map[string]any{"value": 7.0}},
			{Name: "small", Type: "u4", Distribution: synth.DistConstant,
				Params: map[string]any{"value": 0.0}},
			{Name: "counted", Type: "u16", Distribution: synth.DistConstant,
				Params: map[string]any{"value": 0.0}},
			{Name: "region", Type: "categorical_u8", Distribution: synth.DistWeightedCategorical,
				Params: map[string]any{"values": []any{"east", "west"}, "weights": []any{1.0, 1.0}}},
			{Name: "channels", Type: "set_u8", Distribution: synth.DistSetBernoulli,
				Params: map[string]any{"options": []any{"tv", "web", "radio"},
					"frequencies": []any{0.0, 0.0, 0.0}}},
		},
		Rules: []synth.RuleSpec{{SetExpr: map[string]string{
			"flag":     "src == 3",
			"flagnum":  "src == 3",
			"small":    "src * 2",
			"counted":  `len("abcd")`,
			"region":   `src == 3 ? "west" : "east"`,
			"channels": `split("tv,radio", ",")`,
		}}},
	}
	data := mustSynth(t, spec, 11)
	for name, want := range map[string]float64{"flag": 1, "flagnum": 1, "small": 6, "counted": 4} {
		for i, got := range readF64Field(t, data, name) {
			if got != want {
				t.Fatalf("row %d: %s = %v, want %v", i, name, got, want)
			}
		}
	}
	for i, got := range readCategoricalField(t, data, "region") {
		if got != "west" {
			t.Fatalf("row %d: region = %q, want west", i, got)
		}
	}
	_, labels, _ := readSetFieldRows(t, data, "channels")
	for i, sel := range labels {
		if len(sel) != 2 || !hasLabel(sel, "tv") || !hasLabel(sel, "radio") {
			t.Fatalf("row %d: channels = %v, want exactly [tv radio] — split() returns "+
				"[]string and the row wants map[string]bool", i, sel)
		}
	}
}

// TestRules_SetExprStringOutsideTheDeclaredDomainIsRefused is the
// acceptance criterion for a computed category. The refusal has to name
// BOTH the field and the value, because a computed value does not appear
// anywhere in the document an author is reading.
//
// It is a RUN-time refusal by necessity: the value exists for the first
// time on the row that produced it. The alternative — growing the
// dictionary — adds an entry that is indistinguishable from data.
func TestRules_SetExprStringOutsideTheDeclaredDomainIsRefused(t *testing.T) {
	spec := &synth.Spec{
		RowCount: 20,
		Fields: []synth.FieldSpec{
			{Name: "n", Type: "u8", Distribution: synth.DistConstant,
				Params: map[string]any{"value": 1.0}},
			{Name: "region", Type: "categorical_u8", Distribution: synth.DistWeightedCategorical,
				Params: map[string]any{"values": []any{"east", "west"}, "weights": []any{1.0, 1.0}}},
		},
		Rules: []synth.RuleSpec{{SetExpr: map[string]string{"region": `n == 1 ? "north" : "east"`}}},
	}
	_, _, err := synth.SynthBytes(spec, synth.Options{Seed: 2})
	if err == nil {
		t.Fatal("want a refusal for a computed category outside the declared domain")
	}
	var coded *errors.CodedError
	if !stderrors.As(err, &coded) {
		t.Fatalf("want *CodedError, got %T: %v", err, err)
	}
	if coded.Code != errors.PULSE_SYNTH_RULE_VALUE_INVALID {
		t.Fatalf("code = %s, want PULSE_SYNTH_RULE_VALUE_INVALID", coded.Code)
	}
	if !bytes.Contains([]byte(coded.Message), []byte("north")) ||
		!bytes.Contains([]byte(coded.Message), []byte("region")) {
		t.Fatalf("the refusal must name the field and the value: %q", coded.Message)
	}
	if coded.Details[errors.DetailSynthRuleSlot] != "set_expr" {
		t.Fatalf("details slot = %v, want set_expr", coded.Details[errors.DetailSynthRuleSlot])
	}
	if coded.Details["field"] != "region" {
		t.Fatalf("details field = %v, want region", coded.Details["field"])
	}
	if coded.Details["value"] != "north" {
		t.Fatalf("details value = %v, want north", coded.Details["value"])
	}
}

// TestRules_SetExprTimingSplit is the story's two-timing rule asserted as
// a table. A fault the RETURN TYPE settles is refused at SPEC PARSE — no
// row is generated — while a fault only a VALUE settles is refused at row
// time, because that is the first moment it exists.
//
// The split matters to an author in exactly one way: the parse-time
// refusals are the ones a `pulse synth` invocation reports before doing
// any work, and the run-time ones are the ones that can appear after
// 399,999 good rows. Both carry the same code, because both are "the
// value this rule wants to write cannot be written".
func TestRules_SetExprTimingSplit(t *testing.T) {
	build := func(expr string, target string) *synth.Spec {
		return &synth.Spec{
			RowCount: 50,
			Fields: []synth.FieldSpec{
				{Name: "n", Type: "u8", Distribution: synth.DistConstant,
					Params: map[string]any{"value": 10.0}},
				{Name: "small", Type: "u4", Distribution: synth.DistConstant,
					Params: map[string]any{"value": 0.0}},
				{Name: "score", Type: "f64", Distribution: synth.DistNormal,
					Params: map[string]any{"mean": 0.0, "std": 1.0}},
				{Name: "region", Type: "categorical_u8", Distribution: synth.DistWeightedCategorical,
					Params: map[string]any{"values": []any{"east", "west"}, "weights": []any{1.0, 1.0}}},
				{Name: "channels", Type: "set_u8", Distribution: synth.DistSetBernoulli,
					Params: map[string]any{"options": []any{"tv", "web"}, "frequencies": []any{0.5, 0.5}}},
				{Name: "cents", Type: "decimal128", Scale: 2, Distribution: synth.DistNormal,
					Params: map[string]any{"mean": 10.0, "std": 1.0}},
			},
			Rules: []synth.RuleSpec{{SetExpr: map[string]string{target: expr}}},
		}
	}
	cases := []struct {
		name     string
		expr     string
		target   string
		atParse  bool // refused by ParseSpec / validateSpec
		accepted bool
	}{
		{"string result on a numeric target", `"1.5"`, "score", true, false},
		{"number result on a categorical target", "n * 2", "region", true, false},
		{"bool result on a categorical target", "n == 10", "region", true, false},
		{"number result on a set target", "n", "channels", true, false},
		{"string result on a set target", `"tv"`, "channels", true, false},
		{"set selection on a numeric target", "channels", "score", true, false},
		{"categorical read into a numeric target", "region", "score", true, false},
		// The decision, not an oversight: exactness lives in `set`.
		{"string result on a decimal128 target", `"1.25"`, "cents", true, false},
		{"number result on a decimal128 target", "n / 2", "cents", false, true},

		// Value faults: parse cannot see them.
		{"number over the target's range", "n + 10", "small", false, false},
		{"computed category outside the domain", `"north"`, "region", false, false},
		{"undeclared set option", `split("tv,initech", ",")`, "channels", false, false},

		// Admitted.
		{"bool onto a numeric", "n == 10", "score", false, true},
		{"number onto a numeric", "n * 2", "score", false, true},
		{"declared category", `"west"`, "region", false, true},
		{"declared options", `split("tv,web", ",")`, "channels", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := build(tc.expr, tc.target)
			_, _, err := synth.SynthBytes(spec, synth.Options{Seed: 4})
			if tc.accepted {
				if err != nil {
					t.Fatalf("want accepted, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("want a refusal for %s -> %s", tc.expr, tc.target)
			}
			var coded *errors.CodedError
			if !stderrors.As(err, &coded) {
				t.Fatalf("want *CodedError, got %T: %v", err, err)
			}
			if coded.Code != errors.PULSE_SYNTH_RULE_VALUE_INVALID {
				t.Fatalf("code = %s, want PULSE_SYNTH_RULE_VALUE_INVALID (%s)",
					coded.Code, coded.Message)
			}
			// The timing is the claim: a parse-time fault is refused by
			// validateSpec alone, reachable WITHOUT generating a row.
			parseErr := validateOnly(t, spec)
			if tc.atParse && parseErr == nil {
				t.Fatalf("%s -> %s is knowable from the return type and must be refused at "+
					"spec parse, not at row time", tc.expr, tc.target)
			}
			if !tc.atParse && parseErr != nil {
				t.Fatalf("%s -> %s was refused at spec parse (%v) but only a VALUE can settle it; "+
					"a parse-time refusal here rejects a rule that is correct on every row it fires on",
					tc.expr, tc.target, parseErr)
			}
		})
	}
}

// TestRules_SetExprConsumesNoRNG asserts the set_expr arm is pure
// assignment like the rest of the pass: a spec carrying set_expr draws
// the SAME per-row sequence as the same spec without it, asserted on the
// field no rule names.
func TestRules_SetExprConsumesNoRNG(t *testing.T) {
	ruled := mustSynth(t, npsBandSpec(300, npsBandRules()), 55)
	plain := mustSynth(t, npsBandSpec(300, nil), 55)
	a := readF64Field(t, ruled, "id")
	b := readF64Field(t, plain, "id")
	if len(a) != len(b) {
		t.Fatalf("row counts differ (%d vs %d)", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("id row %d: %v with rules vs %v without — the set_expr arm consumed RNG",
				i, a[i], b[i])
		}
	}
	if bytes.Equal(ruled, plain) {
		t.Fatal("the two files are byte-identical; the set_expr rules applied to no row")
	}
	// Determinism with set_expr present.
	again := mustSynth(t, npsBandSpec(300, npsBandRules()), 55)
	if !bytes.Equal(ruled, again) {
		t.Fatalf("same spec + same seed produced different bytes (%d differing)",
			byteDiffCount(ruled, again))
	}
}

// mustSynth is SynthBytes with the error handling folded away.
func mustSynth(t *testing.T, spec *synth.Spec, seed int64) []byte {
	t.Helper()
	data, _, err := synth.SynthBytes(spec, synth.Options{Seed: seed})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	return data
}

// validateOnly reaches validateSpec without generating a row, by
// marshalling the spec and handing it to ParseSpec — the public entry
// point whose whole job is parse-plus-validate. A nil return means the
// spec survived validation and any fault it carries can only surface at
// row time.
func validateOnly(t *testing.T, spec *synth.Spec) error {
	t.Helper()
	blob, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal spec: %v", err)
	}
	_, perr := synth.ParseSpec(blob)
	return perr
}
