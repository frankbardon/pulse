package encoding

import (
	"bytes"
	"io"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

// constantRows returns n rows of the big mixed schema in which the named
// fields hold records[0]'s bytes and null bit on every row, and every
// other field varies (a random donor record per row).
func constantRows(t *testing.T, n int, constant ...string) (*encoding.Schema, [][]byte) {
	t.Helper()
	s := bigMixedSchema()
	recs := generateBigSchemaRecords(t, s, 0xC0457)
	specs := []GroupSpec{{Kind: encoding.GroupKindConstant, Members: constant}}
	return s, parentChildRows(t, s, recs, specs, []int{1}, n, 0xC0458)
}

func detect(t *testing.T, s *encoding.Schema, rows [][]byte) *ConstantDetector {
	t.Helper()
	d, err := NewConstantDetector(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if err := d.Observe(r); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

func names(s *encoding.Schema, idx []int) []string {
	out := []string{}
	for _, i := range idx {
		out = append(out, s.Fields[i].Name)
	}
	return out
}

// setNull sets or clears field fi's null bit in a logical row.
func setNull(s *encoding.Schema, row []byte, fi int, null bool) {
	body := s.RecordByteSize() - s.BitmapByteSize()
	row[body+fi/8] &^= 1 << uint(fi%8)
	if null {
		encoding.BitmapSetNull(row[body:], fi)
	}
}

// TestConstantDetector_FullPass: the detector reports exactly the
// fields constant over EVERY row — including one that is constant for
// the first 650 rows (longer than the default 500-row inference sample)
// and changes after it.
func TestConstantDetector_FullPass(t *testing.T) {
	want := []string{"u16_a", "pb_1", "cat_u16", "amount", "tags_u64", "u8_c"}
	s, rows := constantRows(t, 700, append(want, "u32_b")...)
	// u32_b is constant for 650 rows, then varies.
	span := fieldSpans(s)[fieldIdx(t, s, "u32_b")[0]]
	rows[680][span[0]] ^= 0xFF

	d := detect(t, s, rows)
	if d.Rows() != 700 {
		t.Fatalf("Rows = %d, want 700", d.Rows())
	}
	if got := names(s, d.ConstantFields()); !reflect.DeepEqual(got, want) {
		t.Fatalf("ConstantFields = %v, want %v", got, want)
	}

	// The same rows cut at the sample would have (wrongly) called u32_b
	// constant — which is exactly why detection must see the full pass.
	sample := detect(t, s, rows[:500])
	if got := names(s, sample.ConstantFields()); !reflect.DeepEqual(got, append(append([]string{}, want[:5]...), "u32_b", "u8_c")) {
		t.Fatalf("sample-only ConstantFields = %v", got)
	}
}

// TestConstantDetector_NullSemantics: null-constant and value-constant
// are distinct; a value constant except for some nulls, or a null
// except for one value, is NOT constant.
func TestConstantDetector_NullSemantics(t *testing.T) {
	s, rows := constantRows(t, 60, "u8_b", "u32_a", "f64_a", "tags_u16")
	u8b, u32a, f64a, tags16 := fieldIdx(t, s, "u8_b")[0], fieldIdx(t, s, "u32_a")[0], fieldIdx(t, s, "f64_a")[0], fieldIdx(t, s, "tags_u16")[0]
	for _, r := range rows {
		setNull(s, r, u8b, true)    // null on every row: a null constant
		setNull(s, r, u32a, false)  // a value on every row: a value constant
		setNull(s, r, f64a, false)  // a value on every row but one (below)
		setNull(s, r, tags16, true) // null on every row but one (below)
	}
	setNull(s, rows[41], f64a, true)
	setNull(s, rows[17], tags16, false)

	d := detect(t, s, rows)
	got := names(s, d.ConstantFields())
	if !reflect.DeepEqual(got, []string{"u8_b", "u32_a"}) {
		t.Fatalf("ConstantFields = %v, want [u8_b u32_a]", got)
	}
	if !d.IsNullConstant(u8b) || d.IsNullConstant(u32a) {
		t.Fatalf("IsNullConstant(u8_b)=%v IsNullConstant(u32_a)=%v; want true,false", d.IsNullConstant(u8b), d.IsNullConstant(u32a))
	}
}

// TestPlanConstantElision_Rules pins the plan's defined behaviour at the
// edges: zero and one row elide nothing, an all-constant cohort keeps
// its lowest-index field in the row, a cohort too small to repay the
// descriptor elides nothing, and reserved fields are never elided.
func TestPlanConstantElision_Rules(t *testing.T) {
	s, rows := constantRows(t, 400, "u16_a", "u8_c", "cat_u16")

	for _, n := range []int{0, 1} {
		p, err := PlanConstantElision(detect(t, s, rows[:n]), nil)
		if err != nil || p.Spec != nil || p.Skipped != "too_few_rows" {
			t.Fatalf("%d rows: plan %+v err %v; want too_few_rows", n, p, err)
		}
	}

	// Every row identical: every field is constant.
	same := make([][]byte, 300)
	for i := range same {
		same[i] = rows[0]
	}
	p, err := PlanConstantElision(detect(t, s, same), nil)
	if err != nil || p.Spec == nil {
		t.Fatalf("all-constant: plan %+v err %v", p, err)
	}
	if p.Retained != s.Fields[0].Name || len(p.Fields) != len(s.Fields)-1 || p.Fields[0] != s.Fields[1].Name {
		t.Fatalf("all-constant: retained %q, %d elided (first %q); want %q kept and the rest elided", p.Retained, len(p.Fields), p.Fields[0], s.Fields[0].Name)
	}

	// Two rows of 5 constant bytes cannot repay the descriptor.
	p, err = PlanConstantElision(detect(t, s, rows[:2]), nil)
	if err != nil || p.Spec != nil || p.Skipped != "not_smaller" {
		t.Fatalf("2 rows: plan %+v err %v; want not_smaller", p, err)
	}

	p, err = PlanConstantElision(detect(t, s, rows), []string{"u8_c"})
	if err != nil || p.Spec == nil {
		t.Fatalf("400 rows: plan %+v err %v", p, err)
	}
	if !reflect.DeepEqual(p.Fields, []string{"u16_a", "cat_u16"}) || p.Spec.Kind != encoding.GroupKindConstant {
		t.Fatalf("reserved u8_c: elided %v kind %d; want [u16_a cat_u16] constant", p.Fields, p.Spec.Kind)
	}
	if p.BytesSaved <= 0 {
		t.Fatalf("BytesSaved = %d, want > 0", p.BytesSaved)
	}

	// No constant field at all.
	s2, rows2 := constantRows(t, 400)
	p, err = PlanConstantElision(detect(t, s2, rows2), nil)
	if err != nil || p.Spec != nil || p.Skipped != "no_constant_fields" {
		t.Fatalf("no constants: plan %+v err %v", p, err)
	}
}

// TestPlanConstantElision_EncodesIndistinguishably: the planned cohort
// is a 0x02 file whose stride and narrowed bitmap drop exactly the
// elided fields, whose dictionary-bearing constants keep their own
// dictionaries, and whose logical stream is byte-for-byte the flat
// record region — null constants included.
func TestPlanConstantElision_EncodesIndistinguishably(t *testing.T) {
	s, rows := constantRows(t, 500, "u8_b", "u16_a", "cat_u8", "tags_u128", "amount", "u8_c")
	u8b := fieldIdx(t, s, "u8_b")[0]
	for _, r := range rows {
		setNull(s, r, u8b, true)
	}
	d := detect(t, s, rows)
	p, err := PlanConstantElision(d, nil)
	if err != nil || p.Spec == nil {
		t.Fatalf("plan %+v err %v", p, err)
	}
	if !reflect.DeepEqual(p.Fields, []string{"u8_b", "u16_a", "cat_u8", "amount", "tags_u128", "u8_c"}) {
		t.Fatalf("elided %v", p.Fields)
	}

	enc, err := NewGroupEncoder(s, []GroupSpec{*p.Spec})
	if err != nil {
		t.Fatal(err)
	}
	var spool []byte
	for _, r := range rows {
		if spool, err = enc.EncodeRow(spool, r); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	if err := WritePreamble(&out, enc.Schema()); err != nil {
		t.Fatal(err)
	}
	out.Write(spool)

	flat := flatCohort(t, s, rows)
	if int64(len(flat)-out.Len()) != p.BytesSaved {
		t.Fatalf("file shrank by %d bytes, plan said %d", len(flat)-out.Len(), p.BytesSaved)
	}
	gs, region := recordRegion(t, out.Bytes())
	if out.Bytes()[encoding.HeaderSize-1] != encoding.FormatVersionV2 || len(gs.Groups) != 1 || gs.Groups[0].Kind != encoding.GroupKindConstant {
		t.Fatalf("want a 0x02 cohort with one constant group, got version 0x%02x groups %+v", out.Bytes()[encoding.HeaderSize-1], gs.Groups)
	}
	removed := 1 + 2 + 1 + 16 + 16 + 1
	if got, want := gs.RecordByteSize(), s.RecordByteSize()-removed-(s.BitmapByteSize()-gs.BitmapByteSize()); got != want {
		t.Fatalf("physical stride %d, want %d", got, want)
	}
	if want := (len(s.Fields) - 6 + 7) / 8; gs.BitmapByteSize() != want {
		t.Fatalf("narrowed bitmap %d bytes, want %d", gs.BitmapByteSize(), want)
	}
	if !reflect.DeepEqual(gs.Fields[fieldIdx(t, s, "cat_u8")[0]].Dictionary, s.Fields[fieldIdx(t, s, "cat_u8")[0]].Dictionary) {
		t.Fatal("a constant categorical must keep its own dictionary")
	}
	if !gs.GroupMemberIsNull(0, 0, gs.GroupEntry(0, 0)) {
		t.Fatal("the null constant must carry its null bit in the entry")
	}
	lr, _, err := NewLogicalStream(bytes.NewReader(region), gs)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(lr)
	if err != nil {
		t.Fatal(err)
	}
	_, flatRegion := recordRegion(t, flat)
	if !bytes.Equal(got, flatRegion) {
		t.Fatal("elided cohort's logical stream differs from the flat record region")
	}
}

// BenchmarkConstantElisionDecode measures a cohort with 8 constant
// columns (of 30, 43 of its 176 row bytes) decoded as the flat 0x01
// file and as its constant-elided 0x02 twin, full reuse decode and
// run-skip decode. It reports file bytes and ns per row; the elided
// arm decodes the physical row directly (group_decode.go), so its
// constant members are written once per record and never re-decoded.
func BenchmarkConstantElisionDecode(b *testing.B) {
	t := &testing.T{}
	constant := []string{"u8_b", "u16_a", "pb_1", "cat_u16", "amount", "tags_u64", "tags_u128", "u8_c"}
	s, rows := constantRows(t, 20000, constant...)
	d := detect(t, s, rows)
	p, err := PlanConstantElision(d, nil)
	if err != nil || p.Spec == nil || len(p.Fields) != len(constant) {
		b.Fatalf("plan %+v err %v", p, err)
	}
	enc, err := NewGroupEncoder(s, []GroupSpec{*p.Spec})
	if err != nil {
		b.Fatal(err)
	}
	var spool []byte
	for _, r := range rows {
		if spool, err = enc.EncodeRow(spool, r); err != nil {
			b.Fatal(err)
		}
	}
	var elided bytes.Buffer
	if err := WritePreamble(&elided, enc.Schema()); err != nil {
		b.Fatal(err)
	}
	elided.Write(spool)
	flat := flatCohort(t, s, rows)
	fs, flatRegion := recordRegion(t, flat)
	gs, phys := recordRegion(t, elided.Bytes())
	b.Logf("flat file %d B (stride %d), elided file %d B (stride %d): %.1f%% smaller",
		len(flat), fs.RecordByteSize(), elided.Len(), gs.RecordByteSize(),
		100*float64(len(flat)-elided.Len())/float64(len(flat)))

	run := func(b *testing.B, s *encoding.Schema, region []byte, fileBytes int, runSkip bool) {
		b.ReportMetric(float64(fileBytes), "file_bytes")
		for i := 0; i < b.N; i++ {
			rr := NewRecordReader(bytes.NewReader(region), s)
			var rec ReusableRecord = &dualRecord{indexedTestRecord: newIndexedTestRecord(s)}
			if runSkip {
				rec = newRunSkipTestRecord(s)
			}
			for {
				if err := rr.ReadRecordReused(rec); err != nil {
					if err == io.EOF {
						break
					}
					b.Fatal(err)
				}
			}
		}
		b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/float64(len(rows)), "ns/row")
	}
	b.Run("v1", func(b *testing.B) { run(b, fs, flatRegion, len(flat), false) })
	b.Run("v2_elided", func(b *testing.B) { run(b, gs, phys, elided.Len(), false) })
	b.Run("v1_runskip", func(b *testing.B) { run(b, fs, flatRegion, len(flat), true) })
	b.Run("v2_elided_runskip", func(b *testing.B) { run(b, gs, phys, elided.Len(), true) })
}
