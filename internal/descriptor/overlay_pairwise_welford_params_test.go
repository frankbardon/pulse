package descriptor

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Predict-arm coverage for the Welford-input mode-selector refusal.
//
// OVERLAY_PAIRWISE_WELCH_T and OVERLAY_PAIRWISE_TWO_MEANS_Z read mean,
// variance AND n from the AGG_WELFORD triple, so neither `n_source` nor
// `p_source` reaches the math. Before this gate, setting one was a
// silent no-op: the caller believed they had moved the n leg and had
// not. The refusal is deliberately NOT scoped to the distinct modes —
// if the selector is meaningless on these kinds it is meaningless for
// every mode, and a rule that refused three of nine would be
// incoherent.
//
// descriptor.ValidateOverlays is reached from exactly one place
// (predict.go), so these exercise the validator directly.

// pwWelfordKinds is the pair of kinds that read the Welford triple.
// Sourced from types.PairwiseKindUsesWelford so a third such kind
// cannot be added without this table noticing.
func pwWelfordKinds(t *testing.T) []types.OverlayKind {
	t.Helper()
	var out []types.OverlayKind
	for _, k := range types.AllOverlayKinds() {
		if types.PairwiseKindUsesWelford(k) {
			out = append(out, k)
		}
	}
	if len(out) != 2 {
		t.Fatalf("expected exactly 2 Welford-input pairwise kinds, got %d (%v)", len(out), out)
	}
	return out
}

// pwProportionKinds is the complementary pair — the kinds that DO read
// the selectors and must stay unaffected by the refusal.
func pwProportionKinds(t *testing.T) []types.OverlayKind {
	t.Helper()
	var out []types.OverlayKind
	for _, k := range types.AllOverlayKinds() {
		if types.IsPairwiseOverlayKind(k) && !types.PairwiseKindUsesWelford(k) {
			out = append(out, k)
		}
	}
	if len(out) != 2 {
		t.Fatalf("expected exactly 2 proportion-input pairwise kinds, got %d (%v)", len(out), out)
	}
	return out
}

// pwAllNSources is every n_source mode the validator accepts on a
// proportion-input kind: the six that predate this effort plus the
// three distinct-key modes E1 added.
var pwAllNSources = []string{
	types.PairwiseNSourceCellNUnweighted,
	types.PairwiseNSourceCellValueWeight,
	types.PairwiseNSourceCellWeightSum,
	types.PairwiseNSourceRowMarginN,
	types.PairwiseNSourceColumnMarginN,
	types.PairwiseNSourceNWithin,
	types.PairwiseNSourceNWithinDistinct,
	types.PairwiseNSourceRowMarginDistinct,
	types.PairwiseNSourceColumnMarginDistinct,
}

// pwWelfordRequest builds a minimal well-formed pairwise request: a
// MATRIX host, ROW scope, empty Ref. Only Params varies, so every
// failure these tests observe is the params gate and not a shape gate.
func pwWelfordRequest(kind types.OverlayKind, params string) *types.Request {
	var raw json.RawMessage
	if params != "" {
		raw = json.RawMessage(params)
	}
	return &types.Request{
		Cohort: &types.Cohort{Filename: "c.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "brand"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "wave"}},
			Cell:    &types.Aggregation{Type: types.AGG_WELFORD, Field: "score"},
			Shape:   types.CrosstabShapeMatrix,
		},
		Overlays: []types.OverlaySpec{{
			Name:   "pw",
			Kind:   kind,
			Scope:  types.OverlayScopeRow,
			Params: raw,
		}},
	}
}

func pwWelfordValidate(kind types.OverlayKind, params string) *descriptor.Envelope {
	env := descriptor.NewEnvelope(nil)
	ValidateOverlays(env, pwWelfordRequest(kind, params), nil, nil)
	return env
}

// TestValidateOverlays_WelfordKindRefusesEveryNSource is the core
// assertion: all nine modes, on both Welford kinds, refused. The six
// pre-existing modes are in the table deliberately — this is the
// behaviour break, and scoping it to the three new modes would leave
// the rule incoherent.
func TestValidateOverlays_WelfordKindRefusesEveryNSource(t *testing.T) {
	for _, kind := range pwWelfordKinds(t) {
		for _, mode := range pwAllNSources {
			t.Run(string(kind)+"/"+mode, func(t *testing.T) {
				env := pwWelfordValidate(kind, `{"n_source":"`+mode+`"}`)
				got := pwPartFindError(env, string(errors.PULSE_OVERLAY_PARAM_MISSING))
				if got == nil {
					t.Fatalf("expected %s for n_source=%s on %s; got codes %v",
						errors.PULSE_OVERLAY_PARAM_MISSING, mode, kind, pwPartErrorCodes(env))
				}
				// Details must name the kind, the param and the reason
				// so `pulse errors lookup` plus Details is enough to fix
				// the request without reading source.
				for key, want := range map[string]any{
					"kind":     string(kind),
					"param":    "n_source",
					"n_source": mode,
					"index":    0,
				} {
					if have := got.Details[key]; have != want {
						t.Errorf("Details[%q] = %v, want %v", key, have, want)
					}
				}
				reason, _ := got.Details["reason"].(string)
				if !strings.Contains(reason, "AGG_WELFORD") {
					t.Errorf("Details[reason] = %q, want it to name the Welford triple", reason)
				}
				if !strings.Contains(got.Message, "does not accept n_source") {
					t.Errorf("Message = %q, want it to state the param is not accepted", got.Message)
				}
			})
		}
	}
}

// TestValidateOverlays_WelfordKindRefusesUnknownNSource pins the ORDER
// of the two n_source checks. A bogus mode on a Welford kind must be
// refused as not-accepted, not as unknown — "unknown n_source" would
// tell the caller a known one would work, and none would.
func TestValidateOverlays_WelfordKindRefusesUnknownNSource(t *testing.T) {
	for _, kind := range pwWelfordKinds(t) {
		env := pwWelfordValidate(kind, `{"n_source":"not_a_mode"}`)
		got := pwPartFindError(env, string(errors.PULSE_OVERLAY_PARAM_MISSING))
		if got == nil {
			t.Fatalf("%s: expected %s; got codes %v", kind,
				errors.PULSE_OVERLAY_PARAM_MISSING, pwPartErrorCodes(env))
		}
		if strings.Contains(got.Message, "unknown n_source") {
			t.Errorf("%s: Message = %q, want the not-accepted refusal, not the unknown-mode one", kind, got.Message)
		}
		if have := got.Details["param"]; have != "n_source" {
			t.Errorf("%s: Details[param] = %v, want n_source", kind, have)
		}
	}
}

// TestValidateOverlays_WelfordKindRefusesPSource covers the second
// selector. p_source is inert on these kinds for exactly the same
// reason n_source is — both moments come from the triple — so it
// refuses under the same code and the same key shape.
func TestValidateOverlays_WelfordKindRefusesPSource(t *testing.T) {
	for _, kind := range pwWelfordKinds(t) {
		for _, mode := range []string{types.PairwisePSourceCellValuePct, types.PairwisePSourceCellValue} {
			t.Run(string(kind)+"/"+mode, func(t *testing.T) {
				env := pwWelfordValidate(kind, `{"p_source":"`+mode+`"}`)
				got := pwPartFindError(env, string(errors.PULSE_OVERLAY_PARAM_MISSING))
				if got == nil {
					t.Fatalf("expected %s for p_source=%s on %s; got codes %v",
						errors.PULSE_OVERLAY_PARAM_MISSING, mode, kind, pwPartErrorCodes(env))
				}
				for key, want := range map[string]any{
					"kind":     string(kind),
					"param":    "p_source",
					"p_source": mode,
				} {
					if have := got.Details[key]; have != want {
						t.Errorf("Details[%q] = %v, want %v", key, have, want)
					}
				}
			})
		}
	}
}

// TestValidateOverlays_WelfordKindReportsBothSelectors — a request
// setting both gets both refusals in one pass, not the first one and a
// second round trip.
func TestValidateOverlays_WelfordKindReportsBothSelectors(t *testing.T) {
	for _, kind := range pwWelfordKinds(t) {
		env := pwWelfordValidate(kind, `{"n_source":"row_margin_n","p_source":"cell_value"}`)
		var params []string
		for _, e := range env.Errors {
			if e.Code != string(errors.PULSE_OVERLAY_PARAM_MISSING) {
				continue
			}
			if p, ok := e.Details["param"].(string); ok {
				params = append(params, p)
			}
		}
		if len(params) != 2 {
			t.Fatalf("%s: refused params = %v, want both n_source and p_source in one pass", kind, params)
		}
	}
}

// TestValidateOverlays_WelfordKindAcceptsAbsentSelectors — the default
// path is untouched. An absent Params blob, an empty object, an
// explicit empty selector, pair_along_dim and n_within_depth all stay
// valid on a Welford kind.
//
// n_within_depth is deliberately in this list. It is equally inert
// here, but its inertness is a CONSEQUENCE of n_source's rather than an
// independent fact — a non-zero depth without a slab n_source is just
// as inert on the proportion kinds, so refusing it only on the Welford
// kinds would be the incoherent rule this story exists to avoid. It is
// also a plain int whose zero value is meaningful, so "was it set?" is
// not observable after decode.
func TestValidateOverlays_WelfordKindAcceptsAbsentSelectors(t *testing.T) {
	for _, kind := range pwWelfordKinds(t) {
		for _, params := range []string{
			"",
			`{}`,
			`{"n_source":""}`,
			`{"p_source":""}`,
			`{"pair_along_dim":0}`,
			`{"n_within_depth":2}`,
			`{"n_within_depth":0,"pair_along_dim":0}`,
		} {
			t.Run(string(kind)+"/"+params, func(t *testing.T) {
				env := pwWelfordValidate(kind, params)
				if len(env.Errors) != 0 {
					t.Fatalf("params %s on %s: expected no errors, got %v", params, kind, pwPartErrorCodes(env))
				}
			})
		}
	}
}

// TestValidateOverlays_ProportionKindsStillAcceptSelectors guards the
// blast radius. The refusal must not leak onto the two kinds that
// genuinely read the selectors.
func TestValidateOverlays_ProportionKindsStillAcceptSelectors(t *testing.T) {
	for _, kind := range pwProportionKinds(t) {
		for _, mode := range pwAllNSources {
			env := pwWelfordValidate(kind, `{"n_source":"`+mode+`"}`)
			if got := pwPartFindError(env, string(errors.PULSE_OVERLAY_PARAM_MISSING)); got != nil {
				t.Errorf("%s n_source=%s: unexpected refusal %q", kind, mode, got.Message)
			}
		}
		for _, mode := range []string{types.PairwisePSourceCellValuePct, types.PairwisePSourceCellValue} {
			env := pwWelfordValidate(kind, `{"p_source":"`+mode+`"}`)
			if got := pwPartFindError(env, string(errors.PULSE_OVERLAY_PARAM_MISSING)); got != nil {
				t.Errorf("%s p_source=%s: unexpected refusal %q", kind, mode, got.Message)
			}
		}
	}
}
