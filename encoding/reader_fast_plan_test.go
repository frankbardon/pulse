package encoding

import (
	"bytes"
	"fmt"
	"io"
	"reflect"
	"testing"
)

// restrictReusedRecord copies a reusableTestRecord's maps restricted to
// the retained set — the expected shape of a projected decode. The
// plan-driven reused path must reproduce this exactly. Restriction rules
// mirror ReadRecordReusedWithPlan's writes:
//   - values[name] survives iff keep accepts name;
//   - nulls[name] survives iff keep accepts name (bitmap surfaces nulls
//     only for retained nullable fields);
//   - wide[name] survives iff keep accepts name (decimal128 / set masks
//     ride the same retained gate).
func restrictReusedRecord(full *reusableTestRecord, retained []string) *reusableTestRecord {
	keep := make(map[string]struct{}, len(retained))
	for _, n := range retained {
		keep[n] = struct{}{}
	}
	out := newReusableTestRecord()
	for k, v := range full.values {
		if _, ok := keep[k]; ok {
			out.values[k] = v
		}
	}
	for k, v := range full.nulls {
		if _, ok := keep[k]; ok {
			out.nulls[k] = v
		}
	}
	for k, v := range full.wide {
		if _, ok := keep[k]; ok {
			out.wide[k] = v
		}
	}
	return out
}

// decodeFullReused runs the full-decode reuse path (ReadRecordReused) on
// one raw record and returns the populated test record.
func decodeFullReused(t *testing.T, schema *Schema, raw []byte, label string) *reusableTestRecord {
	t.Helper()
	rr := NewRecordReader(bytes.NewReader(raw), schema)
	rec := newReusableTestRecord()
	if err := rr.ReadRecordReused(rec); err != nil {
		t.Fatalf("%s: ReadRecordReused: %v", label, err)
	}
	return rec
}

// decodePlanReused runs the plan-aware reuse path on one raw record and
// returns the populated test record.
func decodePlanReused(t *testing.T, schema *Schema, raw []byte, retained []string, label string) *reusableTestRecord {
	t.Helper()
	plan, err := schema.BuildDecodePlan(retained)
	if err != nil {
		t.Fatalf("%s: BuildDecodePlan: %v", label, err)
	}
	rr := NewRecordReader(bytes.NewReader(raw), schema)
	rec := newReusableTestRecord()
	if err := rr.ReadRecordReusedWithPlan(rec, keepFromNames(retained), plan); err != nil {
		t.Fatalf("%s: ReadRecordReusedWithPlan: %v", label, err)
	}
	return rec
}

// assertPlanReusedMatchesRestricted asserts the plan-driven reuse output
// equals the full-reuse output restricted to the retained set.
func assertPlanReusedMatchesRestricted(t *testing.T, schema *Schema, raw []byte, retained []string, label string) {
	t.Helper()
	want := restrictReusedRecord(decodeFullReused(t, schema, raw, label), retained)
	got := decodePlanReused(t, schema, raw, retained, label)
	if !reflect.DeepEqual(want.values, got.values) {
		t.Errorf("%s values mismatch:\n  want=%v\n  got=%v", label, want.values, got.values)
	}
	if !reflect.DeepEqual(want.nulls, got.nulls) {
		t.Errorf("%s nulls mismatch:\n  want=%v\n  got=%v", label, want.nulls, got.nulls)
	}
	if !reflect.DeepEqual(want.wide, got.wide) {
		t.Errorf("%s wide mismatch:\n  want=%v\n  got=%v", label, want.wide, got.wide)
	}
}

// TestReadRecordReusedWithPlan_MatchesReusedRestrictedPerType sweeps every
// 2^N projection subset of each per-type fixture and asserts the plan-aware
// reuse output equals the full-reuse output restricted to that subset —
// including subsets that drop a bit-packed member sharing a byte with a
// retained member and subsets that drop/retain a nullable field.
func TestReadRecordReusedWithPlan_MatchesReusedRestrictedPerType(t *testing.T) {
	for _, f := range perTypeFixtures() {
		f := f
		t.Run(f.name, func(t *testing.T) {
			if got := len(f.schema.Fields); got > 12 {
				t.Fatalf("fixture %s has %d fields; subset sweep intractable", f.name, got)
			}
			records := generatePerTypeRecords(t, f, 0x9A11+int64(len(f.name)))
			fieldCount := uint(len(f.schema.Fields))
			masks := uint64(1) << fieldCount
			for mask := uint64(0); mask < masks; mask++ {
				retained := subsetFromMask(f.schema, mask)
				for ri, raw := range records {
					label := fmt.Sprintf("%s/mask=%0*b/rec=%d", f.name, fieldCount, mask, ri)
					assertPlanReusedMatchesRestricted(t, f.schema, raw, retained, label)
					if t.Failed() {
						return
					}
				}
			}
		})
	}
}

// TestReadRecordReusedWithPlan_MatchesReusedRestrictedBigMixed exercises the
// 26-field mixed schema (every type, nullable variants, bit-packed runs,
// decimal128, every set width, rotating null patterns) across several
// hand-chosen retained subsets that stress the tricky cases:
//   - a bit-packed member retained while its byte-sharing neighbour is
//     dropped (u4_lo kept, u4_hi dropped; pb_0 kept, pb_1 dropped);
//   - a nullable field retained vs dropped (u32_a / amount / tags_u64);
//   - the whole nullable set dropped (bitmap becomes a pure skip);
//   - the empty set (whole stride skipped).
func TestReadRecordReusedWithPlan_MatchesReusedRestrictedBigMixed(t *testing.T) {
	schema := bigMixedSchema()
	records := generateBigSchemaRecords(t, schema, 0xB16B0F00)

	subsets := [][]string{
		{}, // empty ⇒ whole stride is one SkipBytes
		{"u8_a"},
		{"u4_lo"},               // bit-packed member; u4_hi shares its byte, dropped
		{"u4_hi"},               // the other nibble; u4_lo dropped
		{"pb_0"},                // packed_bool; pb_1 shares its group, dropped
		{"pb_1"},                // the other bit; pb_0 dropped
		{"u4_lo", "pb_1"},       // two bit-packed members, non-adjacent within the run
		{"u32_a"},               // nullable retained
		{"u8_a", "u16_a"},       // nullable u32_a NOT retained (bitmap skip for it)
		{"amount"},              // decimal128 nullable retained
		{"tags_u16"},            // set nullable retained
		{"tags_u64", "tags_u8"}, // set nullable + set non-null
		{"u8_b", "u32_a", "date_a", "f64_a", "amount", "tags_u16", "tags_u64", "u16_b"}, // all nullable retained
		{"u8_a", "u16_c", "u64_b"},         // scattered non-null tail
		{"u4_lo", "u4_hi", "pb_0", "pb_1"}, // full bit-packed run retained
	}

	for si, retained := range subsets {
		for ri, raw := range records {
			label := fmt.Sprintf("big/subset=%d/rec=%d", si, ri)
			assertPlanReusedMatchesRestricted(t, schema, raw, retained, label)
			if t.Failed() {
				return
			}
		}
	}
}

// TestReadRecordReusedWithPlan_NilPlanEqualsReused asserts that passing a
// nil plan is byte-identical to ReadRecordReused (the full-decode reuse
// path). This is the guarantee that no-projection reuse is unchanged.
func TestReadRecordReusedWithPlan_NilPlanEqualsReused(t *testing.T) {
	schema := bigMixedSchema()
	records := generateBigSchemaRecords(t, schema, 0x711D1A00)

	for ri, raw := range records {
		refRR := NewRecordReader(bytes.NewReader(raw), schema)
		ref := newReusableTestRecord()
		if err := refRR.ReadRecordReused(ref); err != nil {
			t.Fatalf("rec=%d: ReadRecordReused: %v", ri, err)
		}

		subjRR := NewRecordReader(bytes.NewReader(raw), schema)
		subj := newReusableTestRecord()
		if err := subjRR.ReadRecordReusedWithPlan(subj, nil, nil); err != nil {
			t.Fatalf("rec=%d: ReadRecordReusedWithPlan(nil): %v", ri, err)
		}

		if !reflect.DeepEqual(ref.values, subj.values) {
			t.Errorf("rec=%d nil-plan values mismatch:\n  reused=%v\n  plan=%v", ri, ref.values, subj.values)
		}
		if !reflect.DeepEqual(ref.nulls, subj.nulls) {
			t.Errorf("rec=%d nil-plan nulls mismatch:\n  reused=%v\n  plan=%v", ri, ref.nulls, subj.nulls)
		}
		if !reflect.DeepEqual(ref.wide, subj.wide) {
			t.Errorf("rec=%d nil-plan wide mismatch:\n  reused=%v\n  plan=%v", ri, ref.wide, subj.wide)
		}
	}
}

// countingSeeker wraps a *bytes.Reader and records how many bytes are
// consumed via Read (i.e. actually decoded off the wire) vs skipped via
// Seek. It proves projection engages under reuse: only the retained
// group's on-wire bytes should be Read; the rest should be Seek'd past.
type countingSeeker struct {
	inner     *bytes.Reader
	bytesRead int
}

func newCountingSeeker(raw []byte) *countingSeeker {
	return &countingSeeker{inner: bytes.NewReader(raw)}
}

func (c *countingSeeker) Read(p []byte) (int, error) {
	n, err := c.inner.Read(p)
	c.bytesRead += n
	return n, err
}

func (c *countingSeeker) Seek(offset int64, whence int) (int64, error) {
	return c.inner.Seek(offset, whence)
}

// TestReadRecordReusedWithPlan_SkipsUnreadBytes proves the projection
// actually engages under reuse: on a wide schema with a tiny retained
// set, the plan path Reads only the retained group's on-wire bytes and
// Seeks past everything else — strictly fewer bytes than the full stride
// (which the full-reuse path reads in its entirety).
func TestReadRecordReusedWithPlan_SkipsUnreadBytes(t *testing.T) {
	// 200 u32 fields = 800-byte stride; retain exactly one in the middle.
	fields := make([]Field, 0, 200)
	for i := 0; i < 200; i++ {
		fields = append(fields, Field{Name: "f" + itoa(i), Type: FieldTypeU32})
	}
	schema := &Schema{Fields: fields}
	stride := schema.RecordByteSize()
	if stride != 200*4 {
		t.Fatalf("unexpected stride %d", stride)
	}

	// Build one record: field i carries value i.
	vals := map[string]any{}
	for i := 0; i < 200; i++ {
		vals["f"+itoa(i)] = uint64(i)
	}
	raw := encodeRecord(t, schema, vals, nil)

	retained := []string{"f100"}
	plan, err := schema.BuildDecodePlan(retained)
	if err != nil {
		t.Fatalf("BuildDecodePlan: %v", err)
	}

	cs := newCountingSeeker(raw)
	rr := NewRecordReader(cs, schema)
	rec := newReusableTestRecord()
	if err := rr.ReadRecordReusedWithPlan(rec, keepFromNames(retained), plan); err != nil {
		t.Fatalf("ReadRecordReusedWithPlan: %v", err)
	}

	// Correctness: only f100 decoded, with value 100.
	if got := rec.values["f100"]; got != 100 {
		t.Fatalf("f100 = %v, want 100", got)
	}
	if len(rec.values) != 1 {
		t.Fatalf("expected exactly 1 decoded value, got %d: %v", len(rec.values), rec.values)
	}

	// Projection engages: the single retained u32 is 4 bytes; the full
	// stride is 800. The plan path must Read only the retained group's
	// bytes (4) and Seek past the rest — far fewer than the full stride.
	if cs.bytesRead != 4 {
		t.Fatalf("plan path read %d bytes, want 4 (only the retained u32 group); "+
			"projection is not engaging under reuse", cs.bytesRead)
	}

	// Contrast: the full-reuse path reads the ENTIRE stride.
	cs2 := newCountingSeeker(raw)
	rr2 := NewRecordReader(cs2, schema)
	if err := rr2.ReadRecordReused(newReusableTestRecord()); err != nil {
		t.Fatalf("ReadRecordReused: %v", err)
	}
	if cs2.bytesRead != stride {
		t.Fatalf("full-reuse path read %d bytes, want full stride %d", cs2.bytesRead, stride)
	}
}

// TestReadRecordReusedWithPlan_EOF asserts EOF surfacing on an exhausted
// reader and on a partial trailing record, matching ReadRecordReused.
func TestReadRecordReusedWithPlan_EOF(t *testing.T) {
	schema := bigMixedSchema()
	records := generateBigSchemaRecords(t, schema, 0xE0F0F100)
	retained := []string{"u8_a", "u32_a", "amount", "u4_lo"}
	plan, err := schema.BuildDecodePlan(retained)
	if err != nil {
		t.Fatalf("BuildDecodePlan: %v", err)
	}
	keep := keepFromNames(retained)

	const n = 4
	var buf bytes.Buffer
	for i := 0; i < n; i++ {
		buf.Write(records[i])
	}
	rr := NewRecordReader(bytes.NewReader(buf.Bytes()), schema)
	rec := newReusableTestRecord()
	for i := 0; i < n; i++ {
		if err := rr.ReadRecordReusedWithPlan(rec, keep, plan); err != nil {
			t.Fatalf("record %d: unexpected error: %v", i, err)
		}
	}
	if err := rr.ReadRecordReusedWithPlan(rec, keep, plan); err != io.EOF {
		t.Fatalf("expected io.EOF after %d records, got %v", n, err)
	}

	// Partial trailing record.
	stride := schema.RecordByteSize()
	partial := append([]byte{}, buf.Bytes()...)
	partial = append(partial, records[0][:stride/2]...)
	rr2 := NewRecordReader(bytes.NewReader(partial), schema)
	for i := 0; i < n; i++ {
		if err := rr2.ReadRecordReusedWithPlan(rec, keep, plan); err != nil {
			t.Fatalf("record %d (partial run): unexpected error: %v", i, err)
		}
	}
	if err := rr2.ReadRecordReusedWithPlan(rec, keep, plan); err != io.EOF {
		t.Fatalf("expected io.EOF on partial trailing record, got %v", err)
	}
}
