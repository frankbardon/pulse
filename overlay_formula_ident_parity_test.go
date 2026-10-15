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
		return specRefusalHost(cohort, types.OverlaySpec{Name: "f", Kind: types.OverlayKindFormula, Scope: types.OverlayScopeCell, Params: params})
	}

	for _, c := range []struct {
		name, formula, ident string
		want                 errors.Code
	}{
		{"unknown callee", "bogusfn(cell)", "bogusfn", errors.PULSE_OVERLAY_FORMULA_INVALID_IDENT},
		{"unknown variable", "cell + bogusvar", "bogusvar", errors.PULSE_OVERLAY_FORMULA_INVALID_IDENT},
		{"vector built-in", "vsum(cell)", "vsum", errors.PULSE_OVERLAY_FORMULA_INVALID_IDENT},
		{"compose slot path", "slot.a.cell + cell", "slot.a.cell", errors.PULSE_OVERLAY_FORMULA_INVALID_IDENT},
		// The whole Request-host gate is shared (U35 #432): an empty
		// formula was the runtime's PULSE_OVERLAY_PARAM_MISSING and a
		// syntax error its compiler message.
		{"empty formula", "", "", errors.PULSE_OVERLAY_FORMULA_INVALID_IDENT},
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
