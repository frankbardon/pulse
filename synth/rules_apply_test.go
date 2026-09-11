package synth_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"math"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/synth"
	"github.com/spf13/afero"
)

// ruleValueTolerance mirrors modelValueTolerance: a model over a
// `normal` target reduces ALGEBRAICALLY to its own prediction, but the
// value really does make the copula round trip (standardise, phi,
// quantile), and floating point does not promise
// mean + std*((p-mean)/std) is bit-equal to p.
const ruleValueTolerance = 1e-9

// ruleModelSpec is the fixture for the placement tests: two numerics
// each carrying a DETERMINISTIC linear model (ResidualStd 0) keyed to
// the same categorical, so "the value generation inferred for this row"
// is an exact number a test can name rather than a distribution it has
// to sample.
//
//	region == "east" -> spend 100, score 10
//	region == "west" -> spend 140, score 15
//
// `spend` is declared nullable with null_rate 0, so the sampler produces
// no nulls of its own and EVERY null in the generated file came from a
// rule.
func ruleModelSpec(rows int, rules []synth.RuleSpec) *synth.Spec {
	return &synth.Spec{
		RowCount: rows,
		Fields: []synth.FieldSpec{
			{Name: "id", Type: "u32", Distribution: synth.DistMonotonicFrom,
				Params: map[string]any{"start": 1.0}},
			{Name: "aware", Type: "packed_bool", Distribution: synth.DistBernoulli,
				Params: map[string]any{"p": 0.5}},
			{Name: "region", Type: "categorical_u8", Distribution: synth.DistWeightedCategorical,
				Params: map[string]any{"values": []any{"east", "west"}, "weights": []any{1.0, 1.0}}},
			{Name: "spend", Type: "f64", Nullable: true,
				Distribution: synth.DistNormal, Params: map[string]any{"mean": 100.0, "std": 25.0}},
			{Name: "score", Type: "f64",
				Distribution: synth.DistNormal, Params: map[string]any{"mean": 10.0, "std": 2.0}},
		},
		Models: []synth.FieldModelSpec{
			{Field: "spend", Intercept: 100, ResidualStd: 0, Predictors: []synth.ModelPredictorSpec{
				{Kind: "categorical_level", Field: "region", Level: "west", Coefficient: 40},
			}},
			{Field: "score", Intercept: 10, ResidualStd: 0, Predictors: []synth.ModelPredictorSpec{
				{Kind: "categorical_level", Field: "region", Level: "west", Coefficient: 5},
			}},
		},
		Rules: rules,
	}
}

// readFieldRows decodes one field's raw per-record value together with
// its null-bitmap state, in file order. The existing readField helper
// returns the value only, and the difference is the whole point of the
// no-leak criterion: a masked field must carry 0 ON THE WIRE, which is a
// property of the file rather than of the row map the pass wrote.
func readFieldRows(t *testing.T, data []byte, name string) (values []float64, nulls []bool) {
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
	vals := make(map[string]float64)
	nl := make(map[string]bool)
	for {
		if err := rr.ReadRecord(vals, nl); err != nil {
			break
		}
		values = append(values, vals[name])
		nulls = append(nulls, nl[name])
	}
	return values, nulls
}

// TestRules_GateMasksAndReplacesWhileTheInferredValueSurvives is the
// story's central assertion and the one the placement falsification
// targets. Four claims, all over the SAME generated file, because each
// is trivially satisfiable by abandoning the others — a pass that nulls
// everything satisfies the mask half, a pass that does nothing satisfies
// the survival half:
//
//  1. On a GATED row, set_null masks the modelled field.
//  2. On a GATED row, the masked field's ON-WIRE value is 0 — read from
//     the decoded bytes, not from the row map.
//  3. On a GATED row, set replaces the model's inference with the
//     literal.
//  4. On a NON-GATED row, BOTH fields still carry the value their model
//     produced. This is the `if gate then null else inferred` contract,
//     and it is what fails if the rule pass is moved ahead of the model
//     stage in drawRow.
func TestRules_GateMasksAndReplacesWhileTheInferredValueSurvives(t *testing.T) {
	spec := ruleModelSpec(400, []synth.RuleSpec{{
		When:    "aware == 0",
		SetNull: []string{"spend"},
		Set:     map[string]any{"score": 7.0},
	}})
	data, res, err := synth.SynthBytes(spec, synth.Options{Seed: 7})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", res.Warnings)
	}

	aware := readF64Field(t, data, "aware")
	region := readCategoricalField(t, data, "region")
	spend, spendNull := readFieldRows(t, data, "spend")
	score, scoreNull := readFieldRows(t, data, "score")

	inferredSpend := map[string]float64{"east": 100, "west": 140}
	inferredScore := map[string]float64{"east": 10, "west": 15}

	var gated, open int
	for i := range aware {
		if aware[i] == 0 {
			gated++
			if !spendNull[i] {
				t.Fatalf("row %d: gated row must have spend null", i)
			}
			if spend[i] != 0 {
				t.Fatalf("row %d: masked spend leaked %v onto the wire, want 0", i, spend[i])
			}
			if scoreNull[i] {
				t.Fatalf("row %d: a set literal must clear the null mask", i)
			}
			if math.Abs(score[i]-7) > ruleValueTolerance {
				t.Fatalf("row %d: gated score = %v, want the rule literal 7", i, score[i])
			}
			continue
		}
		open++
		if spendNull[i] {
			t.Fatalf("row %d: non-gated row must not be masked", i)
		}
		if want := inferredSpend[region[i]]; math.Abs(spend[i]-want) > ruleValueTolerance {
			t.Fatalf("row %d (region %s): spend = %v, want the model-inferred %v",
				i, region[i], spend[i], want)
		}
		if want := inferredScore[region[i]]; math.Abs(score[i]-want) > ruleValueTolerance {
			t.Fatalf("row %d (region %s): score = %v, want the model-inferred %v — "+
				"a `set` must write the literal on GATED rows ONLY", i, region[i], score[i], want)
		}
	}
	if gated == 0 || open == 0 {
		t.Fatalf("fixture degenerate: %d gated / %d non-gated rows, want both", gated, open)
	}
}

// TestRules_NoWhenAppliesToEveryRow pins the slot's most surprising
// default: an ABSENT `when` means EVERY ROW, not "never". A rule
// computing a derived field normally carries no predicate at all.
func TestRules_NoWhenAppliesToEveryRow(t *testing.T) {
	spec := ruleModelSpec(120, []synth.RuleSpec{{
		Set:     map[string]any{"score": 3.0},
		SetNull: []string{"spend"},
	}})
	data, _, err := synth.SynthBytes(spec, synth.Options{Seed: 3})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	score, _ := readFieldRows(t, data, "score")
	spend, spendNull := readFieldRows(t, data, "spend")
	if len(score) != 120 {
		t.Fatalf("row count = %d, want 120", len(score))
	}
	for i := range score {
		if math.Abs(score[i]-3) > ruleValueTolerance {
			t.Fatalf("row %d: score = %v, want 3 on every row", i, score[i])
		}
		if !spendNull[i] || spend[i] != 0 {
			t.Fatalf("row %d: spend = %v null=%v, want a masked 0 on every row",
				i, spend[i], spendNull[i])
		}
	}
}

// TestRules_DeclarationOrderLastWriteWins asserts the ordering contract
// RuleSpec's doc promises, over the three orderings that can disagree.
// Declaration order rather than any sort is what an author can read off
// the document, so it has to be the applied order.
func TestRules_DeclarationOrderLastWriteWins(t *testing.T) {
	cases := []struct {
		name     string
		rules    []synth.RuleSpec
		wantVal  float64
		wantNull bool
	}{
		{
			name: "two set literals, the later one wins",
			rules: []synth.RuleSpec{
				{Set: map[string]any{"score": 1.0}},
				{Set: map[string]any{"score": 2.0}},
			},
			wantVal: 2,
		},
		{
			name: "set then set_null, the null wins",
			rules: []synth.RuleSpec{
				{Set: map[string]any{"spend": 55.0}},
				{SetNull: []string{"spend"}},
			},
			wantVal: 0, wantNull: true,
		},
		{
			name: "set_null then set, the value wins and clears the mask",
			rules: []synth.RuleSpec{
				{SetNull: []string{"spend"}},
				{Set: map[string]any{"spend": 55.0}},
			},
			wantVal: 55,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			field := "score"
			if len(tc.rules[0].SetNull) > 0 || tc.rules[0].Set["spend"] != nil {
				field = "spend"
			}
			spec := ruleModelSpec(40, tc.rules)
			data, _, err := synth.SynthBytes(spec, synth.Options{Seed: 5})
			if err != nil {
				t.Fatalf("SynthBytes: %v", err)
			}
			values, nulls := readFieldRows(t, data, field)
			for i := range values {
				if nulls[i] != tc.wantNull {
					t.Fatalf("row %d: %s null = %v, want %v", i, field, nulls[i], tc.wantNull)
				}
				if math.Abs(values[i]-tc.wantVal) > ruleValueTolerance {
					t.Fatalf("row %d: %s = %v, want %v", i, field, values[i], tc.wantVal)
				}
			}
		})
	}
}

// TestRules_ConsumeNoRNG asserts the pass is pure assignment: a spec
// carrying rules draws the SAME per-row random sequence as the same spec
// with the rules removed. Asserted on every field NO rule names — if the
// pass consumed a draw, every downstream field would shift.
//
// The fixture's rules deliberately touch the LAST two fields in schema
// order and a model target, so a stolen draw would be visible in the
// model stage's own normals as well as in the samplers.
//
// Both rules are deliberately NON-CLAIMING (E2-S2): a set_null never
// claims, and the `set` carries a `when`. That restriction is what
// keeps this test measuring the PASS rather than the arbitration — an
// unconditional `set` over a model target pre-claims the field, drops
// its model, and so removes that drawer's own unconditional
// NormFloat64 from every row. The stream legitimately shifts there, and
// TestRuleClaim_ClaimingRuleShiftsTheStreamNonClaimingDoesNot pins both
// halves of that distinction.
func TestRules_ConsumeNoRNG(t *testing.T) {
	rules := []synth.RuleSpec{
		{When: "aware == 0", SetNull: []string{"spend"}},
		{When: "aware == 1", Set: map[string]any{"score": 7.0}},
	}
	withRules := ruleModelSpec(300, rules)
	withoutRules := ruleModelSpec(300, nil)

	ruled, _, err := synth.SynthBytes(withRules, synth.Options{Seed: 21})
	if err != nil {
		t.Fatalf("SynthBytes with rules: %v", err)
	}
	plain, _, err := synth.SynthBytes(withoutRules, synth.Options{Seed: 21})
	if err != nil {
		t.Fatalf("SynthBytes without rules: %v", err)
	}

	for _, name := range []string{"id", "aware"} {
		a := readF64Field(t, ruled, name)
		b := readF64Field(t, plain, name)
		if len(a) != len(b) {
			t.Fatalf("%s: row counts differ (%d vs %d)", name, len(a), len(b))
		}
		for i := range a {
			if a[i] != b[i] {
				t.Fatalf("%s row %d: %v with rules vs %v without — the rule pass consumed RNG",
					name, i, a[i], b[i])
			}
		}
	}
	ra := readCategoricalField(t, ruled, "region")
	rb := readCategoricalField(t, plain, "region")
	for i := range ra {
		if ra[i] != rb[i] {
			t.Fatalf("region row %d: %q with rules vs %q without — the rule pass consumed RNG",
				i, ra[i], rb[i])
		}
	}
	// ...and the rules really did fire, so the comparison above is not
	// vacuous.
	if bytes.Equal(ruled, plain) {
		t.Fatal("the two files are byte-identical; the rules applied to no row")
	}
}

// preRuleApplySpecHash pins the bytes a RULES-FREE spec generates, as
// produced by the tree BEFORE the rule pass existed (E1-S2, 4bb587a).
// This is the load-bearing regression guard of the story: the pass is
// additive, so every spec in the world that declares no rule must be
// generated exactly as it was.
//
// A byte pin is normally avoided in this package because math.Exp and
// math.Log are architecture-specific in the standard library (see
// preModelsSpecHash). THIS spec is safe to pin and that is a property of
// the spec, not luck: every sampler it uses is comparison or
// barriered-arithmetic only (monotonic_from, bernoulli, uniform —
// `u.min + float64(rng.Float64()*(u.max-u.min))`, weighted_categorical,
// set_bernoulli), it declares no model and no correlation, and it never
// reaches NormFloat64, Exp or Log. Anything added to it must keep that
// true or the gate becomes flaky rather than protective.
const preRuleApplySpecHash = "bb456a2e9fb29666d817aee9bb275f9efc2ba342abb5db4cb4b0a37a71435c55"

// ruleFreeSpec is the pinned spec. See preRuleApplySpecHash.
func ruleFreeSpec() *synth.Spec {
	return &synth.Spec{
		RowCount: 64,
		Fields: []synth.FieldSpec{
			{Name: "id", Type: "u32", Distribution: synth.DistMonotonicFrom,
				Params: map[string]any{"start": 1.0}},
			{Name: "aware", Type: "packed_bool", Distribution: synth.DistBernoulli,
				Params: map[string]any{"p": 0.5}},
			{Name: "nps", Type: "u4", Distribution: synth.DistUniform,
				Params: map[string]any{"min": 0.0, "max": 11.0}},
			{Name: "region", Type: "categorical_u8", Distribution: synth.DistWeightedCategorical,
				Params: map[string]any{"values": []any{"east", "west"}, "weights": []any{0.6, 0.4}}},
			{Name: "perception", Type: "u4", Nullable: true, NullRate: 0.25,
				Distribution: synth.DistUniform, Params: map[string]any{"min": 1.0, "max": 6.0}},
			{Name: "channels", Type: "set_u8", Distribution: synth.DistSetBernoulli,
				Params: map[string]any{"options": []any{"tv", "web", "radio"},
					"frequencies": []any{0.5, 0.5, 0.5}}},
		},
	}
}

// TestRules_RulesFreeSpecIsByteIdenticalToPreStory is the byte-identity
// gate. It replaces the inert half of E1-S2's
// TestRules_ValidateAndDoNothing: that test asserted a DECLARED rule
// changed no bytes, which this story makes false by design, so the
// property worth keeping — additivity — is re-stated against a spec that
// declares no rule at all, and pinned to the pre-story bytes rather than
// to a sibling run (which would pass even if both sides regressed).
func TestRules_RulesFreeSpecIsByteIdenticalToPreStory(t *testing.T) {
	data, _, err := synth.SynthBytes(ruleFreeSpec(), synth.Options{Seed: 4242})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != preRuleApplySpecHash {
		t.Fatalf("a rules-free spec no longer generates the pre-story bytes\n got %s (%d bytes)\nwant %s",
			got, len(data), preRuleApplySpecHash)
	}
}

// TestRules_DeterministicAcrossRuns asserts the determinism contract
// still holds WITH rules present: same spec + same seed, byte-identical
// output. The pass consumes no RNG and walks fixed slices rather than
// the `set` map, so a Go map seed cannot reach the file.
func TestRules_DeterministicAcrossRuns(t *testing.T) {
	build := func() *synth.Spec {
		return ruleModelSpec(200, []synth.RuleSpec{{
			When:    "aware == 0",
			SetNull: []string{"spend"},
			Set:     map[string]any{"score": 7.0, "region": "west"},
		}})
	}
	first, _, err := synth.SynthBytes(build(), synth.Options{Seed: 31})
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, _, err := synth.SynthBytes(build(), synth.Options{Seed: 31})
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("same spec + same seed produced different bytes (%d differing)",
			byteDiffCount(first, second))
	}
}

// TestRules_SetLiteralTakesTheRowShapeForEveryValueClass covers the
// three row-value classes a `set` literal has to be coerced into, and in
// particular the one carried over from E1-S2: a set_* literal arrives
// from JSON as an ARRAY of declared option names and the row holds a
// map[string]bool. The coercion is constantRowValue — the `constant`
// distribution's own — reused rather than rewritten.
//
// Asserted against the decoded FILE: a categorical resolves through the
// dictionary, a set_* through its bitmask.
func TestRules_SetLiteralTakesTheRowShapeForEveryValueClass(t *testing.T) {
	spec := &synth.Spec{
		RowCount: 50,
		Fields: []synth.FieldSpec{
			{Name: "aware", Type: "packed_bool", Distribution: synth.DistBernoulli,
				Params: map[string]any{"p": 0.5}},
			{Name: "flag", Type: "packed_bool", Distribution: synth.DistBernoulli,
				Params: map[string]any{"p": 0.5}},
			{Name: "region", Type: "categorical_u8", Distribution: synth.DistWeightedCategorical,
				Params: map[string]any{"values": []any{"east", "west"}, "weights": []any{1.0, 1.0}}},
			{Name: "channels", Type: "set_u8", Distribution: synth.DistSetBernoulli,
				Params: map[string]any{"options": []any{"tv", "web", "radio"},
					"frequencies": []any{0.5, 0.5, 0.5}}},
		},
		Rules: []synth.RuleSpec{{
			Set: map[string]any{
				"region":   "west",
				"channels": []any{"tv", "radio"},
				"flag":     true,
			},
		}},
	}
	data, _, err := synth.SynthBytes(spec, synth.Options{Seed: 13})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	for i, got := range readCategoricalField(t, data, "region") {
		if got != "west" {
			t.Fatalf("row %d: region = %q, want west", i, got)
		}
	}
	for i, got := range readF64Field(t, data, "flag") {
		if got != 1 {
			t.Fatalf("row %d: flag = %v, want 1 (a bool literal means 1/0)", i, got)
		}
	}
	_, labels, _ := readSetFieldRows(t, data, "channels")
	for i, sel := range labels {
		if len(sel) != 2 || !hasLabel(sel, "tv") || !hasLabel(sel, "radio") {
			t.Fatalf("row %d: channels = %v, want exactly [tv radio] — "+
				"a set_* literal must be coerced from []any to the row's map[string]bool", i, sel)
		}
	}
}

// TestRules_SetNullOnNonNullableFieldIsRefused pins the one outcome the
// pass cannot make honest, and pins that it is now REFUSED rather than
// warned. encodeRow writes a null bit only for a NULLABLE field, so a
// set_null over a field declared without `"nullable": true` writes the
// type's zero as an ordinary value — indistinguishable from a real 0 on
// a u4 or a packed_bool. The rule fires and the file cannot show it,
// which is the silent class the layer exists to remove, and it is
// knowable from the field declaration before a row exists.
//
// It was a warning through E1. A warning was not enough: the terminal
// summary caps each kind at three examples, so on a survey-shaped spec
// the entire signal for a missing gate was three lines among thousands.
//
// The second half is the remedy, and it belongs in the same test: the
// fix is on the FIELD, and declaring it nullable both validates and
// produces a real null bit — so the refusal is a redirection rather than
// a dead end.
func TestRules_SetNullOnNonNullableFieldIsRefused(t *testing.T) {
	spec := ruleModelSpec(20, []synth.RuleSpec{{SetNull: []string{"score"}}})
	if _, _, err := synth.SynthBytes(spec, synth.Options{Seed: 17}); err == nil {
		t.Fatal("want a refusal for a set_null over a non-nullable field")
	} else if !bytes.Contains([]byte(err.Error()), []byte("PULSE_SYNTH_RULE_FIELD_NOT_NULLABLE")) {
		t.Fatalf("want PULSE_SYNTH_RULE_FIELD_NOT_NULLABLE, got %v", err)
	}

	spec = ruleModelSpec(20, []synth.RuleSpec{{SetNull: []string{"score"}}})
	for i := range spec.Fields {
		if spec.Fields[i].Name == "score" {
			spec.Fields[i].Nullable = true
		}
	}
	data, res, err := synth.SynthBytes(spec, synth.Options{Seed: 17})
	if err != nil {
		t.Fatalf("declaring the field nullable must be the whole remedy: %v", err)
	}
	for _, w := range res.Warnings {
		if bytes.Contains([]byte(w), []byte("non-nullable")) {
			t.Fatalf("nothing is non-nullable any more: %q", w)
		}
	}
	values, nulls := readFieldRows(t, data, "score")
	for i := range values {
		if !nulls[i] {
			t.Fatalf("row %d: want a real null bit now that the field is nullable", i)
		}
		if values[i] != 0 {
			t.Fatalf("row %d: score = %v, want 0 on the wire for a masked field", i, values[i])
		}
	}
}

// TestRules_AppliedOnlyToTheGeneratedPartition asserts rules never touch
// the copied source rows of a `synth from-profile --source` run.
// AugmentFromProfile calls generate() for its GENERATED partition only
// and re-encodes the source records straight through, so the property is
// structural — this pins it through the real facade path so a future
// refactor cannot quietly route the source rows past drawRow.
func TestRules_AppliedOnlyToTheGeneratedPartition(t *testing.T) {
	base := &synth.Spec{
		RowCount: 60,
		Fields: []synth.FieldSpec{
			{Name: "id", Type: "u32", Distribution: synth.DistMonotonicFrom,
				Params: map[string]any{"start": 1.0}},
			// Constant 5 until a rule says otherwise, so the two
			// partitions are told apart by value alone.
			{Name: "mark", Type: "u8", Distribution: synth.DistConstant,
				Params: map[string]any{"value": 5.0}},
		},
	}
	srcData, _, err := synth.SynthBytes(base, synth.Options{Seed: 2})
	if err != nil {
		t.Fatalf("source SynthBytes: %v", err)
	}

	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "/source.pulse", srcData, 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}

	ruled := &synth.Spec{RowCount: 40, Fields: base.Fields, Rules: []synth.RuleSpec{
		{Set: map[string]any{"mark": 9.0}},
	}}
	if _, err := p.Synth(context.Background(), ruled, "/out.pulse", pulse.SynthOptions{
		Seed: 2, SourceCohort: "/source.pulse",
	}); err != nil {
		t.Fatalf("augment synth: %v", err)
	}
	out, err := afero.ReadFile(fs, "/out.pulse")
	if err != nil {
		t.Fatalf("read out: %v", err)
	}

	synthetic := readF64Field(t, out, synth.SyntheticFieldName)
	mark := readF64Field(t, out, "mark")
	if len(mark) != 100 {
		t.Fatalf("row count = %d, want 60 source + 40 generated", len(mark))
	}
	var src, gen int
	for i := range mark {
		if synthetic[i] == 0 {
			src++
			if mark[i] != 5 {
				t.Fatalf("source row %d: mark = %v, want the untouched 5 — "+
					"a rule reached the copied _synthetic=false partition", i, mark[i])
			}
			continue
		}
		gen++
		if mark[i] != 9 {
			t.Fatalf("generated row %d: mark = %v, want the rule literal 9", i, mark[i])
		}
	}
	if src != 60 || gen != 40 {
		t.Fatalf("partition sizes = %d source / %d generated, want 60 / 40", src, gen)
	}
}

// TestRules_WhenReadsNullStateViaIsnull exercises the one part of the
// `when` environment that cannot come from the row: null state. A nulled
// field still carries its drawn value in the row map (nullableSampler
// returns the value alongside isNull=true), so `perception == 0` would
// answer about the VALUE; only isnull() answers about the mask, and the
// mask has to be rebound to the current row before the first evaluation
// or the gate describes the PREVIOUS row.
//
// Skip logic is the motivating use of the whole layer and it is stated
// in exactly this shape ("the perception block is not asked of ..."), so
// a gate over a null is not an edge case.
func TestRules_WhenReadsNullStateViaIsnull(t *testing.T) {
	spec := &synth.Spec{
		RowCount: 300,
		Fields: []synth.FieldSpec{
			{Name: "perception", Type: "u4", Nullable: true, NullRate: 0.5,
				Distribution: synth.DistUniform, Params: map[string]any{"min": 1.0, "max": 6.0}},
			{Name: "skipped", Type: "packed_bool", Distribution: synth.DistBernoulli,
				Params: map[string]any{"p": 0.0}},
		},
		Rules: []synth.RuleSpec{{
			When: `isnull("perception")`,
			Set:  map[string]any{"skipped": true},
		}},
	}
	data, _, err := synth.SynthBytes(spec, synth.Options{Seed: 23})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	_, perceptionNull := readFieldRows(t, data, "perception")
	skipped := readF64Field(t, data, "skipped")
	var nulls, flagged int
	for i := range skipped {
		if perceptionNull[i] {
			nulls++
			if skipped[i] != 1 {
				t.Fatalf("row %d: perception is null but skipped = %v", i, skipped[i])
			}
			continue
		}
		if skipped[i] != 0 {
			t.Fatalf("row %d: perception is present but skipped = %v — "+
				"the gate read a stale null mask", i, skipped[i])
		}
		flagged++
	}
	if nulls == 0 || flagged == 0 {
		t.Fatalf("fixture degenerate: %d null / %d present rows, want both", nulls, flagged)
	}
}

// TestRules_GateComposesWithTheFieldsOwnNullRate is the committed,
// shape-equivalent form of the real-profile calibration: a modelled
// nullable numeric that ALREADY nulls a quarter of its rows on its own
// (null_rate 0.2526, the rate 50 of the motivating 122-field survey
// profile's fields carry) gated by a packed_bool.
//
// The two null sources have to compose without either erasing the other,
// and the fixtures above cannot see that because their targets have
// null_rate 0. Three claims:
//
//   - EVERY gated row is null, whatever the sampler did.
//   - EVERY non-gated row is IDENTICAL to the rules-free run at the same
//     seed, value and null bit alike — so the field keeps its own
//     sampler nulls and its own model-inferred values.
//   - The field's own null rate still shows up among the non-gated rows.
//
// The gate is a packed_bool because the row holds exactly 1.0 / 0.0 for
// one, so `aware == 0` is an EXACT test. A `== k` gate over a numeric
// reconstructed as a continuous distribution is not: the row carries the
// pre-rounding float and the wire carries round(f), so `familiarity == 1`
// fires only on the draws that land exactly on 1.0 (which, for a clamped
// normal, is the clamp's point mass) and not on the whole wire-value-1
// bucket. Comparisons (`>= 9`) are the well-behaved numeric form.
func TestRules_GateComposesWithTheFieldsOwnNullRate(t *testing.T) {
	const rows = 4000
	build := func(rules []synth.RuleSpec) *synth.Spec {
		return &synth.Spec{
			RowCount: rows,
			Fields: []synth.FieldSpec{
				{Name: "aware", Type: "packed_bool", Distribution: synth.DistBernoulli,
					Params: map[string]any{"p": 0.75}},
				{Name: "region", Type: "categorical_u8", Distribution: synth.DistWeightedCategorical,
					Params: map[string]any{"values": []any{"east", "west"}, "weights": []any{1.0, 1.0}}},
				{Name: "regard", Type: "u4", Nullable: true, NullRate: 0.2526093295989762,
					Distribution: synth.DistNormal,
					Params:       map[string]any{"mean": 3.0, "std": 1.0, "min": 1.0, "max": 5.0}},
			},
			Models: []synth.FieldModelSpec{{
				Field: "regard", Intercept: 3, ResidualStd: 0.5,
				Predictors: []synth.ModelPredictorSpec{
					{Kind: "categorical_level", Field: "region", Level: "west", Coefficient: 1},
				},
			}},
			Rules: rules,
		}
	}
	plain, _, err := synth.SynthBytes(build(nil), synth.Options{Seed: 777})
	if err != nil {
		t.Fatalf("rules-free SynthBytes: %v", err)
	}
	ruled, _, err := synth.SynthBytes(build([]synth.RuleSpec{{
		When: "aware == 0", SetNull: []string{"regard"},
	}}), synth.Options{Seed: 777})
	if err != nil {
		t.Fatalf("ruled SynthBytes: %v", err)
	}

	aware := readF64Field(t, ruled, "aware")
	pv, pn := readFieldRows(t, plain, "regard")
	rv, rn := readFieldRows(t, ruled, "regard")
	var gated, open, openOwnNulls, ruleSourced int
	for i := range aware {
		if aware[i] == 0 {
			gated++
			if !rn[i] {
				t.Fatalf("row %d: gated row not null", i)
			}
			if rv[i] != 0 {
				t.Fatalf("row %d: masked value %v leaked onto the wire", i, rv[i])
			}
			if !pn[i] {
				ruleSourced++
			}
			continue
		}
		open++
		if rv[i] != pv[i] || rn[i] != pn[i] {
			t.Fatalf("row %d: non-gated row moved (%v/%v vs %v/%v) — "+
				"the rule reached a row its when did not select", i, rv[i], rn[i], pv[i], pn[i])
		}
		if rn[i] {
			openOwnNulls++
		}
	}
	if gated == 0 || open == 0 || ruleSourced == 0 {
		t.Fatalf("fixture degenerate: %d gated / %d non-gated / %d rule-sourced nulls",
			gated, open, ruleSourced)
	}
	// The field's own null_rate must still be visible where the rule did
	// not fire; if it were not, "non-gated rows are unchanged" would be
	// true of a field that simply has no nulls.
	if rate := float64(openOwnNulls) / float64(open); rate < 0.20 || rate > 0.31 {
		t.Fatalf("non-gated own null rate = %.4f, want near the declared 0.2526", rate)
	}
}
