package pulse_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestDecimalAggregation_PredictMatchesRuntime: a decimal128 field on a
// built-in aggregator with no decimal arm is refused by BOTH sides with
// ONE PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL — never a predict warning the
// runtime then contradicts — at every aggregation slot (top level,
// grouped, crosstab cell, crosstab margin aggregation); a decimal-arm
// aggregator — AGG_NULL_COUNT included, a bitmap-only count — runs on
// both sides. (AGG_WEIGHTED_MEAN never reaches the
// rule: it always carries a weight, refused PULSE_WEIGHT_UNSUPPORTED
// over a decimal128 value field before any dispatch.)
func TestDecimalAggregation_PredictMatchesRuntime(t *testing.T) {
	t.Parallel()

	p, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	shapes := map[string]string{
		"top":     `{"aggregations":[$A]}`,
		"grouped": `{"groups":[{"type":"GROUP_CATEGORY","field":"g"}],"aggregations":[$A]}`,
		"cell":    `{"crosstab":{"rows":[{"type":"GROUP_CATEGORY","field":"g"}],"columns":[{"type":"GROUP_CATEGORY","field":"t_categorical_u8"}],"cell":$A}}`,
		"margin":  `{"crosstab":{"rows":[{"type":"GROUP_CATEGORY","field":"g"}],"columns":[{"type":"GROUP_CATEGORY","field":"t_categorical_u8"}],"cell":{"type":"AGG_COUNT","field":"x","label":"n"},"margin_aggregations":[$A]}}`,
	}
	aggs := map[string]string{
		"AGG_MEDIAN":     `{"type":"AGG_MEDIAN","field":"t_decimal128","label":"a"}`,
		"AGG_NULL_COUNT": `{"type":"AGG_NULL_COUNT","field":"t_decimal128","label":"a"}`,
		"AGG_PERCENTILE": `{"type":"AGG_PERCENTILE","field":"t_decimal128","label":"a","params":{"percentile":90}}`,
		"AGG_MODE":       `{"type":"AGG_MODE","field":"t_decimal128","label":"a"}`,
		"AGG_SUM":        `{"type":"AGG_SUM","field":"t_decimal128","label":"a"}`,
	}
	for shapeName, shape := range shapes {
		for op, agg := range aggs {
			body := strings.Replace(shape, "$A", agg, 1)
			mk := func() *types.Request {
				var req types.Request
				if err := json.Unmarshal([]byte(body), &req); err != nil {
					t.Fatalf("%s %s: %v", shapeName, op, err)
				}
				req.Cohort = &types.Cohort{Filename: cohort}
				return &req
			}
			_, rerr := p.Process(ctx, mk())
			env := predictEnvelope(t, p, fs, cohort, mk())
			var codes []string
			for _, e := range env.Errors {
				codes = append(codes, e.Code)
			}
			for _, w := range env.Warnings {
				if w.Code == string(errors.PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL) {
					t.Errorf("%s %s: decimal refusal as a warning", shapeName, op)
				}
			}
			if op == "AGG_SUM" || op == "AGG_NULL_COUNT" {
				if rerr != nil || len(codes) != 0 {
					t.Errorf("%s %s over decimal128: runtime %v, predict %v; want both to run", shapeName, op, rerr, codes)
				}
				continue
			}
			var ce *errors.CodedError
			if !stderrors.As(rerr, &ce) || ce.Code != errors.PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL {
				t.Errorf("%s %s: runtime %v, want PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL", shapeName, op, rerr)
			}
			if len(codes) != 1 || codes[0] != string(errors.PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL) {
				t.Errorf("%s %s: predict errors %v, want one PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL", shapeName, op, codes)
			}
		}
	}
}
