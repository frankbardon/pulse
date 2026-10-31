package synth

import (
	"bytes"
	stderrors "errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/limits"
	"github.com/spf13/afero"
)

// E3-S2 (FR-21): the fidelity report's disclosure section.

// disclosureEveryTypeSpec is one field per declarable type (renamed so
// they coexist), every scalar nullable, so the distance and the
// exact-match key see every column shape and the null rule.
func disclosureEveryTypeSpec(rows int) *Spec {
	reps := representativeFieldSpecs()
	names := make([]string, 0, len(reps))
	for n := range reps {
		names = append(names, n)
	}
	sort.Strings(names)
	spec := &Spec{RowCount: rows}
	for _, n := range names {
		f := reps[n]
		f.Name = "f_" + n
		if f.Vector == nil {
			f.Nullable, f.NullRate = true, 0.2
		}
		spec.Fields = append(spec.Fields, f)
	}
	return spec
}

// splitPulse returns data's schema and record bytes.
func splitPulse(t *testing.T, data []byte) (*encoding.Schema, []byte) {
	t.Helper()
	r := bytes.NewReader(data)
	v, err := encoding.ReadHeader(r)
	if err != nil {
		t.Fatal(err)
	}
	schema, err := encoding.ReadSchema(r, v)
	if err != nil {
		t.Fatal(err)
	}
	return schema, data[len(data)-r.Len():]
}

// disclosureCohort lays out a tagged cohort: every row of src tagged
// false, then the first copies rows of src tagged true (exact copies),
// then every row of fresh tagged true.
func disclosureCohort(t *testing.T, src, fresh []byte, copies int) (*encoding.Schema, []byte) {
	t.Helper()
	srcSchema, srcRecords := splitPulse(t, src)
	freshSchema, freshRecords := splitPulse(t, fresh)
	merged, err := buildMergedSchema(srcSchema, freshSchema)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if _, err := reencodeRecords(bytes.NewReader(srcRecords), srcSchema, merged, false, &buf); err != nil {
		t.Fatal(err)
	}
	stride := srcSchema.RecordByteSize()
	if _, err := reencodeRecords(bytes.NewReader(srcRecords[:copies*stride]), srcSchema, merged, true, &buf); err != nil {
		t.Fatal(err)
	}
	if _, err := reencodeRecords(bytes.NewReader(freshRecords), freshSchema, merged, true, &buf); err != nil {
		t.Fatal(err)
	}
	return merged, buf.Bytes()
}

func synthBytesT(t *testing.T, spec *Spec, seed int64) []byte {
	t.Helper()
	data, _, err := SynthBytes(spec, Options{Seed: seed})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	return data
}

// TestDisclosure_ExactCopiesReportMatchesAndZeroDistance is the
// acceptance bar: generated rows that are exact copies of source rows
// count as exact matches and sit at distance 0, across every field type;
// fresh draws match nothing and sit above 0.
func TestDisclosure_ExactCopiesReportMatchesAndZeroDistance(t *testing.T) {
	t.Parallel()
	src := synthBytesT(t, disclosureEveryTypeSpec(60), 1)
	fresh := synthBytesT(t, disclosureEveryTypeSpec(20), 2)

	schema, records := disclosureCohort(t, src, fresh, 5)
	d := computeDisclosure(schema, records, 7, DefaultDisclosureSample, 4)
	if d.Error != "" {
		t.Fatalf("Error = %q", d.Error)
	}
	if d.Columns != len(schema.Fields)-1 || d.SourceRows != 60 || d.SyntheticRows != 25 || d.SampleRows != 25 {
		t.Errorf("shape = %+v, want columns %d, 60 source, 25 synthetic, all sampled", d, len(schema.Fields)-1)
	}
	if d.ExactMatches != 5 {
		t.Errorf("ExactMatches = %d, want 5 (the copies; a fresh draw of every type matches nothing)", d.ExactMatches)
	}
	if want := 5.0 / 25.0; d.ExactMatchRate != want {
		t.Errorf("ExactMatchRate = %v, want %v", d.ExactMatchRate, want)
	}
	// 5 of 25 rows (20%) sit at 0, so the 5th percentile is 0 and the
	// median is a fresh row's distance.
	if d.DCRP5 != 0 {
		t.Errorf("DCRP5 = %v, want 0 with 20%% exact copies", d.DCRP5)
	}
	if !(d.DCRMedian > 0 && d.DCRMedian <= 1) {
		t.Errorf("DCRMedian = %v, want in (0, 1]", d.DCRMedian)
	}

	// Copies alone: every row matches and every distance is 0.
	allCopies, recs := disclosureCohortCopiesOnly(t, src, 10)
	d = computeDisclosure(allCopies, recs, 7, DefaultDisclosureSample, 1)
	if d.ExactMatches != 10 || d.ExactMatchRate != 1 || d.DCRMedian != 0 || d.DCRP5 != 0 {
		t.Errorf("copies only: %+v, want 10 matches, rate 1, DCR 0", d)
	}
}

// disclosureCohortCopiesOnly is src tagged false followed by its first
// copies rows tagged true.
func disclosureCohortCopiesOnly(t *testing.T, src []byte, copies int) (*encoding.Schema, []byte) {
	t.Helper()
	srcSchema, srcRecords := splitPulse(t, src)
	merged, err := buildMergedSchema(srcSchema, srcSchema)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if _, err := reencodeRecords(bytes.NewReader(srcRecords), srcSchema, merged, false, &buf); err != nil {
		t.Fatal(err)
	}
	stride := srcSchema.RecordByteSize()
	if _, err := reencodeRecords(bytes.NewReader(srcRecords[:copies*stride]), srcSchema, merged, true, &buf); err != nil {
		t.Fatal(err)
	}
	return merged, buf.Bytes()
}

// TestDisclosure_WorkerCountMovesNoBit: each sampled row's minimum is its
// own, so splitting the rows across workers leaves every figure's bits.
func TestDisclosure_WorkerCountMovesNoBit(t *testing.T) {
	t.Parallel()
	schema, records := disclosureCohort(t, synthBytesT(t, disclosureEveryTypeSpec(80), 11),
		synthBytesT(t, disclosureEveryTypeSpec(40), 12), 3)
	want := computeDisclosure(schema, records, 5, DefaultDisclosureSample, 1)
	for _, w := range []int{2, 3, 8, 64} {
		if got := computeDisclosure(schema, records, 5, DefaultDisclosureSample, w); !reflect.DeepEqual(got, want) ||
			math.Float64bits(got.DCRMedian) != math.Float64bits(want.DCRMedian) ||
			math.Float64bits(got.DCRP5) != math.Float64bits(want.DCRP5) {
			t.Errorf("workers=%d: %+v, want %+v", w, got, want)
		}
	}
}

// TestDisclosure_SampleIsSeededCappedAndDistinct: past the cap the DCR
// reads exactly cap distinct generated rows, chosen by the seed alone;
// the exact-match count still reads every generated row.
func TestDisclosure_SampleIsSeededCappedAndDistinct(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ n, k int }{{100, 7}, {10, 9}, {50, 1}, {1000, 999}} {
		a := disclosureSampleOrdinals(c.n, c.k, 3)
		if len(a) != c.k {
			t.Fatalf("n=%d k=%d: %d ordinals", c.n, c.k, len(a))
		}
		for i, o := range a {
			if o < 0 || o >= c.n || (i > 0 && o <= a[i-1]) {
				t.Fatalf("n=%d k=%d: ordinals %v not distinct, ascending, in range", c.n, c.k, a)
			}
		}
		if b := disclosureSampleOrdinals(c.n, c.k, 3); !reflect.DeepEqual(a, b) {
			t.Errorf("n=%d k=%d: same seed drew %v then %v", c.n, c.k, a, b)
		}
	}
	if a, b := disclosureSampleOrdinals(100, 7, 3), disclosureSampleOrdinals(100, 7, 4); reflect.DeepEqual(a, b) {
		t.Errorf("seeds 3 and 4 drew the same sample %v", a)
	}
	if got := disclosureSampleOrdinals(5, 10, 3); !reflect.DeepEqual(got, []int{0, 1, 2, 3, 4}) {
		t.Errorf("under the cap = %v, want every row", got)
	}

	schema, records := disclosureCohort(t, synthBytesT(t, disclosureEveryTypeSpec(30), 21),
		synthBytesT(t, disclosureEveryTypeSpec(40), 22), 4)
	d := computeDisclosure(schema, records, 9, 6, 2)
	if d.SampleRows != 6 || d.SyntheticRows != 44 || d.ExactMatches != 4 {
		t.Errorf("capped: %+v, want 6 sampled of 44, all 4 copies matched", d)
	}
}

// TestDisclosure_GowerTerms pins every per-column rule: the range
// normalisation and its clamp, the zero range, NaN, the Jaccard set
// distance, the 0/1 nominal mismatch, the vector mean and the null rule.
func TestDisclosure_GowerTerms(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		a, b, r, want float64
	}{
		{1, 3, 4, 0.5}, {3, 1, 4, 0.5}, {0, 10, 4, 1}, {2, 2, 0, 0}, {2, 3, 0, 1},
		{math.NaN(), math.NaN(), 1, 0}, {math.NaN(), 1, 1, 1}, {1, 2, math.Inf(1), 1},
	} {
		if got := numericDistance(c.a, c.b, c.r); got != c.want {
			t.Errorf("numericDistance(%v, %v, range %v) = %v, want %v", c.a, c.b, c.r, got, c.want)
		}
	}
	m := func(bits ...int) encoding.SetMask {
		var s encoding.SetMask
		for _, b := range bits {
			s = s.WithBit(b)
		}
		return s
	}
	for _, c := range []struct {
		a, b encoding.SetMask
		want float64
	}{{m(), m(), 0}, {m(0, 1), m(0, 1), 0}, {m(0, 1), m(1, 2), 2.0 / 3.0}, {m(0), m(200), 1}} {
		if got := jaccardDistance(c.a, c.b); got != c.want {
			t.Errorf("jaccardDistance = %v, want %v", got, c.want)
		}
	}

	// One row per side, four columns: f64 (range 4 from the two source
	// rows), categorical, set, vec dim 2 (element ranges 2 and 4).
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "x", Type: encoding.FieldTypeF64},
		{Name: "c", Type: encoding.FieldTypeCategoricalU8},
		{Name: "s", Type: encoding.FieldTypeSetU8},
		{Name: "v", Type: encoding.FieldTypeVecF64},
		{Name: SyntheticFieldName, Type: encoding.FieldTypePackedBool},
	}}
	plan := newDisclosurePlan(schema)
	plan.cols[3].width, plan.slots = 2, 4 // the vector's dim (no VECTORS section on a bare schema)
	src := &disclosureRows{
		vals:  []float64{0, 1, 0, 0, 4, 1, 2, 4},
		nulls: []bool{false, false, false, false, false, false, false, false},
		sets:  []encoding.SetMask{m(0), m(0)},
		n:     2,
	}
	plan.ranges = sourceRanges(plan, src)
	if want := []float64{4, 0, 2, 4}; !reflect.DeepEqual(plan.ranges, want) {
		t.Fatalf("ranges = %v, want %v", plan.ranges, want)
	}
	syn := &disclosureRows{
		// x=1 (0.25 from row 0), c=1 (match row 0), s={0,1} (0.5),
		// v=(1,0) (mean of 0.5 and 0 = 0.25 from row 0).
		vals:  []float64{1, 1, 1, 0},
		nulls: []bool{false, false, false, false},
		sets:  []encoding.SetMask{m(0, 1)},
		n:     1,
	}
	if got, want := closestDistance(plan, src, syn, 0), (0.25+0+0.5+0.25)/4; got != want {
		t.Errorf("closestDistance = %v, want %v", got, want)
	}
	// A null on one side scores 1, on both sides 0.
	syn.nulls = []bool{true, false, false, false}
	if got, want := closestDistance(plan, src, syn, 0), (1+0+0.5+0.25)/4; got != want {
		t.Errorf("one-sided null: closestDistance = %v, want %v", got, want)
	}
	src.nulls[0], src.nulls[4] = true, true
	if got, want := closestDistance(plan, src, syn, 0), (0+0+0.5+0.25)/4; got != want {
		t.Errorf("both null: closestDistance = %v, want %v", got, want)
	}
}

// TestDisclosure_Quantile pins the type-7 interpolation.
func TestDisclosure_Quantile(t *testing.T) {
	t.Parallel()
	x := []float64{0, 1, 2, 3}
	if got := disclosureQuantile(x, 0.5); got != 1.5 {
		t.Errorf("median = %v, want 1.5", got)
	}
	if got := disclosureQuantile(x, 0.05); math.Abs(got-0.15) > 1e-15 {
		t.Errorf("p5 = %v, want 0.15", got)
	}
	if got := disclosureQuantile([]float64{0.4}, 0.05); got != 0.4 {
		t.Errorf("single = %v, want 0.4", got)
	}
}

// TestDisclosure_EmptySourceIsAnErrorNotAFigure: with no source row the
// distance is undefined, so the section says why instead of a 0.
func TestDisclosure_EmptySourceIsAnErrorNotAFigure(t *testing.T) {
	t.Parallel()
	src := synthBytesT(t, disclosureEveryTypeSpec(5), 31)
	srcSchema, _ := splitPulse(t, src)
	merged, err := buildMergedSchema(srcSchema, srcSchema)
	if err != nil {
		t.Fatal(err)
	}
	d := computeDisclosure(merged, nil, 1, DefaultDisclosureSample, 1)
	if d.Error == "" || d.SourceRows != 0 {
		t.Errorf("empty cohort: %+v, want an Error", d)
	}
}

// TestDisclosure_BudgetChargesMaxEstimatedMemory: the pre-flight charges
// the estimate against MaxEstimatedMemory and refuses past it, reads
// nothing when the section is off, and refuses a negative sample.
func TestDisclosure_BudgetChargesMaxEstimatedMemory(t *testing.T) {
	t.Parallel()
	fs := afero.NewMemMapFs()
	src := synthBytesT(t, disclosureEveryTypeSpec(50), 41)
	if err := afero.WriteFile(fs, "/src.pulse", src, 0o644); err != nil {
		t.Fatal(err)
	}
	srcSchema, _ := splitPulse(t, src)
	spec := disclosureEveryTypeSpec(30)
	est := DisclosureMemoryEstimate(srcSchema, 50, 30, DefaultDisclosureSample)
	if est <= 0 {
		t.Fatalf("estimate = %d", est)
	}
	opts := Options{SourceCohort: "/src.pulse", FidelityReportPath: "/r.json"}
	at := limits.Limits{MaxEstimatedMemory: est}
	if err := CheckDisclosureBudget(fs, spec, opts, at); err != nil {
		t.Errorf("at the estimate: %v, want nil", err)
	}
	under := limits.Limits{MaxEstimatedMemory: est - 1}
	err := CheckDisclosureBudget(fs, spec, opts, under)
	if ce := codedErr(err); ce == nil || ce.Code != "PULSE_LIMIT_EXCEEDED" ||
		ce.Details["limit"] != string(limits.MaxEstimatedMemory) || ce.Details["observed"] != est {
		t.Errorf("one byte under: %v, want PULSE_LIMIT_EXCEEDED on max_estimated_memory observing %d", err, est)
	}
	off := opts
	off.DisableDisclosure = true
	if err := CheckDisclosureBudget(fs, spec, off, under); err != nil {
		t.Errorf("disabled: %v, want nil", err)
	}
	neg := opts
	neg.DisclosureSample = -1
	if ce := codedErr(CheckDisclosureBudget(fs, spec, neg, limits.Limits{})); ce == nil || ce.Code != "SERVICE_VALIDATION" {
		t.Errorf("negative sample: %v, want SERVICE_VALIDATION", ce)
	}
	// A smaller sample cap is a smaller charge.
	if small := DisclosureMemoryEstimate(srcSchema, 50, 30, 10); small >= est {
		t.Errorf("cap 10 estimate %d, want below the uncapped %d", small, est)
	}
}

// TestDisclosure_ReadsRecordsOnce sanity-checks the decoder against the
// plain reader: every row decodes and the tag splits them.
func TestDisclosure_ReadsRecordsOnce(t *testing.T) {
	t.Parallel()
	schema, records := disclosureCohort(t, synthBytesT(t, disclosureEveryTypeSpec(12), 51),
		synthBytesT(t, disclosureEveryTypeSpec(8), 52), 2)
	dec := newDisclosureDecoder(newDisclosurePlan(schema), records)
	var src, syn int
	for {
		s, ok := dec.next()
		if !ok {
			break
		}
		if s {
			syn++
		} else {
			src++
		}
	}
	rr := encx.NewRecordReader(bytes.NewReader(records), schema)
	n := 0
	for rr.ReadRecord(map[string]float64{}, map[string]bool{}) == nil {
		n++
	}
	if src != 12 || syn != 10 || n != 22 {
		t.Errorf("decoded %d source + %d synthetic of %d, want 12 + 10 of 22", src, syn, n)
	}
}

// codedErr unwraps err to its *errors.CodedError, nil when it is none.
func codedErr(err error) *errors.CodedError {
	var ce *errors.CodedError
	if stderrors.As(err, &ce) {
		return ce
	}
	return nil
}

// TestDisclosure_ExactKeyIsFaithfulPerType: for every field type alone,
// two rows share an exact-match key exactly when they decode to the same
// value (null equal to null) — the key reads the decimal's digits, the
// set's mask and the vector's elements, not a lossy echo.
func TestDisclosure_ExactKeyIsFaithfulPerType(t *testing.T) {
	t.Parallel()
	for name, f := range representativeFieldSpecs() {
		if f.Vector == nil {
			f.Nullable, f.NullRate = true, 0.2
		}
		data := synthBytesT(t, &Spec{RowCount: 40, Fields: []FieldSpec{f}}, 71)
		schema, records := splitPulse(t, data)
		dec := newDisclosureDecoder(newDisclosurePlan(schema), records)
		var keys, refs []string
		for {
			if _, ok := dec.next(); !ok {
				break
			}
			keys = append(keys, dec.rowKey())
			v, isNull := decodedFieldValue(&schema.Fields[0], dec.values, dec.nulls, dec.wide)
			ref := "null"
			if !isNull {
				ref = fmt.Sprintf("%T:%v", v, v)
				if d, ok := v.(encoding.Decimal128); ok {
					ref = d.String(schema.Fields[0].Scale)
				}
			}
			refs = append(refs, ref)
		}
		distinct := map[string]bool{}
		for i := range keys {
			distinct[refs[i]] = true
			for j := range keys {
				if (keys[i] == keys[j]) != (refs[i] == refs[j]) {
					t.Fatalf("%s: rows %d and %d: key equal %v, value equal %v (%s vs %s)",
						name, i, j, keys[i] == keys[j], refs[i] == refs[j], refs[i], refs[j])
				}
			}
		}
		if len(distinct) < 2 {
			t.Errorf("%s: fixture drew %d distinct values, want at least 2", name, len(distinct))
		}
	}
}
