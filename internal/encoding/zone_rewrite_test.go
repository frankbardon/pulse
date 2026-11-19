package encoding

import (
	"bytes"
	stderrors "errors"
	"io"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// undeclaredZoneCohort is zoneCohortFile with no default declared: a
// flat 0x01 cohort whose `tz` field holds valid zones.
func undeclaredZoneCohort(t *testing.T) []byte {
	t.Helper()
	s := zoneCohortSchema()
	s.ZoneField = ""
	return zoneCohortFile(t, s)
}

// zonePreambleSplit returns the schema, version and bytes after data's preamble.
func zonePreambleSplit(t *testing.T, data []byte) (*encoding.Schema, byte, []byte) {
	t.Helper()
	r := bytes.NewReader(data)
	s, v, err := ReadPreamble(r)
	if err != nil {
		t.Fatalf("ReadPreamble: %v", err)
	}
	rest, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return s, v, rest
}

// TestSetZoneFieldBytes_SetThenClearByteIdentical: setting a default on
// a flat cohort moves it to 0x02 with the ZONE section and the record
// region verbatim; clearing it returns the ORIGINAL bytes exactly.
func TestSetZoneFieldBytes_SetThenClearByteIdentical(t *testing.T) {
	t.Parallel()

	orig := undeclaredZoneCohort(t)
	if orig[encoding.HeaderSize-1] != encoding.FormatVersionV1 {
		t.Fatalf("fixture version 0x%02x, want 0x01", orig[encoding.HeaderSize-1])
	}
	set, rep, err := SetZoneFieldBytes(orig, "tz")
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	if !rep.Changed || rep.Previous != "" || rep.Current != "tz" {
		t.Fatalf("set report %+v", rep)
	}
	gotSchema, v, gotRecords := zonePreambleSplit(t, set)
	_, _, origRecords := zonePreambleSplit(t, orig)
	if v != encoding.FormatVersionV2 || gotSchema.ZoneField != "tz" {
		t.Fatalf("set cohort: version 0x%02x zone %q, want 0x02 tz", v, gotSchema.ZoneField)
	}
	if !bytes.Equal(gotRecords, origRecords) {
		t.Fatal("set rewrote the record region")
	}
	// The set cohort is exactly what a writer declaring the default
	// produces.
	if want := zoneCohortFile(t, zoneCohortSchema()); !bytes.Equal(set, want) {
		t.Fatal("set cohort differs from a cohort written declaring the default")
	}
	cleared, rep, err := SetZoneFieldBytes(set, "")
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if !rep.Changed || rep.Previous != "tz" || rep.Current != "" {
		t.Fatalf("clear report %+v", rep)
	}
	if !bytes.Equal(cleared, orig) {
		t.Fatal("set then clear is not byte-identical to the original")
	}
}

// TestSetZoneFieldBytes_Unchanged: re-declaring the default a cohort
// already carries writes nothing and returns the input itself.
func TestSetZoneFieldBytes_Unchanged(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		data  []byte
		field string
	}{
		"same default": {zoneCohortFile(t, zoneCohortSchema()), "tz"},
		"clear none":   {undeclaredZoneCohort(t), ""},
	} {
		out, rep, err := SetZoneFieldBytes(tc.data, tc.field)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if rep.Changed || &out[0] != &tc.data[0] {
			t.Errorf("%s: changed=%v, or a copy was returned", name, rep.Changed)
		}
	}
}

// TestSetZoneFieldBytes_Refusals: the import rule and coding — a name
// resolving to no field or to a non-categorical one is SERVICE_VALIDATION
// details.option "zone_field" (details.zone_field kept); a dictionary
// entry that is no zone keeps PULSE_TIMEZONE_UNKNOWN.
func TestSetZoneFieldBytes_Refusals(t *testing.T) {
	t.Parallel()

	data := undeclaredZoneCohort(t)
	for _, field := range []string{"nope", "n", "at"} {
		_, _, err := SetZoneFieldBytes(data, field)
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != errors.SERVICE_VALIDATION ||
			ce.Details["option"] != "zone_field" || ce.Details[encoding.DetailZoneField] != field {
			t.Errorf("set %q: %v (details %v), want SERVICE_VALIDATION option zone_field", field, err, detailsOf(err))
		}
	}
	bad := zoneCohortSchema()
	bad.ZoneField = ""
	if _, err := bad.Fields[1].Dictionary.Add("Mars/Olympus"); err != nil {
		t.Fatal(err)
	}
	_, _, err := SetZoneFieldBytes(zoneCohortFile(t, bad), "tz")
	if !errors.HasCode(err, errors.PULSE_TIMEZONE_UNKNOWN) {
		t.Fatalf("bad entry: %v, want PULSE_TIMEZONE_UNKNOWN", err)
	}
}

func detailsOf(err error) map[string]any {
	var ce *errors.CodedError
	if stderrors.As(err, &ce) {
		return ce.Details
	}
	return nil
}

// TestSetZoneFieldFile_Atomic: a refusal or a failed rename leaves the
// cohort byte-identical and no temp file behind; success swaps it.
func TestSetZoneFieldFile_Atomic(t *testing.T) {
	t.Parallel()

	orig := undeclaredZoneCohort(t)
	base := afero.NewMemMapFs()
	if err := afero.WriteFile(base, "/d/c.pulse", orig, 0o644); err != nil {
		t.Fatal(err)
	}
	check := func(what string) {
		t.Helper()
		got, err := afero.ReadFile(base, "/d/c.pulse")
		if err != nil || !bytes.Equal(got, orig) {
			t.Fatalf("%s: cohort changed (%v)", what, err)
		}
		ents, _ := afero.ReadDir(base, "/d")
		if len(ents) != 1 {
			t.Fatalf("%s: %d files left in the directory, want 1", what, len(ents))
		}
	}
	if _, err := SetZoneFieldFile(base, "/d/c.pulse", "n"); !errors.HasCode(err, errors.SERVICE_VALIDATION) {
		t.Fatalf("refusal: %v", err)
	}
	check("refusal")
	if _, err := SetZoneFieldFile(faultFs{Fs: base, renameErr: stderrors.New("injected")}, "/d/c.pulse", "tz"); !errors.HasCode(err, errors.ENCODING_IO) {
		t.Fatalf("rename failure: %v, want ENCODING_IO", err)
	}
	check("rename failure")
	rep, err := SetZoneFieldFile(base, "/d/c.pulse", "tz")
	if err != nil || !rep.Changed {
		t.Fatalf("set: %+v, %v", rep, err)
	}
	got, _ := afero.ReadFile(base, "/d/c.pulse")
	if s, _, _ := zonePreambleSplit(t, got); s.ZoneField != "tz" {
		t.Fatalf("published cohort declares %q", s.ZoneField)
	}
	if _, err := SetZoneFieldFile(base, "/d/c.pulse", ""); err != nil {
		t.Fatal(err)
	}
	check("set then clear")
}

// TestValidateStructuralCohesion_ZoneField: shards of one archive must
// declare the same default zone field — "" against a default included.
func TestValidateStructuralCohesion_ZoneField(t *testing.T) {
	t.Parallel()

	declared := zoneCohortSchema()
	plain := zoneCohortSchema()
	plain.ZoneField = ""
	if _, err := ValidateStructuralCohesion(declared, zoneCohortSchema()); err != nil {
		t.Fatalf("agreeing shards refused: %v", err)
	}
	for name, pair := range map[string][2]*encoding.Schema{
		"declared vs none": {declared, plain},
		"none vs declared": {plain, declared},
	} {
		_, err := ValidateStructuralCohesion(pair[0], pair[1])
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_SHARD_SCHEMA_MISMATCH || ce.Details["dimension"] != "zone_field" {
			t.Errorf("%s: %v, want PULSE_SHARD_SCHEMA_MISMATCH dimension zone_field", name, err)
		}
	}
}

// TestSchemaClones_KeepZoneField: every schema-cloning helper in this
// package that a rewriter routes through keeps the default.
func TestSchemaClones_KeepZoneField(t *testing.T) {
	t.Parallel()

	s := zoneCohortSchema()
	enc, err := NewGroupEncoder(s, []GroupSpec{{Members: []string{"tz"}}})
	if err != nil {
		t.Fatal(err)
	}
	set := zoneCohortSchema()
	set.Fields[2].Type = encoding.FieldTypeSetU8
	set.Fields[2].Dictionary = encoding.NewDictionary()
	widened, err := WidenSchemaSetField(set, "n", encoding.FieldTypeSetU16)
	if err != nil {
		t.Fatal(err)
	}
	extended, _, err := MergeDictUnion(s, s)
	if err != nil {
		t.Fatal(err)
	}
	for name, got := range map[string]*encoding.Schema{
		"NewGroupEncoder":      enc.Schema(),
		"WidenSchemaSetField":  widened,
		"cloneSchema":          cloneSchema(s),
		"shareSchema":          shareSchema(s),
		"MergeDictUnion":       extended,
		"WithZoneField":        WithZoneField(s, "tz"),
		"Logical (public API)": enc.Schema().Logical(),
	} {
		if got.ZoneField != "tz" {
			t.Errorf("%s dropped the default zone field", name)
		}
	}
}
