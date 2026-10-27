package pulse_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestGroupDate_RefusesNonDateFamily pins GROUP_DATE's field contract
// on both sides, at every grouper position it can occupy: a `date` or
// `datetime` column is accepted, every other scalar type is refused
// PULSE_FIELD_TYPE_UNSUPPORTED (once at predict, the runtime's code),
// and a vector column keeps its PULSE_VECTOR_FIELD_NOT_SCALAR refusal.
// The sub-day component (`hour`) on a non-date scalar reports the type
// refusal alone — the table's row runs before the datetime rule, in
// the factory's order on both sides.
func TestGroupDate_RefusesNonDateFamily(t *testing.T) {
	t.Parallel()

	p, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	templates := []struct{ name, body string }{
		{"groups/month", `{"groups":[{"type":"GROUP_DATE","field":"$F","params":{"component":"month"}}],"aggregations":[{"type":"AGG_COUNT","field":"x"}]}`},
		{"groups/hour", `{"groups":[{"type":"GROUP_DATE","field":"$F","params":{"component":"hour"}}],"aggregations":[{"type":"AGG_COUNT","field":"x"}]}`},
		{"crosstab/rows", `{"crosstab":{"rows":[{"type":"GROUP_DATE","field":"$F","params":{"component":"year"}}],"columns":[{"type":"GROUP_CATEGORY","field":"t_categorical_u8"}],"cell":{"type":"AGG_COUNT","field":"x"}}}`},
	}
	for _, tc := range templates {
		for _, ft := range acceptanceFieldTypes() {
			body := strings.ReplaceAll(tc.body, "$F", "t_"+ft.String())
			mk := func() *types.Request {
				var req types.Request
				if err := json.Unmarshal([]byte(body), &req); err != nil {
					t.Fatalf("%s: %v", tc.name, err)
				}
				req.Cohort = &types.Cohort{Filename: cohort}
				return &req
			}
			var want errors.Code
			switch {
			case ft.IsVector():
				want = errors.PULSE_VECTOR_FIELD_NOT_SCALAR
			case ft == encoding.FieldTypeDateTime:
			case ft == encoding.FieldTypeDate:
				if tc.name == "groups/hour" {
					// A date carries no time of day: the datetime rule.
					want = errors.PROCESSING_CONFIG
				}
			default:
				want = errors.PULSE_FIELD_TYPE_UNSUPPORTED
			}
			_, rerr := p.Process(ctx, mk())
			var ce *errors.CodedError
			stderrors.As(rerr, &ce)
			switch {
			case want == "" && rerr != nil:
				t.Errorf("%s × %s: runtime refused %v, want accepted", tc.name, ft, rerr)
			case want != "" && (ce == nil || ce.Code != want):
				t.Errorf("%s × %s: runtime returned %v, want %s", tc.name, ft, rerr, want)
			}
			var codes []string
			for _, e := range predictEnvelope(t, p, fs, cohort, mk()).Errors {
				codes = append(codes, e.Code)
			}
			switch {
			case want == "" && len(codes) != 0:
				t.Errorf("%s × %s: predict reported %v, want accepted", tc.name, ft, codes)
			case want != "" && (len(codes) != 1 || codes[0] != string(want)):
				t.Errorf("%s × %s: predict reported %v, want exactly [%s]", tc.name, ft, codes, want)
			}
		}
	}
}
