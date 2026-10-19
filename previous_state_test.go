package pulse_test

import (
	"context"
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

// previousStateParityCases is every ATTR_PREVIOUS_STATE refusal over the
// acceptance cohort (t_<type> carries every field type, subj is
// categorical): the shared slot rule (internal/prevstate), the
// field-reference walk (source columns only, vector fields), the
// foreign-slot rule, the zone rule and the weight class.
var previousStateParityCases = []struct {
	name string
	code errors.Code
	attr types.Attribute
	// derived: an earlier ATTR_DATE_PART writes column "d".
	derived bool
}{
	{name: "field_missing", code: errors.PROCESSING_CONFIG, attr: types.Attribute{SubjectField: "subj", OrderField: "t_date"}},
	{name: "subject_missing", code: errors.PROCESSING_CONFIG, attr: types.Attribute{Field: "g", OrderField: "t_date"}},
	{name: "order_missing", code: errors.PROCESSING_CONFIG, attr: types.Attribute{Field: "g", SubjectField: "subj"}},
	{name: "target", code: errors.PROCESSING_CONFIG, attr: types.Attribute{Field: "g", SubjectField: "subj", OrderField: "t_date", Target: "x"}},
	{name: "predictors", code: errors.PROCESSING_CONFIG, attr: types.Attribute{Field: "g", SubjectField: "subj", OrderField: "t_date", Predictors: []string{"x"}}},
	{name: "expression", code: errors.PROCESSING_CONFIG, attr: types.Attribute{Field: "g", SubjectField: "subj", OrderField: "t_date", Expression: "x"}},
	{name: "state_coded_u8", code: errors.PULSE_FIELD_TYPE_UNSUPPORTED, attr: types.Attribute{Field: "t_u8", SubjectField: "subj", OrderField: "t_date"}},
	{name: "state_f64", code: errors.PULSE_FIELD_TYPE_UNSUPPORTED, attr: types.Attribute{Field: "t_f64", SubjectField: "subj", OrderField: "t_date"}},
	{name: "state_set", code: errors.PULSE_FIELD_TYPE_UNSUPPORTED, attr: types.Attribute{Field: "t_set_u8", SubjectField: "subj", OrderField: "t_date"}},
	{name: "state_vector", code: errors.PULSE_VECTOR_FIELD_NOT_SCALAR, attr: types.Attribute{Field: "t_vec_f32", SubjectField: "subj", OrderField: "t_date"}},
	{name: "subject_set", code: errors.PULSE_FIELD_TYPE_UNSUPPORTED, attr: types.Attribute{Field: "g", SubjectField: "t_set_u16", OrderField: "t_date"}},
	{name: "subject_vector", code: errors.PULSE_VECTOR_FIELD_NOT_SCALAR, attr: types.Attribute{Field: "g", SubjectField: "t_vec_f64", OrderField: "t_date"}},
	{name: "subject_unknown", code: errors.SERVICE_VALIDATION, attr: types.Attribute{Field: "g", SubjectField: "nope", OrderField: "t_date"}},
	{name: "order_u32", code: errors.PULSE_FIELD_TYPE_UNSUPPORTED, attr: types.Attribute{Field: "g", SubjectField: "subj", OrderField: "t_u32"}},
	{name: "order_f64", code: errors.PULSE_FIELD_TYPE_UNSUPPORTED, attr: types.Attribute{Field: "g", SubjectField: "subj", OrderField: "t_f64"}},
	{name: "order_derived", code: errors.SERVICE_VALIDATION, attr: types.Attribute{Field: "g", SubjectField: "subj", OrderField: "d"}, derived: true},
	{name: "tz", code: errors.PROCESSING_CONFIG, attr: types.Attribute{Field: "g", SubjectField: "subj", OrderField: "t_datetime", TimeZone: "UTC"}},
	{name: "tz_field", code: errors.PROCESSING_CONFIG, attr: types.Attribute{Field: "g", SubjectField: "subj", OrderField: "t_datetime", TimeZoneField: "g"}},
	{name: "weight", code: errors.PROCESSING_CONFIG, attr: types.Attribute{Field: "g", SubjectField: "subj", OrderField: "t_date", Weight: types.SlotWeightField("x")}},
}

// TestPreviousState_PredictRefusalsMatchRuntime: every request the
// runtime refuses for an ATTR_PREVIOUS_STATE slot, Process and
// ProcessStream refuse alike, and predict reports the same code,
// message and details.
func TestPreviousState_PredictRefusalsMatchRuntime(t *testing.T) {
	p, fs, cohort := acceptanceCohort(t)
	for _, c := range previousStateParityCases {
		t.Run(c.name, func(t *testing.T) {
			mk := func() *types.Request {
				a := c.attr
				a.Type, a.Label = types.ATTR_PREVIOUS_STATE, "o"
				attrs := []*types.Attribute{&a}
				if c.derived {
					attrs = append([]*types.Attribute{{Type: types.ATTR_DATE_PART, Field: "t_date", Label: "d",
						Params: []byte(`{"part":"month"}`)}}, attrs...)
				}
				return &types.Request{
					Cohort:       &types.Cohort{Filename: cohort},
					Attributes:   attrs,
					Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "o"}},
				}
			}
			if ce := assertPredictMatchesRefusal(t, p, fs, cohort, mk); ce.Code != c.code {
				t.Errorf("runtime code %s (%s), want %s", ce.Code, ce.Message, c.code)
			}
		})
	}
}

// TestPreviousState_ForeignSlotsRefused: subject_field / order_field on
// any other attribute type is refused on both sides (it would be
// ignored silently).
func TestPreviousState_ForeignSlotsRefused(t *testing.T) {
	p, fs, cohort := acceptanceCohort(t)
	for _, a := range []types.Attribute{
		{Type: types.ATTR_ZSCORE, Field: "x", SubjectField: "subj"},
		{Type: types.ATTR_CODE_IN, Field: "g", OrderField: "t_date", Params: []byte(`{"codes":["a"]}`)},
	} {
		t.Run(string(a.Type), func(t *testing.T) {
			mk := func() *types.Request {
				attr := a
				attr.Label = "o"
				return &types.Request{
					Cohort:       &types.Cohort{Filename: cohort},
					Attributes:   []*types.Attribute{&attr},
					Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "o"}},
				}
			}
			ce := assertPredictMatchesRefusal(t, p, fs, cohort, mk)
			if ce.Code != errors.PROCESSING_CONFIG {
				t.Errorf("code %s, want %s", ce.Code, errors.PROCESSING_CONFIG)
			}
		})
	}
}

// assertPredictMatchesRefusal runs mk on Process and ProcessStream
// (both must refuse, alike) and predict (whose errors must carry the
// runtime refusal's code, message and details); it returns the
// runtime refusal.
func assertPredictMatchesRefusal(t *testing.T, p *pulse.Pulse, fs afero.Fs, cohort string, mk func() *types.Request) *errors.CodedError {
	t.Helper()
	ctx := context.Background()
	_, rerr := p.Process(ctx, mk())
	ce := prevStateCoded(t, "process", rerr)
	_, serr := p.ProcessStream(ctx, mk())
	if se := prevStateCoded(t, "stream", serr); se.Code != ce.Code || se.Message != ce.Message {
		t.Errorf("stream %s %q, process %s %q", se.Code, se.Message, ce.Code, ce.Message)
	}
	env := predictEnvelope(t, p, fs, cohort, mk())
	var hit *descriptor.EnvelopeEntry
	for _, e := range env.Errors {
		if e.Code == string(ce.Code) && e.Message == ce.Message {
			hit = e
		}
	}
	if hit == nil {
		t.Fatalf("predict errors %+v lack the runtime refusal %s %q", env.Errors, ce.Code, ce.Message)
	}
	if !reflect.DeepEqual(hit.Details, ce.Details) && !(len(hit.Details) == 0 && len(ce.Details) == 0) {
		t.Errorf("details: predict %#v, runtime %#v", hit.Details, ce.Details)
	}
	return ce
}

// prevStateCoded asserts err is a coded refusal and returns it.
func prevStateCoded(t *testing.T, arm string, err error) *errors.CodedError {
	t.Helper()
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) {
		t.Fatalf("%s: want a coded refusal, got %v", arm, err)
	}
	return ce
}

// panelCohort writes a long panel (id u64, seen date, plan categorical
// {free, paid}, all nullable but id) from rows of {id, seen, plan}; a
// nil cell is a null.
func panelCohort(t *testing.T, p *pulse.Pulse, name string, rows [][3]any) {
	t.Helper()
	schema := encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU64, Description: "Customer identifier of the panel row"},
		{Name: "seen", Type: encoding.FieldTypeDate, Nullable: true, Description: "Calendar day the plan was observed"},
		{Name: "plan", Type: encoding.FieldTypeCategoricalU8, Nullable: true, Description: "Subscription plan observed that day"},
		{Name: "x", Type: encoding.FieldTypeF64, Description: "Unrelated numeric column the request reads"},
	}}
	bld, err := p.NewCohortBuilder(context.Background(), name, schema, pulse.CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for i, r := range rows {
		if err := bld.Append(pulse.CohortRow{r[0], r[1], r[2], float64(i)}); err != nil {
			t.Fatalf("row %d: %v", i, err)
		}
	}
	if _, err := bld.Close(); err != nil {
		t.Fatal(err)
	}
}

// prevStateRequest sums the previous-state code and counts its nulls.
func prevStateRequest(cohort string) *types.Request {
	return &types.Request{
		Cohort: &types.Cohort{Filename: cohort},
		Attributes: []*types.Attribute{{Type: types.ATTR_PREVIOUS_STATE, Field: "plan", SubjectField: "id",
			OrderField: "seen", Label: "prev"}},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "prev", Label: "sum_prev"},
			{Type: types.AGG_NULL_COUNT, Field: "prev", Label: "null_prev"},
			{Type: types.AGG_SUM, Field: "x", Label: "sum_x"},
		},
	}
}

// TestPreviousState_ProjectedDecodeReadsSubjectAndOrder runs the
// attribute end to end over a cohort read through the default projected
// decode: subject_field and order_field are read (not projected out,
// which would null every output), and u64 subjects 2^53 and 2^53 + 1 —
// one float64 echo — stay two subjects.
func TestPreviousState_ProjectedDecodeReadsSubjectAndOrder(t *testing.T) {
	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	const big = uint64(1) << 53
	panelCohort(t, p, "panel.pulse", [][3]any{
		{uint64(7), int32(1), "free"}, // free = 0: first → null
		{uint64(7), int32(2), "paid"}, // paid = 1: reads free (0)
		{uint64(7), int32(3), "free"}, // reads paid (1)
		{big, int32(1), "paid"},       // first → null
		{big + 1, int32(2), "free"},   // its own first → null (merged with big it would read 1)
		{uint64(8), nil, "paid"},      // null order → null, never a predecessor
		{uint64(8), int32(5), "free"}, // first keyed record → null
		{uint64(9), int32(1), nil},    // null state
		{uint64(9), int32(2), "paid"}, // previous state null → null (no carry-forward)
		{uint64(9), int32(3), "free"}, // reads paid (1)
	})
	resp, err := p.Process(context.Background(), prevStateRequest("panel.pulse"))
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("data %+v", resp.Data)
	}
	row := resp.Data[0]
	if row["sum_prev"] != 2.0 || row["null_prev"] != 7.0 {
		t.Errorf("sum_prev=%v null_prev=%v, want 2 and 7 (row %+v)", row["sum_prev"], row["null_prev"], row)
	}
	for _, w := range resp.Warnings {
		if w.Code == string(errors.PULSE_PREVIOUS_STATE_AMBIGUOUS_TIE) {
			t.Errorf("tie-free panel warned %+v", w)
		}
	}
}

// TestPreviousState_AmbiguousTieWarnsOrFailsUnderStrict: end to end,
// an ambiguous tie warns (details n_ambiguous, n_subjects_ambiguous) and the run
// succeeds; under Options.Strict the run fails with the same code and
// details.
func TestPreviousState_AmbiguousTieWarnsOrFailsUnderStrict(t *testing.T) {
	rows := [][3]any{
		{uint64(1), int32(1), "free"},
		{uint64(1), int32(1), "paid"}, // tie on differing states
		{uint64(1), int32(2), "free"}, // ambiguous → null
		{uint64(1), int32(3), "paid"}, // reads free (0)
		{uint64(2), int32(1), "paid"},
		{uint64(2), int32(1), "paid"}, // tie on one state
		{uint64(2), int32(2), "free"}, // reads paid (1)
	}
	for _, strict := range []bool{false, true} {
		fs := afero.NewMemMapFs()
		p, err := pulse.New(pulse.Options{FS: fs, Strict: strict})
		if err != nil {
			t.Fatal(err)
		}
		panelCohort(t, p, "tie.pulse", rows)
		resp, err := p.Process(context.Background(), prevStateRequest("tie.pulse"))
		if strict {
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_PREVIOUS_STATE_AMBIGUOUS_TIE {
				t.Fatalf("strict: err %v, want %s", err, errors.PULSE_PREVIOUS_STATE_AMBIGUOUS_TIE)
			}
			if ce.Details["n_ambiguous"] != int64(1) || ce.Details["n_subjects_ambiguous"] != int64(1) {
				t.Errorf("strict details %+v", ce.Details)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if row := resp.Data[0]; row["sum_prev"] != 1.0 || row["null_prev"] != 5.0 {
			t.Errorf("sum_prev=%v null_prev=%v, want 1 and 5", row["sum_prev"], row["null_prev"])
		}
		var hit *types.ResponseWarning
		for _, w := range resp.Warnings {
			if w.Code == string(errors.PULSE_PREVIOUS_STATE_AMBIGUOUS_TIE) {
				hit = w
			}
		}
		if hit == nil {
			t.Fatalf("no %s warning in %+v", errors.PULSE_PREVIOUS_STATE_AMBIGUOUS_TIE, resp.Warnings)
		}
		if hit.Details["n_ambiguous"] != int64(1) || hit.Details["n_subjects_ambiguous"] != int64(1) {
			t.Errorf("warning details %+v", hit.Details)
		}
	}
}
