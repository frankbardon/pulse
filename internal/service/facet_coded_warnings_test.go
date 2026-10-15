package service

import (
	"context"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestFacetSchema_TruncationWarningIsCoded pins #462: a discrete_top_k
// cut is the coded PULSE_FACET_VALUES_TRUNCATED entry with structured
// details, never a "field … truncated" string; the additive tally of
// the same field carries its own entry marked additive.
func TestFacetSchema_TruncationWarningIsCoded(t *testing.T) {
	svc, path := buildCategoricalCohort(t)
	for _, tc := range []struct {
		name     string
		additive []string
		want     []map[string]any
	}{
		{"field", nil, []map[string]any{
			{"field": "region", "discrete_top_k": 2, "distinct": int64(4), "dropped": 2},
		}},
		{"additive", []string{"region"}, []map[string]any{
			{"field": "region", "discrete_top_k": 2, "distinct": int64(4), "dropped": 2},
			{"field": "region", "discrete_top_k": 2, "distinct": int64(4), "dropped": 2, "additive": true},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := svc.FacetSchema(context.Background(), &types.FacetRequest{
				Cohort: &types.Cohort{Filename: path}, Fields: []string{"region"}, DiscreteTopK: 2, AdditiveFields: tc.additive,
			})
			if err != nil {
				t.Fatalf("FacetSchema: %v", err)
			}
			var got []map[string]any
			for _, w := range resp.Warnings {
				if w.Code != string(errors.PULSE_FACET_VALUES_TRUNCATED) || w.Message == "" {
					t.Fatalf("warning %+v, want a coded %s", w, errors.PULSE_FACET_VALUES_TRUNCATED)
				}
				got = append(got, w.Details)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("details %v, want %v", got, tc.want)
			}
		})
	}
}
