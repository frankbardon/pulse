package pulse_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// TestRegressionUnknownType_PredictRuntimeParity: a regression type no
// registry resolves — a never-registered name, a namespaced
// extension-shaped name whose registration is absent (REG_ACME_FIT),
// one past a valid slot, one under a holdout — is refused by predict
// and by every runtime surface that takes a regressions slot (Process,
// ProcessStream, Compose, a Compose fit; Materialize refuses the slot
// itself) with the same code (PULSE_REGRESSION_INVALID)
// and the same sentence, indexed by the slot's position in the request.
func TestRegressionUnknownType_PredictRuntimeParity(t *testing.T) {
	t.Parallel()

	p, fsys := windowParityCohort(t, pulse.Extensions{})
	for _, tc := range []struct {
		name, typ string
		lead      string // a valid slot placed before the unknown one
		extra     string // extra keys on the unknown slot
		index     int
	}{
		{name: "never-registered", typ: "REG_NOPE"},
		{name: "unregistered-extension", typ: "REG_ACME_FIT"},
		{name: "second-slot", typ: "REG_NOPE", lead: `{"type": "REG_OLS", "target": "x", "predictors": ["y"]}, `, index: 1},
		{name: "holdout", typ: "REG_NOPE", lead: `{"type": "REG_OLS", "target": "x", "predictors": ["y"]}, `, extra: `, "holdout": "t > 8"`, index: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := func() *types.Request {
				var r types.Request
				slot := `{"regressions": [` + tc.lead + `{"type": "` + tc.typ + `", "target": "x", "predictors": ["y"]` + tc.extra + `}]}`
				if err := json.Unmarshal([]byte(slot), &r); err != nil {
					t.Fatal(err)
				}
				r.Cohort = &types.Cohort{Filename: "series.pulse"}
				return &r
			}
			want := assertUnknownRegressionTypeParity(t, p, fsys, tc.typ, req)
			if wantMsg := "regression[" + string(rune('0'+tc.index)) + "]: unknown regression type " + tc.typ; want.Message != wantMsg {
				t.Errorf("message = %q, want %q", want.Message, wantMsg)
			}
			if want.Details["regression_index"] != float64(tc.index) && want.Details["regression_index"] != tc.index {
				t.Errorf("details.regression_index = %v, want %d", want.Details["regression_index"], tc.index)
			}
			if want.Details["type"] != tc.typ {
				t.Errorf("details.type = %v, want %s", want.Details["type"], tc.typ)
			}
		})
	}
	if md, ok := errors.MetadataFor(errors.PULSE_REGRESSION_INVALID); !ok || len(md.Fixups) == 0 {
		t.Fatalf("PULSE_REGRESSION_INVALID has no fixups: %+v", md)
	}
}

// assertUnknownRegressionTypeParity: predict reports the unknown
// regression type as exactly one PULSE_REGRESSION_INVALID error, and
// Process, ProcessStream, Compose and a Compose fit each
// refuse the same request with that code, message and details, as a
// *errors.CodedError. Returns predict's error.
func assertUnknownRegressionTypeParity(t *testing.T, p *pulse.Pulse, fsys afero.Fs, typ string, req func() *types.Request) *errors.CodedError {
	t.Helper()
	ctx := context.Background()
	env := predictEnvelope(t, p, fsys, "series.pulse", req())
	if len(env.Errors) != 1 || env.Errors[0].Code != string(errors.PULSE_REGRESSION_INVALID) || !strings.Contains(env.Errors[0].Message, "unknown regression type "+typ) {
		t.Fatalf("predict did not refuse %s as one unknown regression type: %+v", typ, env.Errors)
	}
	pe := env.Errors[0]

	_, perr := p.Process(ctx, req())
	_, serr := p.ProcessStream(ctx, req())
	_, cerr := p.Compose(ctx, &types.ComposedRequest{Requests: []*types.Request{req()}})
	surfaces := []struct {
		name string
		err  error
	}{{"Process", perr}, {"ProcessStream", serr}, {"Compose", cerr}}
	// A Compose fit holds exactly one regression: only the first-slot
	// cases reach it as a fit.
	if r := req(); len(r.Regressions) == 1 {
		fitEnv, err := p.PredictCompose(ctx, &types.ComposedRequest{
			Fits:     []types.FitSpec{{Name: "m", Request: req()}},
			Requests: []*types.Request{{Cohort: &types.Cohort{Filename: "series.pulse"}, Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x"}}}},
		})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, e := range fitEnv.Errors {
			if e.Code == pe.Code && e.Message == pe.Message {
				found = true
			}
		}
		if !found {
			t.Errorf("PredictCompose did not refuse fit %s with %s %q: %+v", typ, pe.Code, pe.Message, fitEnv.Errors)
		}
		_, ferr := p.Compose(ctx, &types.ComposedRequest{
			Fits:     []types.FitSpec{{Name: "m", Request: req()}},
			Requests: []*types.Request{{Cohort: &types.Cohort{Filename: "series.pulse"}, Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x"}}}},
		})
		surfaces = append(surfaces, struct {
			name string
			err  error
		}{"Compose fit", ferr})
	}
	for _, s := range surfaces {
		var ce *errors.CodedError
		if !stderrors.As(s.err, &ce) {
			t.Errorf("%s accepted unknown regression type %s: err = %v", s.name, typ, s.err)
			continue
		}
		if ce.Code != errors.PULSE_REGRESSION_INVALID || ce.Message != pe.Message {
			t.Errorf("%s refused %s as %s %q, want predict's %s %q", s.name, typ, ce.Code, ce.Message, errors.PULSE_REGRESSION_INVALID, pe.Message)
			continue
		}
		if ce.Details["type"] != pe.Details["type"] || ce.Details["regression_index"] != pe.Details["regression_index"] {
			t.Errorf("%s details = %v, want predict's %v", s.name, ce.Details, pe.Details)
		}
	}
	return &errors.CodedError{Code: errors.Code(pe.Code), Message: pe.Message, Details: pe.Details}
}

// TestTestUnknownType_PredictRuntimeParity: an unknown test type, in
// the tier-1 `tests` slot or the tier-2 `post_tests` slot, is refused by
// predict and the runtime with PULSE_TEST_UNKNOWN_TYPE and the same
// sentence.
func TestTestUnknownType_PredictRuntimeParity(t *testing.T) {
	t.Parallel()

	p, fsys := windowParityCohort(t, pulse.Extensions{})
	ctx := context.Background()
	for _, tc := range []struct {
		name, slot, want string
	}{
		{name: "tests", slot: `{"tests": [{"type": "TEST_NOPE", "field": "x", "split": "g"}]}`, want: "test type TEST_NOPE is not registered as a row test"},
		{name: "post_tests", slot: `{"groups": [{"type": "GROUP_CATEGORY", "field": "g"}], "aggregations": [{"type": "AGG_COUNT", "field": "x", "label": "n"}], "post_tests": [{"type": "TEST_NOPE", "field": "n"}]}`, want: "test type TEST_NOPE is not registered as a post test"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := func() *types.Request {
				var r types.Request
				if err := json.Unmarshal([]byte(tc.slot), &r); err != nil {
					t.Fatal(err)
				}
				r.Cohort = &types.Cohort{Filename: "series.pulse"}
				return &r
			}
			env := predictEnvelope(t, p, fsys, "series.pulse", req())
			found := false
			for _, e := range env.Errors {
				if e.Code == string(errors.PULSE_TEST_UNKNOWN_TYPE) {
					found = true
					if e.Message != tc.want {
						t.Errorf("predict message = %q, want %q", e.Message, tc.want)
					}
				}
			}
			if !found {
				t.Fatalf("predict did not refuse TEST_NOPE: %+v", env.Errors)
			}
			_, err := p.Process(ctx, req())
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_TEST_UNKNOWN_TYPE || ce.Message != tc.want {
				t.Errorf("Process err = %v, want PULSE_TEST_UNKNOWN_TYPE %q", err, tc.want)
			}
		})
	}
}
