package pulse_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestRequestFormula_IdentRefusalMatchesPredict (#440, #418): a
// Request-host (MATRIX) OVERLAY_FORMULA naming an unknown function or
// variable is PULSE_OVERLAY_FORMULA_INVALID_IDENT from predict AND the
// runtime — same message and details, index included — on the fused and
// buffered crosstab arms and inside a Compose slot. The runtime used to
// compile first and surface PULSE_OVERLAY_FORMULA_PARSE_ERROR. A vector
// built-in keeps its own built-in refusal on both sides; an embedder
// ExprFunction and a scalar-maths built-in still run.
func TestRequestFormula_IdentRefusalMatchesPredict(t *testing.T) {
	p, fs, cohort := acceptanceCohort(t)
	ext := pulse.Extensions{ExprFunctions: []pulse.ExprFunction{{Name: "boost", Fn: func(x float64) float64 { return 2 * x }}}}
	withExt, err := pulse.New(pulse.Options{FS: fs, Extensions: ext})
	if err != nil {
		t.Fatal(err)
	}
	buffered, err := pulse.New(pulse.Options{FS: fs, Extensions: ext, DisableCrosstabFusion: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	host := func(formula string) *types.Request {
		params, _ := json.Marshal(map[string]any{"formula": formula})
		switch formula {
		case "<no params>":
			params = nil
		case "<no formula key>":
			params = json.RawMessage(`{}`)
		case "<non-string>":
			params = json.RawMessage(`{"formula": 2}`)
		}
		return specRefusalHost(cohort, types.OverlaySpec{Name: "f", Kind: types.OverlayKindFormula, Scope: types.OverlayScopeCell, Params: params})
	}

	for _, c := range []struct {
		name, formula, ident string
		want                 errors.Code
	}{
		{"unknown callee", "bogusfn(cell)", "bogusfn", errors.PULSE_OVERLAY_FORMULA_INVALID_IDENT},
		{"unknown variable", "cell + bogusvar", "bogusvar", errors.PULSE_OVERLAY_FORMULA_INVALID_IDENT},
		{"vector built-in", "vsum(cell)", "vsum", errors.PULSE_OVERLAY_FORMULA_INVALID_IDENT},
		// U58: the trig set and the constants are reserved but outside
		// the overlay subset (exprenv.overlayAllowed).
		{"trig built-in", "sin(cell)", "sin", errors.PULSE_OVERLAY_FORMULA_INVALID_IDENT},
		{"constant built-in", "cell * pi()", "pi", errors.PULSE_OVERLAY_FORMULA_INVALID_IDENT},
		{"compose slot path", "slot.a.cell + cell", "slot.a.cell", errors.PULSE_OVERLAY_FORMULA_INVALID_IDENT},
		// The whole Request-host gate is shared (U35 #432); a spec with
		// no usable formula is PULSE_OVERLAY_PARAM_MISSING on every
		// FORMULA arm (E4-S3: the more specific code for "no formula"),
		// a syntax error the gate's parse message.
		{"empty formula", "", "", errors.PULSE_OVERLAY_PARAM_MISSING},
		{"no params", "<no params>", "", errors.PULSE_OVERLAY_PARAM_MISSING},
		{"no formula key", "<no formula key>", "", errors.PULSE_OVERLAY_PARAM_MISSING},
		{"non-string formula", "<non-string>", "", errors.PULSE_OVERLAY_PARAM_MISSING},
		{"parse error", "cell +", "", errors.PULSE_OVERLAY_FORMULA_PARSE_ERROR},
	} {
		t.Run(c.name, func(t *testing.T) {
			pe := firstPredictError(t, predictEnvelope(t, withExt, fs, cohort, host(c.formula)), nil)
			if pe == nil || pe.Code != string(c.want) || pe.Details["index"] != 1 ||
				(c.ident != "" && pe.Details["unknown_identifier"] != c.ident) {
				t.Fatalf("predict %+v, want %s %q at index 1", pe, c.want, c.ident)
			}
			want := c.want
			_, err := withExt.Process(ctx, host(c.formula))
			sameRefusal(t, "process fused", pe, err, want)
			_, err = buffered.Process(ctx, host(c.formula))
			sameRefusal(t, "process buffered", pe, err, want)
			_, err = withExt.Compose(ctx, &types.ComposedRequest{Requests: []*types.Request{host(c.formula)}})
			sameRefusal(t, "compose slot", pe, unlocated(t, err), want)
		})
	}

	// In reach: an embedder callee, a scalar-maths built-in and the
	// MATRIX namespace run clean on both sides; without the embedder
	// registration the callee is unknown on both.
	for _, formula := range []string{"boost(cell)", "sqrt(cell) + margin_row", "cell / margin_grand"} {
		if env := predictEnvelope(t, withExt, fs, cohort, host(formula)); len(env.Errors) != 0 {
			t.Errorf("%s: predict %+v", formula, env.Errors)
		}
		if _, err := withExt.Process(ctx, host(formula)); err != nil {
			t.Errorf("%s: process %v", formula, err)
		}
	}
	pe := firstPredictError(t, predictEnvelope(t, p, fs, cohort, host("boost(cell)")), nil)
	_, err = p.Process(ctx, host("boost(cell)"))
	sameRefusal(t, "no extension", pe, err, errors.PULSE_OVERLAY_FORMULA_INVALID_IDENT)
}

// TestComposeFormula_MissingFormulaMatchesPredict (E4-S3): a Compose
// OVERLAY_FORMULA with no usable formula is PULSE_OVERLAY_PARAM_MISSING
// {index, kind, param, reason} from predict and the runtime alike — the
// code every FORMULA arm (Request host MATRIX / SERIES, Compose) uses
// for "no formula"; a present formula that does not parse keeps
// PULSE_OVERLAY_FORMULA_PARSE_ERROR.
func TestComposeFormula_MissingFormulaMatchesPredict(t *testing.T) {
	p, _ := nsFixture(t, pulse.Options{})
	ctx := context.Background()
	for _, c := range []struct {
		name   string
		params map[string]any
		want   errors.Code
		reason string
	}{
		{"missing", map[string]any{}, errors.PULSE_OVERLAY_PARAM_MISSING, "missing formula param"},
		{"empty", map[string]any{"formula": ""}, errors.PULSE_OVERLAY_PARAM_MISSING, "formula param is empty or non-string"},
		{"non-string", map[string]any{"formula": 3.0}, errors.PULSE_OVERLAY_PARAM_MISSING, "formula param is empty or non-string"},
		{"parse error", map[string]any{"formula": "value +"}, errors.PULSE_OVERLAY_FORMULA_PARSE_ERROR, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			mk := func() *pulse.ComposedRequest {
				s := cfSpec("f", types.OverlayKindFormula, types.OverlayScopeGroup, "")
				s.Params = c.params
				return cfSlots(types.OverlayShapeSeries, s)
			}
			env, err := p.PredictCompose(ctx, mk())
			if err != nil {
				t.Fatal(err)
			}
			if len(env.Errors) != 1 {
				t.Fatalf("predict %v, want one refusal", envCodes(env))
			}
			pe := env.Errors[0]
			if c.reason != "" && (pe.Details["reason"] != c.reason || pe.Details["param"] != "formula") {
				t.Fatalf("predict details %v", pe.Details)
			}
			_, err = p.Compose(ctx, mk())
			sameRefusal(t, "compose", pe, err, c.want)
		})
	}
}
