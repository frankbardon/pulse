package descriptor

import (
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// TestValidateChainAndFacet_CompressedCohortCode: a zstd transfer
// artifact at the cohort path is PULSE_COHORT_COMPRESSED on the chain
// and facet validators too — the code whose fixup names the fix —
// matching Inspect and Predict (headerErrorCode), not ENCODING_INVALID.
func TestValidateChainAndFacet_CompressedCohortCode(t *testing.T) {
	data := append(encoding.ZstdMagic[:], []byte("synthetic-not-a-real-frame")...)
	chain := ValidateChainFromBytes(data, &types.ChainRequest{
		Cohort: &types.Cohort{Filename: "x.pulse"},
		Stages: []*types.ChainStage{{Request: &types.Request{
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x"}},
		}}},
	})
	facet := ValidateFacetFromBytes(data, &types.FacetRequest{
		Cohort: &types.Cohort{Filename: "x.pulse"}, Fields: []string{"x"},
	})
	for name, env := range map[string]*descriptor.Envelope{"chain": chain, "facet": facet} {
		if len(env.Errors) == 0 || env.Errors[0].Code != "PULSE_COHORT_COMPRESSED" {
			t.Errorf("%s: errors = %+v, want errors[0].code PULSE_COHORT_COMPRESSED", name, env.Errors)
		}
	}
}
