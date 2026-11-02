package integration_test

import (
	"context"
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestTestOptions_PredictMatchesRuntime (U64 #644): every refusal of
// the opt-in test options — TEST_T params.variance, TEST_MANN_WHITNEY_U
// params.exact, TEST_CHISQ params.correct, TEST_PROP_Z params.interval
// / params.correct (internal/testopts) and OVERLAY_CHISQ_MATRIX
// params.correct (overlayhost.ParseChiSqMatrixParams) — is raised by
// predict and the runtime with the same code and message, and each
// accepted form runs on both.
func TestTestOptions_PredictMatchesRuntime(t *testing.T) {
	t.Parallel()

	p, fs, cohort := packedBoolLevelCohort(t)
	ctx := context.Background()
	const crosstab = `"crosstab":{"rows":[{"type":"GROUP_CATEGORY","field":"cat_flag"}],"columns":[{"type":"GROUP_CATEGORY","field":"cat_grp"}],"cell":{"type":"AGG_COUNT","field":"x"}}`
	cases := []struct {
		name, body string
		code       errors.Code
	}{
		{"variance not an enum value", `{"tests":[{"type":"TEST_T","field":"x","split_by":"cat_grp","params":{"variance":"equal"}}]}`, errors.PROCESSING_CONFIG},
		{"variance not a string", `{"tests":[{"type":"TEST_T","field":"x","split_by":"cat_grp","params":{"variance":true}}]}`, errors.PROCESSING_CONFIG},
		{"pooled one-sample", `{"tests":[{"type":"TEST_T","field":"x","params":{"variance":"pooled"}}]}`, errors.PROCESSING_CONFIG},
		{"exact not a bool", `{"tests":[{"type":"TEST_MANN_WHITNEY_U","field":"x","split_by":"cat_grp","params":{"exact":"yes"}}]}`, errors.PROCESSING_CONFIG},
		{"chisq correct not a bool", `{"tests":[{"type":"TEST_CHISQ","rows":"cat_flag","cols":"cat_grp","params":{"correct":1}}]}`, errors.PROCESSING_CONFIG},
		{"interval not an enum value", `{"tests":[{"type":"TEST_PROP_Z","field":"cat_flag","params":{"success":"1","p0":0.5,"interval":"score"}}]}`, errors.PROCESSING_CONFIG},
		{"wilson two-proportion", `{"tests":[{"type":"TEST_PROP_Z","field":"cat_flag","split_by":"cat_grp","params":{"success":"1","interval":"wilson"}}]}`, errors.PROCESSING_CONFIG},
		{"prop correct with covariate", `{"tests":[{"type":"TEST_PROP_Z","field":"cat_flag","split_by":"cat_grp","params":{"success":"1","covariate":"y","correct":true}}]}`, errors.PROCESSING_CONFIG},
		{"overlay correct not a bool", `{` + crosstab + `,"overlays":[{"kind":"OVERLAY_CHISQ_MATRIX","scope":"matrix","params":{"correct":"yes"}}]}`, errors.PULSE_OVERLAY_PARAM_MISSING},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := decodeRequest(t, c.body)
			req.Cohort = &types.Cohort{Filename: cohort}
			_, err := p.Process(ctx, req)
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != c.code {
				t.Fatalf("runtime: %v, want %s", err, c.code)
			}
			env := predictEnvelope(t, p, fs, cohort, req)
			if len(env.Errors) != 1 || env.Errors[0].Code != string(c.code) || env.Errors[0].Message != ce.Message {
				t.Fatalf("predict %+v, runtime %s %q", env.Errors, ce.Code, ce.Message)
			}
		})
	}

	// The accepted forms (defaults spelled out included).
	for _, body := range []string{
		`{"tests":[{"type":"TEST_T","field":"x","split_by":"cat_grp","params":{"variance":"pooled"}}]}`,
		`{"tests":[{"type":"TEST_T","field":"x","split_by":"cat_grp","params":{"variance":"pooled","covariate":"y"}}]}`,
		`{"tests":[{"type":"TEST_T","field":"x","params":{"variance":"unequal"}}]}`,
		`{"tests":[{"type":"TEST_MANN_WHITNEY_U","field":"x","split_by":"cat_grp","params":{"exact":true}}]}`,
		`{"tests":[{"type":"TEST_CHISQ","rows":"cat_flag","cols":"cat_grp","params":{"correct":true}}]}`,
		`{"tests":[{"type":"TEST_CHISQ","rows":"cat_flag","params":{"correct":true,"expected":{"0":0.5,"1":0.5}}}]}`,
		`{"tests":[{"type":"TEST_PROP_Z","field":"cat_flag","params":{"success":"1","p0":0.4,"interval":"wilson","correct":true}}]}`,
		`{"tests":[{"type":"TEST_PROP_Z","field":"cat_flag","split_by":"cat_grp","params":{"success":"1","correct":true,"interval":"wald"}}]}`,
		`{` + crosstab + `,"overlays":[{"kind":"OVERLAY_CHISQ_MATRIX","scope":"matrix","params":{"correct":true}}]}`,
	} {
		req := decodeRequest(t, body)
		req.Cohort = &types.Cohort{Filename: cohort}
		if env := predictEnvelope(t, p, fs, cohort, req); len(env.Errors) != 0 {
			t.Errorf("predict refused %s: %+v", body, env.Errors)
		}
		if _, err := p.Process(ctx, req); err != nil {
			t.Errorf("runtime refused %s: %v", body, err)
		}
	}
}
