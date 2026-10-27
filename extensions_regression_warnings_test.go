package pulse_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Extension regression warnings (U62 E2-S1): a model's
// extend.RegressionOutput.Warnings ride its result, are lifted onto
// Response.Warnings (so the envelope promotes them), and under Strict
// fail the slot with the warning's own code.

const (
	regWarnBuffered types.RegressionType = "REG_ACME_WARN_OFF"
	regWarnStream   types.RegressionType = "REG_ACME_WARN_ON"
)

// diagnosticWarning is the warning the test models emit.
func diagnosticWarning() *types.ResponseWarning {
	return &types.ResponseWarning{
		Code:    string(perr.PROCESSING_REGRESSION_DIAGNOSTIC),
		Message: "chains did not mix",
		Details: map[string]any{"reason": "rhat", "rhat": 1.2},
	}
}

// warnBuffered is bufferedSlope plus one diagnostic warning.
type warnBuffered struct{ bufferedSlope }

func (b *warnBuffered) Fit(rows extend.Rows) (extend.RegressionOutput, error) {
	out, err := b.bufferedSlope.Fit(rows)
	out.Warnings = []*types.ResponseWarning{diagnosticWarning()}
	return out, err
}

// warnStream is streamSlope plus one diagnostic warning.
type warnStream struct{ streamSlope }

func (s *warnStream) Merge(other extend.StreamingRegression) error {
	return s.streamSlope.Merge(&other.(*warnStream).streamSlope)
}

func (s *warnStream) Finalize() (extend.RegressionOutput, error) {
	out, err := s.streamSlope.Finalize()
	out.Warnings = []*types.ResponseWarning{diagnosticWarning()}
	return out, err
}

func warningExtensions() pulse.Extensions {
	calls := &regCalls{}
	ext := regressionExtensions(calls)
	ext.Regressions = append(ext.Regressions,
		pulse.RegressionRegistration{Name: regWarnBuffered, Description: "buffered slope that warns", PValues: true, OutputSchema: slopeOutputs,
			Factory: func(spec *types.RegressionSpec, _ *encoding.Schema) (extend.Regression, error) {
				return &warnBuffered{bufferedSlope{acc: newSlopeAcc(spec), calls: calls}}, nil
			}},
		pulse.RegressionRegistration{Name: regWarnStream, Description: "streaming slope that warns", Streamable: true, PValues: true, OutputSchema: slopeOutputs,
			Factory: func(spec *types.RegressionSpec, _ *encoding.Schema) (extend.Regression, error) {
				return &warnStream{streamSlope{acc: newSlopeAcc(spec), calls: calls}}, nil
			}},
	)
	return ext
}

func openWarnE2E(t *testing.T, strict bool) (*pulse.Pulse, *types.Cohort) {
	t.Helper()
	fsys := afero.NewMemMapFs()
	writeParityCohort(t, fsys, "e2e.pulse", paritySchema(t), 0, paritySmallRows)
	p, err := pulse.New(pulse.Options{FS: fsys, Extensions: warningExtensions(), Strict: strict})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	return p, &types.Cohort{Filename: "e2e.pulse"}
}

func warnedSpec(t types.RegressionType, name string) *types.RegressionSpec {
	s := slopeSpec(t)
	s.Name = name
	return s
}

// TestExtensions_RegressionWarningsReachResultAndResponse: on the
// buffered and the streaming arm, the warning rides the slot's result
// (and its JSON) and is lifted once onto Response.Warnings, located by
// details.regression / details.index; the unwarned slot beside it has
// none and its figures equal a run without the warned slot.
func TestExtensions_RegressionWarningsReachResultAndResponse(t *testing.T) {
	t.Parallel()

	p, cohort := openWarnE2E(t, false)
	alone := processE2E(t, p, regressionRequest(cohort, warnedSpec(regBufferedName, "quiet")))
	for _, typ := range []types.RegressionType{regWarnBuffered, regWarnStream} {
		t.Run(string(typ), func(t *testing.T) {
			resp := processE2E(t, p, regressionRequest(cohort, warnedSpec(regBufferedName, "quiet"), warnedSpec(typ, "noisy")))
			quiet, noisy := resp.Regressions[0], resp.Regressions[1]
			if noisy.Error != nil || len(noisy.Warnings) != 1 || noisy.Warnings[0].Code != string(perr.PROCESSING_REGRESSION_DIAGNOSTIC) ||
				noisy.Warnings[0].Details["reason"] != "rhat" {
				t.Fatalf("noisy result warnings = %+v (error %+v), want the model's one diagnostic", noisy.Warnings, noisy.Error)
			}
			if _, set := noisy.Warnings[0].Details["regression"]; set {
				t.Errorf("result warning details gained the lift's locator: %v", noisy.Warnings[0].Details)
			}
			if len(noisy.Coefficients) == 0 {
				t.Error("warned slot lost its figures")
			}
			if quiet.Error != nil || len(quiet.Warnings) != 0 {
				t.Errorf("quiet slot = warnings %+v error %+v, want neither", quiet.Warnings, quiet.Error)
			}
			if quiet.Coefficients["qty"] != alone.Regressions[0].Coefficients["qty"] {
				t.Errorf("quiet slot changed beside the warned one: %v vs %v", quiet.Coefficients, alone.Regressions[0].Coefficients)
			}
			var lifted []*types.ResponseWarning
			for _, w := range resp.Warnings {
				if w.Code == string(perr.PROCESSING_REGRESSION_DIAGNOSTIC) {
					lifted = append(lifted, w)
				}
			}
			if len(lifted) != 1 || lifted[0].Details["regression"] != "noisy" || lifted[0].Details["index"] != 1 || lifted[0].Details["reason"] != "rhat" {
				t.Fatalf("response warnings = %+v, want one lifted diagnostic at regressions[1] noisy", resp.Warnings)
			}
			raw, err := json.Marshal(noisy)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(raw), `"warnings":[{"code":"PROCESSING_REGRESSION_DIAGNOSTIC"`) {
				t.Errorf("result wire lacks its warnings: %s", raw)
			}
		})
	}
}

// TestExtensions_RegressionWarningsAbsentByteIdentical: a model that
// emits no warning leaves no warnings key on its result and adds
// nothing to Response.Warnings.
func TestExtensions_RegressionWarningsAbsentByteIdentical(t *testing.T) {
	t.Parallel()

	p, cohort := openWarnE2E(t, false)
	resp := processE2E(t, p, regressionRequest(cohort, warnedSpec(regBufferedName, "quiet")))
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"warnings":[{`) || len(resp.Warnings) != 0 {
		t.Errorf("warning-free response carries warnings: %s", raw)
	}
}

// TestExtensions_RegressionWarningsStrictFailsTheSlot: under Strict
// the warned slot fails with the warning's own code — the per-slot
// Strict refusal, details {slot_kind, index, label} beside the
// warning's own — on both arms; an unwarned request still runs.
func TestExtensions_RegressionWarningsStrictFailsTheSlot(t *testing.T) {
	t.Parallel()

	p, cohort := openWarnE2E(t, true)
	for _, typ := range []types.RegressionType{regWarnBuffered, regWarnStream} {
		t.Run(string(typ), func(t *testing.T) {
			_, err := p.Process(context.Background(), regressionRequest(cohort, warnedSpec(regBufferedName, "quiet"), warnedSpec(typ, "noisy")))
			ce := requireCodedError(t, err, perr.PROCESSING_REGRESSION_DIAGNOSTIC)
			if ce.Details["slot_kind"] != "regressions" || ce.Details["index"] != 1 || ce.Details["label"] != "noisy" || ce.Details["reason"] != "rhat" {
				t.Errorf("strict details = %v, want slot_kind regressions, index 1, label noisy, reason rhat", ce.Details)
			}
		})
	}
	resp := processE2E(t, p, regressionRequest(cohort, warnedSpec(regBufferedName, "quiet")))
	if resp.Regressions[0].Error != nil || len(resp.Warnings) != 0 {
		t.Errorf("strict unwarned run = %+v / %+v, want a clean fit", resp.Regressions[0].Error, resp.Warnings)
	}
}

// TestExtensions_RegressionWarningsComposeFit: a Compose fit's
// regression warnings lift to ComposedResponse.Warnings with
// details.fit, and its fit result keeps them; under Strict the batch
// fails with the warning's code.
func TestExtensions_RegressionWarningsComposeFit(t *testing.T) {
	t.Parallel()

	batch := func(cohort *types.Cohort) *types.ComposedRequest {
		return &types.ComposedRequest{
			Fits: []types.FitSpec{
				{Name: "quiet", Request: regressionRequest(cohort, warnedSpec(regBufferedName, ""))},
				{Name: "noisy", Request: regressionRequest(cohort, warnedSpec(regWarnBuffered, ""))},
			},
			Requests: []*types.Request{{Cohort: cohort, Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "score", Label: "n"}}}},
		}
	}
	p, cohort := openWarnE2E(t, false)
	out, err := p.Compose(context.Background(), batch(cohort))
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	var lifted []*types.ResponseWarning
	for _, w := range out.Warnings {
		if w.Code == string(perr.PROCESSING_REGRESSION_DIAGNOSTIC) {
			lifted = append(lifted, w)
		}
	}
	if len(lifted) != 1 || lifted[0].Details["fit"] != 1 {
		t.Fatalf("composed warnings = %+v, want one diagnostic with details.fit 1", out.Warnings)
	}
	strict, scohort := openWarnE2E(t, true)
	_, err = strict.Compose(context.Background(), batch(scohort))
	requireCodedError(t, err, perr.PROCESSING_REGRESSION_DIAGNOSTIC)
}

// TestExtensions_RegressionWarningsRefused: a nil warning, an empty
// message or an unregistered code is the output mismatch, at the
// registration probe.
func TestExtensions_RegressionWarningsRefused(t *testing.T) {
	t.Parallel()

	with := func(ws ...*types.ResponseWarning) extend.RegressionOutput {
		out := okOutput()
		out.Warnings = ws
		return out
	}
	for _, tc := range []struct {
		name, reason string
		out          extend.RegressionOutput
	}{
		{"nil", "invalid_warning", with(nil)},
		{"empty_message", "invalid_warning", with(&types.ResponseWarning{Code: string(perr.PROCESSING_REGRESSION_DIAGNOSTIC)})},
		{"unregistered_code", "unregistered_warning_code", with(&types.ResponseWarning{Code: "ACME_NOT_MIXED", Message: "x"})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := pulse.New(newTestOptions(pulse.Extensions{Regressions: []pulse.RegressionRegistration{{Name: "REG_ACME_PROBE", Factory: modelFactory(probeModel{out: tc.out})}}}))
			ce := requireCodedError(t, err, perr.PULSE_EXTENSION_REGRESSION_OUTPUT_MISMATCH)
			if ce.Details["reason"] != tc.reason {
				t.Errorf("details.reason = %v, want %s", ce.Details["reason"], tc.reason)
			}
		})
	}
	if _, err := pulse.New(newTestOptions(pulse.Extensions{Regressions: []pulse.RegressionRegistration{{Name: "REG_ACME_PROBE",
		Factory: modelFactory(probeModel{out: with(diagnosticWarning())})}}})); err != nil {
		t.Fatalf("a registered warning was refused: %v", err)
	}
}
