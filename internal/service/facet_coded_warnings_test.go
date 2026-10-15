package service

import (
	"bytes"
	"context"
	"reflect"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
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

// TestFacet_SmallTopKPredictMatchesRuntimeCode: predict-facet's caution
// on a small discrete_top_k carries the code the runtime raises when
// that cap cuts a value list — PULSE_FACET_VALUES_TRUNCATED — never a
// field-description code.
func TestFacet_SmallTopKPredictMatchesRuntimeCode(t *testing.T) {
	svc, path := buildCategoricalCohort(t)
	req := &types.FacetRequest{Cohort: &types.Cohort{Filename: path}, Fields: []string{"region"}, DiscreteTopK: 2}
	resp, err := svc.FacetSchema(context.Background(), req)
	if err != nil {
		t.Fatalf("FacetSchema: %v", err)
	}
	runtime := map[string]bool{}
	for _, w := range resp.Warnings {
		runtime[w.Code] = true
	}
	data, err := afero.ReadFile(svc.fs.Fs(), path)
	if err != nil {
		t.Fatal(err)
	}
	env := descx.ValidateFacet(bytes.NewReader(data), req)
	predicted := map[string]bool{}
	for _, w := range env.Warnings {
		predicted[w.Code] = true
	}
	code := string(errors.PULSE_FACET_VALUES_TRUNCATED)
	if !runtime[code] || !predicted[code] {
		t.Fatalf("runtime warnings %v, predict warnings %v: both must carry %s", runtime, predicted, code)
	}
	if predicted[string(errors.PULSE_FIELD_DESCRIPTION_LOW_QUALITY)] {
		t.Error("predict-facet still raises PULSE_FIELD_DESCRIPTION_LOW_QUALITY for a small discrete_top_k")
	}
}
