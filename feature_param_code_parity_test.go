package pulse_test

import (
	"context"
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse/errors"
)

// TestFeatureParamRefusal_PredictMatchesRuntime pins E2-S4 (governing
// rule 3): every feature param refusal — a missing field, the train /
// test ratios, the bucketize edges, the target-encoding params, the
// polynomial degree — is raised under ONE code on both sides, the
// runtime constructor's PROCESSING_CONFIG (predict used
// SERVICE_VALIDATION), and predict reports it exactly once.
func TestFeatureParamRefusal_PredictMatchesRuntime(t *testing.T) {
	p, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	const count = `,"aggregations":[{"type":"AGG_COUNT","field":"x"}]}`
	cases := []struct{ family, name, body string }{
		{"field", "FEAT_LOG no field", `{"features":[{"type":"FEAT_LOG"}]` + count},
		{"field", "FEAT_ONE_HOT no field", `{"features":[{"type":"FEAT_ONE_HOT"}]` + count},
		{"split", "no params", `{"features":[{"type":"FEAT_TRAIN_TEST_SPLIT"}]` + count},
		{"split", "malformed", `{"features":[{"type":"FEAT_TRAIN_TEST_SPLIT","params":{"ratios":"x"}}]` + count},
		{"split", "one ratio", `{"features":[{"type":"FEAT_TRAIN_TEST_SPLIT","params":{"ratios":[1]}}]` + count},
		{"split", "negative ratio", `{"features":[{"type":"FEAT_TRAIN_TEST_SPLIT","params":{"ratios":[1.5,-0.5]}}]` + count},
		{"split", "sum not 1", `{"features":[{"type":"FEAT_TRAIN_TEST_SPLIT","params":{"ratios":[0.5,0.4],"stratify":"x"}}]` + count},
		{"bucketize", "no params", `{"features":[{"type":"FEAT_BUCKETIZE","field":"x"}]` + count},
		{"bucketize", "malformed", `{"features":[{"type":"FEAT_BUCKETIZE","field":"x","params":{"quantiles":"x"}}]` + count},
		{"bucketize", "neither", `{"features":[{"type":"FEAT_BUCKETIZE","field":"x","params":{"quantiles":0}}]` + count},
		{"bucketize", "both", `{"features":[{"type":"FEAT_BUCKETIZE","field":"x","params":{"quantiles":4,"boundaries":[1,2]}}]` + count},
		{"target", "no params", `{"features":[{"type":"FEAT_TARGET_ENCODE","field":"g"}]` + count},
		{"target", "malformed", `{"features":[{"type":"FEAT_TARGET_ENCODE","field":"g","params":{"target":5}}]` + count},
		{"target", "no target", `{"features":[{"type":"FEAT_TARGET_ENCODE","field":"g","params":{"smoothing":1}}]` + count},
		{"target", "negative smoothing", `{"features":[{"type":"FEAT_TARGET_ENCODE","field":"g","params":{"target":"x","smoothing":-1}}]` + count},
		{"target", "negative smoothing, bad field", `{"features":[{"type":"FEAT_TARGET_ENCODE","field":"x","params":{"target":"x","smoothing":-1}}]` + count},
		{"poly", "no params", `{"features":[{"type":"FEAT_POLY","field":"x"}]` + count},
		{"poly", "malformed", `{"features":[{"type":"FEAT_POLY","field":"x","params":{"degree":"x"}}]` + count},
		{"poly", "degree 1", `{"features":[{"type":"FEAT_POLY","field":"x","params":{"degree":1}}]` + count},
		{"poly", "degree 11", `{"features":[{"type":"FEAT_POLY","field":"x","params":{"degree":11}}]` + count},
	}
	for _, c := range cases {
		_, rerr := p.Process(ctx, categoricalRequest(t, c.body, cohort))
		var ce *errors.CodedError
		if !stderrors.As(rerr, &ce) || ce.Code != errors.PROCESSING_CONFIG {
			t.Errorf("%s / %s runtime: %v, want %s", c.family, c.name, rerr, errors.PROCESSING_CONFIG)
			continue
		}
		env := predictEnvelope(t, p, fs, cohort, categoricalRequest(t, c.body, cohort))
		if len(env.Errors) != 1 || env.Errors[0].Code != string(ce.Code) {
			var got []string
			for _, e := range env.Errors {
				got = append(got, e.Code+" "+e.Message)
			}
			t.Errorf("%s / %s predict: %v, want one %s (runtime: %s)", c.family, c.name, got, ce.Code, ce.Message)
		}
	}
}
