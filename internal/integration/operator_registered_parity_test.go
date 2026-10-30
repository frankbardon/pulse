package integration_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestUnregisteredOperator_PredictMatchesRuntime: a slot naming an
// operator the instance never registered is refused by both sides with
// the runtime's PROCESSING_CONFIG "unknown <family> type: <name>" — once
// at predict — at every slot that builds one (it used to predict
// valid for an aggregation, group, filterer or attribute, and refuse
// SERVICE_VALIDATION for a feature).
func TestUnregisteredOperator_PredictMatchesRuntime(t *testing.T) {
	t.Parallel()

	p, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	for _, c := range []struct{ body, msg string }{
		{`{"groups":[{"type":"GROUP_NEVER_REGISTERED","field":"x"}],"aggregations":[{"type":"AGG_COUNT","field":"x"}]}`, "unknown group type: GROUP_NEVER_REGISTERED"},
		{`{"crosstab":{"rows":[{"type":"GROUP_NEVER_REGISTERED","field":"g"}],"columns":[{"type":"GROUP_CATEGORY","field":"g"}],"cell":{"type":"AGG_COUNT","field":"x"}}}`, "unknown group type: GROUP_NEVER_REGISTERED"},
		{`{"crosstab":{"rows":[{"type":"GROUP_CATEGORY","field":"g"}],"columns":[{"type":"GROUP_NEVER_REGISTERED","field":"g"}],"cell":{"type":"AGG_COUNT","field":"x"}}}`, "unknown group type: GROUP_NEVER_REGISTERED"},
		{`{"aggregations":[{"type":"AGG_NEVER_REGISTERED","field":"x"}]}`, "unknown aggregation type: AGG_NEVER_REGISTERED"},
		{`{"crosstab":{"rows":[{"type":"GROUP_CATEGORY","field":"g"}],"columns":[{"type":"GROUP_CATEGORY","field":"g"}],"cell":{"type":"AGG_NEVER_REGISTERED","field":"x"}}}`, "unknown aggregation type: AGG_NEVER_REGISTERED"},
		{`{"crosstab":{"rows":[{"type":"GROUP_CATEGORY","field":"g"}],"columns":[{"type":"GROUP_CATEGORY","field":"g"}],"cell":{"type":"AGG_COUNT","field":"x"},"margin_aggregations":[{"type":"AGG_NEVER_REGISTERED","field":"x","label":"m"}]}}`, "unknown crosstab margin aggregation type: AGG_NEVER_REGISTERED"},
		{`{"filterers":[{"type":"FILTER_NEVER_REGISTERED","field":"x"}],"aggregations":[{"type":"AGG_COUNT","field":"x"}]}`, "unknown filter type: FILTER_NEVER_REGISTERED"},
		{`{"attributes":[{"type":"ATTR_NEVER_REGISTERED","field":"x","label":"o"}],"aggregations":[{"type":"AGG_COUNT","field":"x"}]}`, "unknown attribute type: ATTR_NEVER_REGISTERED"},
		{`{"features":[{"type":"FEAT_NEVER_REGISTERED","field":"x"}],"aggregations":[{"type":"AGG_COUNT","field":"x"}]}`, "unknown feature type: FEAT_NEVER_REGISTERED"},
	} {
		mk := func() *types.Request {
			var req types.Request
			if err := json.Unmarshal([]byte(c.body), &req); err != nil {
				t.Fatal(err)
			}
			req.Cohort = &types.Cohort{Filename: cohort}
			return &req
		}
		_, rerr := p.Process(ctx, mk())
		var ce *errors.CodedError
		if !stderrors.As(rerr, &ce) || ce.Code != errors.PROCESSING_CONFIG || !strings.Contains(ce.Message, c.msg) {
			t.Errorf("%s: runtime %v, want PROCESSING_CONFIG %q", c.body, rerr, c.msg)
		}
		env := predictEnvelope(t, p, fs, cohort, mk())
		if len(env.Errors) != 1 || env.Errors[0].Code != string(errors.PROCESSING_CONFIG) || env.Errors[0].Message != c.msg {
			var got []string
			for _, e := range env.Errors {
				got = append(got, e.Code+" "+e.Message)
			}
			t.Errorf("%s: predict %v, want one PROCESSING_CONFIG %q", c.body, got, c.msg)
		}
	}
}
