package synth_test

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/synth"
)

// buildSetFieldCohort emits a minimal valid single-file .pulse cohort
// with one set_u8 field named "channels" whose dictionary is exactly
// options, in order. Row r selects option i iff r < counts[i] — a
// deterministic (not probabilistic) assignment so the resulting
// per-option selection frequency is an EXACT rational (counts[i] /
// rowCount), letting the acceptance test assert hand-computed ground
// truth rather than a statistically-tolerant approximation.
func buildSetFieldCohort(t *testing.T, options []string, counts []int, rowCount int) []byte {
	t.Helper()
	if len(options) != len(counts) {
		t.Fatalf("options/counts length mismatch: %d vs %d", len(options), len(counts))
	}
	dict := encoding.NewDictionary()
	for _, v := range options {
		if _, err := dict.Add(v); err != nil {
			t.Fatalf("dict.Add(%q): %v", v, err)
		}
	}
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "channels", Type: encoding.FieldTypeSetU8, ByteOffset: 0, Dictionary: dict},
		},
	}
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	for r := 0; r < rowCount; r++ {
		var mask byte
		for i, c := range counts {
			if r < c {
				mask |= 1 << uint(i)
			}
		}
		buf.WriteByte(mask)
	}
	return buf.Bytes()
}

// TestProfile_SetFieldMarginalFrequencies is E5-S1's non-negotiable
// acceptance bar: a "select all that apply" fixture (6 options, wildly
// uneven selection rates) must profile without error and produce
// per-option frequencies matching the fixture's known ground truth
// exactly — not a smoke test that capture merely doesn't crash.
func TestProfile_SetFieldMarginalFrequencies(t *testing.T) {
	options := []string{"email", "sms", "push", "mail", "phone", "survey"}
	// Deliberately uneven: some options selected far more often than
	// others, per the story's fixture requirement.
	counts := []int{950, 500, 300, 100, 50, 10}
	const rowCount = 1000

	data := buildSetFieldCohort(t, options, counts, rowCount)

	prof, err := synth.ProfileBytes(data, synth.ProfileOptions{})
	if err != nil {
		t.Fatalf("ProfileBytes: %v", err)
	}
	if len(prof.Fields) != 1 {
		t.Fatalf("expected exactly one field profile, got %d", len(prof.Fields))
	}
	fp := prof.Fields[0]

	// Regression guard for the bug this story fixes: a set_* field must
	// never be silently mis-classified as numeric or categorical.
	if fp.Numeric != nil {
		t.Errorf("set_u8 field must not populate Numeric (silent bitmask-as-scalar corruption), got %+v", fp.Numeric)
	}
	if fp.Categorical != nil {
		t.Errorf("set_u8 field must not populate Categorical, got %+v", fp.Categorical)
	}
	if fp.NullRate != 0 {
		t.Errorf("NullRate = %v, want 0 (no nulls in this fixture)", fp.NullRate)
	}

	if fp.Set == nil {
		t.Fatal("expected Set profile to be populated for a set_u8 field")
	}
	if fp.Set.N != rowCount {
		t.Errorf("Set.N = %d, want %d (no nulls)", fp.Set.N, rowCount)
	}
	if len(fp.Set.Options) != len(options) {
		t.Fatalf("expected %d options, got %d: %+v", len(options), len(fp.Set.Options), fp.Set.Options)
	}
	for i, opt := range fp.Set.Options {
		if opt.Value != options[i] {
			t.Errorf("option[%d].Value = %q, want %q (options must be in dictionary/bit order)", i, opt.Value, options[i])
		}
		if opt.Count != counts[i] {
			t.Errorf("option %q Count = %d, want %d", opt.Value, opt.Count, counts[i])
		}
		wantFreq := float64(counts[i]) / float64(rowCount)
		if math.Abs(opt.Frequency-wantFreq) > 1e-9 {
			t.Errorf("option %q Frequency = %v, want %v (ground truth)", opt.Value, opt.Frequency, wantFreq)
		}
	}
}

// TestProfile_SetFieldNullRowsExcludedFromFrequencyDenominator asserts
// null rows are counted toward NullRate but excluded from Set.N and
// every option's Frequency denominator — mirroring how Numeric and
// Categorical marginal profiling already treat nulls.
func TestProfile_SetFieldNullRowsExcludedFromFrequencyDenominator(t *testing.T) {
	dict := encoding.NewDictionary()
	for _, v := range []string{"a", "b", "c"} {
		if _, err := dict.Add(v); err != nil {
			t.Fatalf("dict.Add: %v", err)
		}
	}
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "tags", Type: encoding.FieldTypeSetU8, ByteOffset: 0, Nullable: true, Dictionary: dict},
		},
	}
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	// 8 non-null rows with bit 0 ("a") always set.
	for i := 0; i < 8; i++ {
		buf.WriteByte(0b001)      // data byte: option "a" selected
		buf.WriteByte(0b00000000) // bitmap: field 0 not null
	}
	// 2 null rows.
	for i := 0; i < 2; i++ {
		buf.WriteByte(0x00)       // data byte: irrelevant, overridden by bitmap
		buf.WriteByte(0b00000001) // bitmap: field 0 is null
	}

	prof, err := synth.ProfileBytes(buf.Bytes(), synth.ProfileOptions{})
	if err != nil {
		t.Fatalf("ProfileBytes: %v", err)
	}
	if len(prof.Fields) != 1 {
		t.Fatalf("expected exactly one field profile, got %d", len(prof.Fields))
	}
	fp := prof.Fields[0]
	if fp.NullRate != 0.2 {
		t.Errorf("NullRate = %v, want 0.2 (2 of 10 rows null)", fp.NullRate)
	}
	if fp.Set == nil {
		t.Fatal("expected Set profile to be populated")
	}
	if fp.Set.N != 8 {
		t.Errorf("Set.N = %d, want 8 (null rows excluded)", fp.Set.N)
	}
	for _, opt := range fp.Set.Options {
		if opt.Value == "a" {
			if opt.Count != 8 || opt.Frequency != 1.0 {
				t.Errorf("option a: Count=%d Frequency=%v, want Count=8 Frequency=1.0", opt.Count, opt.Frequency)
			}
		} else if opt.Count != 0 || opt.Frequency != 0 {
			t.Errorf("option %q: Count=%d Frequency=%v, want 0/0 (never selected)", opt.Value, opt.Count, opt.Frequency)
		}
	}
}

// TestProfile_SetFieldExcludedFromConditionalNumericPairs is the
// downstream-corruption regression this story's fix targets: before
// the fix, a set_* field silently fell into the numeric accumulator and
// would have been swept into --conditional's numeric-numeric pair
// capture (jointFieldNames) alongside real numeric fields, corrupting
// any pair the set field's name accidentally sorted into. With the
// fix, a set_* field must never appear in Conditional.NumericPairs.
func TestProfile_SetFieldExcludedFromConditionalNumericPairs(t *testing.T) {
	dict := encoding.NewDictionary()
	for _, v := range []string{"x", "y", "z"} {
		if _, err := dict.Add(v); err != nil {
			t.Fatalf("dict.Add: %v", err)
		}
	}
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "amount", Type: encoding.FieldTypeF64, ByteOffset: 0},
			{Name: "quantity", Type: encoding.FieldTypeF64, ByteOffset: 8},
			{Name: "channels", Type: encoding.FieldTypeSetU8, ByteOffset: 16, Dictionary: dict},
		},
	}
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	const rowCount = 200
	for r := 0; r < rowCount; r++ {
		amount := float64(r)
		quantity := float64(r) * 2 // perfectly correlated with amount
		var rec [17]byte
		binary.LittleEndian.PutUint64(rec[0:8], math.Float64bits(amount))
		binary.LittleEndian.PutUint64(rec[8:16], math.Float64bits(quantity))
		rec[16] = byte(r % 8) // arbitrary varying mask, must never be read as a numeric value
		buf.Write(rec[:])
	}

	prof, err := synth.ProfileBytes(buf.Bytes(), synth.ProfileOptions{IncludeConditional: true})
	if err != nil {
		t.Fatalf("ProfileBytes: %v", err)
	}
	if prof.Conditional == nil {
		t.Fatal("expected Conditional section to be populated")
	}
	if len(prof.Conditional.NumericPairs) != 1 {
		t.Fatalf("expected exactly one numeric-numeric pair (amount x quantity), got %d: %+v",
			len(prof.Conditional.NumericPairs), prof.Conditional.NumericPairs)
	}
	pair := prof.Conditional.NumericPairs[0]
	if pair.A == "channels" || pair.B == "channels" {
		t.Fatalf("set_* field leaked into numeric-numeric conditional pair capture: %+v", pair)
	}
	if pair.N != rowCount {
		t.Errorf("pair.N = %d, want %d", pair.N, rowCount)
	}

	// The set field must still get its own correct marginal profile,
	// unaffected by living alongside numeric fields under --conditional.
	var setField *synth.FieldProfile
	for i := range prof.Fields {
		if prof.Fields[i].Name == "channels" {
			setField = &prof.Fields[i]
		}
	}
	if setField == nil {
		t.Fatal("expected a field profile for channels")
	}
	if setField.Numeric != nil {
		t.Errorf("channels must not have a Numeric profile, got %+v", setField.Numeric)
	}
	if setField.Set == nil {
		t.Fatal("expected channels to have a Set profile")
	}
}
