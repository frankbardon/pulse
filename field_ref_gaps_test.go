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

// TestFieldRefs_EmptyFilterFieldRefusedLikePredict: every built-in
// filterer except FILTER_EXPRESSION reads Field, so an empty one is a
// field-reference refusal — the shared rule's code, message and details
// on Process, ProcessStream, predict, FacetSchema / ValidateFacet and,
// located, inside Compose. Before, predict accepted it and the runtime
// failed later with an operator-specific PROCESSING_CONFIG (or kept no
// rows). FILTER_EXPRESSION without a Field still runs on both sides.
func TestFieldRefs_EmptyFilterFieldRefusedLikePredict(t *testing.T) {
	fs, cohort := zoneCohort(t)
	data, err := afero.ReadFile(fs, cohort)
	if err != nil {
		t.Fatal(err)
	}
	p := zonePulse(t, fs, "")
	ctx := context.Background()
	opts := &descx.PredictOptions{SchemaLoader: schemaLoaderFor(fs)}
	for _, typ := range types.AllFiltererTypes() {
		if typ == types.FILTER_EXPRESSION {
			continue
		}
		fil := func() []*types.Filterer {
			return []*types.Filterer{{Type: typ, Values: []string{"a"}}}
		}
		mk := func() *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: cohort}, Filterers: fil(), Aggregations: countAgg()}
		}
		t.Run(string(typ), func(t *testing.T) {
			_, rerr := p.Process(ctx, mk())
			ce := requireCode(t, rerr, errors.SERVICE_VALIDATION)
			if ce.Message != "filter references unknown field: " || ce.Details["filter"] != string(typ) {
				t.Fatalf("runtime = %q %v", ce.Message, ce.Details)
			}
			sameEntry(t, predictEnvelope(t, p, fs, cohort, mk()), rerr)
			_, serr := p.ProcessStream(ctx, mk())
			if se := requireCode(t, serr, errors.SERVICE_VALIDATION); se.Message != ce.Message {
				t.Fatalf("stream = %q, process = %q", se.Message, ce.Message)
			}

			facet := func() *types.FacetRequest {
				return &types.FacetRequest{Cohort: &types.Cohort{Filename: cohort}, Fields: []string{"cat"}, Filterers: fil()}
			}
			_, ferr := p.FacetSchema(ctx, facet())
			requireCode(t, ferr, errors.SERVICE_VALIDATION)
			sameEntry(t, descx.ValidateFacetWithOptions(bytes.NewReader(data), facet(), opts), ferr)

			compose := func() *types.ComposedRequest {
				return &types.ComposedRequest{Requests: []*types.Request{
					{Cohort: &types.Cohort{Filename: cohort}, Aggregations: countAgg()}, mk(),
				}}
			}
			_, cerr := p.Compose(ctx, compose())
			if cc := requireCode(t, cerr, errors.SERVICE_VALIDATION); cc.Details["request"] != 1 {
				t.Fatalf("details = %v, want request=1", cc.Details)
			}
			sameEntry(t, descx.ValidateComposeWithOptions(compose(), opts), cerr)
		})
	}
	t.Run("FILTER_EXPRESSION needs no field", func(t *testing.T) {
		req := func() *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: cohort}, Aggregations: countAgg(),
				Filterers: []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "n > 1"}}}
		}
		if _, err := p.Process(ctx, req()); err != nil {
			t.Fatalf("runtime: %v", err)
		}
		if env := predictEnvelope(t, p, fs, cohort, req()); len(env.Errors) != 0 {
			t.Fatalf("predict: %+v", env.Errors)
		}
	})
}
