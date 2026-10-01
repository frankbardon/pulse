package service

import (
	"context"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
)

func TestFacetSchema_Labels_Replace(t *testing.T) {
	svc, path := setupGroupableCohort(t)
	attachLabelExtensions(t, svc, map[string]processing.LabelTable{
		"country_names": {Rows: map[string]string{
			"US":  "United States",
			"USA": "United States",
			"CA":  "Canada",
		}},
	})
	got, err := svc.FacetSchema(context.Background(), &types.FacetRequest{
		Cohort: &types.Cohort{Filename: path},
		Fields: []string{"country"},
		Labels: []*types.LabelBinding{
			{Field: "country", Table: "country_names", Mode: types.LabelModeReplace},
		},
	})
	if err != nil {
		t.Fatalf("FacetSchema: %v", err)
	}
	field := got.Fields["country"]
	if field == nil || field.Discrete == nil {
		t.Fatalf("expected discrete country field; got %+v", field)
	}
	seen := map[string]bool{}
	for _, vc := range field.Discrete.Values {
		seen[vc.Value] = true
	}
	if !seen["United States (US)"] || !seen["United States (USA)"] || !seen["Canada"] {
		t.Fatalf("expected disambiguated label values; got %v", seen)
	}
	sawWarn := false
	for _, w := range got.Warnings {
		if strings.Contains(w, string(errors.PULSE_LABEL_COLLISION)) {
			sawWarn = true
		}
	}
	if !sawWarn {
		t.Fatalf("expected PULSE_LABEL_COLLISION warning; got %+v", got.Warnings)
	}
}

func TestFacetSchema_Labels_Augment(t *testing.T) {
	svc, path := setupGroupableCohort(t)
	attachLabelExtensions(t, svc, map[string]processing.LabelTable{
		"country_names": {Rows: map[string]string{"US": "United States", "CA": "Canada"}},
	})
	got, err := svc.FacetSchema(context.Background(), &types.FacetRequest{
		Cohort: &types.Cohort{Filename: path},
		Fields: []string{"country"},
		Labels: []*types.LabelBinding{
			{Field: "country", Table: "country_names", Mode: types.LabelModeAugment},
		},
	})
	if err != nil {
		t.Fatalf("FacetSchema: %v", err)
	}
	if got.Fields["country"] == nil {
		t.Fatal("raw country field missing")
	}
	sibling := got.Fields["country_label"]
	if sibling == nil || sibling.Discrete == nil {
		t.Fatalf("expected sibling country_label FacetField; got %+v", sibling)
	}
	seen := map[string]bool{}
	for _, vc := range sibling.Discrete.Values {
		seen[vc.Value] = true
	}
	if !seen["United States"] || !seen["Canada"] || !seen["USA"] {
		t.Fatalf("expected labels + raw fallback in sibling; got %v", seen)
	}
}

func TestFacetSchema_Labels_UnknownTable(t *testing.T) {
	svc, path := setupGroupableCohort(t)
	_, err := svc.FacetSchema(context.Background(), &types.FacetRequest{
		Cohort: &types.Cohort{Filename: path},
		Fields: []string{"country"},
		Labels: []*types.LabelBinding{{Field: "country", Table: "missing"}},
	})
	if !errors.HasCode(err, errors.PULSE_LABEL_TABLE_UNKNOWN) {
		t.Fatalf("expected PULSE_LABEL_TABLE_UNKNOWN; got %v", err)
	}
}
