package service

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
)

// TestAutoLabels_ProcessAugments verifies a configured default binding
// augments a grouped-aggregation result with the <field>_label sibling
// without the caller passing any Labels on the request.
func TestAutoLabels_ProcessAugments(t *testing.T) {
	svc, path := setupGroupableCohort(t)
	attachLabelExtensions(t, svc, map[string]processing.LabelTable{
		"country_names": {Rows: map[string]string{"US": "United States", "CA": "Canada", "USA": "USofA"}},
	})
	svc.SetAutoLabels([]*types.LabelBinding{
		{Field: "country", Table: "country_names", Mode: types.LabelModeAugment},
	})

	resp, err := svc.Process(context.Background(), &types.Request{
		Cohort: &types.Cohort{Filename: path},
		Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "country"}},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "amount", Label: "total"},
		},
	})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	labels := map[string]string{}
	for _, row := range resp.Data {
		key, _ := row["country"].(string)
		lbl, _ := row["country_label"].(string)
		labels[key] = lbl
	}
	if labels["US"] != "United States" {
		t.Fatalf("expected country_label for US; got %+v", labels)
	}
	if labels["CA"] != "Canada" {
		t.Fatalf("expected country_label for CA; got %+v", labels)
	}
}

// TestAutoLabels_SampleAugments verifies the Sample path injects the
// default binding too.
func TestAutoLabels_SampleAugments(t *testing.T) {
	svc, path := setupCategoricalCohort(t)
	attachLabelExtensions(t, svc, map[string]processing.LabelTable{
		"country_names": {Rows: map[string]string{"US": "United States", "CA": "Canada"}},
	})
	svc.SetAutoLabels([]*types.LabelBinding{
		{Field: "country", Table: "country_names", Mode: types.LabelModeAugment},
	})
	rows, _, err := svc.SampleWithRequest(context.Background(), &types.SampleRequest{
		Cohort: &types.Cohort{Filename: path}, N: 4,
	}, path)
	if err != nil {
		t.Fatalf("SampleWithRequest: %v", err)
	}
	if rows[0]["country"] != "US" || rows[0]["country_label"] != "United States" {
		t.Fatalf("expected raw + sibling label; got %+v", rows[0])
	}
}

// TestAutoLabels_AbsentFieldSkipped verifies a default for a field that
// is not in the cohort schema is silently skipped — no error, no column.
func TestAutoLabels_AbsentFieldSkipped(t *testing.T) {
	svc, path := setupCategoricalCohort(t)
	attachLabelExtensions(t, svc, map[string]processing.LabelTable{
		"brand_names": {Rows: map[string]string{"1": "Nike"}},
	})
	svc.SetAutoLabels([]*types.LabelBinding{
		{Field: "brand_id", Table: "brand_names", Mode: types.LabelModeAugment},
	})
	rows, _, err := svc.SampleWithRequest(context.Background(), &types.SampleRequest{
		Cohort: &types.Cohort{Filename: path}, N: 4,
	}, path)
	if err != nil {
		t.Fatalf("SampleWithRequest: %v", err)
	}
	if _, ok := rows[0]["brand_id_label"]; ok {
		t.Fatalf("did not expect brand_id_label for absent field; got %+v", rows[0])
	}
}

// TestAutoLabels_NonCategoricalSkipped verifies a default targeting a
// non-categorical field is skipped rather than failing validation.
func TestAutoLabels_NonCategoricalSkipped(t *testing.T) {
	svc, path := setupCategoricalCohort(t)
	attachLabelExtensions(t, svc, map[string]processing.LabelTable{
		"x": {Rows: map[string]string{"k": "v"}},
	})
	svc.SetAutoLabels([]*types.LabelBinding{
		{Field: "amount", Table: "x", Mode: types.LabelModeAugment},
	})
	rows, _, err := svc.SampleWithRequest(context.Background(), &types.SampleRequest{
		Cohort: &types.Cohort{Filename: path}, N: 4,
	}, path)
	if err != nil {
		t.Fatalf("SampleWithRequest: %v", err)
	}
	if _, ok := rows[0]["amount_label"]; ok {
		t.Fatalf("did not expect amount_label for non-categorical field; got %+v", rows[0])
	}
}

// TestAutoLabels_UnregisteredTableSkipped verifies a default referencing
// a table absent from the registry is skipped at request time (the hard
// error path is pulse.New's validateAutoLabels, not the service layer).
func TestAutoLabels_UnregisteredTableSkipped(t *testing.T) {
	svc, path := setupCategoricalCohort(t)
	attachLabelExtensions(t, svc, map[string]processing.LabelTable{
		"country_names": {Rows: map[string]string{"US": "United States"}},
	})
	svc.SetAutoLabels([]*types.LabelBinding{
		{Field: "country", Table: "missing_table", Mode: types.LabelModeAugment},
	})
	rows, _, err := svc.SampleWithRequest(context.Background(), &types.SampleRequest{
		Cohort: &types.Cohort{Filename: path}, N: 4,
	}, path)
	if err != nil {
		t.Fatalf("SampleWithRequest: %v", err)
	}
	if _, ok := rows[0]["country_label"]; ok {
		t.Fatalf("did not expect country_label for unregistered table; got %+v", rows[0])
	}
}

// TestAutoLabels_CallerBindingWins verifies a caller-supplied binding for
// a field suppresses the default for that same field (no double binding).
func TestAutoLabels_CallerBindingWins(t *testing.T) {
	svc, path := setupCategoricalCohort(t)
	attachLabelExtensions(t, svc, map[string]processing.LabelTable{
		"country_names": {Rows: map[string]string{"US": "United States", "CA": "Canada"}},
	})
	// Default would augment; caller asks for replace. Caller must win.
	svc.SetAutoLabels([]*types.LabelBinding{
		{Field: "country", Table: "country_names", Mode: types.LabelModeAugment},
	})
	rows, _, err := svc.SampleWithRequest(context.Background(), &types.SampleRequest{
		Cohort: &types.Cohort{Filename: path}, N: 4,
		Labels: []*types.LabelBinding{
			{Field: "country", Table: "country_names", Mode: types.LabelModeReplace},
		},
	}, path)
	if err != nil {
		t.Fatalf("SampleWithRequest: %v", err)
	}
	// Replace rewrites the value and emits no sibling column.
	if rows[0]["country"] != "United States" {
		t.Fatalf("expected caller replace to rewrite value; got %+v", rows[0])
	}
	if _, ok := rows[0]["country_label"]; ok {
		t.Fatalf("did not expect sibling column when caller chose replace; got %+v", rows[0])
	}
}
