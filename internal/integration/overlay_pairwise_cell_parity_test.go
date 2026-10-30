package integration_test

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
)

// welfordExtSchema declares the Welford keys an extension cell must
// emit to host a moment pairwise kind.
func welfordExtSchema() descriptor.ComponentSchema {
	return descriptor.ComponentSchema{
		Keys: []descriptor.ComponentKey{
			{Name: "mean", Type: "float64", Description: "Mean of the brand scores."},
			{Name: "variance", Type: "float64", Description: "Sample variance stand-in."},
		},
		Mergeability: descriptor.None,
	}
}

func welfordExtEmit(instance extend.Aggregator) (map[string]any, error) {
	a := instance.(*brandScoreAggregator)
	mean := 0.0
	if a.count > 0 {
		mean = a.sum / float64(a.count)
	}
	return map[string]any{"mean": mean, "variance": 1.0}, nil
}

// TestPairwiseMoment_CellShapeMatchesRuntime (#448):
// OVERLAY_PAIRWISE_WELCH_T / _TWO_MEANS_Z read the Welford triple off
// each cell's components, so a crosstab cell whose components cannot
// carry it is PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE from predict as
// from the runtime — on the fused and buffered arms and inside a Compose
// slot. Predict used to accept AGG_MODE and AGG_SUM cells. The verdict
// reads the capability table, so an extension cell is judged by its
// declared ComponentSchema: one declaring mean + variance runs, one
// without them is refused on both sides.
func TestPairwiseMoment_CellShapeMatchesRuntime(t *testing.T) {
	t.Parallel()

	_, fs, cohort := acceptanceCohort(t)
	ext := pulse.Extensions{Aggregators: []pulse.AggregatorRegistration{
		{
			Name: "AGG_ACME_BRAND_SCORE", Factory: brandScoreFactory, Streamable: true,
			Accepts: []encoding.FieldType{encoding.FieldTypeF64}, ComponentSchema: brandScoreSchema(), ComponentsFunc: brandScoreEmit,
		},
		{
			Name: "AGG_ACME_BRAND_MOMENTS", Factory: brandScoreFactory, Streamable: true,
			Accepts: []encoding.FieldType{encoding.FieldTypeF64}, ComponentSchema: welfordExtSchema(), ComponentsFunc: welfordExtEmit,
		},
	}}
	p, err := pulse.New(pulse.Options{FS: fs, Extensions: ext})
	if err != nil {
		t.Fatal(err)
	}
	buffered, err := pulse.New(pulse.Options{FS: fs, Extensions: ext, DisableCrosstabFusion: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	host := func(kind types.OverlayKind, cell types.AggregationType) *types.Request {
		return markovCrosstab(cohort, []*types.Group{{Type: types.GROUP_CATEGORY, Field: "subj"}}, cell, nil,
			types.OverlaySpec{Name: "pw", Kind: kind, Scope: types.OverlayScopeColumn})
	}
	for _, kind := range []types.OverlayKind{types.OverlayKindPairwiseWelchT, types.OverlayKindPairwiseTwoMeansZ} {
		for _, c := range []struct {
			cell   types.AggregationType
			refuse bool
		}{
			{types.AGG_MODE, true},
			{types.AGG_SUM, true},
			{"AGG_ACME_BRAND_SCORE", true},
			{types.AGG_WELFORD, false},
			{"AGG_ACME_BRAND_MOMENTS", false},
		} {
			t.Run(string(kind)+"/"+string(c.cell), func(t *testing.T) {
				env := predictEnvelope(t, p, fs, cohort, host(kind, c.cell))
				if !c.refuse {
					if len(env.Errors) != 0 {
						t.Fatalf("predict %v", envCodes(env))
					}
					for name, inst := range map[string]*pulse.Pulse{"fused": p, "buffered": buffered} {
						if _, err := inst.Process(ctx, host(kind, c.cell)); err != nil {
							t.Fatalf("%s: %v", name, err)
						}
					}
					if _, err := p.Compose(ctx, composeOneSlot(cohort, host(kind, c.cell))); err != nil {
						t.Fatalf("compose: %v", err)
					}
					return
				}
				if len(env.Errors) != 1 {
					t.Fatalf("predict %v, want one refusal", envCodes(env))
				}
				pe := env.Errors[0]
				if pe.Details["index"] != 0 || pe.Details["cell_type"] != string(c.cell) {
					t.Fatalf("predict details %v", pe.Details)
				}
				want := errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE
				for name, inst := range map[string]*pulse.Pulse{"fused": p, "buffered": buffered} {
					_, err := inst.Process(ctx, host(kind, c.cell))
					ce := codeOf(t, err)
					if ce.Code != want || pe.Code != string(want) || pe.Message != ce.Message {
						t.Fatalf("%s: predict %s %q, runtime %s %q", name, pe.Code, pe.Message, ce.Code, ce.Message)
					}
				}
				assertComposeParity(t, p, "compose slot", func() *types.ComposedRequest {
					s := host(kind, c.cell)
					s.Label = "pw"
					return composeOneSlot(cohort, s)
				}, want, 1)
			})
		}
	}
}
