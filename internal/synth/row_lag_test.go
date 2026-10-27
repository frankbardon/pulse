package synth_test

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/synth"
)

// adstockSpec is the recursive-adstock shape: a seeded random `spend`
// and a `true_adstock` a rule derives from spend and its OWN previous
// value.
func adstockSpec(rows int) *synth.Spec {
	return &synth.Spec{
		RowCount: rows,
		Fields: []synth.FieldSpec{
			{Name: "spend", Type: "f64", Distribution: synth.DistNormal,
				Params: map[string]any{"mean": 100.0, "std": 30.0}},
			{Name: "true_adstock", Type: "f64", Distribution: synth.DistUniform,
				Params: map[string]any{"min": 0.0, "max": 1.0}},
		},
		Rules: []synth.RuleSpec{{SetExpr: map[string]string{
			"true_adstock": `spend + 0.6 * lag("true_adstock", 1, 0)`,
		}}},
	}
}

// TestSynthLag_RecursiveAdstock: true_adstock = spend + 0.6 * its own
// previous value equals the recursion computed by hand over the written
// spend column, bit for bit. The expr VM evaluates the product and the
// sum as two operations, so the hand side writes the product's rounding
// barrier explicitly.
func TestSynthLag_RecursiveAdstock(t *testing.T) {
	t.Parallel()

	data, _, err := synth.SynthBytes(adstockSpec(300), synth.Options{Seed: 17})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	spend, ad := readF64Field(t, data, "spend"), readF64Field(t, data, "true_adstock")
	if len(ad) != 300 {
		t.Fatalf("rows = %d, want 300", len(ad))
	}
	prev := 0.0
	for i := range ad {
		want := spend[i] + float64(0.6*prev)
		if math.Float64bits(ad[i]) != math.Float64bits(want) {
			t.Fatalf("row %d: true_adstock = %v, want %v (spend %v + 0.6 * %v)", i, ad[i], want, spend[i], prev)
		}
		prev = want
	}
}

// TestSynthLag_ByteIdentical: one spec and seed, two byte-identical
// cohorts, with lag in set_expr, when and a rejecting constraint.
func TestSynthLag_ByteIdentical(t *testing.T) {
	t.Parallel()

	spec := adstockSpec(400)
	spec.Fields = append(spec.Fields, synth.FieldSpec{Name: "flag", Type: "f64", Distribution: synth.DistConstant,
		Params: map[string]any{"value": 0.0}})
	spec.Rules = append(spec.Rules, synth.RuleSpec{When: `lag("spend", 2, 0) > 110`, Set: map[string]any{"flag": 1.0}})
	spec.Constraints = []synth.ConstraintSpec{{Expr: `spend > lag("spend", 1, 0) - 40`}}
	spec.MaxRejectionRate = 0.9
	a, ra, err := synth.SynthBytes(spec, synth.Options{Seed: 5})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	b, _, err := synth.SynthBytes(spec, synth.Options{Seed: 5})
	if err != nil {
		t.Fatalf("SynthBytes again: %v", err)
	}
	if ra.RowsRejected == 0 {
		t.Fatal("the constraint rejected nothing")
	}
	if !bytes.Equal(a, b) {
		t.Fatal("two runs of one spec and seed differ")
	}
}

// TestSynthLag_RejectedNotBuffered: under a constraint rejecting about
// half the attempts, lag("x", 1) on every row is the PREVIOUS WRITTEN
// row's x — never a rejected attempt's.
func TestSynthLag_RejectedNotBuffered(t *testing.T) {
	t.Parallel()

	spec := rejectingSpec(200)
	spec.Rules = []synth.RuleSpec{{SetExpr: map[string]string{"r": `lag("x", 1, -1)`}}}
	data, res, err := synth.SynthBytes(spec, synth.Options{Seed: 11})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	if res.RowsRejected == 0 {
		t.Fatal("no rejection; the test does not exercise redraws")
	}
	xs, rs := readF64Field(t, data, "x"), readF64Field(t, data, "r")
	for i := range rs {
		want := -1.0
		if i > 0 {
			want = xs[i-1]
		}
		if math.Float64bits(rs[i]) != math.Float64bits(want) {
			t.Fatalf("row %d: lag(x, 1) = %v, want %v (the previous accepted row)", i, rs[i], want)
		}
	}
}

// TestSynthLag_WhenAndConstraint: lag reaches `when` and `constraints[]`
// too. The constraint bounds each step of x against the previous
// accepted x, and the `when` flags rows whose previous x was high.
func TestSynthLag_WhenAndConstraint(t *testing.T) {
	t.Parallel()

	spec := rejectingSpec(150)
	spec.Constraints = []synth.ConstraintSpec{{Expr: `abs(x - lag("x", 1, x)) < 0.3`}}
	spec.Rules = []synth.RuleSpec{{When: `lag("x", 1, 0) > 0.5`, Set: map[string]any{"r": 1.0}}}
	data, res, err := synth.SynthBytes(spec, synth.Options{Seed: 8})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	if res.RowsRejected == 0 {
		t.Fatal("no rejection")
	}
	xs, rs := readF64Field(t, data, "x"), readF64Field(t, data, "r")
	flagged := 0
	for i := 1; i < len(xs); i++ {
		if math.Abs(xs[i]-xs[i-1]) >= 0.3 {
			t.Fatalf("row %d: step %v -> %v breaks the lag constraint", i, xs[i-1], xs[i])
		}
		want := -1.0
		if xs[i-1] > 0.5 {
			want = 1
			flagged++
		}
		if rs[i] != want {
			t.Fatalf("row %d: r = %v, want %v (previous x %v)", i, rs[i], want, xs[i-1])
		}
	}
	if rs[0] != -1 {
		t.Fatalf("row 0: r = %v, want -1 (the default 0 is not > 0.5)", rs[0])
	}
	if flagged == 0 || flagged == len(xs)-1 {
		t.Fatalf("flagged %d of %d rows; the when does not discriminate", flagged, len(xs)-1)
	}
}

// lagFields is a small field set for the default / null / typing tests.
func lagFields() []synth.FieldSpec {
	return []synth.FieldSpec{
		{Name: "x", Type: "f64", Distribution: synth.DistMonotonicFrom, Params: map[string]any{"start": 10.0}},
		{Name: "z", Type: "f64", Nullable: true, NullRate: 0.4, Distribution: synth.DistUniform,
			Params: map[string]any{"min": 0.0, "max": 1.0}},
		{Name: "y", Type: "f64", Distribution: synth.DistConstant, Params: map[string]any{"value": 0.0}},
		{Name: "w", Type: "f64", Distribution: synth.DistConstant, Params: map[string]any{"value": 0.0}},
		{Name: "v", Type: "f64", Distribution: synth.DistConstant, Params: map[string]any{"value": 0.0}},
		{Name: "u", Type: "f64", Distribution: synth.DistConstant, Params: map[string]any{"value": 0.0}},
	}
}

// TestSynthLag_DefaultAndNullBeforeN: before n rows exist, lag returns
// its default, or null (here caught by `??`) when none is given. Past
// that, a lagged row whose field was NULL reads null, not the default.
func TestSynthLag_DefaultAndNullBeforeN(t *testing.T) {
	t.Parallel()

	spec := &synth.Spec{RowCount: 80, Fields: lagFields(), Rules: []synth.RuleSpec{{SetExpr: map[string]string{
		"y": `lag("x", 3, -2)`,
		"w": `lag("x", 3) ?? -1`,
		"v": `lag("z", 1, 99) ?? -5`,
		"u": `lag("x", 3, row())`,
	}}}}
	data, _, err := synth.SynthBytes(spec, synth.Options{Seed: 2})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	xs, ys, ws, vs := readF64Field(t, data, "x"), readF64Field(t, data, "y"), readF64Field(t, data, "w"), readF64Field(t, data, "v")
	zs, znull := readFieldRows(t, data, "z")
	for i := range ys {
		wantY, wantW := -2.0, -1.0
		if i >= 3 {
			wantY, wantW = xs[i-3], xs[i-3]
		}
		if ys[i] != wantY || ws[i] != wantW {
			t.Fatalf("row %d: lag(x, 3, -2) = %v (want %v), lag(x, 3) ?? -1 = %v (want %v)", i, ys[i], wantY, ws[i], wantW)
		}
	}
	// An int-typed default (row()) lands as the field's float.
	for i, u := range readF64Field(t, data, "u") {
		want := float64(i)
		if i >= 3 {
			want = xs[i-3]
		}
		if u != want {
			t.Fatalf("row %d: lag(x, 3, row()) = %v, want %v", i, u, want)
		}
	}
	nulls := 0
	for i := range vs {
		want := 99.0
		switch {
		case i == 0:
		case znull[i-1]:
			want = -5
			nulls++
		default:
			want = zs[i-1]
		}
		if math.Float64bits(vs[i]) != math.Float64bits(want) {
			t.Fatalf("row %d: lag(z, 1, 99) ?? -5 = %v, want %v", i, vs[i], want)
		}
	}
	if nulls == 0 {
		t.Fatal("no null z was lagged; the null arm is not exercised")
	}
}

// TestSynthLag_UnguardedNullIsLoud: lag with no default writing straight
// to a field is null on row 0, and the coercion matrix refuses it at row
// time rather than writing a silent 0.
func TestSynthLag_UnguardedNullIsLoud(t *testing.T) {
	t.Parallel()

	spec := &synth.Spec{RowCount: 5, Fields: lagFields(), Rules: []synth.RuleSpec{{SetExpr: map[string]string{"y": `lag("x", 1)`}}}}
	_, _, err := synth.SynthBytes(spec, synth.Options{Seed: 1})
	ce, ok := err.(*errors.CodedError)
	if !ok || ce.Code != errors.PULSE_SYNTH_RULE_VALUE_INVALID {
		t.Fatalf("unguarded null lag: %v, want PULSE_SYNTH_RULE_VALUE_INVALID", err)
	}
}

const lagDocFields = `"fields": [
	{"name": "x", "type": "f64", "distribution": "uniform", "params": {"min": 0, "max": 1}},
	{"name": "y", "type": "f64", "distribution": "uniform", "params": {"min": 0, "max": 1}},
	{"name": "region", "type": "categorical_u8", "distribution": "weighted_categorical", "params": {"values": ["east", "west"]}}
]`

func lagDoc(tail string) string {
	return `{"row_count": 10, ` + lagDocFields + `, ` + tail + `}`
}

// TestSynthLag_ValidationRefusals: lag's name and offset are literals
// checked at spec parse, in every slot, with their own codes.
func TestSynthLag_ValidationRefusals(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		call string
		code errors.Code
	}{
		{`lag(x, 1)`, errors.PULSE_SYNTH_LAG_FIELD_INVALID},
		{`lag("nope", 1)`, errors.PULSE_SYNTH_LAG_FIELD_INVALID},
		{`lag("x" + "", 1)`, errors.PULSE_SYNTH_LAG_FIELD_INVALID},
		{`lag("x", 0)`, errors.PULSE_SYNTH_LAG_OFFSET_INVALID},
		{`lag("x", -1)`, errors.PULSE_SYNTH_LAG_OFFSET_INVALID},
		{`lag("x", 1025)`, errors.PULSE_SYNTH_LAG_OFFSET_INVALID},
		{`lag("x", 1.5)`, errors.PULSE_SYNTH_LAG_OFFSET_INVALID},
		{`lag("x", row())`, errors.PULSE_SYNTH_LAG_OFFSET_INVALID},
		{`lag("x")`, errors.PULSE_SYNTH_RULE_EXPR_INVALID},
	} {
		call := tc.call
		for _, tail := range []string{
			fmt.Sprintf(`"rules": [{"set_expr": {"y": %q}}]`, "("+call+" ?? 0) + 1"),
			fmt.Sprintf(`"rules": [{"when": %q, "set": {"y": 1}}]`, "("+call+" ?? 0) > 0"),
		} {
			ce := expectCode(t, lagDoc(tail), tc.code)
			if ce.Details["function"] != "lag" {
				t.Errorf("%s: details.function = %v", tail, ce.Details["function"])
			}
		}
		code := tc.code
		if code == errors.PULSE_SYNTH_RULE_EXPR_INVALID {
			code = errors.SERVICE_VALIDATION
		}
		expectCode(t, lagDoc(fmt.Sprintf(`"constraints": [{"expr": %q}]`, "("+call+" ?? 0) >= 0")), code)
	}
	ce := expectCode(t, lagDoc(`"rules": [{"set_expr": {"y": "lag(\"nope\", 1, 0)"}}]`), errors.PULSE_SYNTH_LAG_FIELD_INVALID, `"nope"`)
	if ce.Details["lag_field"] != "nope" || ce.Details["field"] != "y" {
		t.Errorf("details = %v, want lag_field nope and field y", ce.Details)
	}
	// The cap itself is accepted, and so is a self-lag.
	if _, err := synth.ParseSpec([]byte(lagDoc(`"rules": [{"set_expr": {"y": "lag(\"x\", 1024, 0) + lag(\"y\", 1, 0)"}}], "constraints": [{"expr": "lag(\"region\", 2, \"east\") != \"\""}]`))); err != nil {
		t.Fatalf("valid lag calls refused: %v", err)
	}
}

// TestSynthLag_ReturnIsTypedByTheLaggedField: lag of a categorical is a
// string, so writing it to an f64 target is refused at PARSE by the
// coercion matrix's static half, a numeric default for it does not
// compile, and lag of a categorical into a categorical copies the
// previous row's level.
func TestSynthLag_ReturnIsTypedByTheLaggedField(t *testing.T) {
	t.Parallel()

	expectCode(t, lagDoc(`"rules": [{"set_expr": {"y": "lag(\"region\", 1, \"east\")"}}]`), errors.PULSE_SYNTH_RULE_VALUE_INVALID)
	expectCode(t, lagDoc(`"rules": [{"set_expr": {"region": "lag(\"region\", 1, 0)"}}]`), errors.PULSE_SYNTH_RULE_EXPR_INVALID)

	spec := &synth.Spec{RowCount: 60, Fields: []synth.FieldSpec{
		{Name: "region", Type: "categorical_u8", Distribution: synth.DistWeightedCategorical,
			Params: map[string]any{"values": []any{"east", "west", "north"}}},
		{Name: "prev", Type: "categorical_u8", Distribution: synth.DistWeightedCategorical,
			Params: map[string]any{"values": []any{"east", "west", "north"}}},
		{Name: "brands", Type: "set_u8", Distribution: synth.DistSetBernoulli, Params: map[string]any{
			"options": []any{"acme", "globex", "initech"}, "frequencies": []any{0.5, 0.5, 0.5}}},
		{Name: "prev_brands", Type: "set_u8", Distribution: synth.DistSetBernoulli, Params: map[string]any{
			"options": []any{"acme", "globex", "initech"}, "frequencies": []any{0.5, 0.5, 0.5}}},
	}, Rules: []synth.RuleSpec{{SetExpr: map[string]string{
		"prev":        `lag("region", 1, "north")`,
		"prev_brands": `lag("brands", 1, ["initech"])`,
	}}}}
	data, _, err := synth.SynthBytes(spec, synth.Options{Seed: 3})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	region, prev := readCategoricalField(t, data, "region"), readCategoricalField(t, data, "prev")
	_, brands, _ := readSetFieldRows(t, data, "brands")
	_, prevBrands, _ := readSetFieldRows(t, data, "prev_brands")
	for i := range prev {
		wantLevel, wantSet := "north", []string{"initech"}
		if i > 0 {
			wantLevel, wantSet = region[i-1], brands[i-1]
		}
		if prev[i] != wantLevel {
			t.Fatalf("row %d: prev = %q, want %q", i, prev[i], wantLevel)
		}
		if fmt.Sprint(prevBrands[i]) != fmt.Sprint(wantSet) {
			t.Fatalf("row %d: prev_brands = %v, want %v", i, prevBrands[i], wantSet)
		}
	}
}

// TestSynthLag_ConsumesNoRNG: a lag that binds nothing (a constraint it
// can never fail, a rule rewriting a constant) moves no drawn byte.
func TestSynthLag_ConsumesNoRNG(t *testing.T) {
	t.Parallel()

	plain := rejectingSpec(120)
	withLag := rejectingSpec(120)
	withLag.Constraints = append(withLag.Constraints, synth.ConstraintSpec{Expr: `lag("x", 7, 0) >= 0`})
	withLag.Rules = []synth.RuleSpec{{When: `lag("r", 2, 0) > 100`, Set: map[string]any{"r": 5.0}}}
	a, _, err := synth.SynthBytes(plain, synth.Options{Seed: 13})
	if err != nil {
		t.Fatalf("SynthBytes(plain): %v", err)
	}
	b, _, err := synth.SynthBytes(withLag, synth.Options{Seed: 13})
	if err != nil {
		t.Fatalf("SynthBytes(lag): %v", err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("a lag that binds nothing changed the cohort")
	}
}

// TestSynthLag_SpecWithoutLagIsByteIdentical: the pre-U58 row-env spec
// still writes the bytes pinned before lag existed (preSharedMathSpecHash
// was taken on the tree before this story and is unchanged by it).
func TestSynthLag_SpecWithoutLagIsByteIdentical(t *testing.T) {
	t.Parallel()

	data, _, err := synth.SynthBytes(preU58RowEnvSpec(), synth.Options{Seed: 4242})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	if got := sha256Hex(data); got != preSharedMathSpecHash {
		t.Fatalf("a lag-free spec changed bytes\n got %s\nwant %s", got, preSharedMathSpecHash)
	}
}

// TestSynthLag_AugmentStartsEmptyAtTheFirstGeneratedRow: augmentation
// copies the source rows first, but lag's state starts EMPTY at the
// first generated row — it returns the default there, never the last
// source row's value, and thereafter reads generated rows only.
func TestSynthLag_AugmentStartsEmptyAtTheFirstGeneratedRow(t *testing.T) {
	t.Parallel()

	fields := []synth.FieldSpec{
		{Name: "x", Type: "f64", Distribution: synth.DistMonotonicFrom, Params: map[string]any{"start": 1000.0}},
		{Name: "y", Type: "f64", Distribution: synth.DistConstant, Params: map[string]any{"value": 0.0}},
	}
	srcData, _, err := synth.SynthBytes(&synth.Spec{RowCount: 30, Fields: fields}, synth.Options{Seed: 2})
	if err != nil {
		t.Fatalf("source SynthBytes: %v", err)
	}
	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "/source.pulse", srcData, 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	p, err := pulse.New(pulse.Options{FS: fs, DisablePanicRecovery: true})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	gen := []synth.FieldSpec{
		{Name: "x", Type: "f64", Distribution: synth.DistMonotonicFrom, Params: map[string]any{"start": 1.0}},
		fields[1],
	}
	spec := &synth.Spec{RowCount: 20, Fields: gen, Rules: []synth.RuleSpec{{SetExpr: map[string]string{"y": `lag("x", 1, -1)`}}}}
	if _, err := p.Synth(context.Background(), spec, "/out.pulse", pulse.SynthOptions{Seed: 2, SourceCohort: "/source.pulse"}); err != nil {
		t.Fatalf("augment synth: %v", err)
	}
	out, err := afero.ReadFile(fs, "/out.pulse")
	if err != nil {
		t.Fatalf("read out: %v", err)
	}
	synthetic, xs, ys := readF64Field(t, out, synth.SyntheticFieldName), readF64Field(t, out, "x"), readF64Field(t, out, "y")
	first := -1
	for i := range ys {
		if synthetic[i] == 0 {
			continue
		}
		if first < 0 {
			first = i
			if ys[i] != -1 {
				t.Fatalf("first generated row %d: lag(x, 1, -1) = %v, want the default -1 (a source row was read)", i, ys[i])
			}
			continue
		}
		if ys[i] != xs[i-1] {
			t.Fatalf("generated row %d: lag = %v, want the previous generated x %v", i, ys[i], xs[i-1])
		}
	}
	if first < 0 || len(ys)-first != 20 {
		t.Fatalf("generated partition starts at %d of %d rows, want 20 generated", first, len(ys))
	}
}

// TestSynthLag_VectorField: lag of a vector is its previous row's
// []float64 (indexable), and an expr number list is accepted as its
// default.
func TestSynthLag_VectorField(t *testing.T) {
	t.Parallel()

	spec := &synth.Spec{RowCount: 40, Fields: []synth.FieldSpec{
		{Name: "emb", Type: "vec_f64", Distribution: synth.DistNormal,
			Params: map[string]any{"mean": 0.0, "std": 1.0}, Vector: &synth.VectorSpec{Dim: 2}},
		{Name: "e1", Type: "f64", Distribution: synth.DistConstant, Params: map[string]any{"value": 0.0}},
		{Name: "d", Type: "f64", Distribution: synth.DistConstant, Params: map[string]any{"value": 0.0}},
	}, Rules: []synth.RuleSpec{{SetExpr: map[string]string{
		"e1": `emb[1]`,
		"d":  `lag("emb", 1, [0.5, 2])[1]`,
	}}}}
	data, _, err := synth.SynthBytes(spec, synth.Options{Seed: 6})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	e1, d := readF64Field(t, data, "e1"), readF64Field(t, data, "d")
	for i := range d {
		want := 2.0
		if i > 0 {
			want = e1[i-1]
		}
		if math.Float64bits(d[i]) != math.Float64bits(want) {
			t.Fatalf("row %d: lag(emb, 1)[1] = %v, want %v", i, d[i], want)
		}
	}
}
