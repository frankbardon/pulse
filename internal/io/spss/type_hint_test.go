package spss

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/internal/io"
	"github.com/frankbardon/pulse/internal/spsstest"
	"github.com/spf13/afero"
)

// importSavReport imports a `.sav` and returns the cohort bytes and the
// source warnings the mapping raised.
func importSavReport(t *testing.T, fs afero.Fs, sav, cohort string) ([]byte, []*perr.CodedError) {
	t.Helper()
	job := pio.NewImportJob(NewReader(fs, sav), cohort)
	job.FS = fs
	rep, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("importing %s: %v", sav, err)
	}
	out, err := afero.ReadFile(fs, cohort)
	if err != nil {
		t.Fatal(err)
	}
	return out, rep.SourceWarnings
}

func dec(t *testing.T, s string) encoding.Decimal128 {
	t.Helper()
	d, _, err := encoding.ParseDecimal128(s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func notRestored(warns []*perr.CodedError) []*perr.CodedError {
	var out []*perr.CodedError
	for _, w := range warns {
		if w.Code == perr.PULSE_SPSS_TYPE_NOT_RESTORED {
			out = append(out, w)
		}
	}
	return out
}

// TestTypeHint_RoundTripRestoresEveryType: a native cohort exported to
// `.sav` and imported back is the SAME cohort, byte for byte, for every field
// type the synthesised export writes as one variable — each integer width at
// its ceiling, f32, packed_bool, decimal128 at two shapes, date, datetime and
// all three categorical rungs, nullable and not. Before the record 7/18 hint
// every numeric came back f64 and a wide categorical came back
// categorical_u8.
func TestTypeHint_RoundTripRestoresEveryType(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		fld  encoding.Field
		rows []any
	}{
		{"u4", encoding.Field{Type: encoding.FieldTypeU4}, []any{uint64(0), uint64(15), uint64(7)}},
		{"u8", encoding.Field{Type: encoding.FieldTypeU8, Nullable: true}, []any{uint64(255), nil, uint64(0)}},
		{"u16", encoding.Field{Type: encoding.FieldTypeU16}, []any{uint64(65535), uint64(1), uint64(1000000 % 65536)}},
		{"u32", encoding.Field{Type: encoding.FieldTypeU32}, []any{uint64(4294967295), uint64(1234567), uint64(0)}},
		{"u64", encoding.Field{Type: encoding.FieldTypeU64, Nullable: true}, []any{uint64(1<<53 - 1), uint64(10000000), nil}},
		{"f32", encoding.Field{Type: encoding.FieldTypeF32}, []any{float32(0.1), float32(-3.25e7), float32(1e-20)}},
		{"f64", encoding.Field{Type: encoding.FieldTypeF64}, []any{0.1, -2.5, 1e300}},
		{"packed_bool", encoding.Field{Type: encoding.FieldTypePackedBool, Nullable: true}, []any{true, false, nil}},
		{"decimal128_10_2", encoding.Field{Type: encoding.FieldTypeDecimal128, Precision: 10, Scale: 2, Nullable: true},
			[]any{dec(t, "12345678.91"), nil, dec(t, "-0.05")}},
		{"decimal128_20_4", encoding.Field{Type: encoding.FieldTypeDecimal128, Precision: 20, Scale: 4},
			[]any{dec(t, "1234567890.1234"), dec(t, "0.0001"), dec(t, "0.0000")}},
		{"date", encoding.Field{Type: encoding.FieldTypeDate}, []any{int32(-5000), int32(19000), int32(0)}},
		{"datetime", encoding.Field{Type: encoding.FieldTypeDateTime, Nullable: true}, []any{int64(-946684783), int64(1700000000), nil}},
		{"categorical_u8", encoding.Field{Type: encoding.FieldTypeCategoricalU8}, []any{"north", "south", "north"}},
		{"categorical_u16", encoding.Field{Type: encoding.FieldTypeCategoricalU16, Nullable: true}, []any{"a", nil, "b"}},
		{"categorical_u32", encoding.Field{Type: encoding.FieldTypeCategoricalU32}, []any{"x", "y", "z"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fld := tc.fld
			fld.Name, fld.Description = "v", "A measured value"
			schema := &encoding.Schema{Fields: []encoding.Field{
				{Name: "id", Type: encoding.FieldTypeF64, Description: "Row id"}, fld,
			}}
			rows := make([][]any, len(tc.rows))
			for i, v := range tc.rows {
				rows[i] = []any{float64(i), v}
			}
			fs, cohort := vecCohort(t, schema, rows)
			sav, _, err := exportVecSav(t, fs, cohort, WriterOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if err := afero.WriteFile(fs, "hint.sav", sav, 0o644); err != nil {
				t.Fatal(err)
			}
			back, warns := importSavReport(t, fs, "hint.sav", "back.pulse")
			if w := notRestored(warns); len(w) > 0 {
				t.Errorf("unexpected %v", w)
			}
			got := cohortSchema(t, fs, "back.pulse").Fields[1]
			if got.Type != tc.fld.Type || got.Precision != tc.fld.Precision || got.Scale != tc.fld.Scale {
				t.Errorf("re-imported type = %s(%d,%d), want %s(%d,%d)",
					got.Type, got.Precision, got.Scale, tc.fld.Type, tc.fld.Precision, tc.fld.Scale)
			}
			orig, err := afero.ReadFile(fs, cohort)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(orig, back) {
				diffSchemas(t, cohortSchema(t, fs, cohort), cohortSchema(t, fs, "back.pulse"))
				t.Errorf("the re-imported cohort is not the exported one: %d bytes, want %d", len(back), len(orig))
			}
		})
	}
}

// TestTypeHint_U64PastExactRangeIsNotRounded: a u64 at or past 2^53 has no
// exact double, so the value in the `.sav` may not be the one exported. The
// import refuses to restore u64 from it — f64 plus a coded warning naming the
// value — rather than writing a rounded integer under the original type.
func TestTypeHint_U64PastExactRangeIsNotRounded(t *testing.T) {
	t.Parallel()

	for _, big := range []uint64{1 << 53, 1<<53 + 1, 1<<63 + 12345} {
		fs, cohort := vecCohort(t, &encoding.Schema{Fields: []encoding.Field{
			{Name: "key", Type: encoding.FieldTypeU64, Description: "Account key"},
		}}, [][]any{{uint64(7)}, {big}})
		sav, _, err := exportVecSav(t, fs, cohort, WriterOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if err := afero.WriteFile(fs, "big.sav", sav, 0o644); err != nil {
			t.Fatal(err)
		}
		_, warns := importSavReport(t, fs, "big.sav", "back.pulse")
		if got := cohortSchema(t, fs, "back.pulse").Fields[0].Type; got != encoding.FieldTypeF64 {
			t.Errorf("%d: re-imported type = %s, want f64", big, got)
		}
		w := notRestored(warns)
		if len(w) != 1 || w[0].Details[perr.DetailSPSSPulseType] != "u64" || w[0].Details[perr.DetailSPSSVariable] != "key" {
			t.Errorf("%d: warnings = %v, want one PULSE_SPSS_TYPE_NOT_RESTORED for key/u64", big, w)
		}
	}
}

// hintedSpec is a third-party-shaped `.sav` whose one numeric variable
// carries the given record 7/18 text.
func hintedSpec(attrs string, values ...spsstest.Value) spsstest.Spec {
	cases := make([][]spsstest.Value, len(values))
	for i, v := range values {
		cases[i] = []spsstest.Value{v, spsstest.Text("x")}
	}
	return spsstest.Spec{
		Vars: []spsstest.Var{
			{Name: "AGE", Label: "Age in years", Print: spsstest.Format{Type: spsstest.FormatF, Width: 8, Decimals: 2}},
			{Name: "TAG", Label: "Tag", Width: 4},
		},
		Cases:             cases,
		VarAttributes:     attrs,
		CharacterEncoding: "UTF-8",
	}
}

func importSpec(t *testing.T, spec spsstest.Spec) (*encoding.Schema, []*perr.CodedError, []byte) {
	t.Helper()
	s, warns, out, _ := importSpecFS(t, spec)
	return s, warns, out
}

func importSpecFS(t *testing.T, spec spsstest.Spec) (*encoding.Schema, []*perr.CodedError, []byte, afero.Fs) {
	t.Helper()
	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "s.sav", build(t, spec), 0o644); err != nil {
		t.Fatal(err)
	}
	out, warns := importSavReport(t, fs, "s.sav", "s.pulse")
	return cohortSchema(t, fs, "s.pulse"), warns, out, fs
}

// TestTypeHint_ValueThatDoesNotFitFallsBackToF64: a hint the data does not
// fit — a hand-edited `.sav` — never truncates. The variable maps f64 and one
// PULSE_SPSS_TYPE_NOT_RESTORED names the variable, the hint and the first
// value that did not fit.
func TestTypeHint_ValueThatDoesNotFitFallsBackToF64(t *testing.T) {
	t.Parallel()

	cases := []struct {
		hint  string
		value float64
	}{
		{"u8", 2.5},
		{"u8", 256},
		{"u4", 16},
		{"u16", -1},
		{"u32", 4294967296},
		{"packed_bool", 2},
		{"f32", 0.1},
		{"decimal128(5,2)", 1.234},
		{"decimal128(5,2)", 1234.5},
	}
	for _, tc := range cases {
		spec := hintedSpec("AGE:@PulseType('"+tc.hint+"'\n)", spsstest.Num(1), spsstest.SysMis(), spsstest.Num(tc.value))
		s, warns, _, fs := importSpecFS(t, spec)
		if got := s.Fields[0].Type; got != encoding.FieldTypeF64 {
			t.Errorf("%s / %v: type = %s, want f64", tc.hint, tc.value, got)
		}
		// The fallback column holds the file's doubles, not a value
		// rendered under the refused type.
		if rows := cohortRows(t, fs, "s.pulse"); len(rows) != 3 || rows[2][0] != formatNumericValue(tc.value) {
			t.Errorf("%s / %v: rows = %v, want the stored double", tc.hint, tc.value, rows)
		}
		w := notRestored(warns)
		if len(w) != 1 || w[0].Details[perr.DetailSPSSPulseType] != tc.hint ||
			w[0].Details[perr.DetailSPSSValue] != formatNumericValue(tc.value) {
			t.Errorf("%s / %v: warnings = %v", tc.hint, tc.value, w)
		}
	}

	// The same file with a value that fits restores the type, so the
	// fallback above is the value's doing and not the fixture's.
	s, warns, _ := importSpec(t, hintedSpec("AGE:@PulseType('u8'\n)", spsstest.Num(1), spsstest.SysMis(), spsstest.Num(200)))
	if s.Fields[0].Type != encoding.FieldTypeU8 || !s.Fields[0].Nullable || len(notRestored(warns)) > 0 {
		t.Errorf("fitting values: type %s nullable %v warnings %v, want nullable u8 and none",
			s.Fields[0].Type, s.Fields[0].Nullable, warns)
	}
}

// TestTypeHint_HintTheVariableCannotCarryWarns: an unreadable hint, or one
// naming a type the variable's SPSS shape cannot carry, is reported and
// otherwise ignored.
func TestTypeHint_HintTheVariableCannotCarryWarns(t *testing.T) {
	t.Parallel()

	cases := []struct {
		attrs string
		field int
		want  encoding.FieldType
	}{
		{"AGE:@PulseType('u9'\n)", 0, encoding.FieldTypeF64},
		{"AGE:@PulseType('set_u8'\n)", 0, encoding.FieldTypeF64},
		{"AGE:@PulseType('categorical_u16'\n)", 0, encoding.FieldTypeF64},
		{"AGE:@PulseType('u8'\n'u16'\n)", 0, encoding.FieldTypeF64},
		{"TAG:@PulseType('u8'\n)", 1, encoding.FieldTypeCategoricalU8},
		{"TAG:@PulseType('date'\n)", 1, encoding.FieldTypeCategoricalU8},
	}
	for _, tc := range cases {
		s, warns, _ := importSpec(t, hintedSpec(tc.attrs, spsstest.Num(1), spsstest.Num(2)))
		if got := s.Fields[tc.field].Type; got != tc.want {
			t.Errorf("%q: type = %s, want %s", tc.attrs, got, tc.want)
		}
		if w := notRestored(warns); len(w) != 1 {
			t.Errorf("%q: warnings = %v, want one PULSE_SPSS_TYPE_NOT_RESTORED", tc.attrs, w)
		}
	}
}

// TestTypeHint_ThirdPartyFilesImportUnchanged pins two hint-free fixtures —
// one carrying ordinary record 7/17 and 7/18 attributes — to the cohort
// digests they imported to before type hints existed.
func TestTypeHint_ThirdPartyFilesImportUnchanged(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		spec spsstest.Spec
		want string
	}{
		"kitchen-sink": {kitchenSinkSpec(), "31f8428637ea250892fea533abac72a042fc854d81a2d8b156f54e09fa89ff46"},
		"extension":    {spsstest.ExtensionReferenceSpec(), "c01d7241339db0e48473a88a40bf9db86e3c4abdb0535a850626d1c96bd898dc"},
	} {
		_, warns, out := importSpec(t, tc.spec)
		if got := digest(out); got != tc.want {
			t.Errorf("%s: cohort digest = %s, want the pre-hint %s", name, got, tc.want)
		}
		if w := notRestored(warns); len(w) > 0 {
			t.Errorf("%s: unexpected %v", name, w)
		}
	}
}

// TestTypeHint_SidecarPathOwnsTheAttribute: on a re-export through the
// metadata sidecar, the source's verbatim record 7/18 keeps every other
// attribute while the `@PulseType` hint is re-derived from the cohort — kept
// for a column still needing it (the second export is a fixed point), dropped
// for one the plain import already yields (a stale hint never survives).
func TestTypeHint_SidecarPathOwnsTheAttribute(t *testing.T) {
	t.Parallel()

	spec := hintedSpec("AGE:$@Role('0'\n)@PulseType('u8'\n)/TAG:@PulseType('f64'\n)$@Role('1'\n)",
		spsstest.Num(1), spsstest.Num(2))
	spec.Vars[1].Width = 0 // TAG numeric too: an f64 hint there is redundant
	spec.Cases = [][]spsstest.Value{{spsstest.Num(1), spsstest.Num(3.5)}, {spsstest.Num(2), spsstest.Num(4)}}
	c := runCycle(t, spec, WriterOptions{})

	if got := cohortSchema(t, c.fs, "second.pulse").Fields[0].Type; got != encoding.FieldTypeU8 {
		t.Errorf("AGE after the sidecar-path round trip = %s, want u8", got)
	}
	if !bytes.Equal(c.first, c.second) || !bytes.Equal(c.out, c.again) {
		t.Error("the sidecar-path round trip is not a fixed point")
	}
	d, err := parseDictionary(c.out)
	if err != nil {
		t.Fatal(err)
	}
	x, ok := d.rawExtension(extVarAttributes)
	if !ok {
		t.Fatal("the re-export dropped record 7/18")
	}
	if got, want := string(x.payload), "AGE:$@Role('0'\n)@PulseType('u8'\n)/TAG:$@Role('1'\n)"; got != want {
		t.Errorf("record 7/18 = %q, want %q", got, want)
	}
}

// TestTypeHint_UnparseableSourceAttributesStayVerbatim: a source record
// 7/18 this grammar cannot read is re-emitted untouched and receives no
// hint, rather than being rewritten blind.
func TestTypeHint_UnparseableSourceAttributesStayVerbatim(t *testing.T) {
	t.Parallel()

	raw := []byte("SEX:$@Origine('cle')\n")
	f := &outFile{
		varAttrs: &RawText{Subtype: extVarAttributes, Raw: raw},
		vars:     []*outVar{{name: "AGE", field: 0, enc: EncodeNumeric, setBit: -1}},
	}
	applyTypeHints(f, &encoding.Schema{Fields: []encoding.Field{{Name: "AGE", Type: encoding.FieldTypeU8}}})
	if !bytes.Equal(f.varAttrs.Raw, raw) {
		t.Errorf("record 7/18 = %q, want the source bytes untouched", f.varAttrs.Raw)
	}

	// A parseable one gains the hint in place.
	f.varAttrs = &RawText{Subtype: extVarAttributes, Raw: []byte("AGE:$@Role('0'\n)\n")}
	applyTypeHints(f, &encoding.Schema{Fields: []encoding.Field{{Name: "AGE", Type: encoding.FieldTypeU8}}})
	if got, want := string(f.varAttrs.Raw), "AGE:$@Role('0'\n)@PulseType('u8'\n)\n"; got != want {
		t.Errorf("record 7/18 = %q, want %q", got, want)
	}
}

// TestTypeHint_ParseVarAttributes covers the record 7/18 grammar edges.
func TestTypeHint_ParseVarAttributes(t *testing.T) {
	t.Parallel()

	for _, bad := range []string{":a('1'\n)", "A:('1'\n)", "A:k('1')", "A:k('1'\n)x", "A"} {
		if _, _, ok := parseVarAttributes([]byte(bad)); ok {
			t.Errorf("%q parsed; want refused", bad)
		}
	}
	sets, trailer, ok := parseVarAttributes([]byte("A:k('1'\n'2'\n)j('3'\n)/B:k('4'\n)\n"))
	if !ok || len(sets) != 2 || len(sets[0].attrs) != 2 || len(sets[0].attrs[0].values) != 2 || string(trailer) != "\n" {
		t.Fatalf("parse = %+v %q %v", sets, trailer, ok)
	}
	if got := string(renderVarAttributes(sets, trailer)); got != "A:k('1'\n'2'\n)j('3'\n)/B:k('4'\n)\n" {
		t.Errorf("render = %q", got)
	}
	for _, s := range []string{"u8", "decimal128(18,4)", "categorical_u32", "f64"} {
		if h, ok := parseTypeHint(s); !ok || h.String() != s {
			t.Errorf("parseTypeHint(%q) = %v %v", s, h, ok)
		}
	}
	for _, s := range []string{"decimal128(0,0)", "decimal128(5,6)", "decimal128(5)", "decimal128(5,2", "vec_f64", "set_u8"} {
		if _, ok := parseTypeHint(s); ok {
			t.Errorf("parseTypeHint(%q) accepted", s)
		}
	}
	if !strings.Contains(typeHint{ft: encoding.FieldTypeU8}.render(3), "3") {
		t.Error("render")
	}
}

// TestTypeHint_PredictMatchesImport: import predict reports the type the
// run writes, hint honoured or refused.
func TestTypeHint_PredictMatchesImport(t *testing.T) {
	t.Parallel()

	for _, value := range []float64{200, 2.5} {
		fs := afero.NewMemMapFs()
		spec := hintedSpec("AGE:@PulseType('u8'\n)", spsstest.Num(1), spsstest.Num(value))
		if err := afero.WriteFile(fs, "s.sav", build(t, spec), 0o644); err != nil {
			t.Fatal(err)
		}
		job := pio.NewImportJob(NewReader(fs, "s.sav"), "s.pulse")
		job.FS = fs
		pred, err := job.Predict(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		importSavReport(t, fs, "s.sav", "s.pulse")
		got := cohortSchema(t, fs, "s.pulse").Fields[0].Type
		if pred.Schema.Fields[0].Type != got {
			t.Errorf("%v: predict says %s, the import wrote %s", value, pred.Schema.Fields[0].Type, got)
		}
	}
}
