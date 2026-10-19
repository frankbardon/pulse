package spss

import (
	"bufio"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// carrySchema reads the imported fixture cohort's schema and appends f64
// columns named extra.
func carrySchema(t *testing.T, fs afero.Fs, cohort string, extra ...string) *encoding.Schema {
	t.Helper()
	f, err := fs.Open(cohort)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	r := bufio.NewReader(f)
	ver, err := encoding.ReadHeader(r)
	if err != nil {
		t.Fatal(err)
	}
	s, err := encoding.ReadSchema(r, ver)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range extra {
		s.Fields = append(s.Fields, encoding.Field{Name: name, Type: encoding.FieldTypeF64, Nullable: true, Description: "derived " + name})
	}
	return s
}

func variableNamed(doc *Document, name string) *Variable {
	for i := range doc.Payload.Variables {
		if strings.EqualFold(doc.Payload.Variables[i].Name, name) {
			return &doc.Payload.Variables[i]
		}
	}
	return nil
}

// TestSidecarCarry_AddsAPlainNumericPerNewColumn: a column the document does
// not know gets a numeric variable with a unique minted short name; every
// recorded variable keeps its entry.
func TestSidecarCarry_AddsAPlainNumericPerNewColumn(t *testing.T) {
	fs, cohort, src := importFixture(t, richSpec())
	c, warn := PrepareSidecarCarry(fs, cohort)
	if warn != nil || c == nil {
		t.Fatalf("prepare: %v", warn)
	}
	doc, err := c.rewrite(carrySchema(t, fs, cohort, "Q1_z"), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(doc.Payload.Variables), len(src.Payload.Variables)+1; got != want {
		t.Fatalf("variables = %d, want %d", got, want)
	}
	v := variableNamed(doc, "Q1_z")
	if v == nil || v.Kind != "numeric" || v.PulseType != "f64" || v.Label != "derived Q1_z" || len(v.Categories) != 0 {
		t.Fatalf("new variable = %+v", v)
	}
	for _, other := range doc.Payload.Variables[:len(doc.Payload.Variables)-1] {
		if strings.EqualFold(other.ShortName, v.ShortName) {
			t.Fatalf("minted short name %q collides with %q", v.ShortName, other.Name)
		}
	}
	if !strings.EqualFold(variableNamed(src, "Satisfaction").Name, variableNamed(doc, "Satisfaction").Name) ||
		len(variableNamed(doc, "Satisfaction").Categories) == 0 {
		t.Error("a recorded variable lost its categories")
	}
}

// TestSidecarCarry_ReplacedValuesBecomePlainNumeric: a recorded variable
// whose values were replaced loses its codes and labels; one the response
// sets refer to cannot be carried at all.
func TestSidecarCarry_ReplacedValuesBecomePlainNumeric(t *testing.T) {
	fs, cohort, _ := importFixture(t, richSpec())
	c, _ := PrepareSidecarCarry(fs, cohort)
	s := carrySchema(t, fs, cohort)
	for i := range s.Fields {
		if s.Fields[i].Name == "Satisfaction" {
			s.Fields[i] = encoding.Field{Name: "Satisfaction", Type: encoding.FieldTypeF64, Nullable: true}
		}
	}
	doc, err := c.rewrite(s, []string{"Satisfaction"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if v := variableNamed(doc, "Satisfaction"); v.Kind != "numeric" || len(v.Categories) != 0 || v.Missing != nil {
		t.Errorf("replaced variable = %+v, want a plain numeric", v)
	}

	_, err = c.rewrite(carrySchema(t, fs, cohort), []string{"MD1"}, nil)
	if ce := codedErr(t, err); ce.Code != perr.PULSE_SPSS_SIDECAR_INVALID || !strings.Contains(ce.Message, "refers to it") {
		t.Errorf("replacing a response-set member: %v", err)
	}
}

// TestSidecarCarry_WriteForIsFreshAndExports: the written document is
// fingerprinted over the derived cohort, so the export honours it.
func TestSidecarCarry_WriteForIsFreshAndExports(t *testing.T) {
	fs, cohort, _ := importFixture(t, plainSpec())
	c, _ := PrepareSidecarCarry(fs, cohort)
	raw, err := afero.ReadFile(fs, cohort)
	if err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(fs, "copy.pulse", raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if w := c.WriteFor(fs, "copy.pulse", carrySchema(t, fs, "copy.pulse"), nil, nil); w != nil {
		t.Fatal(w)
	}
	res, err := LoadSidecar(fs, "copy.pulse", WriterOptions{})
	if err != nil || res.Status != SidecarStatusPresent {
		t.Fatalf("carried sidecar: %v %v", res, err)
	}
	if _, _, err := exportVecSav(t, fs, "copy.pulse", WriterOptions{}); err != nil {
		t.Fatal(err)
	}
}

// TestSidecarCarry_MirroredColumnCopiesItsSourceVariable: a new column
// mirroring a recorded categorical (the previous-state column, holding
// that field's codes under its type and dictionary) is described as the
// source variable — codes, labels, missing specification — under its own
// name, short name, index and label, nullable; a mirror the document
// cannot honour (an unrecorded or replaced source, another type) refuses
// the carry rather than mis-describing the column.
func TestSidecarCarry_MirroredColumnCopiesItsSourceVariable(t *testing.T) {
	fs, cohort, src := importFixture(t, richSpec())
	c, _ := PrepareSidecarCarry(fs, cohort)
	withPrev := func(typ encoding.FieldType) *encoding.Schema {
		s := carrySchema(t, fs, cohort)
		sat := s.Field("Satisfaction")
		s.Fields = append(s.Fields, encoding.Field{Name: "prev_sat", Type: typ, Nullable: true,
			Description: "previous satisfaction", Dictionary: sat.Dictionary})
		return s
	}
	sat := withPrev(encoding.FieldTypeF64).Field("Satisfaction")
	doc, err := c.rewrite(withPrev(sat.Type), nil, map[string]string{"prev_sat": "Satisfaction"})
	if err != nil {
		t.Fatal(err)
	}
	v, orig := variableNamed(doc, "prev_sat"), variableNamed(src, "Satisfaction")
	if v == nil || len(v.Categories) == 0 || !reflect.DeepEqual(v.Categories, orig.Categories) ||
		!reflect.DeepEqual(v.Missing, orig.Missing) || v.Kind != orig.Kind || v.PulseType != orig.PulseType {
		t.Fatalf("mirrored variable %+v, want Satisfaction's description %+v", v, orig)
	}
	if v.Label != "previous satisfaction" || !v.Nullable || strings.EqualFold(v.ShortName, orig.ShortName) ||
		v.Index == orig.Index || v.Position != len(doc.Payload.Variables)-1 {
		t.Errorf("mirrored variable identity %+v", v)
	}
	if &v.Categories[0] == &variableNamed(doc, "Satisfaction").Categories[0] {
		t.Error("mirrored variable shares the source's categories")
	}

	replacedSat := withPrev(sat.Type)
	for i := range replacedSat.Fields {
		if replacedSat.Fields[i].Name == "Satisfaction" {
			replacedSat.Fields[i] = encoding.Field{Name: "Satisfaction", Type: encoding.FieldTypeF64, Nullable: true}
		}
	}
	shortDict := withPrev(sat.Type)
	one := encoding.NewDictionary()
	if _, err := one.Add("only"); err != nil {
		t.Fatal(err)
	}
	shortDict.Fields[len(shortDict.Fields)-1].Dictionary = one
	for name, tc := range map[string]struct {
		schema   *encoding.Schema
		replaced []string
		src      string
	}{
		"unrecorded": {withPrev(sat.Type), nil, "Nope"},
		"replaced":   {replacedSat, []string{"Satisfaction"}, "Satisfaction"},
		"type":       {withPrev(encoding.FieldTypeF64), nil, "Satisfaction"},
		"dictionary": {shortDict, nil, "Satisfaction"},
	} {
		_, err := c.rewrite(tc.schema, tc.replaced, map[string]string{"prev_sat": tc.src})
		if ce := codedErr(t, err); ce.Code != perr.PULSE_SPSS_SIDECAR_INVALID || !strings.Contains(ce.Message, "prev_sat") {
			t.Errorf("%s: %v, want a PULSE_SPSS_SIDECAR_INVALID refusal of prev_sat", name, err)
		}
	}
}
