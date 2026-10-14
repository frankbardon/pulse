package processing

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// TestComparison_RecordReference (U30 E3-S2): a {record} reference binds
// the stored record the Service resolved onto the request's model.Set,
// read by member column name — a null member a missing element (its
// pair drops) — for a row-local and a fitted metric, against the
// oracles; a member the record's cohort holds no numeric column for is
// PULSE_VECTOR_REFERENCE_MISMATCH record_members_absent; the bound
// lookup refusal surfaces with its own code, located on the slot; a
// record the Service never resolved is PROCESSING_INTERNAL.
func TestComparison_RecordReference(t *testing.T) {
	// The stored record's cohort: x f64, y u8, z f64 (nullable), g
	// categorical — another schema than the scanned rows'.
	refSchema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "x", Type: encoding.FieldTypeF64},
		{Name: "y", Type: encoding.FieldTypeU8},
		{Name: "z", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "id", Type: encoding.FieldTypeU32},
	}}
	full := NewStoredRecord(NewRecordWithWide(refSchema, map[string]float64{"x": 2, "y": 3, "z": 1, "id": 7}, nil, nil), refSchema, "ref.pulse")
	nullZ := NewStoredRecord(NewRecordWithWide(refSchema, map[string]float64{"x": 2, "y": 3, "id": 7}, map[string]bool{"z": true}, nil), refSchema, "ref.pulse")
	catSchema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "x", Type: encoding.FieldTypeF64},
		{Name: "y", Type: encoding.FieldTypeF64},
		{Name: "z", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()},
	}}
	catZ := NewStoredRecord(NewRecordWithWide(catSchema, map[string]float64{"x": 2, "y": 3, "z": 0}, nil, nil), catSchema, "cat.pulse")
	lookupErr := errors.NewCodedErrorWithDetails(errors.PULSE_INDEX_MISSING, "no sidecar point-lookup index found for these key fields",
		map[string]any{"cohort": "ref.pulse"})

	reference := `{"record":{"cohort":{"filename":"ref.pulse"},"keys":[{"field":"id","value":"7"}]}}`
	key := vectors.ComparisonReference{Form: vectors.ReferenceRecord, Cohort: &types.Cohort{Filename: "ref.pulse"},
		Keys: []types.LookupKey{{Field: "id", Value: "7"}}}.RecordKey()
	sd := cmpSD(false)
	for _, c := range []struct {
		name    string
		typ     types.AttributeType
		metric  string
		binding *model.RecordBinding
		oracle  func(x []float64) float64
		code    errors.Code
		reason  string
	}{
		{name: "similarity", typ: types.ATTR_PROFILE_SIMILARITY, metric: "cosine", binding: &model.RecordBinding{Record: full},
			oracle: func(x []float64) float64 { return oracleCosine(x, []float64{2, 3, 1}) }},
		{name: "null member is a missing element", typ: types.ATTR_PROFILE_SIMILARITY, metric: "cosine", binding: &model.RecordBinding{Record: nullZ},
			oracle: func(x []float64) float64 { return oracleCosine(x, []float64{2, 3, math.NaN()}) }},
		{name: "fitted metric", typ: types.ATTR_VEC_DISTANCE, metric: "std_euclidean", binding: &model.RecordBinding{Record: full},
			oracle: func(x []float64) float64 { return oracleEuclid(x, []float64{2, 3, 1}, sd) }},
		{name: "categorical member column", typ: types.ATTR_VEC_DISTANCE, metric: "euclidean", binding: &model.RecordBinding{Record: catZ},
			code: errors.PULSE_VECTOR_REFERENCE_MISMATCH, reason: vectors.ReferenceReasonRecordMembersAbsent},
		{name: "lookup refusal", typ: types.ATTR_VEC_DISTANCE, metric: "euclidean", binding: &model.RecordBinding{Err: lookupErr},
			code: errors.PULSE_INDEX_MISSING},
		{name: "never resolved", typ: types.ATTR_PROFILE_SIMILARITY, metric: "cosine", code: errors.PROCESSING_INTERNAL},
	} {
		t.Run(c.name, func(t *testing.T) {
			schema := cmpSchema()
			recs := cmpRecords(schema)
			req := &types.Request{Attributes: []*types.Attribute{{Type: c.typ, Label: "out", Fields: []string{"x", "y", "z"},
				Params: json.RawMessage(`{"metric":"` + c.metric + `","reference":` + reference + `}`)}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "out", Label: "n"}}}
			p := NewProcessor(schema)
			set := &model.Set{}
			if c.binding != nil {
				set = set.WithRecords(map[string]model.RecordBinding{key: *c.binding})
			}
			p.SetModels(set)
			_, err := p.Process(context.Background(), req, NewSliceIterator(recs))
			if c.code != "" {
				var ce *errors.CodedError
				if !stderrors.As(err, &ce) || ce.Code != c.code {
					t.Fatalf("Process: %v, want %s", err, c.code)
				}
				if c.reason != "" && (ce.Details["reason"] != c.reason || ce.Details["cohort"] != "cat.pulse") {
					t.Errorf("details %+v, want reason %s naming the cohort", ce.Details, c.reason)
				}
				if c.code == errors.PULSE_INDEX_MISSING {
					if ce.Details["attribute"] != "out" || ce.Details["cohort"] != "ref.pulse" {
						t.Errorf("details %+v, want the lookup's located on the slot", ce.Details)
					}
					if _, located := lookupErr.Details["attribute"]; located {
						t.Error("the shared bound refusal was mutated")
					}
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			scored := 0
			for i, r := range recs {
				got, ok := r.NumericValue("out")
				if !ok {
					got = math.NaN()
				}
				want := c.oracle(cmpRows[i][:])
				if !math.IsNaN(want) {
					scored++
				}
				if !cmpClose(got, want) {
					t.Errorf("row %d: %v, want %v", i, got, want)
				}
			}
			if scored < 8 {
				t.Fatalf("vacuous: %d rows scored", scored)
			}
		})
	}
}

// TestStoredRecord_Member pins the member reader: integer, float and
// packed_bool columns are known (a null one present false), vector
// elements read by "v[k]", anything else unknown.
func TestStoredRecord_Member(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "u", Type: encoding.FieldTypeU16},
		{Name: "f", Type: encoding.FieldTypeF32, Nullable: true},
		{Name: "b", Type: encoding.FieldTypePackedBool},
		{Name: "c", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()},
		{Name: "d", Type: encoding.FieldTypeDate},
	}}
	rec := NewStoredRecord(NewRecordWithWide(schema, map[string]float64{"u": 5, "b": 1, "c": 0, "d": 3}, map[string]bool{"f": true}, nil), schema, "x.pulse")
	for _, c := range []struct {
		name           string
		v              float64
		present, known bool
	}{
		{"u", 5, true, true}, {"f", math.NaN(), false, true}, {"b", 1, true, true},
		{"c", math.NaN(), false, false}, {"d", math.NaN(), false, false}, {"nope", math.NaN(), false, false},
	} {
		v, present, known := rec.Member(c.name)
		if present != c.present || known != c.known || (c.present && v != c.v) {
			t.Errorf("Member(%s) = %v %v %v, want %v %v %v", c.name, v, present, known, c.v, c.present, c.known)
		}
	}
	if rec.Cohort() != "x.pulse" {
		t.Errorf("Cohort = %q", rec.Cohort())
	}
}
