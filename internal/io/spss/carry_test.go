package spss

import (
	"bufio"
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
	doc, err := c.rewrite(carrySchema(t, fs, cohort, "Q1_z"), nil)
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
	doc, err := c.rewrite(s, []string{"Satisfaction"})
	if err != nil {
		t.Fatal(err)
	}
	if v := variableNamed(doc, "Satisfaction"); v.Kind != "numeric" || len(v.Categories) != 0 || v.Missing != nil {
		t.Errorf("replaced variable = %+v, want a plain numeric", v)
	}

	_, err = c.rewrite(carrySchema(t, fs, cohort), []string{"MD1"})
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
	if w := c.WriteFor(fs, "copy.pulse", carrySchema(t, fs, "copy.pulse"), nil); w != nil {
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
