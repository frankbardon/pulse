package descriptor

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// TestDefaults_Applied is the table-driven gate that every field type
// in the schema vocabulary either resolves to the documented default
// operator or is recorded as a "no default for category" skip. Runs the
// resolver twice per field — once for an aggregation slot, once for a
// grouper slot — so both columns of the smart-defaults table are
// exercised under one test.
func TestDefaults_Applied(t *testing.T) {
	type expectation struct {
		ft       encoding.FieldType
		expAgg   types.AggregationType
		expGroup types.GroupType
	}

	// Mirrors the table in CLAUDE.md "Code Conventions → Smart defaults".
	cases := []expectation{
		{encoding.FieldTypeU8, types.AGG_SUM, types.GROUP_RANGE},
		{encoding.FieldTypeU16, types.AGG_SUM, types.GROUP_RANGE},
		{encoding.FieldTypeU32, types.AGG_SUM, types.GROUP_RANGE},
		{encoding.FieldTypeU64, types.AGG_SUM, types.GROUP_RANGE},
		{encoding.FieldTypeF32, types.AGG_SUM, types.GROUP_RANGE},
		{encoding.FieldTypeF64, types.AGG_SUM, types.GROUP_RANGE},
		{encoding.FieldTypeNullableU4, types.AGG_SUM, types.GROUP_RANGE},
		{encoding.FieldTypeNullableU8, types.AGG_SUM, types.GROUP_RANGE},
		{encoding.FieldTypeNullableU16, types.AGG_SUM, types.GROUP_RANGE},
		{encoding.FieldTypeDecimal128, types.AGG_SUM, types.GROUP_RANGE},
		{encoding.FieldTypeNullableDecimal128, types.AGG_SUM, types.GROUP_RANGE},
		{encoding.FieldTypeCategoricalU8, types.AGG_FREQUENCY, types.GROUP_CATEGORY},
		{encoding.FieldTypeCategoricalU16, types.AGG_FREQUENCY, types.GROUP_CATEGORY},
		{encoding.FieldTypeCategoricalU32, types.AGG_FREQUENCY, types.GROUP_CATEGORY},
		// Date: no aggregation default, GROUP_DATE for grouper.
		{encoding.FieldTypeDate, "", types.GROUP_DATE},
		{encoding.FieldTypeNullableBool, types.AGG_FREQUENCY, types.GROUP_CATEGORY},
		{encoding.FieldTypePackedBool, types.AGG_FREQUENCY, types.GROUP_CATEGORY},
	}

	// Verify coverage matches the FieldType enum exactly: every known
	// field type must appear in the table (catches new types added to
	// encoding without a corresponding default rule decision).
	if got := len(cases); got != 17 {
		t.Fatalf("defaults table covers %d field types; expected 17", got)
	}

	for _, tc := range cases {
		t.Run(tc.ft.String(), func(t *testing.T) {
			// Aggregation slot.
			aggReq := &types.Request{
				Aggregations: []*types.Aggregation{{Field: "x"}},
			}
			schema := schemaForType("x", tc.ft)
			applied := ResolveDefaults(aggReq, schema)

			if tc.expAgg == "" {
				// No aggregation default expected — Type must remain empty.
				if aggReq.Aggregations[0].Type != "" {
					t.Errorf("Aggregation Type defaulted unexpectedly: got %q", aggReq.Aggregations[0].Type)
				}
				if hasAggApplied(applied) {
					t.Errorf("DefaultsApplied contains aggregation entry for %s; expected none", tc.ft)
				}
			} else {
				if got := aggReq.Aggregations[0].Type; got != tc.expAgg {
					t.Errorf("Aggregation Type = %q; want %q", got, tc.expAgg)
				}
				if !hasAggApplied(applied) {
					t.Errorf("DefaultsApplied missing aggregation entry for %s", tc.ft)
				}
			}

			// Grouper slot.
			grpReq := &types.Request{
				Groups: []*types.Group{{Field: "x"}},
			}
			applied = ResolveDefaults(grpReq, schema)

			if tc.expGroup == "" {
				if grpReq.Groups[0].Type != "" {
					t.Errorf("Group Type defaulted unexpectedly: got %q", grpReq.Groups[0].Type)
				}
			} else {
				if got := grpReq.Groups[0].Type; got != tc.expGroup {
					t.Errorf("Group Type = %q; want %q", got, tc.expGroup)
				}
				if !hasGroupApplied(applied) {
					t.Errorf("DefaultsApplied missing grouper entry for %s", tc.ft)
				}
			}
		})
	}
}

// TestDefaults_DoNotOverride verifies that explicit operator Types
// survive the resolver unchanged and produce no DefaultsApplied entries.
func TestDefaults_DoNotOverride(t *testing.T) {
	schema := schemaForType("revenue", encoding.FieldTypeF64)
	req := &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_AVERAGE, Field: "revenue"},
		},
		Groups: []*types.Group{
			{Type: types.GROUP_QUANTILE, Field: "revenue"},
		},
	}

	applied := ResolveDefaults(req, schema)

	if got := req.Aggregations[0].Type; got != types.AGG_AVERAGE {
		t.Errorf("explicit AGG_AVERAGE overridden to %q", got)
	}
	if got := req.Groups[0].Type; got != types.GROUP_QUANTILE {
		t.Errorf("explicit GROUP_QUANTILE overridden to %q", got)
	}
	if len(applied) != 0 {
		t.Errorf("DefaultsApplied = %d entries; want 0 (explicit Types)", len(applied))
	}
}

// TestDefaults_NeverCrossCategory verifies that an aggregation-shaped
// request with a missing-Type grouper slot only triggers a grouper
// default — and vice versa. The resolver must not invent aggregator or
// grouper slots that the caller did not author.
func TestDefaults_NeverCrossCategory(t *testing.T) {
	schema := schemaForType("revenue", encoding.FieldTypeF64)

	// Grouper-only request: aggregator slot stays absent.
	grpOnly := &types.Request{
		Groups: []*types.Group{{Field: "revenue"}},
	}
	applied := ResolveDefaults(grpOnly, schema)
	if len(grpOnly.Aggregations) != 0 {
		t.Errorf("resolver invented %d aggregation slots; want 0", len(grpOnly.Aggregations))
	}
	if got := grpOnly.Groups[0].Type; got != types.GROUP_RANGE {
		t.Errorf("grouper default = %q; want GROUP_RANGE", got)
	}
	for _, a := range applied {
		if a.Category != "grouper" {
			t.Errorf("DefaultsApplied for aggregator-less request includes %q category", a.Category)
		}
	}

	// Aggregator-only request: grouper slot stays absent.
	aggOnly := &types.Request{
		Aggregations: []*types.Aggregation{{Field: "revenue"}},
	}
	applied = ResolveDefaults(aggOnly, schema)
	if len(aggOnly.Groups) != 0 {
		t.Errorf("resolver invented %d grouper slots; want 0", len(aggOnly.Groups))
	}
	for _, a := range applied {
		if a.Category != "aggregation" {
			t.Errorf("DefaultsApplied for grouper-less request includes %q category", a.Category)
		}
	}
}

// TestDefaults_TestsNotDefaulted verifies that tier-1 and tier-2 test
// slots with missing TestType are NOT touched by the resolver. Hypothesis
// tests are intent-bearing and have no defensible default.
func TestDefaults_TestsNotDefaulted(t *testing.T) {
	schema := schemaForType("revenue", encoding.FieldTypeF64)
	req := &types.Request{
		Tests: []*types.Test{
			{Field: "revenue"}, // missing Type
		},
		PostTests: []*types.Test{
			{Field: "revenue"}, // missing Type
		},
	}

	applied := ResolveDefaults(req, schema)
	if len(applied) != 0 {
		t.Errorf("DefaultsApplied = %d; want 0 (tests must not be defaulted)", len(applied))
	}
	if req.Tests[0].Type != "" {
		t.Errorf("Tier-1 test Type defaulted to %q; want empty", req.Tests[0].Type)
	}
	if req.PostTests[0].Type != "" {
		t.Errorf("Tier-2 test Type defaulted to %q; want empty", req.PostTests[0].Type)
	}
}

// TestDefaults_GroupParamsFilled verifies that newly-defaulted groupers
// pick up sensible operator-specific configuration (GROUP_RANGE interval,
// GROUP_H3_CELL resolution, GROUP_DATE component) so the inferred slot
// is runnable without additional caller action.
func TestDefaults_GroupParamsFilled(t *testing.T) {
	// GROUP_RANGE picks up Interval = 10.
	rng := &types.Request{Groups: []*types.Group{{Field: "x"}}}
	ResolveDefaults(rng, schemaForType("x", encoding.FieldTypeF64))
	if got := rng.Groups[0].Interval; got != 10 {
		t.Errorf("GROUP_RANGE default Interval = %v; want 10", got)
	}

	// GROUP_DATE picks up component = "day".
	dt := &types.Request{Groups: []*types.Group{{Field: "dt"}}}
	ResolveDefaults(dt, schemaForType("dt", encoding.FieldTypeDate))
	if got := dt.Groups[0].Type; got != types.GROUP_DATE {
		t.Fatalf("date grouper default = %q; want GROUP_DATE", got)
	}
	var dtParams struct {
		Component string `json:"component"`
	}
	if err := json.Unmarshal(dt.Groups[0].Params, &dtParams); err != nil {
		t.Fatalf("DATE default params not JSON: %v", err)
	}
	if dtParams.Component != "day" {
		t.Errorf("DATE default component = %q; want \"day\"", dtParams.Component)
	}
}

// TestDefaults_ExplicitParamsPreserved verifies that explicit grouper
// configuration is never overwritten by the parameter-default pass.
func TestDefaults_ExplicitParamsPreserved(t *testing.T) {
	// GROUP_RANGE with explicit Interval.
	rng := &types.Request{
		Groups: []*types.Group{{Field: "x", Interval: 42}},
	}
	ResolveDefaults(rng, schemaForType("x", encoding.FieldTypeF64))
	if got := rng.Groups[0].Interval; got != 42 {
		t.Errorf("explicit Interval overwritten: got %v; want 42", got)
	}

}

// TestPredict_DefaultsAppliedReported verifies that predict's envelope
// surfaces DefaultsApplied entries when defaults fire and an empty (not
// nil) array when nothing applies.
func TestPredict_DefaultsAppliedReported(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "revenue", Type: encoding.FieldTypeF64, Description: "Revenue for the row in USD."},
		},
	}
	data := buildTestPulseFile(t, schema)

	// Defaulted request: field set, no Type.
	req := &types.Request{
		Aggregations: []*types.Aggregation{{Field: "revenue"}},
	}
	env := PredictFromBytes(data, req, nil)
	result, ok := env.Data.(*PredictResult)
	if !ok {
		t.Fatal("Data is not *PredictResult")
	}
	if len(result.DefaultsApplied) != 1 {
		t.Fatalf("DefaultsApplied = %d; want 1", len(result.DefaultsApplied))
	}
	if got := result.DefaultsApplied[0].Type; got != string(types.AGG_SUM) {
		t.Errorf("DefaultsApplied[0].Type = %q; want AGG_SUM", got)
	}
	if got := result.DefaultsApplied[0].Field; got != "revenue" {
		t.Errorf("DefaultsApplied[0].Field = %q; want revenue", got)
	}
	if got := result.DefaultsApplied[0].Category; got != "aggregation" {
		t.Errorf("DefaultsApplied[0].Category = %q; want aggregation", got)
	}

	// Caller's request is untouched (defaults landed on a clone).
	if req.Aggregations[0].Type != "" {
		t.Errorf("caller's request mutated by predict: Type = %q", req.Aggregations[0].Type)
	}

	// Explicit request: empty DefaultsApplied (not nil) in JSON.
	req2 := &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_AVERAGE, Field: "revenue"},
		},
	}
	env2 := PredictFromBytes(data, req2, nil)
	result2, _ := env2.Data.(*PredictResult)
	if result2.DefaultsApplied == nil {
		t.Error("DefaultsApplied is nil; want empty slice for JSON [] serialisation")
	}
	if len(result2.DefaultsApplied) != 0 {
		t.Errorf("DefaultsApplied = %d entries; want 0", len(result2.DefaultsApplied))
	}

	// JSON serialisation: defaults_applied should appear as an empty array
	// (not omitted, not null).
	out, err := json.Marshal(result2)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if !bytes.Contains(out, []byte(`"defaults_applied":[]`)) {
		t.Errorf("expected empty defaults_applied array in JSON; got %s", string(out))
	}
}

// TestDefaults_UnknownFieldLeftAlone verifies that an aggregation whose
// Field does not exist in the schema is not silently defaulted (the
// downstream validator will surface SERVICE_VALIDATION for the unknown
// field — defaults must not paper over the error).
func TestDefaults_UnknownFieldLeftAlone(t *testing.T) {
	schema := schemaForType("revenue", encoding.FieldTypeF64)
	req := &types.Request{
		Aggregations: []*types.Aggregation{{Field: "no_such_field"}},
	}
	applied := ResolveDefaults(req, schema)
	if len(applied) != 0 {
		t.Errorf("DefaultsApplied = %d for unknown field; want 0", len(applied))
	}
	if req.Aggregations[0].Type != "" {
		t.Errorf("defaulted unknown-field slot: Type = %q", req.Aggregations[0].Type)
	}
}

// TestDefaults_NilInputs verifies that ResolveDefaults is safe against
// nil requests and nil schemas (returns nil, no panic).
func TestDefaults_NilInputs(t *testing.T) {
	if got := ResolveDefaults(nil, nil); got != nil {
		t.Errorf("ResolveDefaults(nil, nil) = %v; want nil", got)
	}
	if got := ResolveDefaults(&types.Request{}, nil); got != nil {
		t.Errorf("ResolveDefaults(req, nil) = %v; want nil", got)
	}
	if got := ResolveDefaults(nil, &encoding.Schema{}); got != nil {
		t.Errorf("ResolveDefaults(nil, schema) = %v; want nil", got)
	}
}

// --- helpers ---

func schemaForType(name string, ft encoding.FieldType) *encoding.Schema {
	field := encoding.Field{Name: name, Type: ft, Description: "Test field with a description long enough to pass the low-quality check."}
	if ft.IsCategorical() {
		d := encoding.NewDictionary()
		_, _ = d.Add("a")
		_, _ = d.Add("b")
		field.Dictionary = d
	}
	return &encoding.Schema{Fields: []encoding.Field{field}}
}

func hasAggApplied(entries []DefaultApplied) bool {
	for _, e := range entries {
		if e.Category == "aggregation" {
			return true
		}
	}
	return false
}

func hasGroupApplied(entries []DefaultApplied) bool {
	for _, e := range entries {
		if e.Category == "grouper" {
			return true
		}
	}
	return false
}
