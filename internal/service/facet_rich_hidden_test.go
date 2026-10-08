package service

import (
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/limits"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
)

// TestBuildAdditiveAccumulators_ScopeFiltersUseInstanceRegistry pins
// the additive_fields scope-filter build to the instance registry: a
// filter the instance hides is refused there with the unknown-filter
// error a never-registered name gets. Before the fix the scope filters
// were built against a nil registry, so a hidden built-in compiled.
func TestBuildAdditiveAccumulators_ScopeFiltersUseInstanceRegistry(t *testing.T) {
	region := encoding.NewDictionary()
	_, _ = region.Add("north")
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "age", Type: encoding.FieldTypeF64},
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Dictionary: region},
	}}
	req := func(ft types.FiltererType) *types.FacetRequest {
		return &types.FacetRequest{
			Fields:         []string{"age"},
			AdditiveFields: []string{"age"},
			Filterers:      []*types.Filterer{{Type: ft, Field: "region", Values: []string{"north"}}},
		}
	}
	if _, _, err := buildAdditiveAccumulators(req(types.FILTER_EXCLUDE), schema, nil, limits.Limits{}); err != nil {
		t.Fatalf("unscoped build: %v", err)
	}
	hide := (*processing.ExtensionRegistry)(nil).WithHidden(func(n string) bool { return n == string(types.FILTER_EXCLUDE) })

	_, _, err := buildAdditiveAccumulators(req(types.FILTER_EXCLUDE), schema, hide, limits.Limits{})
	_, _, never := buildAdditiveAccumulators(req("FILTER_NEVER_REGISTERED"), schema, hide, limits.Limits{})
	var ce, cn *errors.CodedError
	if !stderrors.As(err, &ce) || !stderrors.As(never, &cn) {
		t.Fatalf("errors = %v / %v, want coded errors", err, never)
	}
	if ce.Code != errors.PROCESSING_CONFIG || ce.Message != "unknown filter type: FILTER_EXCLUDE" {
		t.Errorf("hidden scope filter: %s %q", ce.Code, ce.Message)
	}
	if cn.Code != ce.Code || cn.Message != "unknown filter type: FILTER_NEVER_REGISTERED" {
		t.Errorf("never-registered scope filter: %s %q", cn.Code, cn.Message)
	}
}
