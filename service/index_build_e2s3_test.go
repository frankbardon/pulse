package service

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/fs"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// dateIndexTestSchema pairs an id (u32) with a date column so
// BuildIndex/Lookup can be keyed on the date field end to end.
// FieldTypeDate shares WriteFieldValue's u32 branch (encoding/record.go),
// so writePulseFile's existing uint64-record helper covers it without a
// dedicated wide-write path.
func dateIndexTestSchema() *encoding.Schema {
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
			{Name: "created", Type: encoding.FieldTypeDate, ByteOffset: 4, CsvColumnIdx: 1},
		},
	}
}

func epochDays(t *testing.T, layout, s string) uint64 {
	t.Helper()
	tm, err := time.Parse(layout, s)
	if err != nil {
		t.Fatalf("time.Parse(%q, %q): %v", layout, s, err)
	}
	return uint64(uint32(tm.Unix() / 86400))
}

func singleKeyLookupRequest(cohort, field, value string, returnColumns []string) *types.LookupRequest {
	return &types.LookupRequest{
		Cohort:        &types.Cohort{Filename: cohort},
		Field:         field,
		Value:         value,
		ReturnColumns: returnColumns,
	}
}

// TestBuildIndexAndLookup_DateKey_ExactMatch proves the full build+lookup
// round trip for a date-keyed sidecar index: BuildIndex scans decoded
// records (Record.NumericValue's already-lossless epoch-day echo),
// Lookup resolves the request literal via encoding.ParseDate, and the
// two must land on the exact same on-wire bytes for the hash probe to
// hit.
func TestBuildIndexAndLookup_DateKey_ExactMatch(t *testing.T) {
	schema := dateIndexTestSchema()
	records := [][]uint64{
		{1, epochDays(t, "2006-01-02", "2024-01-01")},
		{2, epochDays(t, "2006-01-02", "2024-06-15")},
		{3, epochDays(t, "2006-01-02", "2025-12-31")},
	}
	cfg := setupTestFS(t, "cohort.pulse", schema, records)
	svc := New(cfg)

	if _, err := svc.BuildIndex(context.Background(), "cohort.pulse", []string{"created"}); err != nil {
		t.Fatalf("BuildIndex(created): %v", err)
	}

	res, err := svc.Lookup(context.Background(), singleKeyLookupRequest("cohort.pulse", "created", "2024-06-15", []string{"id"}))
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("Rows = %d, want 1", len(res.Rows))
	}
	if got := res.Rows[0]["id"].(float64); got != 2.0 {
		t.Errorf("id = %v, want 2.0 (created=2024-06-15 -> row index 1)", got)
	}
}

// decimalIndexTestSchema pairs id (u32) with a decimal128 "amount"
// column (precision 18, scale 2). Decimal128 needs the dedicated
// 16-byte on-wire API (encoding.WriteDecimal128) rather than
// writePulseFile's WriteFieldValue helper, so this fixture is built by
// hand instead of routing through writePulseFile/setupTestFS.
func decimalIndexTestSchema() *encoding.Schema {
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
			{Name: "amount", Type: encoding.FieldTypeDecimal128, ByteOffset: 4, CsvColumnIdx: 1, Precision: 18, Scale: 2},
		},
	}
}

// setupDecimalIndexTestFS writes a cohort of (id u32, amount decimal128)
// records, where amounts are supplied as integer mantissas already at
// the field's declared scale (2) — e.g. mantissa 1234 == "12.34".
func setupDecimalIndexTestFS(t *testing.T, path string, mantissas []int64) *fs.Config {
	t.Helper()
	schema := decimalIndexTestSchema()
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	for i, m := range mantissas {
		id := uint64(i + 1)
		if err := encoding.WriteFieldValue(&buf, encoding.FieldTypeU32, id); err != nil {
			t.Fatalf("WriteFieldValue(id): %v", err)
		}
		if err := encoding.WriteDecimal128(&buf, encoding.NewDecimal128FromInt(m)); err != nil {
			t.Fatalf("WriteDecimal128: %v", err)
		}
	}
	cfg := fs.NewMemMap()
	if err := afero.WriteFile(cfg.Fs(), path, buf.Bytes(), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return cfg
}

// TestBuildIndexAndLookup_Decimal128Key_ExactMatch proves the full
// build+lookup round trip for a decimal128-keyed sidecar index end to
// end, with NO float64 round-trip anywhere in the path: BuildIndex
// resolves each record's exact mantissa via Record.WideValue +
// encoding.EncodeDecimal128 (processing.KeyFieldOnWireBytes), and
// Lookup resolves the literal "12.34" via encoding.ParseDecimal128 +
// encoding.EncodeDecimal128 (processing.ResolveLookupKeyBytes) — the
// two must land on byte-identical mantissa bytes for the hash probe to
// hit.
func TestBuildIndexAndLookup_Decimal128Key_ExactMatch(t *testing.T) {
	// mantissa 1234 -> "12.34"; 999999 -> "9999.99"; 100 -> "1.00".
	cfg := setupDecimalIndexTestFS(t, "cohort.pulse", []int64{1234, 999999, 100})
	svc := New(cfg)

	if _, err := svc.BuildIndex(context.Background(), "cohort.pulse", []string{"amount"}); err != nil {
		t.Fatalf("BuildIndex(amount): %v", err)
	}

	res, err := svc.Lookup(context.Background(), singleKeyLookupRequest("cohort.pulse", "amount", "12.34", []string{"id"}))
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("Rows = %d, want 1", len(res.Rows))
	}
	if got := res.Rows[0]["id"].(float64); got != 1.0 {
		t.Errorf("id = %v, want 1.0 (amount=12.34 -> row index 0)", got)
	}

	// A logically-equal literal at a DIFFERENT implicit scale ("12.3400")
	// must still hit — it rescales to the same mantissa 1234 before
	// encoding, proving the exact-bytes path is scale-normalizing, not
	// merely a raw-string comparison.
	res2, err := svc.Lookup(context.Background(), singleKeyLookupRequest("cohort.pulse", "amount", "12.3400", []string{"id"}))
	if err != nil {
		t.Fatalf("Lookup (rescaled literal): %v", err)
	}
	if got := res2.Rows[0]["id"].(float64); got != 1.0 {
		t.Errorf("id = %v, want 1.0 for rescaled-equal literal \"12.3400\"", got)
	}
}
