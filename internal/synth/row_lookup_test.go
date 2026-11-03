package synth_test

import (
	"bytes"
	stderrors "errors"
	"fmt"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/synth"
)

// lookupRates is a one-axis table keyed by region.
func lookupRates() map[string]synth.LookupTable {
	return map[string]synth.LookupTable{
		"rates": {Rows: map[string]float64{"east": 0.25, "west": 0.75}, Axes: [][]string{{"east", "west"}}},
		"pair":  {Rows: map[string]float64{"east|1": 10, "east|0": 11, "west|1": 20, "west|0": 21}},
	}
}

func lookupSpec(rows int, rules []synth.RuleSpec, constraints ...synth.ConstraintSpec) *synth.Spec {
	return &synth.Spec{
		RowCount: rows,
		Fields: []synth.FieldSpec{
			{Name: "region", Type: "categorical_u8", Distribution: synth.DistWeightedCategorical,
				Params: map[string]any{"values": []any{"east", "west"}}},
			{Name: "flag", Type: "packed_bool", Distribution: synth.DistBernoulli, Params: map[string]any{"p": 0.5}},
			{Name: "rate", Type: "f64", Distribution: synth.DistUniform, Params: map[string]any{"min": 0.0, "max": 1.0}},
			{Name: "score", Type: "f64", Distribution: synth.DistUniform, Params: map[string]any{"min": 0.0, "max": 1.0}},
		},
		Rules:       rules,
		Constraints: constraints,
	}
}

func codeOf(err error) errors.Code {
	var ce *errors.CodedError
	if stderrors.As(err, &ce) {
		return ce.Code
	}
	return ""
}

// TestSynthLookup_SetExprAndWhenReadTheTable: set_expr writes the table
// value for the row's region; a multi-key call joins a packed_bool key
// as "1" / "0" (the float64 row shape processing also binds); a `when`
// gate reading lookup() fires on exactly the rows its value selects.
func TestSynthLookup_SetExprAndWhenReadTheTable(t *testing.T) {
	t.Parallel()

	spec := lookupSpec(200, []synth.RuleSpec{
		{SetExpr: map[string]string{"rate": `lookup("rates", region)`, "score": `lookup("pair", region, flag)`}},
		{When: `lookup("rates", region) > 0.5`, Set: map[string]any{"flag": 1}},
	})
	data, _, err := synth.SynthBytes(spec, synth.Options{Seed: 3, LookupTables: lookupRates()})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	regions := readCategoricalField(t, data, "region")
	rates, scores, flags := readF64Field(t, data, "rate"), readF64Field(t, data, "score"), readF64Field(t, data, "flag")
	seen := map[string]bool{}
	for i, r := range regions {
		seen[r] = true
		if want := lookupRates()["rates"].Rows[r]; rates[i] != want {
			t.Fatalf("row %d (%s): rate = %v, want %v", i, r, rates[i], want)
		}
		// score is computed BEFORE rule 2 rewrites flag, so it reads the
		// drawn flag: east/west with 0 or 1, never anything else.
		if scores[i] != 10 && scores[i] != 11 && scores[i] != 20 && scores[i] != 21 {
			t.Fatalf("row %d: score = %v, not a pair cell", i, scores[i])
		}
		if r == "west" && flags[i] != 1 {
			t.Fatalf("row %d: west row not gated to flag=1", i)
		}
	}
	if !seen["east"] || !seen["west"] {
		t.Fatalf("regions drawn = %v, want both", seen)
	}
}

// TestSynthLookup_SameTablesByteIdentical_ChangedTableMovesIdentity: same
// spec + seed + tables is byte-identical; a changed cell changes both
// the output and Spec.HashWithLookups, while Hash() stays table-blind.
func TestSynthLookup_SameTablesByteIdentical_ChangedTableMovesIdentity(t *testing.T) {
	t.Parallel()

	spec := lookupSpec(100, []synth.RuleSpec{{SetExpr: map[string]string{"rate": `lookup("rates", region)`}}})
	a, _, err := synth.SynthBytes(spec, synth.Options{Seed: 9, LookupTables: lookupRates()})
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := synth.SynthBytes(spec, synth.Options{Seed: 9, LookupTables: lookupRates()})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("same spec + seed + tables produced different bytes")
	}
	changed := lookupRates()
	changed["rates"].Rows["east"] = 0.3
	c, _, err := synth.SynthBytes(spec, synth.Options{Seed: 9, LookupTables: changed})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a, c) {
		t.Fatal("a changed table cell left the output unchanged")
	}
	h1, h2 := spec.HashWithLookups(lookupRates()), spec.HashWithLookups(changed)
	if h1 == h2 {
		t.Fatal("a changed table cell left HashWithLookups unchanged")
	}
	if h1 != spec.HashWithLookups(lookupRates()) {
		t.Fatal("HashWithLookups is not stable")
	}
	if h1 == spec.Hash() {
		t.Fatal("HashWithLookups of a lookup-calling spec equals the table-blind Hash")
	}
	// A table the spec does not name never moves its identity.
	extra := lookupRates()
	extra["unused"] = synth.LookupTable{Rows: map[string]float64{"x": 1}}
	if spec.HashWithLookups(extra) != h1 {
		t.Fatal("an unreferenced table moved the identity")
	}
	// An unchanged-axis, changed-axes table moves it too.
	axes := lookupRates()
	axes["rates"] = synth.LookupTable{Rows: axes["rates"].Rows, Axes: [][]string{{"east", "west", "north"}}}
	if spec.HashWithLookups(axes) == h1 {
		t.Fatal("changed axes left HashWithLookups unchanged")
	}
}

// TestSynthLookup_SpecWithoutLookupHashUnchanged: a spec calling no
// lookup() keeps its pre-existing identity under any tables.
func TestSynthLookup_SpecWithoutLookupHashUnchanged(t *testing.T) {
	t.Parallel()

	spec := lookupSpec(10, []synth.RuleSpec{{SetExpr: map[string]string{"rate": `score * 2`}}})
	if spec.HashWithLookups(lookupRates()) != spec.Hash() {
		t.Fatal("a lookup-free spec's identity moved with the tables")
	}
	if got := spec.ReferencedLookupTables(); len(got) != 0 {
		t.Fatalf("ReferencedLookupTables = %v, want none", got)
	}
}

// TestSynthLookup_EagerRefusals: a literal call is judged before any row
// is drawn, with processing's codes — an unknown table, a literal null
// key, an off-axis literal key, more keys than axes — in every slot,
// including one no row ever evaluates.
func TestSynthLookup_EagerRefusals(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		call string
		code errors.Code
	}{
		{`lookup("nope", region)`, errors.PULSE_LOOKUP_TABLE_UNKNOWN},
		{`lookup("rates", nil)`, errors.PULSE_LOOKUP_MISS},
		{`lookup("rates", "north")`, errors.PULSE_LOOKUP_MISS},
		{`lookup("rates", region, flag)`, errors.PULSE_LOOKUP_MISS},
	} {
		for _, spec := range []*synth.Spec{
			lookupSpec(5, []synth.RuleSpec{{SetExpr: map[string]string{"rate": tc.call + " ?? 0"}}}),
			lookupSpec(5, []synth.RuleSpec{{When: "(" + tc.call + " ?? 0) > 0", Set: map[string]any{"rate": 1}}}),
			lookupSpec(5, nil, synth.ConstraintSpec{Expr: "(" + tc.call + " ?? 0) >= 0"}),
			// Behind a gate that never fires (rate is drawn in [0, 1]), so
			// only the pre-row check can see the call.
			lookupSpec(5, []synth.RuleSpec{{When: `score > 2`, SetExpr: map[string]string{"rate": tc.call + " ?? 0"}}}),
		} {
			_, res, err := synth.SynthBytes(spec, synth.Options{Seed: 1, LookupTables: lookupRates()})
			if codeOf(err) != tc.code {
				t.Fatalf("%s: err = %v, want %s", tc.call, err, tc.code)
			}
			if res != nil {
				t.Fatalf("%s: a refused run returned a result", tc.call)
			}
		}
	}
	// No tables at all: a literal call is an unknown table, not a compile
	// fault.
	spec := lookupSpec(5, []synth.RuleSpec{{SetExpr: map[string]string{"rate": `lookup("rates", region)`}}})
	if _, _, err := synth.SynthBytes(spec, synth.Options{Seed: 1}); codeOf(err) != errors.PULSE_LOOKUP_TABLE_UNKNOWN {
		t.Fatalf("no tables: err = %v, want PULSE_LOOKUP_TABLE_UNKNOWN", err)
	}
	var ce *errors.CodedError
	_, _, err := synth.SynthBytes(spec, synth.Options{Seed: 1})
	if !stderrors.As(err, &ce) || ce.Details["table"] != "rates" || ce.Details[errors.DetailSynthRule] != 0 || ce.Details["field"] != "rate" {
		t.Fatalf("details = %v, want table rates at rule 0 field rate", ce)
	}
}

// TestSynthLookup_NullFieldKeyIsAMiss: a key that reads a field the row
// holds as null is a null key (PULSE_LOOKUP_MISS, null_key), never the
// field's drawn value — whether the field is the key itself or only
// part of a computed key.
func TestSynthLookup_NullFieldKeyIsAMiss(t *testing.T) {
	t.Parallel()

	for _, key := range []string{`region`, `region + ""`} {
		spec := lookupSpec(50, []synth.RuleSpec{{SetExpr: map[string]string{"rate": `lookup("rates", ` + key + `)`}}})
		spec.Fields[0].Nullable = true
		spec.Fields[0].NullRate = 0.5
		_, _, err := synth.SynthBytes(spec, synth.Options{Seed: 4, LookupTables: lookupRates()})
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_LOOKUP_MISS || ce.Details["null_key"] != 0 {
			t.Fatalf("key %s: err = %v, want PULSE_LOOKUP_MISS with null_key 0", key, err)
		}
		// Guarded by isnull, the same call runs.
		spec.Rules[0].SetExpr["rate"] = `isnull(region) ? 0 : lookup("rates", ` + key + `)`
		if _, _, err := synth.SynthBytes(spec, synth.Options{Seed: 4, LookupTables: lookupRates()}); err != nil {
			t.Fatalf("key %s guarded: %v", key, err)
		}
	}
	// A constraint's null key keeps the code too.
	spec := lookupSpec(50, nil, synth.ConstraintSpec{Expr: `lookup("rates", region) >= 0`})
	spec.Fields[0].Nullable = true
	spec.Fields[0].NullRate = 0.5
	if _, _, err := synth.SynthBytes(spec, synth.Options{Seed: 4, LookupTables: lookupRates()}); codeOf(err) != errors.PULSE_LOOKUP_MISS {
		t.Fatalf("constraint null key: err = %v, want PULSE_LOOKUP_MISS", err)
	}
}

// TestSynthLookup_RowMissKeepsCode: a computed key no row holds misses at
// run time with PULSE_LOOKUP_MISS, not the generic PROCESSING_RUNTIME.
func TestSynthLookup_RowMissKeepsCode(t *testing.T) {
	t.Parallel()

	tables := lookupRates()
	tables["rates"] = synth.LookupTable{Rows: map[string]float64{"east": 1}}
	spec := lookupSpec(50, []synth.RuleSpec{{SetExpr: map[string]string{"rate": `lookup("rates", region)`}}})
	_, _, err := synth.SynthBytes(spec, synth.Options{Seed: 2, LookupTables: tables})
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_LOOKUP_MISS || fmt.Sprint(ce.Details["keys"]) != "[west]" {
		t.Fatalf("err = %v, want PULSE_LOOKUP_MISS for keys [west]", err)
	}
}

// TestSynthLookup_ParseRefusals: the table must be a string literal,
// refused at spec parse (before any table is known) with the site's code;
// a field named lookup shadows the call; the literal form parses.
func TestSynthLookup_ParseRefusals(t *testing.T) {
	t.Parallel()

	for _, call := range []string{`lookup("ra" + "tes", region)`, `lookup(region, region)`, `lookup()`} {
		expectCode(t, lagDoc(fmt.Sprintf(`"rules": [{"set_expr": {"y": %q}}]`, "("+call+" ?? 0) + 1")), errors.PULSE_SYNTH_RULE_EXPR_INVALID)
		expectCode(t, lagDoc(fmt.Sprintf(`"rules": [{"when": %q, "set": {"y": 1}}]`, "("+call+" ?? 0) > 0")), errors.PULSE_SYNTH_RULE_EXPR_INVALID)
		expectCode(t, lagDoc(fmt.Sprintf(`"constraints": [{"expr": %q}]`, "("+call+" ?? 0) >= 0")), errors.SERVICE_VALIDATION)
	}
	if _, err := synth.ParseSpec([]byte(lagDoc(`"rules": [{"set_expr": {"y": "lookup(\"rates\", region) ?? 0"}}]`))); err != nil {
		t.Fatalf("literal lookup refused at parse: %v", err)
	}
	expectCode(t, lagDoc(`"rules": [{"set_expr": {"y": "lookup ?? 0"}}]`), errors.PULSE_SYNTH_RULE_EXPR_INVALID, "bare lookup")
}

// TestSynthLookup_ReferencedTables: the referenced set is every literal
// table name across when, set_expr and constraints, sorted, once each.
func TestSynthLookup_ReferencedTables(t *testing.T) {
	t.Parallel()

	spec := lookupSpec(5, []synth.RuleSpec{
		{When: `lookup("b", region) > 0`, SetExpr: map[string]string{"rate": `lookup("a", region) + lookup("b", region)`}},
	}, synth.ConstraintSpec{Expr: `lookup("c", region) >= 0`})
	if got := fmt.Sprint(spec.ReferencedLookupTables()); got != "[a b c]" {
		t.Fatalf("ReferencedLookupTables = %s, want [a b c]", got)
	}
}

// TestLookupDigest_ContentSensitive: every cell value bit, key and axis
// value moves the digest; map construction order does not.
func TestLookupDigest_ContentSensitive(t *testing.T) {
	t.Parallel()

	base := synth.LookupTable{Rows: map[string]float64{"a": 1, "b": 2}}
	same := synth.LookupTable{Rows: map[string]float64{"b": 2, "a": 1}}
	if synth.LookupDigest(base) != synth.LookupDigest(same) {
		t.Fatal("digest depends on map order")
	}
	for name, other := range map[string]synth.LookupTable{
		"value": {Rows: map[string]float64{"a": 1, "b": 2.0000000000000004}},
		"key":   {Rows: map[string]float64{"a": 1, "c": 2}},
		"axes":  {Rows: map[string]float64{"a": 1, "b": 2}, Axes: [][]string{{"a", "b"}}},
		"zero":  {Rows: map[string]float64{"a": 1, "b": 2, "z": 0}},
	} {
		if synth.LookupDigest(other) == synth.LookupDigest(base) {
			t.Errorf("%s change left the digest unchanged", name)
		}
	}
}

// TestSuggestRules_NeverProposesLookup: the detectors share lookup()'s
// env but no candidate they emit calls it — a table is an instance fact
// the captured cohort cannot evidence.
func TestSuggestRules_NeverProposesLookup(t *testing.T) {
	t.Parallel()

	prof, _ := suggestFixtureProfile(t)
	if len(prof.RuleCandidates) == 0 {
		t.Fatal("the fixture proposed no rule; the check would be vacuous")
	}
	for i, r := range prof.RuleCandidates {
		srcs := []string{r.When}
		for _, v := range r.SetExpr {
			srcs = append(srcs, v)
		}
		for _, s := range srcs {
			if strings.Contains(s, "lookup") {
				t.Fatalf("candidate %d calls lookup(): %q", i, s)
			}
		}
	}
}
