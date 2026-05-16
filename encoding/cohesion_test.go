package encoding_test

import (
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// dictOf builds a fresh Dictionary preloaded with the given values
// in insertion order. Test helper only.
func dictOf(t *testing.T, values ...string) *encoding.Dictionary {
	t.Helper()
	d := encoding.NewDictionary()
	for _, v := range values {
		if _, err := d.Add(v); err != nil {
			t.Fatalf("dict add(%q): %v", v, err)
		}
	}
	return d
}

func baseSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0,
				Description: "primary id"},
			{Name: "country", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 4,
				CsvColumnIdx: 1, Description: "ISO country",
				Dictionary: dictOf(t, "US", "CA")},
		},
	}
}

// ---------- Structural cohesion ----------

func TestShardArchiveStructuralCohesion(t *testing.T) {
	t.Run("identical schemas pass with no warnings", func(t *testing.T) {
		canonical := baseSchema(t)
		incoming := baseSchema(t)
		warnings, err := encoding.ValidateStructuralCohesion(canonical, incoming)
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
		if len(warnings) != 0 {
			t.Errorf("expected no warnings, got %d", len(warnings))
		}
	})

	t.Run("different field count fails", func(t *testing.T) {
		canonical := baseSchema(t)
		incoming := &encoding.Schema{Fields: canonical.Fields[:1]}
		_, err := encoding.ValidateStructuralCohesion(canonical, incoming)
		assertCode(t, err, errors.PULSE_SHARD_SCHEMA_MISMATCH)
	})

	t.Run("same name different type fails", func(t *testing.T) {
		canonical := baseSchema(t)
		incoming := baseSchema(t)
		incoming.Fields[0].Type = encoding.FieldTypeU16
		_, err := encoding.ValidateStructuralCohesion(canonical, incoming)
		assertCode(t, err, errors.PULSE_SHARD_SCHEMA_MISMATCH)
	})

	t.Run("same type different offset fails", func(t *testing.T) {
		canonical := baseSchema(t)
		incoming := baseSchema(t)
		incoming.Fields[0].ByteOffset = 16
		_, err := encoding.ValidateStructuralCohesion(canonical, incoming)
		assertCode(t, err, errors.PULSE_SHARD_SCHEMA_MISMATCH)
	})

	t.Run("different categorical width fails", func(t *testing.T) {
		canonical := baseSchema(t)
		incoming := baseSchema(t)
		incoming.Fields[1].Type = encoding.FieldTypeCategoricalU16
		_, err := encoding.ValidateStructuralCohesion(canonical, incoming)
		assertCode(t, err, errors.PULSE_SHARD_SCHEMA_MISMATCH)
	})

	t.Run("different bit position fails", func(t *testing.T) {
		canonical := baseSchema(t)
		incoming := baseSchema(t)
		incoming.Fields[0].BitPosition = 3
		_, err := encoding.ValidateStructuralCohesion(canonical, incoming)
		assertCode(t, err, errors.PULSE_SHARD_SCHEMA_MISMATCH)
	})

	t.Run("different name fails", func(t *testing.T) {
		canonical := baseSchema(t)
		incoming := baseSchema(t)
		incoming.Fields[0].Name = "renamed"
		_, err := encoding.ValidateStructuralCohesion(canonical, incoming)
		assertCode(t, err, errors.PULSE_SHARD_SCHEMA_MISMATCH)
	})
}

func TestShardArchiveDescriptionDivergenceWarns(t *testing.T) {
	canonical := baseSchema(t)
	incoming := baseSchema(t)
	incoming.Fields[0].Description = "different but still valid"

	warnings, err := encoding.ValidateStructuralCohesion(canonical, incoming)
	if err != nil {
		t.Fatalf("expected no error for description divergence, got %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(warnings))
	}
	if warnings[0].Code != string(errors.PULSE_SHARD_DESCRIPTION_DIVERGENCE) {
		t.Errorf("warning code = %q, want %q",
			warnings[0].Code, errors.PULSE_SHARD_DESCRIPTION_DIVERGENCE)
	}
	if got := warnings[0].Details["field"]; got != "id" {
		t.Errorf("warning details[field] = %v, want \"id\"", got)
	}
}

// ---------- Dictionary prefix rule ----------

func TestShardArchiveDictPrefixRule(t *testing.T) {
	t.Run("incoming is prefix of canonical: accept, no change", func(t *testing.T) {
		canonical := baseSchema(t)
		canonical.Fields[1].Dictionary = dictOf(t, "US", "CA", "MX")
		incoming := baseSchema(t)
		incoming.Fields[1].Dictionary = dictOf(t, "US", "CA")

		got, err := encoding.ValidateDictPrefixRule(canonical, incoming)
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
		if got != canonical {
			t.Errorf("expected returned schema == canonical (no extension), got new schema")
		}
	})

	t.Run("canonical is prefix of incoming: returns extended schema", func(t *testing.T) {
		canonical := baseSchema(t)
		canonical.Fields[1].Dictionary = dictOf(t, "US", "CA")
		incoming := baseSchema(t)
		incoming.Fields[1].Dictionary = dictOf(t, "US", "CA", "MX", "BR")

		got, err := encoding.ValidateDictPrefixRule(canonical, incoming)
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
		if got == canonical {
			t.Fatalf("expected new schema returned for extended canonical, got input pointer")
		}
		extDict, ok := got.Categorical("country")
		if !ok {
			t.Fatalf("country missing from extended canonical")
		}
		if extDict.Count() != 4 {
			t.Errorf("extended dict count = %d, want 4", extDict.Count())
		}
		wantValues := []string{"US", "CA", "MX", "BR"}
		gotValues := extDict.Values()
		for i, v := range wantValues {
			if gotValues[i] != v {
				t.Errorf("extended dict[%d] = %q, want %q", i, gotValues[i], v)
			}
		}
		// Canonical input is untouched (caller is responsible for
		// publishing the extended copy).
		if canonical.Fields[1].Dictionary.Count() != 2 {
			t.Errorf("canonical mutated in-place; want immutability")
		}
	})

	t.Run("neither is prefix: divergence", func(t *testing.T) {
		canonical := baseSchema(t)
		canonical.Fields[1].Dictionary = dictOf(t, "US", "CA", "MX")
		incoming := baseSchema(t)
		incoming.Fields[1].Dictionary = dictOf(t, "US", "CA", "BR")

		_, err := encoding.ValidateDictPrefixRule(canonical, incoming)
		assertCode(t, err, errors.PULSE_SHARD_DICT_DIVERGENCE)
	})

	t.Run("equal dicts: accept, no change", func(t *testing.T) {
		canonical := baseSchema(t)
		incoming := baseSchema(t)
		got, err := encoding.ValidateDictPrefixRule(canonical, incoming)
		if err != nil {
			t.Fatalf("expected nil error for equal dicts, got %v", err)
		}
		if got != canonical {
			t.Errorf("equal dicts should leave canonical unchanged (pointer-equal)")
		}
	})
}

func TestShardArchiveDictWidthOverflow(t *testing.T) {
	// Build a u8 field where the incoming dictionary extends beyond
	// the u8 capacity of 256 entries.
	canonical := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "code", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0,
				CsvColumnIdx: 0, Dictionary: dictOf(t, "A")},
		},
	}
	incomingDict := encoding.NewDictionary()
	for i := 0; i < 257; i++ {
		if _, err := incomingDict.Add(stringOfIndex(i)); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	// Ensure canonical is a prefix — first entry matches.
	incomingDict = encoding.NewDictionary()
	if _, err := incomingDict.Add("A"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	for i := 1; i < 257; i++ {
		if _, err := incomingDict.Add(stringOfIndex(i)); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	incoming := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "code", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0,
				CsvColumnIdx: 0, Dictionary: incomingDict},
		},
	}
	_, err := encoding.ValidateDictPrefixRule(canonical, incoming)
	assertCode(t, err, errors.PULSE_SHARD_DICT_WIDTH_OVERFLOW)
}

// ---------- helpers ----------

func assertCode(t *testing.T, err error, want errors.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error %q, got nil", want)
	}
	coded, ok := err.(*errors.CodedError)
	if !ok {
		t.Fatalf("expected *errors.CodedError, got %T: %v", err, err)
	}
	if coded.Code != want {
		t.Errorf("error code = %q, want %q (msg=%q)", coded.Code, want, coded.Message)
	}
}

// stringOfIndex returns a deterministic short string per index for
// dictionary-fill tests.
func stringOfIndex(i int) string {
	// Use a base-26 letter sequence with a numeric tail to keep entries
	// unique and short. Good enough for "fill the dictionary" tests.
	const letters = "abcdefghijklmnopqrstuvwxyz"
	a := letters[i%26]
	b := letters[(i/26)%26]
	c := letters[(i/(26*26))%26]
	return string([]byte{a, b, c})
}
