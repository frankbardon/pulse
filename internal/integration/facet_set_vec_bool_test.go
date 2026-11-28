package integration_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"reflect"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestFacet_SetVectorPackedBool (E4-S8): pulse_facet over a set_* field
// lists its dictionary members in dictionary order (it returned an
// empty list); over a vec_* field it is refused SERVICE_VALIDATION
// (an empty list, no error, before). A packed_bool value is spelled
// "1" / "0" by pulse_facet, FacetSchema and Sample alike (FacetSchema
// said "true" / "false").
func TestFacet_SetVectorPackedBool(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, ft := range []encoding.FieldType{encoding.FieldTypeSetU8, encoding.FieldTypeSetU64, encoding.FieldTypeSetU256} {
		p, _ := oneFieldCohort(t, ft, []string{"y", "x"}, []string{}, nil, []string{"z", "y"})
		got, err := p.Facet(ctx, "one.pulse", "f")
		if err != nil {
			t.Fatalf("%s: %v", ft, err)
		}
		if want := []string{"y", "x", "z"}; !reflect.DeepEqual(got, want) {
			t.Errorf("Facet %s = %q, want its dictionary %q", ft, got, want)
		}
	}
	for _, ft := range []encoding.FieldType{encoding.FieldTypeVecF32, encoding.FieldTypeVecF64} {
		row := any([]float32{1, 2})
		if ft == encoding.FieldTypeVecF64 {
			row = []float64{1, 2, 3}
		}
		p, _ := oneFieldCohort(t, ft, row)
		got, err := p.Facet(ctx, "one.pulse", "f")
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != errors.SERVICE_VALIDATION ||
			ce.Message != "facet does not support field type "+ft.String() || ce.Details["field"] != "f" {
			t.Errorf("Facet %s = (%q, %v), want SERVICE_VALIDATION facet does not support field type", ft, got, err)
		}
	}
	p, _ := oneFieldCohort(t, encoding.FieldTypePackedBool, true, nil, false, true)
	got, err := p.Facet(ctx, "one.pulse", "f")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"1", "0"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Facet packed_bool = %q, want %q", got, want)
	}
	rich, err := p.FacetSchema(ctx, &types.FacetRequest{Cohort: &types.Cohort{Filename: "one.pulse"}, Fields: []string{"f"}})
	if err != nil {
		t.Fatal(err)
	}
	var richValues []string
	for _, v := range rich.Fields["f"].Discrete.Values {
		richValues = append(richValues, v.Value)
	}
	if want := []string{"1", "0"}; !reflect.DeepEqual(richValues, want) {
		t.Errorf("FacetSchema packed_bool values = %q, want %q", richValues, want)
	}
	sample, err := p.Sample(ctx, "one.pulse", 1)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := json.Marshal(sample[0]["f"]); string(b) != "1" {
		t.Errorf("Sample packed_bool = %s, want 1", b)
	}
}

// TestFacetSchema_SetVectorDatetimeRefusedPredictParity (E4-S8):
// FacetSchema refuses a set_*, vec_* or datetime field — faceted or
// additive — before it reads a record, SERVICE_VALIDATION "facet does
// not support field type <type>", and PredictFacet raises the same
// code, message and details; predict had judged the request valid. A
// supported field (categorical, packed_bool, number, date) passes both.
func TestFacetSchema_SetVectorDatetimeRefusedPredictParity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cases := []struct {
		ft      encoding.FieldType
		row     any
		refused bool
	}{
		{encoding.FieldTypeSetU8, []string{"x"}, true},
		{encoding.FieldTypeSetU128, []string{"x"}, true},
		{encoding.FieldTypeVecF32, []float32{1, 2}, true},
		{encoding.FieldTypeVecF64, []float64{1, 2, 3}, true},
		{encoding.FieldTypeDateTime, int64(5), true},
		{encoding.FieldTypeCategoricalU8, "x", false},
		{encoding.FieldTypePackedBool, true, false},
		{encoding.FieldTypeF64, 1.5, false},
		{encoding.FieldTypeDate, int32(3), false},
		{encoding.FieldTypeDecimal128, encoding.NewDecimal128FromInt(150), false},
	}
	for _, c := range cases {
		p := twoFieldCohort(t, c.ft, c.row)
		for _, slot := range []string{"fields", "additive_fields"} {
			req := &types.FacetRequest{Cohort: &types.Cohort{Filename: "two.pulse"}, Fields: []string{"f"}}
			if slot == "additive_fields" {
				// The categorical k faceted, the typed f additive.
				req.Fields, req.AdditiveFields = []string{"k"}, []string{"f"}
			}
			_, runErr := p.FacetSchema(ctx, req)
			env, err := p.PredictFacet(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			if !c.refused {
				if runErr != nil || len(env.Errors) != 0 {
					t.Errorf("%s %s: run %v, predict errors %v; want both to pass", c.ft, slot, runErr, env.Errors)
				}
				continue
			}
			var ce *errors.CodedError
			if !stderrors.As(runErr, &ce) || ce.Code != errors.SERVICE_VALIDATION {
				t.Fatalf("%s %s: run error %v, want SERVICE_VALIDATION", c.ft, slot, runErr)
			}
			if !predictCarries(env, ce) {
				t.Errorf("%s %s: predict errors %v lack the run's %s %q %v", c.ft, slot, env.Errors, ce.Code, ce.Message, ce.Details)
			}
			if res, ok := env.Data.(*pulse.FacetPredictResult); !ok || res.Valid {
				t.Errorf("%s %s: predict result %#v, want valid false", c.ft, slot, env.Data)
			}
		}
	}
}

// predictCarries reports whether env carries ce's code, message and
// details as one of its errors.
func predictCarries(env *descriptor.Envelope, ce *errors.CodedError) bool {
	for _, e := range env.Errors {
		if e.Code == string(ce.Code) && e.Message == ce.Message && reflect.DeepEqual(e.Details, ce.Details) {
			return true
		}
	}
	return false
}

// twoFieldCohort writes two.pulse: a categorical "k" beside a nullable
// field "f" of type ft holding one row.
func twoFieldCohort(t *testing.T, ft encoding.FieldType, row any) *pulse.Pulse {
	t.Helper()
	p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs()})
	if err != nil {
		t.Fatal(err)
	}
	schema := encoding.Schema{Fields: []encoding.Field{
		{Name: "k", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary(), Description: "A label to facet"},
		{Name: "f", Type: ft, Nullable: true, Description: "The typed value faceted"},
	}}
	switch {
	case ft.IsDecimal():
		schema.Fields[1].Precision, schema.Fields[1].Scale = 10, 2
	case ft.HasDictionary():
		schema.Fields[1].Dictionary = encoding.NewDictionary()
	case ft.IsVector():
		dim := 2
		if ft == encoding.FieldTypeVecF64 {
			dim = 3
		}
		schema.Vectors = []encoding.VectorField{{FieldIndex: 1, Dim: dim}}
	}
	b, err := p.NewCohortBuilder(context.Background(), "two.pulse", schema, pulse.CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Append(pulse.CohortRow{"a", row}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}
