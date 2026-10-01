package processing

import (
	"encoding/json"
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	pulseerrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Acceptance coverage for E2-S4: an overlay failure reports its OWN
// code on the wire, so `pulse errors lookup errors[0].code` returns the
// per-code prose `TestCodesHaveFixups` mandates.
//
// The property is asserted DIRECTLY — take the error a handler really
// raises, push it through the envelope exactly as the `--json` leaves
// do, read `errors[0].code` back off the marshalled JSON, and look that
// string up in errors/fixup_metadata.go. It is deliberately NOT
// inferred from the raise sites: a grep over `NewCodedErrorWithDetails`
// would still pass if the envelope or the lookup index disagreed.

// overlayErrorEnvelopeCode mirrors internal/cli.writeCodedErrorEnvelope:
// unwrap with errors.As, seat the CodedError's own Code in the envelope,
// fall back to the leaf placeholder only for an UNCODED error. Returns
// the code string and the details map as they appear on the wire after a
// full JSON round trip.
func overlayErrorEnvelopeCode(t *testing.T, err error) (string, map[string]any) {
	t.Helper()
	env := descriptor.NewEnvelope(nil)
	var ce *pulseerrors.CodedError
	if stderrors.As(err, &ce) {
		env.AddError(string(ce.Code), ce.Message, ce.Details)
	} else {
		env.AddError("PROCESS_ERROR", err.Error(), nil)
	}

	raw, merr := json.Marshal(env)
	if merr != nil {
		t.Fatalf("marshal envelope: %v", merr)
	}
	var decoded struct {
		Errors []struct {
			Code    string         `json:"code"`
			Message string         `json:"message"`
			Details map[string]any `json:"details"`
		} `json:"errors"`
	}
	if uerr := json.Unmarshal(raw, &decoded); uerr != nil {
		t.Fatalf("unmarshal envelope: %v", uerr)
	}
	if len(decoded.Errors) != 1 {
		t.Fatalf("envelope carries %d errors, want exactly 1: %s", len(decoded.Errors), raw)
	}
	if decoded.Errors[0].Message == "" {
		t.Errorf("envelope errors[0].message is empty; the actionable prose was dropped")
	}
	return decoded.Errors[0].Code, decoded.Errors[0].Details
}

// errCodeLookupHost is a two-row × one-column matrix with the Welford
// triples a pairwise kind reads. Components are attached so the
// COMPONENTS_REQUIRED arm can be reached separately by dropping them.
func errCodeLookupMatrix() *types.MatrixPayload {
	return &types.MatrixPayload{
		RowHeader:    types.AxisHeader{Fields: []string{"brand"}, Types: []string{"GROUP_CATEGORY"}},
		ColumnHeader: types.AxisHeader{Fields: []string{"aud"}, Types: []string{"GROUP_CATEGORY"}},
		RowKeys:      []types.AxisKey{{"A"}, {"B"}},
		ColumnKeys:   []types.AxisKey{{"x"}},
		Cells: [][]types.MatrixCell{
			{{Value: 10.0, Present: true}},
			{{Value: 12.0, Present: true}},
		},
	}
}

// TestOverlayErrorCodes_RoundTripToFixupMetadata is the story's
// acceptance test. One representative failure per overlay fault SHAPE —
// bad param, unsupported scope, missing components, unknown reference —
// each asserted to land a code on `errors[0].code` that resolves to a
// codeMetadata entry with a Message and a usable fixup.
func TestOverlayErrorCodes_RoundTripToFixupMetadata(t *testing.T) {
	cases := []struct {
		shape    string
		wantCode pulseerrors.Code
		raise    func(t *testing.T) error
	}{
		{
			shape:    "bad param",
			wantCode: pulseerrors.PULSE_OVERLAY_PARAM_MISSING,
			raise: func(t *testing.T) error {
				specs := []types.OverlaySpec{{
					Kind:   types.OverlayKindPairwisePropZ,
					Scope:  types.OverlayScopeRow,
					Params: json.RawMessage(`{"n_source":`),
				}}
				_, _, err := applyOverlays(specs, newCrosstabHostViewWithComponents(
					errCodeLookupMatrix(), &types.CrosstabComponents{}))
				return err
			},
		},
		{
			shape:    "unsupported scope",
			wantCode: pulseerrors.PULSE_OVERLAY_SCOPE_UNSUPPORTED,
			raise: func(t *testing.T) error {
				host := newFacetResultDiscrete("category", []types.FacetValueCount{
					{Value: "a", Count: 50},
					{Value: "b", Count: 50},
				}, 2, 100, 100, 0)
				pop := newFacetResultNumeric("category", &types.FacetNumeric{
					Count: 100, Mean: 50, StdDev: 25, Min: 0, Max: 100,
				}, 100, 100, 0)
				popView, rerr := ResolveFacetPopulation(pop, "category")
				if rerr != nil {
					t.Fatalf("ResolveFacetPopulation: %v", rerr)
				}
				_, _, err := applyKSVsPop(ksVsPopSpec(), host.Fields["category"], popView)
				return err
			},
		},
		{
			shape:    "missing components",
			wantCode: pulseerrors.PULSE_OVERLAY_COMPONENTS_REQUIRED,
			raise: func(t *testing.T) error {
				specs := []types.OverlaySpec{{
					Kind:  types.OverlayKindPairwisePropZ,
					Scope: types.OverlayScopeRow,
				}}
				_, _, err := applyOverlays(specs, NewCrosstabHostView(errCodeLookupMatrix()))
				return err
			},
		},
		{
			shape:    "unknown ref",
			wantCode: pulseerrors.PULSE_OVERLAY_REFERENCE_UNKNOWN,
			raise: func(t *testing.T) error {
				byLabel := map[string]*types.Response{
					"baseline": composeSlotOf(types.OverlayShapeScalar),
				}
				_, err := LookupReference(byLabel, "does_not_exist", 0)
				return err
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.shape, func(t *testing.T) {
			err := tc.raise(t)
			if err == nil {
				t.Fatalf("%s: expected a coded overlay failure, got nil", tc.shape)
			}

			code, details := overlayErrorEnvelopeCode(t, err)

			// The placeholder is the exact regression this story
			// removes: a coded overlay fault must never wear it.
			if code == string(pulseerrors.PROCESSING_INTERNAL) {
				t.Fatalf("%s: errors[0].code = PROCESSING_INTERNAL; the real code was swallowed", tc.shape)
			}
			if code != string(tc.wantCode) {
				t.Errorf("%s: errors[0].code = %q, want %q", tc.shape, code, tc.wantCode)
			}

			// The property: the wire code is resolvable prose.
			res, ok := pulseerrors.Lookup(code)
			if !ok {
				t.Fatalf("%s: `pulse errors lookup %s` finds nothing — the code does not round-trip to a codeMetadata entry", tc.shape, code)
			}
			if res.Message == "" {
				t.Errorf("%s: lookup(%s).Message is empty", tc.shape, code)
			}
			if !res.FixupNotApplicable && len(res.Fixups) == 0 {
				t.Errorf("%s: lookup(%s) offers neither a fixup nor FixupNotApplicable", tc.shape, code)
			}

			// The code must not survive as a second copy in Details —
			// two authorities invite a reader to pick the wrong one.
			if dup, present := details["code"]; present {
				t.Errorf("%s: Details still carries a duplicate code echo %v; the CodedError's own Code is authoritative", tc.shape, dup)
			}
		})
	}
}

// TestOverlayErrorCodes_DetailsPayloadSurvives pins the other half of
// the conversion: dropping the `code` echo must not disturb any OTHER
// Details key. The details payload is the actionable half of these
// errors — the slot label, the offending index, the observed shape.
func TestOverlayErrorCodes_DetailsPayloadSurvives(t *testing.T) {
	byLabel := map[string]*types.Response{
		"baseline": composeSlotOf(types.OverlayShapeScalar),
	}
	_, err := LookupReference(byLabel, "does_not_exist", 7)
	if err == nil {
		t.Fatal("LookupReference(unknown): want error, got nil")
	}
	_, details := overlayErrorEnvelopeCode(t, err)

	for key, want := range map[string]any{
		"index":      float64(7), // JSON round-trips ints as float64
		"slot_label": "does_not_exist",
		"which":      "reference",
	} {
		got, present := details[key]
		if !present {
			t.Errorf("Details[%q] missing after the conversion", key)
			continue
		}
		if got != want {
			t.Errorf("Details[%q] = %v (%T), want %v (%T)", key, got, got, want, want)
		}
	}
}

// TestPairwiseWelfordDistinct_PredictAndRuntimeAgreeOnCode records the
// decision E2-S4 had to make. A Welford-input pairwise kind carrying a
// DISTINCT n_source was refused twice under two different codes:
// PULSE_OVERLAY_PARAM_MISSING at predict (the kind does not accept
// n_source at all) and PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE at
// runtime (the cell aggregator is not distinct-key). One request, two
// `pulse errors lookup` answers — and the runtime one pointed at the
// cell aggregator, which is not the fixable thing: predict refuses
// n_source on a Welford kind whatever the host holds.
//
// PARAM_MISSING wins. The runtime now reports predict's code for the
// case it already refused; nothing that succeeded before starts
// failing. This test asserts the runtime half; the predict half lives
// in internal/descriptor/overlay_pairwise_welford_params_test.go.
func TestPairwiseWelfordDistinct_PredictAndRuntimeAgreeOnCode(t *testing.T) {
	for _, kind := range []types.OverlayKind{
		types.OverlayKindPairwiseWelchT,
		types.OverlayKindPairwiseTwoMeansZ,
	} {
		for _, mode := range []string{
			types.PairwiseNSourceNWithinDistinct,
			types.PairwiseNSourceRowMarginDistinct,
			types.PairwiseNSourceColumnMarginDistinct,
		} {
			specs := []types.OverlaySpec{{
				Kind:   kind,
				Scope:  types.OverlayScopeRow,
				Params: mustParams(t, types.PairwiseOverlayParams{NSource: mode}),
			}}
			_, _, err := applyOverlays(specs, pairwiseWelfordHost())
			if err == nil {
				t.Fatalf("%s n_source=%s: want a refusal, got nil", kind, mode)
			}
			code, _ := overlayErrorEnvelopeCode(t, err)
			if code != string(pulseerrors.PULSE_OVERLAY_PARAM_MISSING) {
				t.Errorf("%s n_source=%s: errors[0].code = %q, want %q (predict's code)",
					kind, mode, code, pulseerrors.PULSE_OVERLAY_PARAM_MISSING)
			}
		}
	}
}
