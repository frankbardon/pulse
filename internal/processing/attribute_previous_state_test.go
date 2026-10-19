package processing

import (
	stderrors "errors"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// psNull marks a null cell (input) or a null output (want).
var psNull = math.NaN()

// prevStateSchema: id (u32 subject), d (date order), at (datetime
// order), st (categorical state {A, B, C}), flag (packed_bool state),
// dec (decimal128 subject); every column nullable.
func prevStateSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	dict := encoding.NewDictionary()
	for _, l := range []string{"A", "B", "C"} {
		if _, err := dict.Add(l); err != nil {
			t.Fatal(err)
		}
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32, Nullable: true},
		{Name: "d", Type: encoding.FieldTypeDate, Nullable: true},
		{Name: "at", Type: encoding.FieldTypeDateTime, Nullable: true},
		{Name: "st", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict, Nullable: true},
		{Name: "flag", Type: encoding.FieldTypePackedBool, Nullable: true},
		{Name: "f", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "code", Type: encoding.FieldTypeU8},
		{Name: "tags", Type: encoding.FieldTypeSetU8},
	}}
}

// psRec builds one record from column → value (psNull = a null cell).
func psRec(s *encoding.Schema, cells map[string]float64) *Record {
	vals, nulls := map[string]float64{}, map[string]bool{}
	for k, v := range cells {
		if math.IsNaN(v) {
			nulls[k] = true
			continue
		}
		vals[k] = v
	}
	return NewRecordWithNulls(s, vals, nulls)
}

// The state codes of st.
const (
	psA = 0
	psB = 1
	psC = 2
)

// TestPreviousState_Compute is the rule table: first record, ordering,
// the tie rule (differing / identical / null-bearing ties), the null
// rules (subject, order, previous state — no carry-forward) and both
// order types and state types.
func TestPreviousState_Compute(t *testing.T) {
	s := prevStateSchema(t)
	type row = map[string]float64
	cases := []struct {
		name               string
		field, order       string
		rows               []row
		want               []float64
		ambiguous, ambSubj int64
	}{
		{name: "first_null_then_previous", rows: []row{
			{"id": 1, "d": 10, "st": psA}, {"id": 1, "d": 11, "st": psB}, {"id": 1, "d": 12, "st": psC},
		}, want: []float64{psNull, psA, psB}},
		{name: "ordered_by_order_not_record_order", rows: []row{
			{"id": 1, "d": 12, "st": psC}, {"id": 1, "d": 10, "st": psA}, {"id": 1, "d": 11, "st": psB},
		}, want: []float64{psB, psNull, psA}},
		{name: "subjects_interleaved", rows: []row{
			{"id": 1, "d": 1, "st": psA}, {"id": 2, "d": 1, "st": psC}, {"id": 1, "d": 2, "st": psB}, {"id": 2, "d": 2, "st": psA},
		}, want: []float64{psNull, psNull, psA, psC}},
		{name: "tie_differing_states_is_ambiguous", rows: []row{
			{"id": 1, "d": 1, "st": psA}, {"id": 1, "d": 2, "st": psA}, {"id": 1, "d": 2, "st": psB}, {"id": 1, "d": 3, "st": psC},
		}, want: []float64{psNull, psA, psA, psNull}, ambiguous: 1, ambSubj: 1},
		{name: "tie_identical_states_carry_the_state", rows: []row{
			{"id": 1, "d": 1, "st": psA}, {"id": 1, "d": 1, "st": psA}, {"id": 1, "d": 2, "st": psB}, {"id": 1, "d": 3, "st": psC},
		}, want: []float64{psNull, psNull, psA, psB}},
		{name: "ambiguous_counts_every_record_of_next_time_point", rows: []row{
			{"id": 1, "d": 1, "st": psA}, {"id": 1, "d": 1, "st": psB}, {"id": 1, "d": 2, "st": psC}, {"id": 1, "d": 2, "st": psC},
			{"id": 2, "d": 1, "st": psA}, {"id": 2, "d": 1, "st": psC}, {"id": 2, "d": 5, "st": psA},
			{"id": 1, "d": 3, "st": psA},
		}, want: []float64{psNull, psNull, psNull, psNull, psNull, psNull, psNull, psC}, ambiguous: 3, ambSubj: 2},
		{name: "tie_null_and_value_is_ambiguous", rows: []row{
			{"id": 1, "d": 1, "st": psA}, {"id": 1, "d": 1, "st": psNull}, {"id": 1, "d": 2, "st": psB},
		}, want: []float64{psNull, psNull, psNull}, ambiguous: 1, ambSubj: 1},
		{name: "tie_all_null_is_a_gap_not_ambiguous", rows: []row{
			{"id": 1, "d": 1, "st": psNull}, {"id": 1, "d": 1, "st": psNull}, {"id": 1, "d": 2, "st": psB},
		}, want: []float64{psNull, psNull, psNull}},
		{name: "null_previous_state_no_carry_forward", rows: []row{
			{"id": 1, "d": 1, "st": psA}, {"id": 1, "d": 2, "st": psNull}, {"id": 1, "d": 3, "st": psC},
		}, want: []float64{psNull, psA, psNull}},
		{name: "null_subject_is_null_and_never_a_predecessor", rows: []row{
			{"id": 1, "d": 1, "st": psA}, {"id": psNull, "d": 2, "st": psB}, {"id": 1, "d": 3, "st": psC},
		}, want: []float64{psNull, psNull, psA}},
		{name: "null_order_is_null_and_never_a_predecessor", rows: []row{
			{"id": 1, "d": 1, "st": psA}, {"id": 1, "d": psNull, "st": psB}, {"id": 1, "d": 3, "st": psC},
		}, want: []float64{psNull, psNull, psA}},
		{name: "datetime_order_by_instant", order: "at", rows: []row{
			{"id": 1, "at": 1_700_000_100, "st": psB}, {"id": 1, "at": 1_700_000_000, "st": psA},
		}, want: []float64{psA, psNull}},
		{name: "packed_bool_state", field: "flag", rows: []row{
			{"id": 1, "d": 1, "flag": 1}, {"id": 1, "d": 2, "flag": 0}, {"id": 1, "d": 3, "flag": 1},
		}, want: []float64{psNull, 1, 0}},
		{name: "first_state_code_zero_is_not_confused_with_null", rows: []row{
			{"id": 1, "d": 1, "st": psA}, {"id": 1, "d": 2, "st": psA},
		}, want: []float64{psNull, psA}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			field, order := c.field, c.order
			if field == "" {
				field = "st"
			}
			if order == "" {
				order = "d"
			}
			comp, err := newPreviousStateAttribute(&types.Attribute{Type: types.ATTR_PREVIOUS_STATE, Field: field, SubjectField: "id", OrderField: order, Label: "prev"}, s)
			if err != nil {
				t.Fatal(err)
			}
			recs := make([]*Record, len(c.rows))
			for i, r := range c.rows {
				recs[i] = psRec(s, r)
			}
			got, err := comp.Compute(recs, field)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(c.want) {
				t.Fatalf("got %d values, want %d", len(got), len(c.want))
			}
			for i, w := range c.want {
				if math.IsNaN(w) {
					if !IsAttributeNull(got[i]) {
						t.Errorf("row %d: got %v, want null (AttributeNull)", i, got[i])
					}
					continue
				}
				if got[i] != w {
					t.Errorf("row %d: got %v, want %v", i, got[i], w)
				}
			}
			a := comp.(*previousStateAttribute)
			if a.nAmbiguous != c.ambiguous || a.nAmbiguousSubjects != c.ambSubj {
				t.Errorf("ambiguous = (%d records, %d subjects), want (%d, %d)", a.nAmbiguous, a.nAmbiguousSubjects, c.ambiguous, c.ambSubj)
			}
			warns := AttributeWarnings(comp)
			if c.ambiguous == 0 {
				if len(warns) != 0 {
					t.Errorf("unexpected warnings %+v", warns)
				}
				return
			}
			if len(warns) != 1 || warns[0].Code != string(errors.PULSE_PREVIOUS_STATE_AMBIGUOUS_TIE) {
				t.Fatalf("warnings %+v, want one %s", warns, errors.PULSE_PREVIOUS_STATE_AMBIGUOUS_TIE)
			}
			if warns[0].Details["n_ambiguous"] != c.ambiguous || warns[0].Details["n_subjects_ambiguous"] != c.ambSubj {
				t.Errorf("details %+v", warns[0].Details)
			}
		})
	}
}

// TestPreviousState_StrictFailsTheRun: through the buffered attribute
// pass, an ambiguous tie warns (and writes nulls, never 0) when not
// strict and fails with the warning's own code and details under
// Strict; a tie-free run under Strict passes.
func TestPreviousState_StrictFailsTheRun(t *testing.T) {
	s := prevStateSchema(t)
	attrs := []*types.Attribute{{Type: types.ATTR_PREVIOUS_STATE, Field: "st", SubjectField: "id", OrderField: "d", Label: "prev"}}
	mk := func(tie bool) []*Record {
		second := float64(psA)
		if tie {
			second = psB
		}
		return []*Record{
			psRec(s, map[string]float64{"id": 1, "d": 1, "st": psA}),
			psRec(s, map[string]float64{"id": 1, "d": 1, "st": second}),
			psRec(s, map[string]float64{"id": 1, "d": 2, "st": psC}),
		}
	}
	p := NewProcessor(s)
	recs := mk(true)
	warns, _, err := p.applyAttributes(attrs, recs)
	if err != nil {
		t.Fatalf("non-strict: %v", err)
	}
	if len(warns) != 1 || warns[0].Code != string(errors.PULSE_PREVIOUS_STATE_AMBIGUOUS_TIE) {
		t.Fatalf("non-strict warnings %+v", warns)
	}
	if !recs[2].IsNull("prev") {
		t.Errorf("ambiguous record not null: %v", recs[2])
	}

	p.SetWeighting(nil, true)
	_, _, err = p.applyAttributes(attrs, mk(true))
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_PREVIOUS_STATE_AMBIGUOUS_TIE {
		t.Fatalf("strict: err %v, want %s", err, errors.PULSE_PREVIOUS_STATE_AMBIGUOUS_TIE)
	}
	if ce.Details["n_ambiguous"] != int64(1) || ce.Details["n_subjects_ambiguous"] != int64(1) {
		t.Errorf("strict details %+v", ce.Details)
	}
	if _, _, err := p.applyAttributes(attrs, mk(false)); err != nil {
		t.Errorf("strict, no tie: %v", err)
	}
}

// TestPreviousState_FactoryRefusals: the shared slot rule's refusals,
// by code, through the runtime factory.
func TestPreviousState_FactoryRefusals(t *testing.T) {
	s := prevStateSchema(t)
	base := func() *types.Attribute {
		return &types.Attribute{Type: types.ATTR_PREVIOUS_STATE, Field: "st", SubjectField: "id", OrderField: "d", Label: "prev"}
	}
	for _, c := range []struct {
		name string
		mut  func(*types.Attribute)
		code errors.Code
	}{
		{"field_missing", func(a *types.Attribute) { a.Field = "" }, errors.PROCESSING_CONFIG},
		{"subject_missing", func(a *types.Attribute) { a.SubjectField = "" }, errors.PROCESSING_CONFIG},
		{"order_missing", func(a *types.Attribute) { a.OrderField = "" }, errors.PROCESSING_CONFIG},
		{"target", func(a *types.Attribute) { a.Target = "f" }, errors.PROCESSING_CONFIG},
		{"expression", func(a *types.Attribute) { a.Expression = "1" }, errors.PROCESSING_CONFIG},
		{"coded_integer_state", func(a *types.Attribute) { a.Field = "code" }, errors.PULSE_FIELD_TYPE_UNSUPPORTED},
		{"float_state", func(a *types.Attribute) { a.Field = "f" }, errors.PULSE_FIELD_TYPE_UNSUPPORTED},
		{"set_subject", func(a *types.Attribute) { a.SubjectField = "tags" }, errors.PULSE_FIELD_TYPE_UNSUPPORTED},
		{"numeric_order", func(a *types.Attribute) { a.OrderField = "f" }, errors.PULSE_FIELD_TYPE_UNSUPPORTED},
		{"unknown_order", func(a *types.Attribute) { a.OrderField = "nope" }, errors.PROCESSING_CONFIG},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := base()
			c.mut(a)
			_, err := newPreviousStateAttribute(a, s)
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != c.code {
				t.Fatalf("err %v, want %s", err, c.code)
			}
			// A coded integer state points at re-importing it as a
			// categorical; any other type refusal does not.
			if hint := strings.Contains(ce.Message, "re-import the column as a categorical"); hint != (c.name == "coded_integer_state") {
				t.Errorf("re-import hint present=%v in %q", hint, ce.Message)
			}
		})
	}
	if _, err := newPreviousStateAttribute(base(), s); err != nil {
		t.Errorf("valid slot refused: %v", err)
	}
}

// TestPreviousState_Components: the operator keys of the slot's
// Response.Components.Attributes entry. n_subjects counts the subjects
// holding a record with a present subject and order; every null output
// is a first time point (n_first), a null previous state (n_gap), an
// ambiguous tie (n_ambiguous) or a record with a null subject / order —
// so the four add up with those to n_null.
func TestPreviousState_Components(t *testing.T) {
	s := prevStateSchema(t)
	type row = map[string]float64
	rows := []row{
		// subject 1: A, gap (null state), C ← null previous → n_gap 1.
		{"id": 1, "d": 1, "st": psA}, {"id": 1, "d": 2, "st": psNull}, {"id": 1, "d": 3, "st": psC},
		// subject 2: a differing tie on d1, two records at d2 → n_ambiguous 2;
		// the tied d1 records are both first time points.
		{"id": 2, "d": 1, "st": psA}, {"id": 2, "d": 1, "st": psB}, {"id": 2, "d": 2, "st": psC}, {"id": 2, "d": 2, "st": psC},
		// a null subject and a null order: null, counted by neither key.
		{"id": psNull, "d": 4, "st": psA}, {"id": 3, "d": psNull, "st": psA},
		// subject 4: one record, its first time point.
		{"id": 4, "d": 9, "st": psB},
	}
	comp, err := newPreviousStateAttribute(&types.Attribute{Type: types.ATTR_PREVIOUS_STATE, Field: "st", SubjectField: "id", OrderField: "d", Label: "prev"}, s)
	if err != nil {
		t.Fatal(err)
	}
	recs := make([]*Record, len(rows))
	for i, r := range rows {
		recs[i] = psRec(s, r)
	}
	if _, err := comp.Compute(recs, "st"); err != nil {
		t.Fatal(err)
	}
	meta, ok := comp.(MetaAttribute)
	if !ok {
		t.Fatal("ATTR_PREVIOUS_STATE is not a MetaAttribute")
	}
	got, err := meta.Components()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"n_subjects": int64(3), "n_first": int64(4), "n_gap": int64(1), "n_ambiguous": int64(2)}
	if len(got) != len(want) {
		t.Fatalf("components %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}

	// A second Compute starts the tallies afresh.
	if _, err := comp.Compute(recs[:3], "st"); err != nil {
		t.Fatal(err)
	}
	got, _ = meta.Components()
	if got["n_subjects"] != int64(1) || got["n_first"] != int64(1) || got["n_gap"] != int64(1) || got["n_ambiguous"] != int64(0) {
		t.Errorf("recomputed components %v", got)
	}
}
