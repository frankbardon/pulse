package integration_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestCrosstabMarginAgg_RefusalIndependentOfMargins (U66 E9-S3): a
// declared crosstab.margin_aggregations entry is refused (or run) the
// same way whether or not any margin observes it, on both execution
// arms, and predict reports exactly the runtime's refusal — code,
// message and details. Before E9-S3 the buffered arm constructed an
// auxiliary only when a margin was requested, so a configuration fault
// predict reported went unraised at runtime with no margin asked for.
func TestCrosstabMarginAgg_RefusalIndependentOfMargins(t *testing.T) {
	t.Parallel()

	p, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	const axes = `"rows":[{"type":"GROUP_CATEGORY","field":"subj"}],"columns":[{"type":"GROUP_CATEGORY","field":"t_categorical_u8"}]`
	cells := map[string]string{
		"buffered": `{"type":"AGG_MEDIAN","field":"x","label":"n"}`,
		"count":    `{"type":"AGG_COUNT","field":"x","label":"n"}`,
	}
	margins := map[string]string{
		"none": ``,
		"rows": `,"margins":{"rows":true}`,
		"all":  `,"margins":{"rows":true,"columns":true,"grand":true}`,
	}
	type auxCase struct {
		name, agg string
		code      errors.Code // "" = runs
		// runtimeOnly: predict accepts this at EVERY aggregation slot
		// today (a separate gap, not this slot's), so only the
		// runtime's independence from the margins is asserted.
		runtimeOnly bool
	}
	auxes := []auxCase{
		{name: "average", agg: `{"type":"AGG_AVERAGE","field":"x","label":"a"}`},
		{name: "median categorical", agg: `{"type":"AGG_MEDIAN","field":"g","label":"a"}`, code: errors.PULSE_FIELD_TYPE_UNSUPPORTED},
		{name: "percentile out of range", agg: `{"type":"AGG_PERCENTILE","field":"x","label":"a","params":{"percentile":150}}`, code: errors.PROCESSING_CONFIG, runtimeOnly: true},
		{name: "variance bad ddof", agg: `{"type":"AGG_VARIANCE","field":"x","label":"a","params":{"ddof":7}}`, code: errors.PROCESSING_CONFIG},
		{name: "decimal variance bad ddof", agg: `{"type":"AGG_VARIANCE","field":"t_decimal128","label":"a","params":{"ddof":7}}`, code: errors.PROCESSING_CONFIG},
		{name: "decimal median", agg: `{"type":"AGG_MEDIAN","field":"t_decimal128","label":"a"}`, code: errors.PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL},
		{name: "unknown level", agg: `{"type":"AGG_MEDIAN","field":"g","label":"a","params":{"levels":["a","zz"]}}`, code: errors.PROCESSING_CONFIG},
	}
	// predictMatches reports whether predict's single error is the
	// runtime's refusal — code, message and details.
	predictMatches := func(t *testing.T, where string, ce *errors.CodedError, errs []*descriptor.EnvelopeEntry) {
		t.Helper()
		if len(errs) != 1 {
			t.Errorf("%s: predict errors %+v, want exactly the runtime's", where, errs)
			return
		}
		got := errs[0]
		want, _ := json.Marshal(ce.Details)
		have, _ := json.Marshal(got.Details)
		if got.Code != string(ce.Code) || got.Message != ce.Message || string(want) != string(have) {
			t.Errorf("%s: predict %s %q %s, runtime %s %q %s", where, got.Code, got.Message, have, ce.Code, ce.Message, want)
		}
	}
	for _, aux := range auxes {
		for cellName, cell := range cells {
			var first *errors.CodedError
			for _, mName := range []string{"none", "rows", "all"} {
				body := `{"crosstab":{` + axes + `,"cell":` + cell + margins[mName] + `,"margin_aggregations":[` + aux.agg + `]}}`
				mk := func() *types.Request {
					var req types.Request
					if err := json.Unmarshal([]byte(body), &req); err != nil {
						t.Fatalf("%s: %v", body, err)
					}
					req.Cohort = &types.Cohort{Filename: cohort}
					return &req
				}
				where := aux.name + "/" + cellName + "/" + mName
				_, rerr := p.Process(ctx, mk())
				env := predictEnvelope(t, p, fs, cohort, mk())
				if aux.code == "" {
					if rerr != nil || len(env.Errors) != 0 {
						t.Errorf("%s: runtime %v, predict %+v; want both to run", where, rerr, env.Errors)
					}
					continue
				}
				var ce *errors.CodedError
				if !stderrors.As(rerr, &ce) || ce.Code != aux.code {
					t.Errorf("%s: runtime %v, want %s", where, rerr, aux.code)
					continue
				}
				if !aux.runtimeOnly {
					predictMatches(t, where, ce, env.Errors)
				}
				if first == nil {
					first = ce
				} else if first.Code != ce.Code || first.Message != ce.Message || !reflect.DeepEqual(first.Details, ce.Details) {
					t.Errorf("%s: refusal moved with the margins: %s %q %v vs %s %q %v", where, ce.Code, ce.Message, ce.Details, first.Code, first.Message, first.Details)
				}
			}
		}
	}
}
