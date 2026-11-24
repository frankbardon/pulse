package integration_test

import (
	"context"
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestAnovaRM_DuplicateCellFailsSlotAlone (U69 #678): on the
// packed-bool cohort, split by cat_flag, some subject has both of its
// rows in one flag level — a repeated (subject, condition) cell. The
// refusal is data-dependent, so predict (header and schema only) accepts
// the request; the runtime fails the TEST_ANOVA_RM slot alone with
// PULSE_TEST_BALANCED_DESIGN_REQUIRED (subject / condition in details)
// and a PULSE_SLOT_FAILED warning, the sibling slot keeps its figures,
// and Strict fails the run with the slot's own code.
func TestAnovaRM_DuplicateCellFailsSlotAlone(t *testing.T) {
	t.Parallel()

	p, fs, cohort := packedBoolLevelCohort(t)
	ctx := context.Background()
	for _, params := range []string{``, `,"params":{"incomplete":"mixed"}`} {
		body := `{"tests":[{"type":"TEST_ANOVA_RM","field":"x","split_by":"cat_flag","subject_field":"subj"` + params + `},` +
			`{"type":"TEST_ANOVA_RM","field":"x","split_by":"cat_grp","subject_field":"subj"` + params + `}]}`
		req := decodeRequest(t, body)
		req.Cohort = &types.Cohort{Filename: cohort}
		if env := predictEnvelope(t, p, fs, cohort, req); len(env.Errors) != 0 {
			t.Fatalf("predict refused %s: %+v", body, env.Errors)
		}
		resp, err := p.Process(ctx, req)
		if err != nil {
			t.Fatalf("Process %s: %v", body, err)
		}
		bad, good := resp.Tests[0], resp.Tests[1]
		if bad.Error == nil || bad.Error.Code != string(errors.PULSE_TEST_BALANCED_DESIGN_REQUIRED) {
			t.Fatalf("duplicate slot = %+v", bad)
		}
		if bad.Error.Details["subject"] == nil || bad.Error.Details["condition"] == nil {
			t.Errorf("details = %v, want subject and condition", bad.Error.Details)
		}
		if good.Error != nil {
			t.Errorf("balanced sibling failed: %+v", good.Error)
		}
		found := false
		for _, w := range resp.Warnings {
			if w.Code == string(errors.PULSE_SLOT_FAILED) && w.Details["code"] == string(errors.PULSE_TEST_BALANCED_DESIGN_REQUIRED) {
				found = true
			}
		}
		if !found {
			t.Errorf("no PULSE_SLOT_FAILED warning in %+v", resp.Warnings)
		}
	}

	strict, err := pulse.New(pulse.Options{FS: fs, Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	req := decodeRequest(t, `{"tests":[{"type":"TEST_ANOVA_RM","field":"x","split_by":"cat_flag","subject_field":"subj"}]}`)
	req.Cohort = &types.Cohort{Filename: cohort}
	_, err = strict.Process(ctx, req)
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_TEST_BALANCED_DESIGN_REQUIRED {
		t.Fatalf("strict: %v, want PULSE_TEST_BALANCED_DESIGN_REQUIRED", err)
	}
}
