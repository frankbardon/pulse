package descriptor

import (
	"bytes"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// TestManifestFacetCapability verifies the manifest carries the
// FacetCapability descriptor with every supports_* flag set to true and
// at least one streamable condition documented.
func TestManifestFacetCapability(t *testing.T) {
	m := BuildManifest()
	if m.Facet.Name != "facet_schema" {
		t.Fatalf("Facet.Name = %q, want \"facet_schema\"", m.Facet.Name)
	}
	if !m.Facet.SupportsDiscrete || !m.Facet.SupportsNumeric ||
		!m.Facet.SupportsPercentiles || !m.Facet.SupportsHistogram ||
		!m.Facet.SupportsAdditive {
		t.Fatalf("FacetCapability flags must all be true: %+v", m.Facet)
	}
	if len(m.Facet.StreamableConditions) == 0 {
		t.Fatal("FacetCapability.StreamableConditions must be non-empty")
	}
	if !m.Facet.SupportsOverlays {
		t.Errorf("FacetCapability.SupportsOverlays must be true")
	}
	expected := []string{
		"OVERLAY_CHISQ_VS_POP",
		"OVERLAY_INDEX_VS_POP",
		"OVERLAY_KS_VS_POP",
		"OVERLAY_ZSCORE_VS_POP",
	}
	if len(m.Facet.SupportedOverlayKinds) != len(expected) {
		t.Errorf("FacetCapability.SupportedOverlayKinds length = %d, want %d", len(m.Facet.SupportedOverlayKinds), len(expected))
	} else {
		for i, want := range expected {
			if m.Facet.SupportedOverlayKinds[i] != want {
				t.Errorf("FacetCapability.SupportedOverlayKinds[%d] = %q, want %q (alphabetical order required)", i, m.Facet.SupportedOverlayKinds[i], want)
			}
		}
	}
}

// buildSimplePulseBytes returns a minimal .pulse file with one u16
// "x" field and one categorical "tag" field carrying two values.
func buildSimplePulseBytes(t *testing.T) []byte {
	t.Helper()
	dict := encoding.NewDictionary()
	if _, err := dict.Add("a"); err != nil {
		t.Fatal(err)
	}
	if _, err := dict.Add("b"); err != nil {
		t.Fatal(err)
	}
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "x", Type: encoding.FieldTypeU16, ByteOffset: 0, CsvColumnIdx: 0},
			{Name: "tag", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 2, CsvColumnIdx: 1, Dictionary: dict},
		},
	}
	var buf bytes.Buffer
	encoding.WriteHeader(&buf)
	encoding.WriteSchema(&buf, schema)
	for i := uint64(0); i < 4; i++ {
		encoding.WriteFieldValue(&buf, encoding.FieldTypeU16, i)
		encoding.WriteFieldValue(&buf, encoding.FieldTypeCategoricalU8, i%2)
	}
	return buf.Bytes()
}

func TestValidateFacet_Valid(t *testing.T) {
	data := buildSimplePulseBytes(t)
	env := ValidateFacetFromBytes(data, &types.FacetRequest{
		Cohort: &types.Cohort{Filename: "x.pulse"},
		Fields: []string{"x", "tag"},
	})
	if len(env.Errors) > 0 {
		t.Fatalf("expected no errors; got %+v", env.Errors)
	}
	res, ok := env.Data.(*FacetValidationResult)
	if !ok || !res.Valid {
		t.Fatalf("expected Valid result; got %+v", env.Data)
	}
}

func TestValidateFacet_UnknownField(t *testing.T) {
	data := buildSimplePulseBytes(t)
	env := ValidateFacetFromBytes(data, &types.FacetRequest{
		Cohort: &types.Cohort{Filename: "x.pulse"},
		Fields: []string{"missing"},
	})
	if len(env.Errors) == 0 {
		t.Fatal("expected SERVICE_VALIDATION error for unknown field")
	}
}

func TestValidateFacet_PercentileOnNonNumericWarns(t *testing.T) {
	data := buildSimplePulseBytes(t)
	env := ValidateFacetFromBytes(data, &types.FacetRequest{
		Cohort:             &types.Cohort{Filename: "x.pulse"},
		Fields:             []string{"tag"},
		NumericPercentiles: []float64{0.5},
	})
	if len(env.Errors) > 0 {
		t.Fatalf("expected no errors (just warnings); got %+v", env.Errors)
	}
	if len(env.Warnings) == 0 {
		t.Fatal("expected at least one warning for percentiles on non-numeric field")
	}
}

func TestValidateFacet_HistogramRequiresValidRange(t *testing.T) {
	data := buildSimplePulseBytes(t)
	env := ValidateFacetFromBytes(data, &types.FacetRequest{
		Cohort:           &types.Cohort{Filename: "x.pulse"},
		Fields:           []string{"x"},
		IncludeHistogram: true,
		HistogramRange:   [2]float64{5, 5},
	})
	if len(env.Errors) == 0 {
		t.Fatal("expected SERVICE_VALIDATION error for invalid histogram range")
	}
}

func TestValidateFacet_AdditiveExpressionRejected(t *testing.T) {
	data := buildSimplePulseBytes(t)
	env := ValidateFacetFromBytes(data, &types.FacetRequest{
		Cohort: &types.Cohort{Filename: "x.pulse"},
		Fields: []string{"tag"},
		Filterers: []*types.Filterer{
			{Type: types.FILTER_EXPRESSION, Expression: "tag == \"a\""},
		},
		AdditiveFields: []string{"tag"},
	})
	if len(env.Errors) == 0 {
		t.Fatal("expected SERVICE_VALIDATION error for additive field referenced inside FILTER_EXPRESSION")
	}
}
