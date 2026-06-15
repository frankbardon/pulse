package encoding

import (
	"reflect"
	"testing"
)

// fieldNames extracts the names from a DecodeFields segment for
// readable assertions.
func fieldNames(seg Segment) []string {
	df, ok := seg.(DecodeFields)
	if !ok {
		return nil
	}
	out := make([]string, len(df.Fields))
	for i, f := range df.Fields {
		out[i] = f.Name
	}
	return out
}

// planShape renders the plan as a compact slice of strings for
// table-driven equality checks.
func planShape(p *DecodePlan) []string {
	out := make([]string, 0, len(p.Segments))
	for _, seg := range p.Segments {
		switch s := seg.(type) {
		case SkipBytes:
			out = append(out, "skip:"+itoa(s.N))
		case DecodeFields:
			s2 := "decode:["
			for i, f := range s.Fields {
				if i > 0 {
					s2 += ","
				}
				s2 += f.Name
			}
			s2 += "]"
			out = append(out, s2)
		}
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// TestBuildDecodePlan_EmptyRetainedCoversFullStride verifies AC 4: an
// empty retained set produces a single SkipBytes covering the entire
// record stride (including the bitmap when present).
func TestBuildDecodePlan_EmptyRetainedCoversFullStride(t *testing.T) {
	schema := &Schema{
		Fields: []Field{
			{Name: "a", Type: FieldTypeU8},
			{Name: "b", Type: FieldTypeU32},
			{Name: "c", Type: FieldTypeF64, Nullable: true},
		},
	}
	plan, err := schema.BuildDecodePlan(nil)
	if err != nil {
		t.Fatalf("BuildDecodePlan: %v", err)
	}
	if len(plan.Segments) != 1 {
		t.Fatalf("want 1 segment, got %d (%v)", len(plan.Segments), planShape(plan))
	}
	sb, ok := plan.Segments[0].(SkipBytes)
	if !ok {
		t.Fatalf("want SkipBytes, got %T", plan.Segments[0])
	}
	want := schema.RecordByteSize()
	if sb.N != want {
		t.Errorf("SkipBytes.N = %d, want %d (record stride)", sb.N, want)
	}
	// Empty slice should behave the same as nil.
	plan2, err := schema.BuildDecodePlan([]string{})
	if err != nil {
		t.Fatalf("BuildDecodePlan empty slice: %v", err)
	}
	if !reflect.DeepEqual(planShape(plan), planShape(plan2)) {
		t.Errorf("nil vs empty slice diverge: %v vs %v", planShape(plan), planShape(plan2))
	}
}

// TestBuildDecodePlan_FullRetainedNoSkips verifies AC 5: a full retained
// set produces zero SkipBytes segments. The output is equivalent to the
// per-field walk and will become the equivalence baseline in E2-S3.
func TestBuildDecodePlan_FullRetainedNoSkips(t *testing.T) {
	schema := &Schema{
		Fields: []Field{
			{Name: "a", Type: FieldTypeU8},
			{Name: "b", Type: FieldTypeU32},
			{Name: "c", Type: FieldTypeF64, Nullable: true},
		},
	}
	retained := []string{"a", "b", "c"}
	plan, err := schema.BuildDecodePlan(retained)
	if err != nil {
		t.Fatalf("BuildDecodePlan: %v", err)
	}
	for i, seg := range plan.Segments {
		if _, ok := seg.(SkipBytes); ok {
			t.Errorf("segment %d is SkipBytes; want no skips when fully retained (shape=%v)", i, planShape(plan))
		}
	}
	// Must contain every retained field across all DecodeFields segments
	// (the union, not necessarily one segment — the nullable bitmap
	// segment carries the nullable subset).
	seen := map[string]bool{}
	for _, seg := range plan.Segments {
		if df, ok := seg.(DecodeFields); ok {
			for _, f := range df.Fields {
				seen[f.Name] = true
			}
		}
	}
	for _, name := range retained {
		if !seen[name] {
			t.Errorf("retained field %q not present in plan (%v)", name, planShape(plan))
		}
	}
}

// TestBuildDecodePlan_PerFieldTypeAlone exercises AC 6 (per-type
// coverage): a one-field schema for each field type, retained, produces
// exactly one DecodeFields segment listing that field and no Skips.
func TestBuildDecodePlan_PerFieldTypeAlone(t *testing.T) {
	types := []FieldType{
		FieldTypeU8, FieldTypeU16, FieldTypeU32, FieldTypeU64,
		FieldTypeF32, FieldTypeF64,
		FieldTypeU4, FieldTypeDate, FieldTypePackedBool,
		FieldTypeCategoricalU8, FieldTypeCategoricalU16, FieldTypeCategoricalU32,
		FieldTypeDecimal128,
		FieldTypeSetU8, FieldTypeSetU16, FieldTypeSetU32, FieldTypeSetU64,
	}
	for _, ft := range types {
		t.Run(ft.String(), func(t *testing.T) {
			schema := &Schema{Fields: []Field{{Name: "x", Type: ft}}}
			plan, err := schema.BuildDecodePlan([]string{"x"})
			if err != nil {
				t.Fatalf("BuildDecodePlan: %v", err)
			}
			if len(plan.Segments) != 1 {
				t.Fatalf("want 1 segment, got %v", planShape(plan))
			}
			df, ok := plan.Segments[0].(DecodeFields)
			if !ok {
				t.Fatalf("want DecodeFields, got %T", plan.Segments[0])
			}
			if len(df.Fields) != 1 || df.Fields[0].Name != "x" {
				t.Errorf("DecodeFields = %v, want [x]", fieldNames(plan.Segments[0]))
			}
		})
	}
}

// TestBuildDecodePlan_PerFieldTypeAloneSkipped verifies the
// complementary case: the single field, NOT retained, becomes one
// SkipBytes whose width matches the on-wire byte size of that field.
func TestBuildDecodePlan_PerFieldTypeAloneSkipped(t *testing.T) {
	cases := []struct {
		ft       FieldType
		wantSkip int
	}{
		{FieldTypeU8, 1},
		{FieldTypeU16, 2},
		{FieldTypeU32, 4},
		{FieldTypeU64, 8},
		{FieldTypeF32, 4},
		{FieldTypeF64, 8},
		// Bit-packed singletons consume one byte each (mirrors
		// Schema.RecordByteSize: stride++ per bit-packed field).
		{FieldTypeU4, 1},
		{FieldTypePackedBool, 1},
		{FieldTypeDate, 4},
		{FieldTypeCategoricalU8, 1},
		{FieldTypeCategoricalU16, 2},
		{FieldTypeCategoricalU32, 4},
		{FieldTypeDecimal128, 16},
		{FieldTypeSetU8, 1},
		{FieldTypeSetU16, 2},
		{FieldTypeSetU32, 4},
		{FieldTypeSetU64, 8},
	}
	for _, tc := range cases {
		t.Run(tc.ft.String(), func(t *testing.T) {
			schema := &Schema{Fields: []Field{{Name: "x", Type: tc.ft}}}
			plan, err := schema.BuildDecodePlan([]string{"other"})
			if err != nil {
				t.Fatalf("BuildDecodePlan: %v", err)
			}
			if len(plan.Segments) != 1 {
				t.Fatalf("want 1 segment, got %v", planShape(plan))
			}
			sb, ok := plan.Segments[0].(SkipBytes)
			if !ok {
				t.Fatalf("want SkipBytes, got %T", plan.Segments[0])
			}
			if sb.N != tc.wantSkip {
				t.Errorf("%s: SkipBytes.N = %d, want %d", tc.ft, sb.N, tc.wantSkip)
			}
		})
	}
}

// TestBuildDecodePlan_U4PairFullyRetained covers AC 6 for the u4 pair:
// both members in the retained set produces one DecodeFields covering
// both, no skips.
func TestBuildDecodePlan_U4PairFullyRetained(t *testing.T) {
	schema := &Schema{
		Fields: []Field{
			{Name: "lo", Type: FieldTypeU4, BitPosition: 0},
			{Name: "hi", Type: FieldTypeU4, BitPosition: 4},
		},
	}
	plan, err := schema.BuildDecodePlan([]string{"lo", "hi"})
	if err != nil {
		t.Fatalf("BuildDecodePlan: %v", err)
	}
	got := planShape(plan)
	want := []string{"decode:[lo,hi]"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("plan = %v, want %v", got, want)
	}
}

// TestBuildDecodePlan_U4PairFullySkipped covers the u4 pair fully
// elided: one SkipBytes covering the group's byte size.
func TestBuildDecodePlan_U4PairFullySkipped(t *testing.T) {
	schema := &Schema{
		Fields: []Field{
			{Name: "lo", Type: FieldTypeU4, BitPosition: 0},
			{Name: "hi", Type: FieldTypeU4, BitPosition: 4},
			{Name: "id", Type: FieldTypeU32},
		},
	}
	plan, err := schema.BuildDecodePlan([]string{"id"})
	if err != nil {
		t.Fatalf("BuildDecodePlan: %v", err)
	}
	got := planShape(plan)
	// u4 pair: 2 bytes on-wire (1 byte per bit-packed field per stride
	// math). Combined with no preceding skip, then DecodeFields for id.
	want := []string{"skip:2", "decode:[id]"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("plan = %v, want %v", got, want)
	}
}

// TestBuildDecodePlan_U4PairHalfRetained covers the u4 pair with only
// one member retained: the entire group becomes one DecodeFields
// (bit-packed neighbours stay grouped — the iterator must walk the
// whole run to stay byte-aligned).
func TestBuildDecodePlan_U4PairHalfRetained(t *testing.T) {
	schema := &Schema{
		Fields: []Field{
			{Name: "lo", Type: FieldTypeU4, BitPosition: 0},
			{Name: "hi", Type: FieldTypeU4, BitPosition: 4},
		},
	}
	plan, err := schema.BuildDecodePlan([]string{"hi"})
	if err != nil {
		t.Fatalf("BuildDecodePlan: %v", err)
	}
	got := planShape(plan)
	want := []string{"decode:[lo,hi]"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("plan = %v, want %v", got, want)
	}
}

// TestBuildDecodePlan_PackedBoolRunFullyRetained covers a contiguous
// packed_bool run with every member in the retained set.
func TestBuildDecodePlan_PackedBoolRunFullyRetained(t *testing.T) {
	schema := &Schema{
		Fields: []Field{
			{Name: "b0", Type: FieldTypePackedBool, BitPosition: 0},
			{Name: "b1", Type: FieldTypePackedBool, BitPosition: 1},
			{Name: "b2", Type: FieldTypePackedBool, BitPosition: 2},
		},
	}
	plan, err := schema.BuildDecodePlan([]string{"b0", "b1", "b2"})
	if err != nil {
		t.Fatalf("BuildDecodePlan: %v", err)
	}
	got := planShape(plan)
	want := []string{"decode:[b0,b1,b2]"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("plan = %v, want %v", got, want)
	}
}

// TestBuildDecodePlan_PackedBoolRunFullySkipped covers a contiguous
// packed_bool run with no member retained: one SkipBytes for the whole
// group.
func TestBuildDecodePlan_PackedBoolRunFullySkipped(t *testing.T) {
	schema := &Schema{
		Fields: []Field{
			{Name: "id", Type: FieldTypeU32},
			{Name: "b0", Type: FieldTypePackedBool, BitPosition: 0},
			{Name: "b1", Type: FieldTypePackedBool, BitPosition: 1},
			{Name: "b2", Type: FieldTypePackedBool, BitPosition: 2},
		},
	}
	plan, err := schema.BuildDecodePlan([]string{"id"})
	if err != nil {
		t.Fatalf("BuildDecodePlan: %v", err)
	}
	got := planShape(plan)
	// id retained (4 bytes), then group of 3 packed_bool ⇒ skip 3.
	want := []string{"decode:[id]", "skip:3"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("plan = %v, want %v", got, want)
	}
}

// TestBuildDecodePlan_PackedBoolRunPartiallyRetained covers a
// contiguous packed_bool run where only some members are retained: the
// entire group becomes ONE DecodeFields covering every member (the
// iterator must walk the whole run together; the unretained members'
// map writes will be filtered downstream).
func TestBuildDecodePlan_PackedBoolRunPartiallyRetained(t *testing.T) {
	schema := &Schema{
		Fields: []Field{
			{Name: "b0", Type: FieldTypePackedBool, BitPosition: 0},
			{Name: "b1", Type: FieldTypePackedBool, BitPosition: 1},
			{Name: "b2", Type: FieldTypePackedBool, BitPosition: 2},
		},
	}
	plan, err := schema.BuildDecodePlan([]string{"b1"})
	if err != nil {
		t.Fatalf("BuildDecodePlan: %v", err)
	}
	got := planShape(plan)
	want := []string{"decode:[b0,b1,b2]"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("plan = %v, want %v", got, want)
	}
}

// TestBuildDecodePlan_BitPackedRunStopsAtTypeBoundary covers the rule
// that "bit-packed run" is a contiguous run of any IsBitPacked() fields
// — a u4 immediately followed by a packed_bool stays in the same group,
// because both are bit-packed. The decoder treats them uniformly (1
// byte per bit-packed field on-wire).
func TestBuildDecodePlan_BitPackedRunStopsAtTypeBoundary(t *testing.T) {
	schema := &Schema{
		Fields: []Field{
			{Name: "nibble", Type: FieldTypeU4, BitPosition: 0},
			{Name: "flag", Type: FieldTypePackedBool, BitPosition: 0},
			{Name: "id", Type: FieldTypeU16},
		},
	}
	// Nothing retained from the bit-packed run.
	plan, err := schema.BuildDecodePlan([]string{"id"})
	if err != nil {
		t.Fatalf("BuildDecodePlan: %v", err)
	}
	got := planShape(plan)
	// Bit-packed group: nibble (1) + flag (1) = 2 bytes skip, then id.
	want := []string{"skip:2", "decode:[id]"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("plan = %v, want %v", got, want)
	}
}

// TestBuildDecodePlan_CoalesceContiguousSkips verifies AC 1 / Notes:
// consecutive unprojected non-bit-packed fields coalesce into ONE
// SkipBytes whose N is the sum of byte sizes.
func TestBuildDecodePlan_CoalesceContiguousSkips(t *testing.T) {
	schema := &Schema{
		Fields: []Field{
			{Name: "a", Type: FieldTypeU8},     // skip 1
			{Name: "b", Type: FieldTypeU16},    // skip 2
			{Name: "c", Type: FieldTypeU32},    // skip 4
			{Name: "keep", Type: FieldTypeF64}, // decode
			{Name: "d", Type: FieldTypeU8},     // skip 1
		},
	}
	plan, err := schema.BuildDecodePlan([]string{"keep"})
	if err != nil {
		t.Fatalf("BuildDecodePlan: %v", err)
	}
	got := planShape(plan)
	want := []string{"skip:7", "decode:[keep]", "skip:1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("plan = %v, want %v", got, want)
	}
}

// TestBuildDecodePlan_BitmapWholeRetained covers AC 3: HasBitmap()=true
// AND at least one nullable field retained ⇒ trailing DecodeFields
// segment for the bitmap, listing every nullable field.
func TestBuildDecodePlan_BitmapWholeRetained(t *testing.T) {
	schema := &Schema{
		Fields: []Field{
			{Name: "a", Type: FieldTypeU8, Nullable: true},
			{Name: "b", Type: FieldTypeU16, Nullable: false},
			{Name: "c", Type: FieldTypeU32, Nullable: true},
		},
	}
	plan, err := schema.BuildDecodePlan([]string{"a"})
	if err != nil {
		t.Fatalf("BuildDecodePlan: %v", err)
	}
	// Last segment must be a DecodeFields carrying ALL nullable fields
	// in schema order, regardless of which were retained.
	last := plan.Segments[len(plan.Segments)-1]
	df, ok := last.(DecodeFields)
	if !ok {
		t.Fatalf("last segment is %T, want DecodeFields (bitmap)", last)
	}
	got := []string{}
	for _, f := range df.Fields {
		got = append(got, f.Name)
	}
	want := []string{"a", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("bitmap segment fields = %v, want %v", got, want)
	}
}

// TestBuildDecodePlan_BitmapWholeSkipped covers AC 3: HasBitmap()=true
// AND no nullable field retained ⇒ trailing SkipBytes for the bitmap.
func TestBuildDecodePlan_BitmapWholeSkipped(t *testing.T) {
	schema := &Schema{
		Fields: []Field{
			{Name: "a", Type: FieldTypeU8, Nullable: true},
			{Name: "b", Type: FieldTypeU16, Nullable: false},
			{Name: "c", Type: FieldTypeU32, Nullable: true},
		},
	}
	plan, err := schema.BuildDecodePlan([]string{"b"})
	if err != nil {
		t.Fatalf("BuildDecodePlan: %v", err)
	}
	last := plan.Segments[len(plan.Segments)-1]
	sb, ok := last.(SkipBytes)
	if !ok {
		t.Fatalf("last segment is %T, want SkipBytes (bitmap skip)", last)
	}
	want := schema.BitmapByteSize()
	if sb.N != want {
		t.Errorf("bitmap SkipBytes.N = %d, want %d", sb.N, want)
	}
}

// TestBuildDecodePlan_NoBitmapWhenNoNullable verifies AC 3 negative
// path: !HasBitmap() ⇒ no bitmap segment appended.
func TestBuildDecodePlan_NoBitmapWhenNoNullable(t *testing.T) {
	schema := &Schema{
		Fields: []Field{
			{Name: "a", Type: FieldTypeU8},
			{Name: "b", Type: FieldTypeU32},
		},
	}
	plan, err := schema.BuildDecodePlan([]string{"a", "b"})
	if err != nil {
		t.Fatalf("BuildDecodePlan: %v", err)
	}
	// Total decode coverage must equal the record stride; no trailing
	// bitmap segment.
	bytesCovered := 0
	for _, seg := range plan.Segments {
		switch s := seg.(type) {
		case SkipBytes:
			bytesCovered += s.N
		case DecodeFields:
			for _, f := range s.Fields {
				if f.Type.IsBitPacked() {
					bytesCovered++
				} else {
					bytesCovered += f.Type.ByteSize()
				}
			}
		}
	}
	if bytesCovered != schema.RecordByteSize() {
		t.Errorf("plan covers %d bytes, want %d (stride)", bytesCovered, schema.RecordByteSize())
	}
}

// TestBuildDecodePlan_MixedSchema covers AC 6 (mixed schema with
// categorical + numeric + decimal128 + set_u8 + nullable) plus AC 1
// coalescing across heterogeneous types.
func TestBuildDecodePlan_MixedSchema(t *testing.T) {
	schema := &Schema{
		Fields: []Field{
			{Name: "id", Type: FieldTypeU64},                            // 8 bytes
			{Name: "cat", Type: FieldTypeCategoricalU16},                // 2 bytes
			{Name: "amount", Type: FieldTypeDecimal128, Nullable: true}, // 16 bytes
			{Name: "tags", Type: FieldTypeSetU8},                        // 1 byte
			{Name: "score", Type: FieldTypeF64, Nullable: true},         // 8 bytes
			{Name: "flag", Type: FieldTypePackedBool},                   // 1 byte (bit-packed group of 1)
		},
	}
	// Retain id, amount, flag. Expected layout:
	//   decode:[id]   (8)
	//   skip:2        (cat)
	//   decode:[amount] (16)
	//   skip:1        (tags)
	//   skip:8        (score) — coalesces with tags ⇒ skip:9
	//   decode:[flag] (1, bit-packed group)
	//   decode:[amount,score] (bitmap, both nullable, score is in nullable list even if not retained)
	plan, err := schema.BuildDecodePlan([]string{"id", "amount", "flag"})
	if err != nil {
		t.Fatalf("BuildDecodePlan: %v", err)
	}
	got := planShape(plan)
	want := []string{
		"decode:[id]",
		"skip:2",
		"decode:[amount]",
		"skip:9",
		"decode:[flag]",
		"decode:[amount,score]",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("plan shape = %v, want %v", got, want)
	}

	// Sanity: total decoded + skipped bytes (excluding bitmap, which is
	// counted separately) should match the record stride.
	bytesCovered := 0
	for _, seg := range plan.Segments[:len(plan.Segments)-1] {
		switch s := seg.(type) {
		case SkipBytes:
			bytesCovered += s.N
		case DecodeFields:
			for _, f := range s.Fields {
				if f.Type.IsBitPacked() {
					bytesCovered++
				} else {
					bytesCovered += f.Type.ByteSize()
				}
			}
		}
	}
	wantBytes := schema.RecordByteSize() - schema.BitmapByteSize()
	if bytesCovered != wantBytes {
		t.Errorf("payload coverage = %d, want %d", bytesCovered, wantBytes)
	}
}

// TestBuildDecodePlan_Deterministic verifies AC 7: two calls with the
// same (schema, retained) inputs return equal plans, and the retained
// order does not matter.
func TestBuildDecodePlan_Deterministic(t *testing.T) {
	schema := &Schema{
		Fields: []Field{
			{Name: "a", Type: FieldTypeU8},
			{Name: "b", Type: FieldTypeU16},
			{Name: "c", Type: FieldTypeU32},
			{Name: "d", Type: FieldTypeF64, Nullable: true},
		},
	}
	p1, err := schema.BuildDecodePlan([]string{"a", "c"})
	if err != nil {
		t.Fatalf("p1: %v", err)
	}
	p2, err := schema.BuildDecodePlan([]string{"c", "a"})
	if err != nil {
		t.Fatalf("p2: %v", err)
	}
	if !reflect.DeepEqual(planShape(p1), planShape(p2)) {
		t.Errorf("retained order changes plan: %v vs %v", planShape(p1), planShape(p2))
	}
	p3, err := schema.BuildDecodePlan([]string{"a", "c"})
	if err != nil {
		t.Fatalf("p3: %v", err)
	}
	if !reflect.DeepEqual(planShape(p1), planShape(p3)) {
		t.Errorf("re-build diverges: %v vs %v", planShape(p1), planShape(p3))
	}
}

// TestBuildDecodePlan_UnknownAndDuplicateRetainedIgnored verifies that
// names not present in the schema and duplicates in retained are
// silently accepted and have no effect on the plan.
func TestBuildDecodePlan_UnknownAndDuplicateRetainedIgnored(t *testing.T) {
	schema := &Schema{
		Fields: []Field{
			{Name: "a", Type: FieldTypeU8},
			{Name: "b", Type: FieldTypeU16},
		},
	}
	base, err := schema.BuildDecodePlan([]string{"a"})
	if err != nil {
		t.Fatalf("base: %v", err)
	}
	noisy, err := schema.BuildDecodePlan([]string{"a", "a", "does_not_exist", "phantom"})
	if err != nil {
		t.Fatalf("noisy: %v", err)
	}
	if !reflect.DeepEqual(planShape(base), planShape(noisy)) {
		t.Errorf("noise affects plan: %v vs %v", planShape(base), planShape(noisy))
	}
}

// TestBuildDecodePlan_PureFunctionOfSchema verifies AC 7: the function
// touches no file content — exercising the same schema across
// permutations of retained sets never reads a record, never opens a
// file, never inspects field offsets beyond type/nullability.
func TestBuildDecodePlan_PureFunctionOfSchema(t *testing.T) {
	schema := &Schema{
		Fields: []Field{
			{Name: "a", Type: FieldTypeU8, ByteOffset: 9999, CsvColumnIdx: 7777},
			{Name: "b", Type: FieldTypeU32, ByteOffset: 1, CsvColumnIdx: 8},
		},
	}
	// Wildly inconsistent ByteOffset values must not change the plan —
	// the builder uses ByteSize from the type, not the stored offset.
	plan, err := schema.BuildDecodePlan([]string{"b"})
	if err != nil {
		t.Fatalf("BuildDecodePlan: %v", err)
	}
	got := planShape(plan)
	want := []string{"skip:1", "decode:[b]"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("plan = %v, want %v (ByteOffset must be ignored)", got, want)
	}
}

// TestBuildDecodePlan_BitPackedFlushSeparatesSkipAndDecode covers the
// notes-driven rule: when a bit-packed group sits between an
// unprojected non-bit-packed run and a projected non-bit-packed run,
// the skip and decode segments must NOT merge across the group.
func TestBuildDecodePlan_BitPackedFlushSeparatesSkipAndDecode(t *testing.T) {
	schema := &Schema{
		Fields: []Field{
			{Name: "skip_a", Type: FieldTypeU16},    // 2 bytes skip
			{Name: "b0", Type: FieldTypePackedBool}, // bit-packed, retained
			{Name: "keep_a", Type: FieldTypeU32},    // 4 bytes decode
		},
	}
	plan, err := schema.BuildDecodePlan([]string{"b0", "keep_a"})
	if err != nil {
		t.Fatalf("BuildDecodePlan: %v", err)
	}
	got := planShape(plan)
	want := []string{
		"skip:2",
		"decode:[b0]",
		"decode:[keep_a]",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("plan = %v, want %v", got, want)
	}
}

// TestBuildDecodePlan_SmallPlanForWideSchema is a concrete sanity check
// against the canonical benchmark target: 628 mostly-numeric fields,
// 4 retained scattered through the schema, must yield a small plan
// (well under 50 segments). Uses generated schema entries to keep the
// test self-contained.
func TestBuildDecodePlan_SmallPlanForWideSchema(t *testing.T) {
	const total = 628
	fields := make([]Field, total)
	for i := 0; i < total; i++ {
		fields[i] = Field{Name: "f" + itoa(i), Type: FieldTypeF64}
	}
	schema := &Schema{Fields: fields}
	retained := []string{"f0", "f100", "f300", "f627"}
	plan, err := schema.BuildDecodePlan(retained)
	if err != nil {
		t.Fatalf("BuildDecodePlan: %v", err)
	}
	// 4 retained scattered through 628 fields ⇒ at most 9 segments
	// (alternating skip/decode). The story explicitly calls out 6–10.
	if got := len(plan.Segments); got > 12 {
		t.Errorf("plan has %d segments; expected ≤ 12 for 628→4 retained: %v", got, planShape(plan))
	}
	// Every retained field appears in a DecodeFields segment exactly once.
	seen := map[string]int{}
	for _, seg := range plan.Segments {
		if df, ok := seg.(DecodeFields); ok {
			for _, f := range df.Fields {
				seen[f.Name]++
			}
		}
	}
	for _, name := range retained {
		if seen[name] != 1 {
			t.Errorf("retained %q appears %d times, want 1", name, seen[name])
		}
	}
}
