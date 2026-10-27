package pulse

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"reflect"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// reg_predict_parity_test.go: every ATTR_REG_PREDICT refusal —
// PULSE_MODEL_UNKNOWN, PULSE_MODEL_SCORE_INVALID, PULSE_MODEL_TERM_UNKNOWN,
// PULSE_MODEL_PREDICTOR_MISSING, PULSE_MODEL_PREDICTOR_NOT_NUMERIC — is
// reported by PredictCompose exactly as Compose and ComposeParallel
// return it (code, message, details with details.request), and a plain
// Predict refuses an unbound fit exactly as Process does.

const fitsCategoricalCohort = "fits_categorical.pulse"

func scoringParityPulse(t *testing.T) (*Pulse, afero.Fs) {
	t.Helper()
	fsys := fitsFacadeFS(t)
	// A cohort whose x is categorical: a fit's numeric predictor read
	// off it is non-numeric.
	var rows [][]string
	for i := 0; i < 20; i++ {
		rows = append(rows, []string{[]string{"lo", "hi"}[i%2], jsonNumber(float64(i))})
	}
	createTestPulseFile(t, fsys, fitsCategoricalCohort, []string{"x", "y"}, rows)
	p, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	return p, fsys
}

func scoringSlot(cohort string, attrs ...*types.Attribute) *Request {
	return &Request{
		Label:        "s",
		Cohort:       &types.Cohort{Filename: cohort},
		Attributes:   attrs,
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "y", Label: "sum_y"}},
	}
}

func scoreRef(params string) *types.Attribute {
	return &types.Attribute{Type: types.ATTR_REG_PREDICT, Label: "p", Params: json.RawMessage(params)}
}

func TestRegPredict_PredictRuntimeParity(t *testing.T) {
	t.Parallel()

	p, fsys := scoringParityPulse(t)
	// "lx" exists only in the fit's frame (its own FEAT_LOG).
	logFit := facadeFitRequest("0", "90")
	logFit.Features = []*types.Feature{{Type: types.FEAT_LOG, Field: "x", Label: "lx"}}
	logFit.Regressions[0].Predictors = []string{"lx"}
	cases := []struct {
		name string
		fit  *Request
		slot *Request
		code perr.Code
	}{
		{"unknown fit", facadeFitRequest("0", "90"), scoringSlot(fitsFacadeCohort, scoreRef(`{"fit":"nope"}`)), perr.PULSE_MODEL_UNKNOWN},
		{"scale on OLS", facadeFitRequest("0", "90"), scoringSlot(fitsFacadeCohort, scoreRef(`{"fit":"m","scale":"link"}`)), perr.PULSE_MODEL_SCORE_INVALID},
		{"no fit param", facadeFitRequest("0", "90"), scoringSlot(fitsFacadeCohort, scoreRef(`{}`)), perr.PULSE_MODEL_SCORE_INVALID},
		{"term not a predictor", facadeFitRequest("0", "90"), scoringSlot(fitsFacadeCohort, scoreRef(`{"fit":"m","terms":["week"]}`)), perr.PULSE_MODEL_TERM_UNKNOWN},
		{"predictor missing from the scoring frame", logFit, scoringSlot(fitsFacadeCohort, scoreRef(`{"fit":"m"}`)), perr.PULSE_MODEL_PREDICTOR_MISSING},
		{"predictor non-numeric on the scoring frame", facadeFitRequest("0", "90"), scoringSlot(fitsCategoricalCohort, scoreRef(`{"fit":"m"}`)), perr.PULSE_MODEL_PREDICTOR_NOT_NUMERIC},
	}
	ctx := context.Background()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &ComposedRequest{Fits: []types.FitSpec{{Name: "m", Request: tc.fit}}, Requests: []*Request{tc.slot}}
			env, err := p.PredictCompose(ctx, c)
			if err != nil {
				t.Fatal(err)
			}
			if len(env.Errors) != 1 {
				t.Fatalf("predict errors = %+v, want exactly one", env.Errors)
			}
			pe := env.Errors[0]
			if perr.Code(pe.Code) != tc.code || pe.Details["request"] != 0 {
				t.Fatalf("predict = %s %v, want %s located at request 0", pe.Code, pe.Details, tc.code)
			}
			runs := map[string]func() error{
				"Compose": func() error { _, err := p.Compose(ctx, c); return err },
				"ComposeParallel": func() error {
					_, err := p.ComposeParallel(ctx, c, ComposeOptions{MaxWorkers: 2, FailFast: true})
					return err
				},
			}
			for name, run := range runs {
				var ce *perr.CodedError
				if err := run(); !stderrors.As(err, &ce) {
					t.Fatalf("%s: uncoded %v", name, err)
				}
				if string(ce.Code) != pe.Code || ce.Message != pe.Message || !reflect.DeepEqual(ce.Details, pe.Details) {
					t.Errorf("%s: runtime %s %q %v\n predict %s %q %v", name, ce.Code, ce.Message, ce.Details, pe.Code, pe.Message, pe.Details)
				}
			}
		})
	}

	// Outside a compose nothing is bound: Predict and Process refuse the
	// reference identically (available is empty).
	t.Run("plain request", func(t *testing.T) {
		req := scoringSlot(fitsFacadeCohort, scoreRef(`{"fit":"m"}`))
		data, err := afero.ReadFile(fsys, fitsFacadeCohort)
		if err != nil {
			t.Fatal(err)
		}
		env, err := p.PredictBytes(ctx, data, req)
		if err != nil {
			t.Fatal(err)
		}
		if len(env.Errors) != 1 || env.Errors[0].Code != string(perr.PULSE_MODEL_UNKNOWN) {
			t.Fatalf("predict errors = %+v", env.Errors)
		}
		var ce *perr.CodedError
		if _, err := p.Process(ctx, req); !stderrors.As(err, &ce) {
			t.Fatalf("process: uncoded %v", err)
		}
		pe := env.Errors[0]
		if string(ce.Code) != pe.Code || ce.Message != pe.Message || !reflect.DeepEqual(ce.Details, pe.Details) {
			t.Errorf("runtime %s %q %v\n predict %s %q %v", ce.Code, ce.Message, ce.Details, pe.Code, pe.Message, pe.Details)
		}
	})
}
