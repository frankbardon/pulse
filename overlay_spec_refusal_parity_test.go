package pulse_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"sort"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/overlayhost"
	"github.com/frankbardon/pulse/types"
)

// Every crosstab (MATRIX host) overlay kind with a Ref / Scope rule
// refuses a bad Ref and a bad Scope identically in predict and at
// runtime — code, message and details, index included — on every
// runtime arm: the fused and the buffered Process crosstab and a Compose
// slot's own Request.Overlays. (No ProcessChain stage hosts a crosstab:
// a crosstab-only stage is not mergeable, and one beside aggregations
// runs the aggregations only.) One shared
// rule (overlayhost.CrosstabSpecRefusal) serves both sides; before it
// the runtime re-checked neither for most kinds, so a request predict
// refused ran silently in Process.

// specRefusalCase is one kind × fault row.
type specRefusalCase struct {
	name string
	spec types.OverlaySpec
	want errors.Code
}

// specRefusalCases derives the rows from the shared rule table: a bad
// Ref and a bad Scope per kind (refMargin kinds: Ref.Margin missing and
// an unknown axis), plus a Ref arm predict's old six-arm check missed.
func specRefusalCases(t *testing.T) []specRefusalCase {
	t.Helper()
	kinds := overlayhost.CrosstabSpecKinds()
	sort.Slice(kinds, func(i, j int) bool { return kinds[i] < kinds[j] })
	if len(kinds) < 21 {
		t.Fatalf("rule table lists %d kinds, want every crosstab kind with a Ref / Scope rule", len(kinds))
	}
	var out []specRefusalCase
	for _, kind := range kinds {
		scopes := overlayhost.CrosstabSpecScopes(kind)
		good := types.OverlaySpec{Name: "o", Kind: kind, Scope: scopes[0]}
		var bad types.OverlayScope
		for _, s := range []types.OverlayScope{types.OverlayScopeCell, types.OverlayScopeMatrix, types.OverlayScopeTotal} {
			supported := false
			for _, ok := range scopes {
				supported = supported || ok == s
			}
			if !supported {
				bad = s
				break
			}
		}
		margin := isMarginKind(kind)
		if margin {
			good.Ref = types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow}}
		}
		scope := good
		scope.Scope = bad
		out = append(out, specRefusalCase{string(kind) + "/scope", scope, errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED})
		if margin {
			none := good
			none.Ref = types.OverlayRef{}
			axis := good
			axis.Ref = types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: "diagonal"}}
			out = append(out,
				specRefusalCase{string(kind) + "/ref_missing", none, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE},
				specRefusalCase{string(kind) + "/ref_axis", axis, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE})
			continue
		}
		ref := good
		ref.Ref = types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow}}
		prior := good
		prior.Ref = types.OverlayRef{Prior: &types.OverlayPriorRef{}}
		out = append(out,
			specRefusalCase{string(kind) + "/ref", ref, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE},
			specRefusalCase{string(kind) + "/ref_prior", prior, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE})
	}
	return out
}

func isMarginKind(kind types.OverlayKind) bool {
	switch kind {
	case types.OverlayKindDeltaVsMargin, types.OverlayKindIndexVsMargin, types.OverlayKindShareOfRow,
		types.OverlayKindShareOfCol, types.OverlayKindShareOfTotal, types.OverlayKindZScoreVsMargin:
		return true
	}
	return false
}

// specRefusalHost is a g × g COUNT crosstab carrying spec as its second
// overlay (index 1), behind a well-formed CHISQ_MATRIX at index 0.
func specRefusalHost(cohort string, spec types.OverlaySpec) *types.Request {
	return markovCrosstab(cohort, []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}}, types.AGG_COUNT, nil,
		types.OverlaySpec{Name: "chi", Kind: types.OverlayKindChiSqMatrix, Scope: types.OverlayScopeMatrix}, spec)
}

func firstPredictError(t *testing.T, env *descriptor.Envelope, err error) *descriptor.EnvelopeEntry {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Errors) == 0 {
		return nil
	}
	return env.Errors[0]
}

// unlocated strips a Compose slot locator (details.request) off a
// coded runtime error, failing when a locator names another slot.
func unlocated(t *testing.T, err error) error {
	t.Helper()
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) {
		return err
	}
	d := map[string]any{}
	for k, v := range ce.Details {
		if k == "request" {
			if v != 0 {
				t.Fatalf("compose refusal located at request %v, want 0", v)
			}
			continue
		}
		d[k] = v
	}
	return errors.NewCodedErrorWithDetails(ce.Code, ce.Message, d)
}

func sameRefusal(t *testing.T, arm string, pe *descriptor.EnvelopeEntry, runErr error, want errors.Code) {
	t.Helper()
	var ce *errors.CodedError
	if !stderrors.As(runErr, &ce) {
		t.Fatalf("%s: runtime did not refuse (err %v); predict %+v", arm, runErr, pe)
	}
	if pe == nil {
		t.Fatalf("%s: predict accepted what the runtime refused: %s %q", arm, ce.Code, ce.Message)
	}
	if ce.Code != want || pe.Code != string(ce.Code) || pe.Message != ce.Message {
		t.Fatalf("%s: predict %s %q, runtime %s %q, want %s", arm, pe.Code, pe.Message, ce.Code, ce.Message, want)
	}
	have, _ := json.Marshal(pe.Details)
	got, _ := json.Marshal(ce.Details)
	if string(have) != string(got) {
		t.Fatalf("%s: predict details %s, runtime details %s", arm, have, got)
	}
}

func TestCrosstabOverlay_SpecRefusalsMatchPredict(t *testing.T) {
	p, fs, cohort := acceptanceCohort(t)
	buffered, err := pulse.New(pulse.Options{FS: fs, DisableCrosstabFusion: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// Control: the well-formed host alone runs on every arm.
	ctl := specRefusalHost(cohort, types.OverlaySpec{Name: "o", Kind: types.OverlayKindChiSqRow, Scope: types.OverlayScopeRow})
	if env := predictEnvelope(t, p, fs, cohort, ctl); len(env.Errors) != 0 {
		t.Fatalf("control predict %+v", env.Errors)
	}
	if _, err := p.Process(ctx, ctl); err != nil {
		t.Fatalf("control: %v", err)
	}

	for _, c := range specRefusalCases(t) {
		t.Run(c.name, func(t *testing.T) {
			req := specRefusalHost(cohort, c.spec)
			env := predictEnvelope(t, p, fs, cohort, req)
			pe := firstPredictError(t, env, nil)
			if pe == nil || pe.Details["index"] != 1 {
				t.Fatalf("predict %+v, want a refusal at index 1", pe)
			}
			_, err := p.Process(ctx, specRefusalHost(cohort, c.spec))
			sameRefusal(t, "process fused", pe, err, c.want)
			_, err = buffered.Process(ctx, specRefusalHost(cohort, c.spec))
			sameRefusal(t, "process buffered", pe, err, c.want)

			// A Compose slot is predicted by Predict on the slot request
			// (PredictCompose judges the batch, not each slot's own
			// overlays); the runtime refusal is the same, located by
			// details.request.
			_, err = p.Compose(ctx, &types.ComposedRequest{Requests: []*types.Request{specRefusalHost(cohort, c.spec)}})
			sameRefusal(t, "compose slot", pe, unlocated(t, err), c.want)

		})
	}
}
