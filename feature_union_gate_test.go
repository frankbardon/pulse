package pulse

import (
	"context"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestHiddenUnion_SampleAndLookupRefusedAsUnknownField: the two roots
// outside the Request family — SampleWithRequest and Lookup — refuse a
// union slot the instance hides (capability:union) as an unknown field,
// before any union rule judges it; the default instance runs the same
// request's union rules instead.
func TestHiddenUnion_SampleAndLookupRefusedAsUnknownField(t *testing.T) {
	t.Parallel()

	fsys := parityFS(t)
	control := newParityHost(t, fsys, "")
	h := newParityHost(t, fsys, "minimal")
	ctx := context.Background()
	union := func() []types.UnionMember {
		return []types.UnionMember{
			{Cohort: &types.Cohort{Filename: parityCohort}, Label: "a"},
			{Cohort: &types.Cohort{Filename: parityCohort}, Label: "b"},
		}
	}
	cases := []struct {
		name string
		slot string
		run  func(h *parityHost) *slotGateOutcome
	}{
		{"SampleWithRequest/union", "union", func(h *parityHost) *slotGateOutcome {
			_, err := h.p.SampleWithRequest(ctx, &SampleRequest{Union: union(), N: 2})
			return slotGateFromErr(err)
		}},
		{"SampleWithRequest/union_source_field", "union_source_field", func(h *parityHost) *slotGateOutcome {
			_, err := h.p.SampleWithRequest(ctx, &SampleRequest{UnionSourceField: "src", N: 2})
			return slotGateFromErr(err)
		}},
		{"Lookup/union", "union", func(h *parityHost) *slotGateOutcome {
			_, err := h.p.Lookup(ctx, &LookupRequest{Union: union(), Field: "region", Value: "north",
				Multiplicity: types.LookupMultiplicityFirst})
			return slotGateFromErr(err)
		}},
		{"Lookup/union_source_field", "union_source_field", func(h *parityHost) *slotGateOutcome {
			_, err := h.p.Lookup(ctx, &LookupRequest{Cohort: &types.Cohort{Filename: parityCohort},
				UnionSourceField: "src", Field: "region", Value: "north"})
			return slotGateFromErr(err)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if open := c.run(control); open != nil && open.code == perr.PULSE_REQUEST_UNKNOWN_FIELD {
				t.Fatalf("default instance refuses: %+v", open)
			}
			assertSlotRefusal(t, c.run(h), c.slot, []string{"union", "union_source_field"})
		})
	}
}
