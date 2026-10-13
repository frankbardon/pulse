package synth

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// vecRows decodes a cohort's schema and one vector field's cells in
// file order; a null vector is nil.
func vecRows(t *testing.T, data []byte, name string) (*encoding.Schema, [][]float64) {
	t.Helper()
	r := bytes.NewReader(data)
	schema, _, err := encx.ReadPreamble(r)
	if err != nil {
		t.Fatalf("read preamble: %v", err)
	}
	rr := encx.NewRecordReader(r, schema)
	values := map[string]float64{}
	nulls := map[string]bool{}
	wide := map[string]any{}
	var out [][]float64
	for {
		if err := rr.ReadRecordWithWide(values, nulls, wide); err != nil {
			break
		}
		if nulls[name] {
			out = append(out, nil)
			continue
		}
		v, ok := wide[name].([]float64)
		if !ok {
			t.Fatalf("field %q decoded as %T, want []float64", name, wide[name])
		}
		out = append(out, append([]float64(nil), v...))
	}
	return schema, out
}

func fptr(v float64) *float64 { return &v }

func vecSpec(rows int, f FieldSpec) *Spec {
	return &Spec{RowCount: rows, Fields: []FieldSpec{
		{Name: "id", Type: "u32", Distribution: DistMonotonicFrom, Params: map[string]any{"start": 1.0}},
		f,
	}}
}

// pearsonComplete is an independent two-pass Pearson over the rows where
// both elements are present — deliberately NOT coMoments, so a capture
// bug cannot be checked against itself.
func pearsonComplete(rows [][]float64, i, j int) float64 {
	var xs, ys []float64
	for _, v := range rows {
		if v == nil || math.IsNaN(v[i]) || math.IsNaN(v[j]) {
			continue
		}
		xs, ys = append(xs, v[i]), append(ys, v[j])
	}
	return pearson(xs, ys)
}

// TestSynthVector_FromSchemaHonoursKindBounds: every kind's bounds hold
// on every generated element, and the written schema's VECTORS entry is
// exactly the spec's (dim, kind, scale range, labels).
func TestSynthVector_FromSchemaHonoursKindBounds(t *testing.T) {
	cases := []struct {
		name  string
		field FieldSpec
		check func(t *testing.T, v []float64)
		after func(t *testing.T, rows [][]float64)
	}{
		{
			name: "scale integers in the declared range",
			field: FieldSpec{Name: "v", Type: "vec_f64", Distribution: DistDiscrete,
				Vector: &VectorSpec{Dim: 4, Kind: "scale", ScaleMin: fptr(1), ScaleMax: fptr(7),
					Labels: []string{"a", "b", "c", "d"}}},
			check: func(t *testing.T, v []float64) {
				for _, x := range v {
					if x != math.Trunc(x) || x < 1 || x > 7 {
						t.Fatalf("scale element %v is not an integer in [1, 7]", x)
					}
				}
			},
			after: func(t *testing.T, rows [][]float64) {
				seen := map[float64]bool{}
				for _, v := range rows {
					for _, x := range v {
						seen[x] = true
					}
				}
				if len(seen) != 7 {
					t.Fatalf("scale levels drawn %v, want all of 1..7", seen)
				}
			},
		},
		{
			name: "binary elements are 0 or 1 at the declared prevalence",
			field: FieldSpec{Name: "v", Type: "vec_f32", Distribution: DistBernoulli,
				Params: map[string]any{"p": 0.3}, Vector: &VectorSpec{Dim: 3, Kind: "binary"}},
			check: func(t *testing.T, v []float64) {
				for _, x := range v {
					if x != 0 && x != 1 {
						t.Fatalf("binary element %v", x)
					}
				}
			},
			after: func(t *testing.T, rows [][]float64) {
				ones, n := 0.0, 0.0
				for _, v := range rows {
					for _, x := range v {
						ones += x
						n++
					}
				}
				if p := ones / n; math.Abs(p-0.3) > 0.02 {
					t.Fatalf("binary prevalence %.4f, want 0.3", p)
				}
			},
		},
		{
			name: "composition rows are a simplex",
			field: FieldSpec{Name: "v", Type: "vec_f64", Distribution: DistExponential,
				Vector: &VectorSpec{Dim: 5, Kind: "composition"}},
			check: func(t *testing.T, v []float64) {
				sum := 0.0
				for _, x := range v {
					if x < 0 {
						t.Fatalf("composition element %v < 0", x)
					}
					sum += x
				}
				if math.Abs(sum-1) > 1e-12 {
					t.Fatalf("composition sums to %v", sum)
				}
			},
		},
		{
			name: "scale range clamps a continuous family",
			field: FieldSpec{Name: "v", Type: "vec_f64", Distribution: DistNormal,
				Params: map[string]any{"mean": 0.0, "std": 5.0},
				Vector: &VectorSpec{Dim: 2, Kind: "measure", ScaleMin: fptr(-1), ScaleMax: fptr(1)}},
			check: func(t *testing.T, v []float64) {
				for _, x := range v {
					if x < -1 || x > 1 {
						t.Fatalf("element %v escaped the scale range", x)
					}
				}
			},
		},
		{
			name: "kind-less vector keeps its family's mean",
			field: FieldSpec{Name: "v", Type: "vec_f64", Distribution: DistNormal,
				Params: map[string]any{"mean": 5.0, "std": 2.0}, Vector: &VectorSpec{Dim: 2}},
			after: func(t *testing.T, rows [][]float64) {
				sum, n := 0.0, 0.0
				for _, v := range rows {
					sum += v[0] + v[1]
					n += 2
				}
				if m := sum / n; math.Abs(m-5) > 0.1 {
					t.Fatalf("mean %.4f, want 5", m)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, _, err := SynthBytes(vecSpec(4000, tc.field), Options{Seed: 3})
			if err != nil {
				t.Fatalf("SynthBytes: %v", err)
			}
			schema, rows := vecRows(t, data, "v")
			if len(rows) != 4000 {
				t.Fatalf("rows %d", len(rows))
			}
			got, ok := schema.Vector(1)
			if !ok {
				t.Fatal("output schema carries no VECTORS entry for the vector field")
			}
			want := vectorFieldFor(tc.field, 1)
			gj, _ := json.Marshal(got)
			wj, _ := json.Marshal(want)
			if !bytes.Equal(gj, wj) {
				t.Fatalf("output vector metadata %s, want the spec's %s", gj, wj)
			}
			for _, v := range rows {
				if len(v) != tc.field.Vector.Dim {
					t.Fatalf("row dim %d", len(v))
				}
				if tc.check != nil {
					tc.check(t, v)
				}
			}
			if tc.after != nil {
				tc.after(t, rows)
			}
		})
	}
}

// TestSynthVector_ElementCorrelationIsInduced: declared inter-element
// correlations come back within tolerance, an undeclared pair comes back
// near zero, and the completion is reported.
func TestSynthVector_ElementCorrelationIsInduced(t *testing.T) {
	f := FieldSpec{Name: "v", Type: "vec_f64", Distribution: DistNormal,
		Params: map[string]any{"mean": 0.0, "std": 1.0},
		Vector: &VectorSpec{Dim: 3, Correlations: []VectorCorrelationSpec{
			{A: 1, B: 2, Correlation: 0.8},
			{A: 3, B: 2, Correlation: -0.5},
		}}}
	data, res, err := SynthBytes(vecSpec(20000, f), Options{Seed: 9})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	_, rows := vecRows(t, data, "v")
	if r := pearsonComplete(rows, 0, 1); math.Abs(r-0.8) > 0.02 {
		t.Fatalf("rho(1,2) = %.4f, want 0.8", r)
	}
	if r := pearsonComplete(rows, 1, 2); math.Abs(r+0.5) > 0.02 {
		t.Fatalf("rho(2,3) = %.4f, want -0.5", r)
	}
	if r := pearsonComplete(rows, 0, 2); math.Abs(r) > 0.03 {
		t.Fatalf("undeclared rho(1,3) = %.4f, want ~0", r)
	}
	if !strings.Contains(strings.Join(res.Warnings, "\n"), "completed by assumption: 1 of 3 pair") {
		t.Fatalf("the completed pair was not reported: %v", res.Warnings)
	}
}

// TestSynthVector_NullVectorAndNaNElementRates: the null-vector rate and
// the per-element NaN rate are distinct and both honoured.
func TestSynthVector_NullVectorAndNaNElementRates(t *testing.T) {
	f := FieldSpec{Name: "v", Type: "vec_f64", Nullable: true, NullRate: 0.2,
		Distribution: DistUniform, Vector: &VectorSpec{Dim: 2, NaNRate: 0.1,
			Elements: []VectorElementSpec{{}, {NaNRate: fptr(0.5)}}}}
	data, _, err := SynthBytes(vecSpec(10000, f), Options{Seed: 5})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	_, rows := vecRows(t, data, "v")
	nulls, nan0, nan1, present := 0, 0, 0, 0
	for _, v := range rows {
		if v == nil {
			nulls++
			continue
		}
		present++
		if math.IsNaN(v[0]) {
			nan0++
		}
		if math.IsNaN(v[1]) {
			nan1++
		}
	}
	if r := float64(nulls) / float64(len(rows)); math.Abs(r-0.2) > 0.02 {
		t.Fatalf("null-vector rate %.4f, want 0.2", r)
	}
	if r := float64(nan0) / float64(present); math.Abs(r-0.1) > 0.02 {
		t.Fatalf("element 1 NaN rate %.4f, want 0.1", r)
	}
	if r := float64(nan1) / float64(present); math.Abs(r-0.5) > 0.02 {
		t.Fatalf("element 2 NaN rate %.4f, want the override 0.5", r)
	}
}

// TestSynthVector_F32ElementsAreStoredPrecision: a vec_f32 row value is
// already float32-rounded (so a constraint sees the stored number), and
// the stride is 4 bytes per element.
func TestSynthVector_F32ElementsAreStoredPrecision(t *testing.T) {
	f := FieldSpec{Name: "v", Type: "vec_f32", Distribution: DistNormal,
		Params: map[string]any{"mean": 0.1, "std": 1.0}, Vector: &VectorSpec{Dim: 4}}
	smp, err := newVectorSampler(f)
	if err != nil {
		t.Fatal(err)
	}
	rng := newRng(1)
	for i := 0; i < 200; i++ {
		v, _ := smp.next(rng)
		for _, x := range v.([]float64) {
			if x != float64(float32(x)) {
				t.Fatalf("vec_f32 row element %v is not float32-representable", x)
			}
		}
	}
	data, _, err := SynthBytes(vecSpec(10, f), Options{Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	schema, _ := vecRows(t, data, "v")
	if w := schema.FieldWidth(1); w != 16 {
		t.Fatalf("vec_f32 dim 4 width %d, want 16", w)
	}
}

// TestSynthVector_DeterministicUnderSeed: same spec + seed is
// byte-identical; another seed is not.
func TestSynthVector_DeterministicUnderSeed(t *testing.T) {
	f := FieldSpec{Name: "v", Type: "vec_f64", Nullable: true, NullRate: 0.1, Distribution: DistNormal,
		Vector: &VectorSpec{Dim: 3, NaNRate: 0.05, Correlations: []VectorCorrelationSpec{{A: 1, B: 3, Correlation: 0.4}}}}
	a, _, err := SynthBytes(vecSpec(500, f), Options{Seed: 7})
	if err != nil {
		t.Fatal(err)
	}
	b, _, _ := SynthBytes(vecSpec(500, f), Options{Seed: 7})
	c, _, _ := SynthBytes(vecSpec(500, f), Options{Seed: 8})
	if !bytes.Equal(a, b) {
		t.Fatal("same spec + seed produced different bytes")
	}
	if bytes.Equal(a, c) {
		t.Fatal("a different seed produced identical bytes")
	}
}

// vectorSourceCohort is the integer-exact capture fixture: a 1-7 scale
// battery whose elements are correlated, generated through the
// from-schema path (so its bytes are integers whatever the architecture).
func vectorSourceCohort(t *testing.T, rows int) []byte {
	t.Helper()
	f := FieldSpec{Name: "v", Type: "vec_f32", Nullable: true, NullRate: 0.1, Distribution: DistDiscrete,
		Params: map[string]any{"weights": []any{1.0, 2.0, 4.0, 6.0, 4.0, 2.0, 1.0}},
		Vector: &VectorSpec{Dim: 4, Kind: "scale", ScaleMin: fptr(1), ScaleMax: fptr(7),
			Labels: []string{"q1", "q2", "q3", "q4"},
			Elements: []VectorElementSpec{{}, {}, {NaNRate: fptr(0.2)},
				{Distribution: DistDiscrete, Params: map[string]any{"values": []any{1.0, 4.0, 7.0}, "weights": []any{5.0, 1.0, 1.0}}}},
			Correlations: []VectorCorrelationSpec{{A: 1, B: 2, Correlation: 0.7}, {A: 1, B: 3, Correlation: 0.5}, {A: 2, B: 3, Correlation: 0.4}}}}
	data, _, err := SynthBytes(vecSpec(rows, f), Options{Seed: 21})
	if err != nil {
		t.Fatalf("source SynthBytes: %v", err)
	}
	return data
}

// levelShares is each element's per-level share over present values.
func levelShares(rows [][]float64, k int) map[float64]float64 {
	counts, n := map[float64]float64{}, 0.0
	for _, v := range rows {
		if v == nil || math.IsNaN(v[k]) {
			continue
		}
		counts[v[k]]++
		n++
	}
	for l := range counts {
		counts[l] /= n
	}
	return counts
}

// TestProfileVector_CaptureThenRegenerateReproducesStructure is the
// round trip: profile create captures the per-element histogram, the
// NaN and null-vector rates and the element correlation; from-profile
// reproduces the marginals per level and the correlation within
// tolerance.
func TestProfileVector_CaptureThenRegenerateReproducesStructure(t *testing.T) {
	src := vectorSourceCohort(t, 20000)
	_, srcRows := vecRows(t, src, "v")

	prof, err := ProfileBytes(src, ProfileOptions{})
	if err != nil {
		t.Fatalf("ProfileBytes: %v", err)
	}
	fp := prof.Fields[1]
	if fp.Vector == nil || fp.Numeric != nil {
		t.Fatalf("vector field captured as %+v", fp)
	}
	vp := fp.Vector
	if vp.Dim != 4 || vp.Kind != "scale" || *vp.ScaleMin != 1 || *vp.ScaleMax != 7 || len(vp.Labels) != 4 {
		t.Fatalf("vector metadata not captured: %+v", vp)
	}
	if math.Abs(fp.NullRate-0.1) > 0.01 {
		t.Fatalf("null-vector rate %.4f", fp.NullRate)
	}
	if math.Abs(vp.Elements[2].NaNRate-0.2) > 0.015 {
		t.Fatalf("element 3 NaN rate %.4f", vp.Elements[2].NaNRate)
	}
	// The histogram is exact: compare against an independent count.
	for k := 0; k < 4; k++ {
		d := vp.Elements[k].Numeric.Discrete
		if d == nil {
			t.Fatalf("element %d: an all-integer element captured no histogram", k+1)
		}
		want := levelShares(srcRows, k)
		if len(d.Levels) != len(want) {
			t.Fatalf("element %d: %d levels captured, %d observed", k+1, len(d.Levels), len(want))
		}
		for _, lv := range d.Levels {
			if math.Abs(lv.Frequency-want[lv.Value]) > 1e-12 {
				t.Fatalf("element %d level %v: captured %.6f, observed %.6f", k+1, lv.Value, lv.Frequency, want[lv.Value])
			}
		}
	}
	if len(vp.Correlations) != 6 {
		t.Fatalf("captured %d element pairs, want all 6", len(vp.Correlations))
	}
	for _, c := range vp.Correlations {
		if want := pearsonComplete(srcRows, c.A-1, c.B-1); math.Abs(c.Rho-want) > 1e-9 {
			t.Fatalf("pair (%d,%d) captured %.6f, observed %.6f", c.A, c.B, c.Rho, want)
		}
	}

	spec, warnings := SpecFromProfile(prof, 20000)
	if len(warnings) != 0 {
		t.Fatalf("translation warnings: %v", warnings)
	}
	out, _, err := SynthBytes(spec, Options{Seed: 4})
	if err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	schema, genRows := vecRows(t, out, "v")
	if got, _ := schema.Vector(1); got.Kind != encoding.VectorKindScale || got.Dim != 4 || got.Labels[3] != "q4" {
		t.Fatalf("regenerated vector metadata %+v", got)
	}
	for k := 0; k < 4; k++ {
		want, got := levelShares(srcRows, k), levelShares(genRows, k)
		for l, w := range want {
			if math.Abs(got[l]-w) > 0.015 {
				t.Fatalf("element %d level %v: source %.4f, regenerated %.4f", k+1, l, w, got[l])
			}
		}
		for l := range got {
			if _, ok := want[l]; !ok {
				t.Fatalf("element %d regenerated an unobserved level %v", k+1, l)
			}
		}
	}
	for _, c := range vp.Correlations {
		if r := pearsonComplete(genRows, c.A-1, c.B-1); math.Abs(r-c.Rho) > 0.06 {
			t.Fatalf("pair (%d,%d): source %.4f, regenerated %.4f", c.A, c.B, c.Rho, r)
		}
	}
}

// TestProfileVector_WideVectorAbandonsCorrelation: above
// maxVectorCorrelationDim the correlation is abandoned with a warning,
// never truncated, and the elements are still captured.
func TestProfileVector_WideVectorAbandonsCorrelation(t *testing.T) {
	f := FieldSpec{Name: "v", Type: "vec_f64", Distribution: DistNormal,
		Vector: &VectorSpec{Dim: maxVectorCorrelationDim + 1}}
	src, _, err := SynthBytes(vecSpec(200, f), Options{Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	prof, err := ProfileBytes(src, ProfileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	vp := prof.Fields[1].Vector
	if len(vp.Correlations) != 0 || len(vp.Elements) != maxVectorCorrelationDim+1 {
		t.Fatalf("wide vector: %d correlations, %d elements", len(vp.Correlations), len(vp.Elements))
	}
	if !strings.Contains(strings.Join(prof.Warnings, "\n"), "abandoned, never truncated") {
		t.Fatalf("abandonment not reported: %v", prof.Warnings)
	}
}

// TestProfileVector_AllNullVectorRegeneratesNull: an all-null vector
// column keeps its dim in the profile and regenerates as null.
func TestProfileVector_AllNullVectorRegeneratesNull(t *testing.T) {
	f := FieldSpec{Name: "v", Type: "vec_f64", Nullable: true, NullRate: 1, Distribution: DistNormal,
		Vector: &VectorSpec{Dim: 3, Kind: "measure"}}
	src, _, err := SynthBytes(vecSpec(50, f), Options{Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	prof, err := ProfileBytes(src, ProfileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if vp := prof.Fields[1].Vector; vp == nil || vp.Dim != 3 || vp.N != 0 || prof.Fields[1].NullRate != 1 {
		t.Fatalf("all-null capture %+v", prof.Fields[1])
	}
	spec, _ := SpecFromProfile(prof, 20)
	out, _, err := SynthBytes(spec, Options{Seed: 1})
	if err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	_, rows := vecRows(t, out, "v")
	for _, v := range rows {
		if v != nil {
			t.Fatalf("an all-null vector regenerated a value %v", v)
		}
	}
}

// TestSpecVector_EmitSpecRoundTrip: the profile-derived vector spec
// survives a JSON round trip (`--emit-spec`) to a byte-identical cohort.
func TestSpecVector_EmitSpecRoundTrip(t *testing.T) {
	prof, err := ProfileBytes(vectorSourceCohort(t, 2000), ProfileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	derived, _ := SpecFromProfile(prof, 300)
	raw, err := json.Marshal(derived)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseSpec(raw)
	if err != nil {
		t.Fatalf("the emitted vector spec does not parse: %v", err)
	}
	a, _, err := SynthBytes(derived, Options{Seed: 2})
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := SynthBytes(parsed, Options{Seed: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("emitted vector spec generates a different cohort")
	}
}

// TestAugmentVector_TopUpAndFidelity: a tagged top-up over a vector
// source copies the source vectors verbatim, appends generated ones, and
// the fidelity report's vector arm compares elements and correlation.
func TestAugmentVector_TopUpAndFidelity(t *testing.T) {
	src := vectorSourceCohort(t, 6000)
	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "src.pulse", src, 0o644); err != nil {
		t.Fatal(err)
	}
	prof, err := ProfileBytes(src, ProfileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := SpecFromProfile(prof, 6000)
	res, err := AugmentFromProfile(fs, spec, "src.pulse", "out.pulse", Options{Seed: 3})
	if err != nil {
		t.Fatalf("AugmentFromProfile: %v", err)
	}
	if res.RowsGenerated != 6000 {
		t.Fatalf("generated %d", res.RowsGenerated)
	}
	out, _ := afero.ReadFile(fs, "out.pulse")
	schema, rows := vecRows(t, out, "v")
	_, srcRows := vecRows(t, src, "v")
	if len(rows) != 12000 {
		t.Fatalf("output rows %d", len(rows))
	}
	for i, v := range srcRows {
		if (v == nil) != (rows[i] == nil) {
			t.Fatalf("source row %d null state changed", i)
		}
		for k := range v {
			if v[k] != rows[i][k] && !(math.IsNaN(v[k]) && math.IsNaN(rows[i][k])) {
				t.Fatalf("source row %d element %d changed: %v -> %v", i, k+1, v[k], rows[i][k])
			}
		}
	}
	if meta, ok := schema.Vector(1); !ok || meta.Kind != encoding.VectorKindScale || meta.Dim != 4 {
		t.Fatalf("merged vector metadata %+v", meta)
	}

	recordsStart := len(out) - len(rows)*schema.RecordByteSize()
	stub := func(_, _ *encoding.Schema, _ []byte, _ *types.Test) (*types.TestResult, error) {
		return &types.TestResult{}, nil
	}
	report := BuildFidelityReport(schema, out[recordsStart:], 6000, 6000, stub)
	if len(report.VectorFields) != 1 {
		t.Fatalf("fidelity vector section %+v", report.VectorFields)
	}
	vf := report.VectorFields[0]
	if vf.SourceN == 0 || vf.SyntheticN == 0 || len(vf.Elements) != 4 || vf.CorrelationPairs != 6 {
		t.Fatalf("vector fidelity %+v", vf)
	}
	for _, el := range vf.Elements {
		if el.Error != "" || el.MeanDelta > 0.1 || el.StdDelta > 0.1 || math.Abs(el.SourceNaNRate-el.SyntheticNaNRate) > 0.03 {
			t.Fatalf("element fidelity %+v", el)
		}
	}
	if vf.MaxCorrelationDelta > 0.08 || len(vf.MaxCorrelationPair) != 2 {
		t.Fatalf("correlation fidelity: max delta %.4f at %v", vf.MaxCorrelationDelta, vf.MaxCorrelationPair)
	}
}

// TestAugmentVector_DimMismatchRefused: a spec whose vector dim differs
// from the source's is a schema mismatch, never a misaligned re-encode.
func TestAugmentVector_DimMismatchRefused(t *testing.T) {
	src := vectorSourceCohort(t, 50)
	fs := afero.NewMemMapFs()
	_ = afero.WriteFile(fs, "src.pulse", src, 0o644)
	prof, _ := ProfileBytes(src, ProfileOptions{})
	spec, _ := SpecFromProfile(prof, 10)
	spec.Fields[1].Vector.Dim = 3
	spec.Fields[1].Vector.Labels = nil
	spec.Fields[1].Vector.Elements = spec.Fields[1].Vector.Elements[:3]
	spec.Fields[1].Vector.Correlations = nil
	_, err := AugmentFromProfile(fs, spec, "src.pulse", "out.pulse", Options{})
	if !errors.HasCode(err, errors.PULSE_SYNTH_PROFILE_SCHEMA_MISMATCH) {
		t.Fatalf("dim mismatch: %v", err)
	}
}

// TestSpecVector_Refusals: every malformed vector declaration, and every
// cross-field use of a vector, is refused at spec validation.
func TestSpecVector_Refusals(t *testing.T) {
	good := func() FieldSpec {
		return FieldSpec{Name: "v", Type: "vec_f64", Distribution: DistNormal, Vector: &VectorSpec{Dim: 3}}
	}
	cases := map[string]func(s *Spec){
		"missing vector block":  func(s *Spec) { s.Fields[1].Vector = nil },
		"dim below range":       func(s *Spec) { s.Fields[1].Vector.Dim = 1 },
		"dim above range":       func(s *Spec) { s.Fields[1].Vector.Dim = 1025 },
		"unknown kind":          func(s *Spec) { s.Fields[1].Vector.Kind = "ratio" },
		"half a scale range":    func(s *Spec) { s.Fields[1].Vector.ScaleMin = fptr(0) },
		"inverted scale range":  func(s *Spec) { s.Fields[1].Vector.ScaleMin, s.Fields[1].Vector.ScaleMax = fptr(2), fptr(1) },
		"label count":           func(s *Spec) { s.Fields[1].Vector.Labels = []string{"a"} },
		"element count":         func(s *Spec) { s.Fields[1].Vector.Elements = []VectorElementSpec{{}} },
		"nan rate":              func(s *Spec) { s.Fields[1].Vector.NaNRate = 1.5 },
		"no distribution":       func(s *Spec) { s.Fields[1].Distribution = "" },
		"correlation index":     func(s *Spec) { s.Fields[1].Vector.Correlations = []VectorCorrelationSpec{{A: 0, B: 2}} },
		"self correlation":      func(s *Spec) { s.Fields[1].Vector.Correlations = []VectorCorrelationSpec{{A: 2, B: 2}} },
		"correlation range":     func(s *Spec) { s.Fields[1].Vector.Correlations = []VectorCorrelationSpec{{A: 1, B: 2, Correlation: 2}} },
		"duplicate correlation": func(s *Spec) { s.Fields[1].Vector.Correlations = []VectorCorrelationSpec{{A: 1, B: 2}, {A: 2, B: 1}} },
		"vector block on a scalar": func(s *Spec) {
			s.Fields[0].Vector = &VectorSpec{Dim: 2}
		},
		"vector in a field correlation": func(s *Spec) {
			s.Fields = append(s.Fields, FieldSpec{Name: "x", Type: "f64", Distribution: DistNormal})
			s.Correlations = []CorrelationSpec{{A: "v", B: "x", Correlation: 0.5}}
		},
		"vector as a model target": func(s *Spec) {
			s.Models = []FieldModelSpec{{Field: "v"}}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			s := vecSpec(5, good())
			mutate(s)
			err := validateSpec(s)
			if !errors.HasCode(err, errors.SERVICE_VALIDATION) {
				t.Fatalf("want SERVICE_VALIDATION, got %v", err)
			}
		})
	}
	// An element family the copula cannot drive is refused at build,
	// naming the element.
	bad := good()
	bad.Vector.Elements = []VectorElementSpec{{}, {Distribution: DistPoisson}, {}}
	_, _, err := SynthBytes(vecSpec(5, bad), Options{})
	if err == nil || !strings.Contains(err.Error(), "element 2") {
		t.Fatalf("a poisson element: %v", err)
	}
}

// TestConstraintsVector_IndexesTheRowSlice: a constraint reads a vector
// as its []float64, indexed 0-based by expr.
func TestConstraintsVector_IndexesTheRowSlice(t *testing.T) {
	f := FieldSpec{Name: "v", Type: "vec_f64", Distribution: DistUniform, Vector: &VectorSpec{Dim: 2}}
	s := vecSpec(300, f)
	s.Constraints = []ConstraintSpec{{Expr: "v[0] > 0.3"}}
	data, _, err := SynthBytes(s, Options{Seed: 1})
	if err != nil {
		t.Fatalf("a constraint over a vector element: %v", err)
	}
	_, rows := vecRows(t, data, "v")
	for _, v := range rows {
		if v[0] <= 0.3 {
			t.Fatalf("constraint not applied: element 1 = %v", v[0])
		}
	}
}

// TestRulesVector_SetNullMasksAndSetIsRefused: a rule can null a vector
// (set_null writes no value) but cannot assign one.
func TestRulesVector_SetNullMasksAndSetIsRefused(t *testing.T) {
	f := FieldSpec{Name: "v", Type: "vec_f64", Nullable: true, Distribution: DistNormal, Vector: &VectorSpec{Dim: 2}}
	s := vecSpec(200, f)
	s.Rules = []RuleSpec{{When: "id > 100", SetNull: []string{"v"}}}
	data, _, err := SynthBytes(s, Options{Seed: 1})
	if err != nil {
		t.Fatalf("set_null over a vector: %v", err)
	}
	_, rows := vecRows(t, data, "v")
	for i, v := range rows {
		if i+1 > 100 && v != nil {
			t.Fatalf("row id %d not nulled", i+1)
		}
		if i+1 <= 100 && v == nil {
			t.Fatalf("row id %d nulled without a gate", i+1)
		}
	}
	s.Rules = []RuleSpec{{Set: map[string]any{"v": []any{1.0, 2.0}}}}
	if err := validateSpec(s); !errors.HasCode(err, errors.PULSE_SYNTH_RULE_VALUE_INVALID) {
		t.Fatalf("set onto a vector: %v", err)
	}
}
