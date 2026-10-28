package descriptor

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// validateMat runs ValidateMaterializeWithOptions over data and returns
// the envelope and its result, checking Valid mirrors the errors.
func validateMat(t *testing.T, data []byte, req *types.Request, opts *PredictOptions) (*descriptor.Envelope, *descriptor.MaterializePredictResult) {
	t.Helper()
	env := ValidateMaterializeWithOptions(bytes.NewReader(data), req, opts)
	res, ok := env.Data.(*descriptor.MaterializePredictResult)
	if !ok {
		t.Fatalf("Data is %T, want *descriptor.MaterializePredictResult", env.Data)
	}
	if res.Valid != (len(env.Errors) == 0) {
		t.Fatalf("Valid %v with errors %v", res.Valid, env.Errors)
	}
	if res.Columns == nil || res.DerivedColumns == nil || res.Risks == nil {
		t.Fatalf("result lists must be empty, never null: %+v", res)
	}
	return env, res
}

func codesOf(es []*descriptor.EnvelopeEntry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Code
	}
	return out
}

// validateSource is a three-record source cohort: an id, a nullable
// score, a decimal price, a categorical and a weakly described
// auxiliary count.
func validateSource(t *testing.T) []byte {
	t.Helper()
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32, Description: "Respondent identifier of the panel"},
		{Name: "score", Type: encoding.FieldTypeF64, Nullable: true, Description: "Satisfaction score of the respondent"},
		{Name: "price", Type: encoding.FieldTypeDecimal128, Precision: 10, Scale: 2, Description: "Price paid by the respondent"},
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Dictionary: makeDictionary(t, "north", "south"), Description: "Region of the respondent"},
		// A weak SOURCE description: the run writes the output schema,
		// so predict never warns on it when the column is not written.
		{Name: "aux", Type: encoding.FieldTypeU8, Description: "x"},
	}}
	data := buildTestPulseFile(t, schema)
	stride := schema.RecordByteSize()
	for r := 0; r < 3; r++ {
		rec := make([]byte, stride)
		binary.LittleEndian.PutUint32(rec, uint32(r+1))
		data = append(data, rec...)
	}
	return data
}

// TestValidateMaterialize_EarlyRefusals: the verdict follows the run's
// order — no cohort, a slot materialize refuses, no derived column, an
// unreadable source — each stopping it with the run's code, no column
// resolved. The item-expanded request is echoed once expansion ran.
func TestValidateMaterialize_EarlyRefusals(t *testing.T) {
	t.Parallel()

	src := validateSource(t)
	cohort := &types.Cohort{Filename: "src.pulse"}
	out := &types.MaterializeOutput{Columns: []types.MaterializeOutputColumn{{From: "id"}}}
	for _, tc := range []struct {
		name string
		data []byte
		req  *types.Request
		code errors.Code
		echo bool
	}{
		{"nil request", src, nil, errors.SERVICE_VALIDATION, false},
		{"no cohort", src, &types.Request{Output: out}, errors.SERVICE_VALIDATION, false},
		{"aggregation slot", src, &types.Request{Cohort: cohort, Output: out,
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Label: "n"}}}, errors.PULSE_MATERIALIZE_SLOT_UNSUPPORTED, true},
		{"nothing derived, no output", src, &types.Request{Cohort: cohort}, errors.PULSE_MATERIALIZE_NO_DERIVED, true},
		{"unreadable source", []byte("not a pulse cohort at all"), &types.Request{Cohort: cohort, Output: out}, errors.ENCODING_INVALID, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			env, res := validateMat(t, tc.data, tc.req, &PredictOptions{EchoRequest: true})
			if len(env.Errors) == 0 || env.Errors[0].Code != string(tc.code) {
				t.Fatalf("errors %v, want %s first", codesOf(env.Errors), tc.code)
			}
			if res.Valid || len(res.Columns) != 0 || res.SchemaInfo != nil {
				t.Errorf("result %+v, want invalid with no columns and no schema info", res)
			}
			if (env.Request != nil) != tc.echo {
				t.Errorf("echoed request %v, want echo=%v", env.Request, tc.echo)
			}
		})
	}
	// A nil opts takes the defaults.
	if env := ValidateMaterializeWithOptions(bytes.NewReader(src), nil, nil); len(env.Errors) != 1 {
		t.Errorf("nil opts: errors %v", codesOf(env.Errors))
	}
}

// TestValidateMaterialize_Plan: a valid output block yields the written
// columns (source and derived, renamed, decimal shape, dictionary, the
// column each reads), the derived names, the data-dependent risks (a
// cast, a nullable: false over a nullable column, a declared keyed
// group), the container plan with its groups, and one low-quality
// warning per weak written description followed by the writer's
// too-narrow finding. Strict promotes the warnings; echo returns the
// expanded request.
func TestValidateMaterialize_Plan(t *testing.T) {
	t.Parallel()

	src := validateSource(t)
	no := false
	weak := "x"
	req := &types.Request{
		Cohort:   &types.Cohort{Filename: "src.pulse"},
		Features: []*types.Feature{{Type: types.FEAT_LOG, Field: "score", Label: "log_score"}},
		Output: &types.MaterializeOutput{
			Columns: []types.MaterializeOutputColumn{
				{From: "id", As: "respondent"},
				{From: "score", Type: "u8", Nullable: &no},
				{From: "price"},
				{From: "region", Description: &weak},
				{From: "log_score"},
			},
			Groups: []types.MaterializeGroup{
				{Key: []string{"respondent"}, Members: []string{"price"}},
				{Members: []string{"region"}},
			},
			Shards: &types.MaterializeShards{MaxRecords: 2},
		},
	}
	env, res := validateMat(t, src, req, &PredictOptions{EchoRequest: true})
	if len(env.Errors) != 0 {
		t.Fatalf("errors %v", env.Errors)
	}
	want := []struct {
		name, typ, from string
		derived         bool
	}{
		{"respondent", "u32", "id", false},
		{"score", "u8", "score", false},
		{"price", "decimal128", "price", false},
		{"region", "categorical_u8", "region", false},
		{"log_score", "f64", "log_score", true},
	}
	if len(res.Columns) != len(want) {
		t.Fatalf("columns %+v", res.Columns)
	}
	for i, w := range want {
		c := res.Columns[i]
		if c.Name != w.name || c.Type != w.typ || c.From != w.from || c.Derived != w.derived {
			t.Errorf("column %d = %+v, want %+v", i, c, w)
		}
	}
	if p := res.Columns[2]; p.Precision == nil || *p.Precision != 10 || p.Scale == nil || *p.Scale != 2 {
		t.Errorf("price shape %v/%v, want 10/2", p.Precision, p.Scale)
	}
	if res.Columns[0].Precision != nil || res.Columns[0].Dictionary != nil {
		t.Errorf("u32 column carries a shape or dictionary: %+v", res.Columns[0])
	}
	if d := res.Columns[3].Dictionary; d == nil || len(d.Values) != 2 || d.Values[0] != "north" {
		t.Errorf("region dictionary %+v", d)
	}
	if res.Columns[1].Nullable || !res.Columns[4].Nullable {
		t.Errorf("nullability score=%v log_score=%v, want false / true", res.Columns[1].Nullable, res.Columns[4].Nullable)
	}
	if len(res.DerivedColumns) != 1 || res.DerivedColumns[0] != "log_score" {
		t.Errorf("derived %v", res.DerivedColumns)
	}
	var paths []string
	for _, r := range res.Risks {
		paths = append(paths, r.Path+"="+r.Code)
	}
	wantRisks := []string{
		"output.columns[1].type=" + string(errors.PULSE_MATERIALIZE_CAST_FAILED),
		"output.columns[1].nullable=" + string(errors.PULSE_MATERIALIZE_OUTPUT_NULL),
		"output.groups[0]=" + string(errors.PULSE_GROUP_MEMBER_NOT_CONSTANT),
	}
	if len(paths) != len(wantRisks) {
		t.Fatalf("risks %v, want %v", paths, wantRisks)
	}
	for i := range wantRisks {
		if paths[i] != wantRisks[i] {
			t.Errorf("risk %d = %s, want %s", i, paths[i], wantRisks[i])
		}
	}
	ct := res.Container
	if ct == nil || ct.Kind != "shard_archive" || ct.MaxRecords != 2 || ct.MaxShards == nil || *ct.MaxShards != 2 || len(ct.Groups) != 2 ||
		ct.Groups[0].Verdict != MaterializeGroupPlanned || ct.Groups[1].Verdict == MaterializeGroupPlanned {
		t.Errorf("container %+v groups %+v", ct, ct.Groups)
	}
	if res.SchemaInfo == nil {
		t.Error("no source schema info")
	}
	wantWarn := []string{string(errors.PULSE_FIELD_DESCRIPTION_LOW_QUALITY), string(errors.PULSE_GROUP_TOO_NARROW)}
	if got := codesOf(env.Warnings); len(got) != 2 || got[0] != wantWarn[0] || got[1] != wantWarn[1] {
		t.Errorf("warnings %v, want %v", got, wantWarn)
	}
	if env.Warnings[0].Details["field"] != "region" {
		t.Errorf("low-quality warning on %v, want region", env.Warnings[0].Details["field"])
	}
	if r, ok := env.Request.(*types.Request); !ok || r.Output == nil {
		t.Errorf("echo %T, want the expanded request", env.Request)
	}

	env, res = validateMat(t, src, req, &PredictOptions{Strict: true})
	if got := codesOf(env.Errors); res.Valid || len(got) != 2 || got[0] != wantWarn[0] || len(env.Warnings) != 0 || env.Request != nil {
		t.Errorf("strict: errors %v warnings %v valid %v, want the two warnings promoted", got, codesOf(env.Warnings), res.Valid)
	}
}

// TestValidateMaterialize_PlanRefusalStillPredicts: a plan refusal
// (an unknown output column) leads the errors and leaves the columns
// empty, while the body's own Predict findings (an unknown filter field)
// still follow it.
func TestValidateMaterialize_PlanRefusalStillPredicts(t *testing.T) {
	t.Parallel()

	req := &types.Request{
		Cohort:    &types.Cohort{Filename: "src.pulse"},
		Filterers: []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "nowhere", Values: []string{"1"}}},
		Output:    &types.MaterializeOutput{Columns: []types.MaterializeOutputColumn{{From: "nope"}}},
	}
	env, res := validateMat(t, validateSource(t), req, nil)
	got := codesOf(env.Errors)
	if len(got) < 2 || got[0] != string(errors.PULSE_MATERIALIZE_OUTPUT_UNKNOWN_COLUMN) {
		t.Fatalf("errors %v, want the plan refusal first, then Predict's", got)
	}
	if len(res.Columns) != 0 || res.Container != nil || res.SchemaInfo == nil {
		t.Errorf("result %+v, want no columns or container, schema info kept", res)
	}
}
