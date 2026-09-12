package synth

import (
	"bytes"
	"math"
	"math/rand/v2"
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

// discreteFixture is the 0-10 NPS-shaped staircase every test below
// reuses: a left-skewed 11-level integer scale whose mass is nowhere
// near a normal's.
func discreteFixture() FieldSpec {
	return FieldSpec{
		Name:         "nps",
		Type:         "u4",
		Distribution: DistDiscrete,
		Params: map[string]any{
			"values":  []any{0.0, 1.0, 2.0, 3.0, 4.0, 5.0, 6.0, 7.0, 8.0, 9.0, 10.0},
			"weights": []any{1883.0, 1176.0, 1435.0, 1889.0, 2607.0, 4695.0, 4700.0, 6968.0, 9919.0, 9840.0, 21231.0},
		},
	}
}

func TestParseDiscreteLevels_MomentsAreExact(t *testing.T) {
	d, err := parseDiscreteLevels(discreteFixture())
	if err != nil {
		t.Fatalf("parseDiscreteLevels: %v", err)
	}
	// Exact moments computed independently from the same weights.
	vals := []float64{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	ws := []float64{1883, 1176, 1435, 1889, 2607, 4695, 4700, 6968, 9919, 9840, 21231}
	var total, mean float64
	for _, w := range ws {
		total += w
	}
	for i := range vals {
		mean += float64((ws[i] / total) * vals[i])
	}
	var variance float64
	for i := range vals {
		dv := vals[i] - mean
		variance += float64((ws[i] / total) * float64(dv*dv))
	}
	wantStd := math.Sqrt(variance)
	if math.Abs(d.mean-mean) > 1e-12 {
		t.Errorf("mean = %.15g, want %.15g", d.mean, mean)
	}
	if math.Abs(d.std-wantStd) > 1e-12 {
		t.Errorf("std = %.15g, want %.15g", d.std, wantStd)
	}
	if got := d.cum[len(d.cum)-1]; math.Abs(got-1) > 1e-15 {
		t.Errorf("cumulative weight terminates at %.17g, want 1", got)
	}
}

func TestParseDiscreteLevels_RefusesNonAscendingValues(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values []any
	}{
		{"descending", []any{3.0, 1.0, 2.0}},
		{"duplicate", []any{1.0, 1.0, 2.0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fs := FieldSpec{Name: "f", Distribution: DistDiscrete,
				Params: map[string]any{"values": tc.values}}
			if _, err := parseDiscreteLevels(fs); err == nil {
				t.Fatalf("parseDiscreteLevels accepted %v; a non-monotone support "+
					"makes Q non-monotone in p, which silently breaks the copula's "+
					"rank ordering and the model draw's ordering property", tc.values)
			}
		})
	}
}

// TestDiscreteQuantile_IsTheExactStaircase pins Q: the value whose
// cumulative band contains p, for p at and around every band boundary.
func TestDiscreteQuantile_IsTheExactStaircase(t *testing.T) {
	d, err := parseDiscreteLevels(discreteFixture())
	if err != nil {
		t.Fatalf("parseDiscreteLevels: %v", err)
	}
	for i := range d.values {
		lo := 0.0
		if i > 0 {
			lo = d.cum[i-1]
		}
		hi := d.cum[i]
		mid := lo + (hi-lo)/2
		if got := d.quantile(mid); got != d.values[i] {
			t.Errorf("Q(%.6f) = %v, want level %v (band [%.6f, %.6f])",
				mid, got, d.values[i], lo, hi)
		}
	}
	if got := d.quantile(0); got != d.values[0] {
		t.Errorf("Q(0) = %v, want the lowest level %v", got, d.values[0])
	}
	if got := d.quantile(1); got != d.values[len(d.values)-1] {
		t.Errorf("Q(1) = %v, want the highest level %v", got, d.values[len(d.values)-1])
	}
}

// TestDiscreteSampler_ReproducesEveryLevel is the sampler half of the
// three-writer coverage: the field's OWN draw.
//
// It asserts the PER-LEVEL distribution, not the mean. A clamped normal
// matched to the same mean and std reproduces the mean to three digits
// and gets every interior level wrong, which is exactly how the defect
// this distribution removes survived review.
func TestDiscreteSampler_ReproducesEveryLevel(t *testing.T) {
	fs := discreteFixture()
	smp, err := buildSampler(fs)
	if err != nil {
		t.Fatalf("buildSampler: %v", err)
	}
	d, err := parseDiscreteLevels(fs)
	if err != nil {
		t.Fatalf("parseDiscreteLevels: %v", err)
	}
	rng := newRng(7)
	const n = 200000
	got := map[float64]int{}
	for i := 0; i < n; i++ {
		v, _ := smp.next(rng)
		f, ok := v.(float64)
		if !ok {
			t.Fatalf("sampler emitted %T, want float64", v)
		}
		got[f]++
	}
	for i, level := range d.values {
		want := d.cum[i]
		if i > 0 {
			want -= d.cum[i-1]
		}
		have := float64(got[level]) / n
		if math.Abs(have-want) > 0.005 {
			t.Errorf("level %v: generated %.4f, declared %.4f", level, have, want)
		}
	}
	for v := range got {
		if v != math.Trunc(v) {
			t.Errorf("sampler emitted non-integer %v", v)
		}
	}
}

// TestDiscrete_ClampedNormalCannotDoThis is the measurement that
// justifies the whole arm: the SAME levels reconstructed the old way —
// normal(mean, std) clamped to [min, max] and rounded by the writer —
// against the staircase. Both match the mean; only one matches the
// levels.
func TestDiscrete_ClampedNormalCannotDoThis(t *testing.T) {
	fs := discreteFixture()
	d, err := parseDiscreteLevels(fs)
	if err != nil {
		t.Fatalf("parseDiscreteLevels: %v", err)
	}
	rng := rand.New(rand.NewPCG(1, 2))
	const n = 200000
	hist := map[float64]int{}
	for i := 0; i < n; i++ {
		v := d.mean + float64(d.std*rng.NormFloat64())
		if v < 0 {
			v = 0
		}
		if v > 10 {
			v = 10
		}
		hist[math.Floor(v+0.5)]++
	}
	var worst float64
	var worstLevel float64
	for i, level := range d.values {
		want := d.cum[i]
		if i > 0 {
			want -= d.cum[i-1]
		}
		have := float64(hist[level]) / n
		if e := math.Abs(have - want); e > worst {
			worst, worstLevel = e, level
		}
	}
	if worst < 0.05 {
		t.Fatalf("the clamped-normal reconstruction's worst per-level error is %.4f "+
			"(level %v) — under 0.05, so the fixture no longer demonstrates the defect "+
			"this distribution exists to remove; pick a less normal-looking scale", worst, worstLevel)
	}
	t.Logf("clamped-normal worst per-level error %.4f at level %v", worst, worstLevel)
}

// --- capture side -----------------------------------------------------

// discreteCaptureSchema builds a one-integer-column cohort schema of the
// given type, plus an f64 sibling that must NOT acquire a histogram.
func discreteCaptureSchema(ft encoding.FieldType) *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "scale", Type: ft},
		{Name: "amount", Type: encoding.FieldTypeF64},
	}}
}

// encodeDiscreteRows writes one record per level value supplied.
func encodeDiscreteRows(t *testing.T, schema *encoding.Schema, levels []int64) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	wfs := make([]*writerField, 0, len(schema.Fields))
	for i := range schema.Fields {
		wfs = append(wfs, &writerField{
			spec:  FieldSpec{Name: schema.Fields[i].Name},
			field: &schema.Fields[i],
		})
	}
	row := map[string]any{}
	mask := map[string]bool{}
	for _, lv := range levels {
		row["scale"] = float64(lv)
		row["amount"] = float64(lv) / 3
		if err := encodeRow(&buf, wfs, row, mask, schema.BitmapByteSize()); err != nil {
			t.Fatalf("encodeRow: %v", err)
		}
	}
	return buf.Bytes()
}

func capturedDiscrete(t *testing.T, ft encoding.FieldType, levels []int64) *Profile {
	t.Helper()
	schema := discreteCaptureSchema(ft)
	data := encodeDiscreteRows(t, schema, levels)
	prof, err := ProfileBytes(data, ProfileOptions{})
	if err != nil {
		t.Fatalf("ProfileBytes: %v", err)
	}
	return prof
}

// TestProfileDiscrete_CapturesTheExactHistogram pins the capture: every
// observed level, its exact count, and its share of the NON-NULL count.
func TestProfileDiscrete_CapturesTheExactHistogram(t *testing.T) {
	var rows []int64
	counts := map[int64]int{1: 40, 2: 10, 5: 50}
	for _, lv := range []int64{1, 2, 5} {
		for i := 0; i < counts[lv]; i++ {
			rows = append(rows, lv)
		}
	}
	prof := capturedDiscrete(t, encoding.FieldTypeU4, rows)

	scale := prof.Fields[0]
	if scale.Numeric == nil || scale.Numeric.Discrete == nil {
		t.Fatalf("u4 column carries no discrete histogram: %+v", scale.Numeric)
	}
	got := scale.Numeric.Discrete.Levels
	if len(got) != 3 {
		t.Fatalf("captured %d levels, want 3: %+v", len(got), got)
	}
	for i, want := range []DiscreteLevel{
		{Value: 1, Count: 40, Frequency: 0.40},
		{Value: 2, Count: 10, Frequency: 0.10},
		{Value: 5, Count: 50, Frequency: 0.50},
	} {
		if got[i].Value != want.Value || got[i].Count != want.Count ||
			math.Abs(got[i].Frequency-want.Frequency) > 1e-12 {
			t.Errorf("levels[%d] = %+v, want %+v", i, got[i], want)
		}
	}

	// The f64 sibling must NOT acquire one: the writer stores the float,
	// so a normal reconstruction round-trips and a histogram would be
	// reproducing sampling noise exactly.
	if amount := prof.Fields[1]; amount.Numeric != nil && amount.Numeric.Discrete != nil {
		t.Errorf("f64 column acquired a histogram (%d levels); only integer-quantized types "+
			"have a rounding problem to solve", len(amount.Numeric.Discrete.Levels))
	}
}

// TestProfileDiscrete_EveryIntegerTypeClaimsTheArm pins the type list
// against the one thing that justifies it: writeFieldValueForField's
// Floor(f+0.5) arms.
func TestProfileDiscrete_EveryIntegerTypeClaimsTheArm(t *testing.T) {
	for _, tc := range []struct {
		name string
		ft   encoding.FieldType
		want bool
	}{
		{"u4", encoding.FieldTypeU4, true},
		{"u8", encoding.FieldTypeU8, true},
		{"u16", encoding.FieldTypeU16, true},
		{"u32", encoding.FieldTypeU32, true},
		{"u64", encoding.FieldTypeU64, true},
		{"f32", encoding.FieldTypeF32, false},
		{"f64", encoding.FieldTypeF64, false},
		{"decimal128", encoding.FieldTypeDecimal128, false},
		{"packed_bool", encoding.FieldTypePackedBool, false},
		{"date", encoding.FieldTypeDate, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isIntegerQuantizedFieldType(tc.ft.String()); got != tc.want {
				t.Errorf("isIntegerQuantizedFieldType(%q) = %v, want %v", tc.ft.String(), got, tc.want)
			}
		})
	}
}

// TestProfileDiscrete_AbandonsAboveTheLevelCap is the boundary, asserted
// from BOTH sides in one test because either side alone is satisfiable by
// a capture that never captures anything (or one that never abandons).
//
// Above the cap the histogram is ABANDONED, not truncated: a top-64 view
// of a 200-level column would make every share a share of an arbitrary
// subset, and SpecFromProfile would reconstruct a scale the column does
// not have.
func TestProfileDiscrete_AbandonsAboveTheLevelCap(t *testing.T) {
	rowsFor := func(distinct int) []int64 {
		var out []int64
		for rep := 0; rep < 3; rep++ {
			for i := 0; i < distinct; i++ {
				out = append(out, int64(i))
			}
		}
		return out
	}

	atCap := capturedDiscrete(t, encoding.FieldTypeU16, rowsFor(maxDiscreteLevels))
	if d := atCap.Fields[0].Numeric.Discrete; d == nil {
		t.Errorf("a column with exactly %d distinct levels carries no histogram; the cap is "+
			"inclusive", maxDiscreteLevels)
	} else if len(d.Levels) != maxDiscreteLevels {
		t.Errorf("captured %d levels at the cap, want %d", len(d.Levels), maxDiscreteLevels)
	}

	overCap := capturedDiscrete(t, encoding.FieldTypeU16, rowsFor(maxDiscreteLevels+1))
	if d := overCap.Fields[0].Numeric.Discrete; d != nil {
		t.Errorf("a column with %d distinct levels carried a %d-level histogram; one level "+
			"over the cap must ABANDON, never truncate", maxDiscreteLevels+1, len(d.Levels))
	}
	// The rest of the numeric summary is untouched by the abandonment —
	// that is the fallback SpecFromProfile reconstructs from.
	if n := overCap.Fields[0].Numeric; n == nil || n.Std <= 0 {
		t.Errorf("the abandoned column lost its numeric summary too: %+v", n)
	}
}

// TestProfileDiscrete_RidesTheExistingScan is the "no second pass" gate,
// the same direct measurement TestProfileModels_RidesTheExistingScan
// makes: profileRecords consumes a one-shot io.Reader, so counting bytes
// is counting passes.
//
// Unlike the models fit there is no flag to compare against — the
// histogram is unconditional — so the comparison is against a cohort of
// the same size whose only integer column is an f64 instead, which takes
// the same bytes off the wire and builds no histogram.
func TestProfileDiscrete_RidesTheExistingScan(t *testing.T) {
	var levels []int64
	for i := 0; i < 600; i++ {
		levels = append(levels, int64(i%7))
	}
	withHist := discreteCaptureSchema(encoding.FieldTypeU64)
	noHist := &encoding.Schema{Fields: []encoding.Field{
		{Name: "scale", Type: encoding.FieldTypeF64},
		{Name: "amount", Type: encoding.FieldTypeF64},
	}}

	profA, err := ProfileBytes(encodeDiscreteRows(t, withHist, levels), ProfileOptions{})
	if err != nil {
		t.Fatalf("ProfileBytes(u64): %v", err)
	}
	if profA.Fields[0].Numeric.Discrete == nil {
		t.Fatal("fixture must capture a histogram for this test to mean anything")
	}

	// Both cohorts have identical on-wire record width (u64 and f64 are
	// both 8 bytes), so a second pass for the histogram would show up as
	// a byte-count difference.
	countBytes := func(schema *encoding.Schema) int64 {
		raw := encodeDiscreteRows(t, schema, levels)
		cr := &countingReader{r: bytes.NewReader(raw)}
		if err := encoding.ReadHeader(cr); err != nil {
			t.Fatalf("ReadHeader: %v", err)
		}
		sch, err := encoding.ReadSchema(cr)
		if err != nil {
			t.Fatalf("ReadSchema: %v", err)
		}
		if _, err := profileRecords(sch, cr, ProfileOptions{}); err != nil {
			t.Fatalf("profileRecords: %v", err)
		}
		return cr.n
	}
	if got, want := countBytes(withHist), countBytes(noHist); got != want {
		t.Errorf("capturing the histogram read %d bytes against %d for an identically sized "+
			"cohort without one — it must ride the existing scan", got, want)
	}
}

// TestBuildDiscreteConditionals_DerivesFromTheFieldNotAFlag pins the
// design call behind the conditional-pair arm: the target's staircase is
// resolved from its own FieldSpec, so a pair and the marginal it
// overwrites cannot disagree about what the field is.
func TestBuildDiscreteConditionals_DerivesFromTheFieldNotAFlag(t *testing.T) {
	specs := []FieldSpec{
		{Name: "nps", Type: "u4", Distribution: DistDiscrete, Params: map[string]any{
			"values": []any{1.0, 2.0, 3.0}, "weights": []any{1.0, 2.0, 1.0}}},
		{Name: "amount", Type: "f64", Distribution: DistNormal, Params: map[string]any{
			"mean": 1.0, "std": 1.0}},
		// A single-level support is a constant column: no scale to read a
		// cell's location against, so it is deliberately NOT an entry.
		{Name: "wave", Type: "u8", Distribution: DistDiscrete, Params: map[string]any{
			"values": []any{7.0}}},
	}
	wfs := make([]*writerField, 0, len(specs))
	for i := range specs {
		wfs = append(wfs, &writerField{spec: specs[i]})
	}
	got, err := buildDiscreteConditionals(wfs)
	if err != nil {
		t.Fatalf("buildDiscreteConditionals: %v", err)
	}
	if _, ok := got["nps"]; !ok {
		t.Error("nps (a multi-level discrete field) has no conditional entry")
	}
	if _, ok := got["amount"]; ok {
		t.Error("amount (normal) acquired a discrete conditional entry")
	}
	if _, ok := got["wave"]; ok {
		t.Error("wave (a single-level support) acquired an entry; there is no scale to " +
			"standardise a cell's location against")
	}

	// A spec declaring none allocates nothing, which is what keeps every
	// pre-existing pair's draw byte-identical.
	none, err := buildDiscreteConditionals(wfs[1:2])
	if err != nil {
		t.Fatalf("buildDiscreteConditionals(none): %v", err)
	}
	if none != nil {
		t.Errorf("a spec with no discrete field produced %v, want nil", none)
	}
}
