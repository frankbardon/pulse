package encoding

import "testing"

// TestRecordCountForPayload_Matrix pins the one derivation both
// service.CountRecords and descriptor.Inspect read their single-file
// record count from. The count is the FLOOR and the leftover is
// reported separately; the two arms differ only in what they do with
// the leftover, never in the count.
func TestRecordCountForPayload_Matrix(t *testing.T) {
	// 4 (u32) + 8 (f64) = 12-byte stride, no nullable field.
	plain := &Schema{Fields: []Field{
		{Name: "id", Type: FieldTypeU32, ByteOffset: 0},
		{Name: "score", Type: FieldTypeF64, ByteOffset: 4},
	}}
	if got := plain.RecordByteSize(); got != 12 {
		t.Fatalf("fixture stride = %d, want 12", got)
	}
	// One nullable field ⇒ a 1-byte trailing bitmap ⇒ 13-byte stride.
	nullable := &Schema{Fields: []Field{
		{Name: "id", Type: FieldTypeU32, ByteOffset: 0},
		{Name: "score", Type: FieldTypeF64, ByteOffset: 4, Nullable: true},
	}}
	if got := nullable.RecordByteSize(); got != 13 {
		t.Fatalf("nullable fixture stride = %d, want 13", got)
	}

	tests := []struct {
		name     string
		schema   *Schema
		payload  int64
		count    int64
		trailing int64
		ok       bool
	}{
		{"empty cohort", plain, 0, 0, 0, true},
		{"whole records", plain, 60, 5, 0, true},
		{"truncated tail floors", plain, 63, 5, 3, true},
		{"shorter than one record", plain, 7, 0, 7, true},
		{"bitmap counts in the stride", nullable, 65, 5, 0, true},
		{"bitmap stride truncated", nullable, 60, 4, 8, true},
		{"field-less schema has no records", &Schema{}, 40, 0, 0, false},
		{"negative payload is not a count", plain, -1, 0, 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			count, trailing, ok := tc.schema.RecordCountForPayload(tc.payload)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if count != tc.count {
				t.Errorf("count = %d, want %d", count, tc.count)
			}
			if trailing != tc.trailing {
				t.Errorf("trailing = %d, want %d", trailing, tc.trailing)
			}
		})
	}
}
