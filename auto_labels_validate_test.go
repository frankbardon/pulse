package pulse

import (
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

func TestNew_AutoLabels_UnknownTableRejected(t *testing.T) {
	_, err := New(Options{
		FS: afero.NewMemMapFs(),
		Extensions: Extensions{
			LabelTables: map[string]LabelTable{
				"brand": {Rows: map[string]string{"1": "Nike"}},
			},
		},
		AutoLabels: []LabelBinding{
			{Field: "brand_id", Table: "category", Mode: LabelModeAugment}, // category not registered
		},
	})
	if !errors.HasCode(err, errors.PULSE_LABEL_TABLE_UNKNOWN) {
		t.Fatalf("expected PULSE_LABEL_TABLE_UNKNOWN; got %v", err)
	}
}

func TestNew_AutoLabels_ShapeValidated(t *testing.T) {
	_, err := New(Options{
		FS: afero.NewMemMapFs(),
		Extensions: Extensions{
			LabelTables: map[string]LabelTable{"brand": {Rows: map[string]string{"1": "Nike"}}},
		},
		AutoLabels: []LabelBinding{{Field: "", Table: "brand"}}, // empty field
	})
	if !errors.HasCode(err, errors.SERVICE_VALIDATION) {
		t.Fatalf("expected SERVICE_VALIDATION for empty field; got %v", err)
	}
}

func TestNew_AutoLabels_ValidAccepted(t *testing.T) {
	p, err := New(Options{
		FS: afero.NewMemMapFs(),
		Extensions: Extensions{
			LabelTables: map[string]LabelTable{"brand": {Rows: map[string]string{"1": "Nike"}}},
		},
		AutoLabels: []LabelBinding{{Field: "brand_id", Table: "brand", Mode: LabelModeAugment}},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := p.Service().AutoLabels(); len(got) != 1 || got[0].Field != "brand_id" {
		t.Fatalf("expected one stored auto-label binding; got %+v", got)
	}
}
