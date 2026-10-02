package pulse_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// TestFieldRefs_FacetFiltererRefusedLikeValidator: a FacetSchema
// filterer naming an unknown field is refused at runtime — with and
// without FACET-host overlays — with exactly the code, message and
// details ValidateFacet reports (the shared FieldRefRefusals filterer
// rule). Before, the validator refused it and the runtime filtered on
// an all-null column and kept nothing.
func TestFieldRefs_FacetFiltererRefusedLikeValidator(t *testing.T) {
	fs, cohort := zoneCohort(t)
	data, err := afero.ReadFile(fs, cohort)
	if err != nil {
		t.Fatal(err)
	}
	p := zonePulse(t, fs, "")
	ctx := context.Background()
	opts := &descx.PredictOptions{SchemaLoader: schemaLoaderFor(fs)}
	catParam := json.RawMessage(`{"field":"cat"}`)
	cases := map[string]func() *types.FacetRequest{
		"plain": func() *types.FacetRequest {
			return &types.FacetRequest{Cohort: &types.Cohort{Filename: cohort}, Fields: []string{"cat"},
				Filterers: []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "zz", Values: []string{"a"}}}}
		},
		"with overlay": func() *types.FacetRequest {
			return &types.FacetRequest{Cohort: &types.Cohort{Filename: cohort}, Fields: []string{"cat"},
				Filterers: []*types.Filterer{{Type: types.FILTER_RANGE, Field: "zz", Values: []string{"0", "10"}}},
				Overlays: []types.OverlaySpec{{Name: "ix", Kind: types.OverlayKindIndexVsPop, Scope: types.OverlayScopeGroup,
					Ref: types.OverlayRef{Population: &types.OverlayPopulationRef{Cohort: cohort}}, Params: catParam}}}
		},
	}
	for name, mk := range cases {
		t.Run(name, func(t *testing.T) {
			_, rerr := p.FacetSchema(ctx, mk())
			ce := requireCode(t, rerr, errors.SERVICE_VALIDATION)
			if ce.Message != "filter references unknown field: zz" {
				t.Fatalf("runtime message = %q", ce.Message)
			}
			sameEntry(t, descx.ValidateFacetWithOptions(bytes.NewReader(data), mk(), opts), rerr)
		})
	}
	// A known field still runs on both sides.
	ok := &types.FacetRequest{Cohort: &types.Cohort{Filename: cohort}, Fields: []string{"cat"},
		Filterers: []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "cat", Values: []string{"a"}}}}
	if _, err := p.FacetSchema(ctx, ok); err != nil {
		t.Fatalf("runtime refused a known field: %v", err)
	}
	if env := descx.ValidateFacetWithOptions(bytes.NewReader(data), ok, opts); len(env.Errors) != 0 {
		t.Fatalf("validator refused a known field: %+v", env.Errors)
	}
}
