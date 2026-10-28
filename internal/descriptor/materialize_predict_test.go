package descriptor

import (
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestMaterializePlan_DescriptionLength: an output description past
// encoding.MaxDescriptionBytes is refused by the shared plan with the
// cohort writer's code and details; at the limit it passes.
func TestMaterializePlan_DescriptionLength(t *testing.T) {
	t.Parallel()

	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "a", Type: encoding.FieldTypeU8, Description: "Field a of the source"},
		{Name: "b", Type: encoding.FieldTypeF64, Nullable: true, Description: "Field b of the source"},
	}}
	req := func(n int) *types.Request {
		d := strings.Repeat("x", n)
		return &types.Request{Cohort: &types.Cohort{Filename: "s.pulse"}, Output: &types.MaterializeOutput{
			Columns: []types.MaterializeOutputColumn{{From: "a"}, {From: "b", As: "bee", Description: &d}}}}
	}
	if _, layout, ce := MaterializePlan(req(encoding.MaxDescriptionBytes), schema, nil, "s.pulse", ""); ce != nil || len(layout) != 2 {
		t.Fatalf("at the limit: layout %v, err %v", layout, ce)
	}
	_, _, ce := MaterializePlan(req(encoding.MaxDescriptionBytes+1), schema, nil, "s.pulse", "")
	if ce == nil || ce.Code != errors.PULSE_IMPORT_DESCRIPTION_TOO_LONG ||
		ce.Details["field"] != "bee" || ce.Details["length"] != encoding.MaxDescriptionBytes+1 || ce.Details["max"] != encoding.MaxDescriptionBytes {
		t.Fatalf("over the limit: %v", ce)
	}
}

// TestMaterializeRisks_NullableFalseOverNullable: only a nullable: false
// entry over a column that may hold a null is a risk — a nullable source
// field or a derived column — located at its nullable key.
func TestMaterializeRisks_NullableFalseOverNullable(t *testing.T) {
	t.Parallel()

	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "a", Type: encoding.FieldTypeU8, Description: "Field a of the source"},
		{Name: "b", Type: encoding.FieldTypeF64, Nullable: true, Description: "Field b of the source"},
	}}
	cols := []MaterializeColumn{{Name: "d", Description: "derived d"}}
	no, yes := false, true
	req := &types.Request{Output: &types.MaterializeOutput{Columns: []types.MaterializeOutputColumn{
		{From: "a", Nullable: &no}, {From: "b", Nullable: &no}, {From: "d", As: "dd", Nullable: &no}, {From: "b", As: "b2", Nullable: &yes},
	}}}
	layout, ce := MaterializeOutputLayout(req, schema, cols)
	if ce != nil {
		t.Fatal(ce)
	}
	risks := MaterializeRisks(req, schema, layout)
	if len(risks) != 2 || risks[0].Column != "b" || risks[0].Path != "output.columns[1].nullable" ||
		risks[1].Column != "dd" || risks[1].Path != "output.columns[2].nullable" ||
		risks[0].Code != string(errors.PULSE_MATERIALIZE_OUTPUT_NULL) {
		t.Fatalf("risks = %+v", risks)
	}
	if got := MaterializeRisks(&types.Request{}, schema, layout); got == nil || len(got) != 0 {
		t.Errorf("no output: risks = %#v, want an empty list", got)
	}
}

// TestMaterializeRisks_Casts: a cast a stored value can fail is a risk
// at its type key, coded by its on_cast_error policy and stated as
// fieldcast.Check; a cast that cannot fail is none; it precedes the
// same column's nullable risk.
func TestMaterializeRisks_Casts(t *testing.T) {
	t.Parallel()

	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "a", Type: encoding.FieldTypeU8, Description: "Field a of the source"},
		{Name: "b", Type: encoding.FieldTypeF64, Nullable: true, Description: "Field b of the source"},
	}}
	no := false
	req := &types.Request{Output: &types.MaterializeOutput{Columns: []types.MaterializeOutputColumn{
		{From: "a", Type: "u64"},
		{From: "b", Type: "u8", Nullable: &no},
		{From: "b", As: "b2", Type: "packed_bool", OnCastError: "null"},
	}}}
	layout, ce := MaterializeOutputLayout(req, schema, nil)
	if ce != nil {
		t.Fatal(ce)
	}
	risks := MaterializeRisks(req, schema, layout)
	if len(risks) != 3 {
		t.Fatalf("risks = %+v, want 3", risks)
	}
	if r := risks[0]; r.Column != "b" || r.Path != "output.columns[1].type" || r.Code != string(errors.PULSE_MATERIALIZE_CAST_FAILED) ||
		!strings.HasPrefix(r.Check, "f64 → u8: integral, 0 ≤ v ≤ 255 — ") {
		t.Errorf("risk 0 = %+v", r)
	}
	if r := risks[1]; r.Path != "output.columns[1].nullable" {
		t.Errorf("risk 1 = %+v, want the nullable risk after the cast", r)
	}
	if r := risks[2]; r.Column != "b2" || r.Code != string(errors.PULSE_MATERIALIZE_CAST_NULLED) || !strings.HasPrefix(r.Check, "f64 → packed_bool: v is 0 or 1 — ") {
		t.Errorf("risk 2 = %+v", r)
	}
}
