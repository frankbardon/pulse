package encoding_test

import (
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
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

func TestShardArchiveStructuralCohesion(t *testing.T) {
	t.Run("identical schemas pass with no warnings", func(t *testing.T) {
		canonical := baseSchema(t)
		incoming := baseSchema(t)
		warnings, err := encx.ValidateStructuralCohesion(canonical, incoming)
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
		_, err := encx.ValidateStructuralCohesion(canonical, incoming)
		assertCode(t, err, errors.PULSE_SHARD_SCHEMA_MISMATCH)
	})

	t.Run("same name different type fails", func(t *testing.T) {
		canonical := baseSchema(t)
		incoming := baseSchema(t)
		incoming.Fields[0].Type = encoding.FieldTypeU16
		_, err := encx.ValidateStructuralCohesion(canonical, incoming)
		assertCode(t, err, errors.PULSE_SHARD_SCHEMA_MISMATCH)
	})

	// #373: the stored ByteOffset is a cache of the type-derived
	// layout, so a divergent stored offset is admitted — legacy cohorts
	// (all-zero offsets, pre-canonical schema-file offsets) decode
	// identically and must interoperate.
	t.Run("same type different stored offset passes", func(t *testing.T) {
		canonical := baseSchema(t)
		incoming := baseSchema(t)
		incoming.Fields[0].ByteOffset = 16
		incoming.Fields[1].ByteOffset = 0
		if _, err := encx.ValidateStructuralCohesion(canonical, incoming); err != nil {
			t.Fatalf("stored-offset divergence refused: %v", err)
		}
	})

	t.Run("different categorical width fails", func(t *testing.T) {
		canonical := baseSchema(t)
		incoming := baseSchema(t)
		incoming.Fields[1].Type = encoding.FieldTypeCategoricalU16
		_, err := encx.ValidateStructuralCohesion(canonical, incoming)
		assertCode(t, err, errors.PULSE_SHARD_SCHEMA_MISMATCH)
	})

	t.Run("different bit position fails", func(t *testing.T) {
		canonical := baseSchema(t)
		incoming := baseSchema(t)
		incoming.Fields[0].BitPosition = 3
		_, err := encx.ValidateStructuralCohesion(canonical, incoming)
		assertCode(t, err, errors.PULSE_SHARD_SCHEMA_MISMATCH)
	})

	t.Run("different name fails", func(t *testing.T) {
		canonical := baseSchema(t)
		incoming := baseSchema(t)
		incoming.Fields[0].Name = "renamed"
		_, err := encx.ValidateStructuralCohesion(canonical, incoming)
		assertCode(t, err, errors.PULSE_SHARD_SCHEMA_MISMATCH)
	})
}

func TestShardArchiveDescriptionDivergenceWarns(t *testing.T) {
	canonical := baseSchema(t)
	incoming := baseSchema(t)
	incoming.Fields[0].Description = "different but still valid"

	warnings, err := encx.ValidateStructuralCohesion(canonical, incoming)
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

func TestShardArchiveDictPrefixRule(t *testing.T) {
	t.Run("incoming is prefix of canonical: accept, no change", func(t *testing.T) {
		canonical := baseSchema(t)
		canonical.Fields[1].Dictionary = dictOf(t, "US", "CA", "MX")
		incoming := baseSchema(t)
		incoming.Fields[1].Dictionary = dictOf(t, "US", "CA")

		got, err := encx.ValidateDictPrefixRule(canonical, incoming)
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

		got, err := encx.ValidateDictPrefixRule(canonical, incoming)
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

		_, err := encx.ValidateDictPrefixRule(canonical, incoming)
		assertCode(t, err, errors.PULSE_SHARD_DICT_DIVERGENCE)
	})

	t.Run("equal dicts: accept, no change", func(t *testing.T) {
		canonical := baseSchema(t)
		incoming := baseSchema(t)
		got, err := encx.ValidateDictPrefixRule(canonical, incoming)
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
	_, err := encx.ValidateDictPrefixRule(canonical, incoming)
	assertCode(t, err, errors.PULSE_SHARD_DICT_WIDTH_OVERFLOW)
}

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

// vecCohesionSchema: a vector in the middle and one at the end, laid out
// canonically for the given dims.
func vecCohesionSchema(midDim, lastDim int) *encoding.Schema {
	s := &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32},
		{Name: "mid", Type: encoding.FieldTypeVecF32},
		{Name: "n", Type: encoding.FieldTypeU8},
		{Name: "last", Type: encoding.FieldTypeVecF64, Nullable: true},
	}, Vectors: []encoding.VectorField{
		{FieldIndex: 1, Dim: midDim, Labels: nil},
		{FieldIndex: 3, Dim: lastDim, Kind: encoding.VectorKindScale, HasScale: true, ScaleMin: 1, ScaleMax: 5},
	}}
	encx.LayoutFields(s)
	return s
}

// TestShardArchiveStructuralCohesion_Vectors: the dim is strict at the
// vec field itself (details.dimension vector_dim) — including the
// TRAILING vector, where no later offset moves — the element type is
// the type byte, and labels / kind / scale are description-class
// warnings.
func TestShardArchiveStructuralCohesion_Vectors(t *testing.T) {
	dimOf := func(err error) any {
		t.Helper()
		assertCode(t, err, errors.PULSE_SHARD_SCHEMA_MISMATCH)
		return err.(*errors.CodedError).Details["dimension"]
	}
	if _, err := encx.ValidateStructuralCohesion(vecCohesionSchema(2, 3), vecCohesionSchema(2, 4)); dimOf(err) != "vector_dim" {
		t.Fatalf("trailing dim mismatch: %v", err)
	}
	if _, err := encx.ValidateStructuralCohesion(vecCohesionSchema(2, 3), vecCohesionSchema(5, 3)); dimOf(err) != "vector_dim" {
		t.Fatalf("middle dim mismatch must name the vector, not the next offset: %v", err)
	}
	f32 := vecCohesionSchema(2, 6)
	f32.Fields[3].Type = encoding.FieldTypeVecF32 // 6 x 4 == 3 x 8: same stride
	if _, err := encx.ValidateStructuralCohesion(vecCohesionSchema(2, 3), f32); err == nil {
		t.Fatal("vec_f64 x 3 vs vec_f32 x 6 (equal stride) accepted")
	}

	inc := vecCohesionSchema(2, 3)
	inc.Vectors[0].Labels = []string{"p", "q"}
	inc.Vectors[1].Kind = encoding.VectorKindMeasure
	inc.Vectors[1].HasScale = false
	warnings, err := encx.ValidateStructuralCohesion(vecCohesionSchema(2, 3), inc)
	if err != nil {
		t.Fatalf("metadata divergence refused: %v", err)
	}
	got := map[string]bool{}
	for _, w := range warnings {
		if w.Code != string(errors.PULSE_SHARD_DESCRIPTION_DIVERGENCE) {
			t.Fatalf("unexpected warning code %s", w.Code)
		}
		got[w.Details["field"].(string)+"/"+w.Details["dimension"].(string)] = true
	}
	for _, k := range []string{"mid/vector_labels", "last/vector_kind", "last/vector_scale"} {
		if !got[k] {
			t.Fatalf("missing %s warning; got %v", k, got)
		}
	}
	if len(warnings) != 3 {
		t.Fatalf("warnings = %d, want 3: %+v", len(warnings), warnings)
	}
	if w, err := encx.ValidateStructuralCohesion(vecCohesionSchema(2, 3), vecCohesionSchema(2, 3)); err != nil || len(w) != 0 {
		t.Fatalf("identical vector schemas: warnings %v err %v", w, err)
	}
}

// nullCohesionSchema is a three-field flat schema whose per-field
// nullability the caller chooses; layout is the canonical one.
func nullCohesionSchema(idNull, scoreNull, tagNull bool) *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, Nullable: idNull},
		{Name: "score", Type: encoding.FieldTypeF64, ByteOffset: 4, Nullable: scoreNull},
		{Name: "flag", Type: encoding.FieldTypeU8, ByteOffset: 12, Nullable: tagNull},
	}}
}

// TestShardArchiveStructuralCohesion_Nullability pins the per-field
// nullability rule (#207). An archive field that is NOT nullable can
// never take a shard that declares it nullable: every shard decodes
// under the canonical flags, so the shard's null bits would be ignored
// and a null would read back as its placeholder value. The reverse — a
// nullable archive field, a non-nullable shard field — is admitted only
// when the null bitmaps are the same width (so the strides agree and the
// field's bit, which every writer leaves 0 for a non-nullable field,
// reads as non-null), and never for a parent-group member, whose null
// bits ride the group entry.
func TestShardArchiveStructuralCohesion_Nullability(t *testing.T) {
	for _, tc := range []struct {
		name                string
		canonical, incoming *encoding.Schema
		wantRefused         bool
	}{
		{"identical nullability", nullCohesionSchema(false, true, false), nullCohesionSchema(false, true, false), false},
		{"non-nullable archive, nullable shard, bitmap width equal",
			nullCohesionSchema(false, true, false), nullCohesionSchema(true, true, false), true},
		{"non-nullable archive, nullable shard, bitmap appears",
			nullCohesionSchema(false, false, false), nullCohesionSchema(true, false, false), true},
		{"nullable archive, non-nullable shard, bitmap kept",
			nullCohesionSchema(true, true, false), nullCohesionSchema(false, true, false), false},
		{"nullable archive, non-nullable shard, bitmap dropped",
			nullCohesionSchema(true, false, false), nullCohesionSchema(false, false, false), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := encx.ValidateStructuralCohesion(tc.canonical, tc.incoming)
			if !tc.wantRefused {
				if err != nil {
					t.Fatalf("want admitted, got %v", err)
				}
				return
			}
			assertCode(t, err, errors.PULSE_SHARD_SCHEMA_MISMATCH)
			coded := err.(*errors.CodedError)
			if coded.Details["dimension"] != "nullable" || coded.Details["field"] != "id" {
				t.Fatalf("details = %v, want dimension nullable on field id", coded.Details)
			}
		})
	}
}

// TestShardArchiveStructuralCohesion_NullabilityGroupMember: a group
// member's null bits ride its group's dictionary entry, whose geometry
// the member nullability decides, so neither direction is admitted for
// a member even when the row bitmaps agree.
func TestShardArchiveStructuralCohesion_NullabilityGroupMember(t *testing.T) {
	grouped := func(memberNull bool) *encoding.Schema {
		s := nullCohesionSchema(false, true, memberNull)
		s.Groups = []encoding.Group{{Kind: encoding.GroupKindIndexed, Members: []encoding.GroupMember{{Field: 2}}}}
		return s
	}
	for _, dir := range []struct {
		name           string
		canonNull, inc bool
	}{{"member becomes nullable", false, true}, {"member becomes non-nullable", true, false}} {
		t.Run(dir.name, func(t *testing.T) {
			_, err := encx.ValidateStructuralCohesion(grouped(dir.canonNull), grouped(dir.inc))
			assertCode(t, err, errors.PULSE_SHARD_SCHEMA_MISMATCH)
			if d := err.(*errors.CodedError).Details["dimension"]; d != "nullable" {
				t.Fatalf("dimension = %v, want nullable", d)
			}
		})
	}
}
