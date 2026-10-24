package synth_test

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/synth"
)

// rejectingSpec is a spec whose constraint rejects about half of all
// attempts (x is uniform on [0, 1) and must land below 0.5), plus a
// field `r` a test fills with row() through set_expr.
func rejectingSpec(rows int) *synth.Spec {
	return &synth.Spec{
		RowCount: rows,
		Fields: []synth.FieldSpec{
			{Name: "x", Type: "f64", Distribution: synth.DistUniform,
				Params: map[string]any{"min": 0.0, "max": 1.0}},
			{Name: "r", Type: "f64", Distribution: synth.DistConstant,
				Params: map[string]any{"value": -1.0}},
		},
		Constraints:      []synth.ConstraintSpec{{Expr: "x < 0.5"}},
		MaxRejectionRate: 0.9,
	}
}

// TestSynthRow_AcceptedOrdinal: row() is the 0-based ordinal of the
// ACCEPTED row. With a constraint rejecting about half the attempts the
// written column is exactly 0..row_count-1 — a rejected attempt redraws
// at the same ordinal rather than consuming one.
func TestSynthRow_AcceptedOrdinal(t *testing.T) {
	spec := rejectingSpec(200)
	spec.Rules = []synth.RuleSpec{{SetExpr: map[string]string{"r": "row()"}}}
	data, res, err := synth.SynthBytes(spec, synth.Options{Seed: 11})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	if res.RowsRejected == 0 {
		t.Fatal("the constraint rejected no row; the test does not exercise redraws")
	}
	rs := readF64Field(t, data, "r")
	if len(rs) != 200 {
		t.Fatalf("got %d rows, want 200", len(rs))
	}
	for i, r := range rs {
		if r != float64(i) {
			t.Fatalf("row %d: row() = %v, want %d (rejected attempts must not advance it)", i, r, i)
		}
	}
}

// TestSynthRow_WhenAndConstraintSeeTheAcceptedOrdinal: the same ordinal
// reaches `when` and `constraints[]`. The `when` gates on it exactly;
// the constraint tightens ONE ordinal (3) to x < 0.05, and the row that
// lands there must satisfy it — an attempt-counted row() would apply the
// tightening to whichever attempt happened to be the fourth.
func TestSynthRow_WhenAndConstraintSeeTheAcceptedOrdinal(t *testing.T) {
	spec := rejectingSpec(60)
	spec.Rules = []synth.RuleSpec{{When: "row() >= 10", Set: map[string]any{"r": 1.0}}}
	spec.Constraints = append(spec.Constraints, synth.ConstraintSpec{Expr: "row() != 3 || x < 0.05"})
	data, res, err := synth.SynthBytes(spec, synth.Options{Seed: 3})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	if res.RowsRejected == 0 {
		t.Fatal("no rejection")
	}
	xs, rs := readF64Field(t, data, "x"), readF64Field(t, data, "r")
	for i, r := range rs {
		want := -1.0
		if i >= 10 {
			want = 1
		}
		if r != want {
			t.Fatalf("row %d: r = %v, want %v (when row() >= 10)", i, r, want)
		}
	}
	if xs[3] >= 0.05 {
		t.Fatalf("row 3: x = %v, the constraint keyed on row() == 3 did not hold", xs[3])
	}
	loose := 0
	for i, x := range xs {
		if i != 3 && x >= 0.05 {
			loose++
		}
	}
	if loose == 0 {
		t.Fatal("every row satisfied x < 0.05; the ordinal-3 tightening is indistinguishable")
	}
}

// TestSynthRow_ConsumesNoRNG: writing row() instead of a literal moves
// no other field's bytes — row() reads a counter, never the stream.
func TestSynthRow_ConsumesNoRNG(t *testing.T) {
	withRow := rejectingSpec(150)
	withRow.Rules = []synth.RuleSpec{{When: "row() % 2 == 0", SetExpr: map[string]string{"r": "row() * 3"}}}
	literal := rejectingSpec(150)
	literal.Rules = []synth.RuleSpec{{SetExpr: map[string]string{"r": "0"}}}
	a, _, err := synth.SynthBytes(withRow, synth.Options{Seed: 9})
	if err != nil {
		t.Fatalf("SynthBytes(row): %v", err)
	}
	b, _, err := synth.SynthBytes(literal, synth.Options{Seed: 9})
	if err != nil {
		t.Fatalf("SynthBytes(literal): %v", err)
	}
	xa, xb := readF64Field(t, a, "x"), readF64Field(t, b, "x")
	for i := range xa {
		if math.Float64bits(xa[i]) != math.Float64bits(xb[i]) {
			t.Fatalf("row %d: x moved (%v vs %v) when a rule called row()", i, xa[i], xb[i])
		}
	}
}

// TestSynthRow_PanelResetIdiom: `row() % 52 == 0 ? a : b` — the
// period-start branch of a weekly panel — picks `a` exactly on ordinals
// 0, 52, 104 and `b` everywhere else, under rejection.
func TestSynthRow_PanelResetIdiom(t *testing.T) {
	spec := rejectingSpec(130)
	spec.Fields = append(spec.Fields,
		synth.FieldSpec{Name: "a", Type: "f64", Distribution: synth.DistUniform,
			Params: map[string]any{"min": 100.0, "max": 200.0}},
		synth.FieldSpec{Name: "b", Type: "f64", Distribution: synth.DistUniform,
			Params: map[string]any{"min": 0.0, "max": 1.0}},
	)
	spec.Rules = []synth.RuleSpec{{SetExpr: map[string]string{"r": "row() % 52 == 0 ? a : b"}}}
	data, res, err := synth.SynthBytes(spec, synth.Options{Seed: 21})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	if res.RowsRejected == 0 {
		t.Fatal("no rejection")
	}
	as, bs, rs := readF64Field(t, data, "a"), readF64Field(t, data, "b"), readF64Field(t, data, "r")
	resets := 0
	for i, r := range rs {
		want := bs[i]
		if i%52 == 0 {
			want = as[i]
			resets++
		}
		if math.Float64bits(r) != math.Float64bits(want) {
			t.Fatalf("row %d: r = %v, want %v", i, r, want)
		}
	}
	if resets != 3 {
		t.Fatalf("resets = %d, want 3 (ordinals 0, 52, 104)", resets)
	}
}

// TestSynthRow_ArityAndBareNameRefused: row is typed func() int, so an
// argument is a compile refusal, and a bare `row` with no field of that
// name is refused like every other built-in name (the field-named case
// is TestSynthExprMath_FieldNamedLikeABuiltinStillReadsTheField).
func TestSynthRow_ArityAndBareNameRefused(t *testing.T) {
	expectCode(t, specDoc(`"rules": [{"set_expr": {"y": "row(1)"}}]`), errors.PULSE_SYNTH_RULE_EXPR_INVALID)
	expectCode(t, specDoc(`"rules": [{"set_expr": {"y": "row + 1"}}]`), errors.PULSE_SYNTH_RULE_EXPR_INVALID, "bare row")
	// Constraints compile at generation, not at parse.
	bad := rejectingSpec(5)
	bad.Constraints = []synth.ConstraintSpec{{Expr: "row(2) > 0"}}
	if _, _, err := synth.SynthBytes(bad, synth.Options{Seed: 1}); err == nil {
		t.Fatal("constraint row(2) compiled")
	} else if ce, ok := err.(*errors.CodedError); !ok || ce.Code != errors.SERVICE_VALIDATION {
		t.Fatalf("constraint row(2): %v, want SERVICE_VALIDATION", err)
	}
	if _, err := synth.ParseSpec([]byte(specDoc(`"rules": [{"when": "row() % 7 == 0", "set_expr": {"y": "row() / 2"}}], "constraints": [{"expr": "row() >= 0"}]`))); err != nil {
		t.Fatalf("row() refused: %v", err)
	}
}

// TestSynthMonotonic_SkipsRejected: monotonic_from advances only on an
// ACCEPTED row, so under a rejecting constraint the column is
// start + i*step with no gaps.
func TestSynthMonotonic_SkipsRejected(t *testing.T) {
	spec := rejectingSpec(120)
	spec.Fields = append(spec.Fields, synth.FieldSpec{Name: "id", Type: "u32",
		Distribution: synth.DistMonotonicFrom, Params: map[string]any{"start": 100, "step": 5}})
	// Nullable: the null wrapper sits between the counter and the row
	// loop, and must not hide it.
	spec.Fields = append(spec.Fields, synth.FieldSpec{Name: "down", Type: "f64", Nullable: true, NullRate: 0.2,
		Distribution: synth.DistMonotonicFrom, Params: map[string]any{"start": 0, "step": -2}})
	data, res, err := synth.SynthBytes(spec, synth.Options{Seed: 13})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	if res.RowsRejected == 0 {
		t.Fatal("no rejection")
	}
	ids := readF64Field(t, data, "id")
	downs, downNull := readFieldRows(t, data, "down")
	present := 0
	for i := range ids {
		if ids[i] != float64(100+5*i) {
			t.Fatalf("row %d: id = %v, want %d (a rejected attempt left a gap)", i, ids[i], 100+5*i)
		}
		if downNull[i] {
			continue
		}
		present++
		if downs[i] != float64(-2*i) {
			t.Fatalf("row %d: nullable down = %v, want %d", i, downs[i], -2*i)
		}
	}
	if present == 0 || present == len(ids) {
		t.Fatalf("down present on %d of %d rows; the null wrapper is not exercised", present, len(ids))
	}
}

// TestSynthMonotonic_WithoutRejectionIsByteIdentical pins a spec that
// uses monotonic_from and a constraint that never rejects to the bytes
// it generated BEFORE the counter became accept-only, so the change
// moves nothing for a spec it does not affect.
func TestSynthMonotonic_WithoutRejectionIsByteIdentical(t *testing.T) {
	spec := &synth.Spec{
		RowCount: 300,
		Fields: []synth.FieldSpec{
			{Name: "id", Type: "u64", Distribution: synth.DistMonotonicFrom,
				Params: map[string]any{"start": 1000, "step": 3}},
			{Name: "x", Type: "f64", NullRate: 0.1, Distribution: synth.DistUniform,
				Params: map[string]any{"min": 0.0, "max": 1.0}},
			{Name: "seq", Type: "f64", NullRate: 0.3, Distribution: synth.DistMonotonicFrom,
				Params: map[string]any{"start": 7, "step": -1}},
		},
		Rules:       []synth.RuleSpec{{When: "x > 0.5", SetExpr: map[string]string{"x": "x * 2"}}},
		Constraints: []synth.ConstraintSpec{{Expr: "x < 5"}},
	}
	data, res, err := synth.SynthBytes(spec, synth.Options{Seed: 77})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	if res.RowsRejected != 0 {
		t.Fatalf("the pin spec must not reject; rejected %d", res.RowsRejected)
	}
	if got := sha256Hex(data); got != preAcceptOnlyMonotonicHash {
		t.Fatalf("a spec with no rejection changed bytes\n got %s\nwant %s", got, preAcceptOnlyMonotonicHash)
	}
}

// preAcceptOnlyMonotonicHash is the sha256 of the spec above at seed 77,
// generated on the tree BEFORE E2-S2 (the E2-S1 commit). Uniform draws
// only, so the pin is architecture-independent.
const preAcceptOnlyMonotonicHash = "cd80414592944faa610be67436f32081cb52e72db3fbef8c10253b5d8ce6573c"
