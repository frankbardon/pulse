package descriptor

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/types"
)

// TestMaterializeColumns_PipelineOrderAndDescriptions: features (every
// output of a multi-output one), then attributes, then windows, in
// declaration order, with generated descriptions.
func TestMaterializeColumns_PipelineOrderAndDescriptions(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "spend", Type: encoding.FieldTypeF64},
		{Name: "week", Type: encoding.FieldTypeU16},
		{Name: "region", Type: encoding.FieldTypeCategoricalU8},
	}}
	req := &types.Request{
		Windows: []*types.Window{{Type: types.WIN_EWMA, Field: "spend", Params: json.RawMessage(`{"alpha":0.3}`),
			PartitionBy: []string{"region"}, OrderBy: []types.OrderKey{{Field: "week"}},
			Frame: &types.FrameSpec{Mode: "rows"}}},
		Attributes: []*types.Attribute{{Type: types.ATTR_ZSCORE, Field: "spend", Label: "z"}},
		Features: []*types.Feature{
			{Type: types.FEAT_POLY, Field: "spend", Params: json.RawMessage(`{"degree":3}`)},
			{Type: types.FEAT_LOG, Field: "spend"},
		},
	}
	cols, err := MaterializeColumns(req, schema, nil, "data/harbor.pulse")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, c := range cols {
		names = append(names, c.Name)
		if encx.IsLowQualityDescription(c.Description) || len(c.Description) > materializeDescriptionMax {
			t.Errorf("%s description %q", c.Name, c.Description)
		}
	}
	if got, want := strings.Join(names, ","), "spend_poly_2,spend_poly_3,LOG_spend,z,WIN_EWMA_spend"; got != want {
		t.Fatalf("columns = %s, want %s", got, want)
	}
	if !cols[4].Window || cols[0].Window || cols[3].Window {
		t.Errorf("window flags = %+v", cols)
	}
	if got, want := cols[4].Description,
		"WIN_EWMA of spend (alpha 0.3; partition region; order week) — derived from harbor.pulse"; got != want {
		t.Errorf("window description = %q, want %q", got, want)
	}
	if got, want := cols[1].Description, "FEAT_POLY of spend (degree 3), output spend_poly_3 — derived from harbor.pulse"; got != want {
		t.Errorf("poly description = %q, want %q", got, want)
	}
}

// TestDerivedDescription_CappedOnRuneBoundary: a description past the
// cohort ceiling is cut, valid UTF-8, keeping the provenance suffix.
func TestDerivedDescription_CappedOnRuneBoundary(t *testing.T) {
	long := "FEAT_XY of " + strings.Repeat("é", 800)
	got := derivedDescription(long, "src.pulse")
	if len(got) > materializeDescriptionMax || !utf8.ValidString(got) || !strings.HasSuffix(got, "… — derived from src.pulse") {
		t.Fatalf("len %d valid %v suffix %q", len(got), utf8.ValidString(got), got[len(got)-40:])
	}
	if short := derivedDescription("FEAT_LOG of v", "s.pulse"); short != "FEAT_LOG of v — derived from s.pulse" {
		t.Errorf("short = %q", short)
	}
}

// TestMaterializeSlotRefusal_AcceptedSlots: the accepted slots pass;
// every other slot is refused by name.
func TestMaterializeSlotRefusal_AcceptedSlots(t *testing.T) {
	ok := &types.Request{
		Cohort: &types.Cohort{Filename: "c.pulse"}, Label: "l", TimeZone: "UTC",
		Features:   []*types.Feature{{Type: types.FEAT_LOG, Field: "v"}},
		Filterers:  []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "v"}},
		Attributes: []*types.Attribute{{Type: types.ATTR_ZSCORE, Field: "v"}},
		Windows:    []*types.Window{{Type: types.WIN_ROW_NUMBER}},
		Weight:     &types.WeightSpec{Field: "w"},
	}
	if err := MaterializeSlotRefusal(ok); err != nil {
		t.Fatalf("accepted slots refused: %v", err)
	}
	// sort orders the output rows; it keeps one row per record.
	ok.Sort = []types.OrderKey{{Field: "v", Desc: true}}
	if err := MaterializeSlotRefusal(ok); err != nil {
		t.Fatalf("sort refused: %v", err)
	}
	ok.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "v"}}
	err := MaterializeSlotRefusal(ok)
	if err == nil || err.Code != errors.PULSE_MATERIALIZE_SLOT_UNSUPPORTED || err.Details["slot"] != "groups" {
		t.Fatalf("groups: %v", err)
	}
	if _, err := MaterializeColumns(&types.Request{}, nil, nil, ""); err == nil || err.Code != errors.PULSE_MATERIALIZE_NO_DERIVED {
		t.Fatalf("empty request: %v", err)
	}
}

// TestMaterializeColumns_FrameClauseOnlyWhereRead: a window's
// description states its frame only when the operator reads it —
// WIN_EWMA (IgnoresFrame, cumulative) gets none under any frame, a
// running sum and a moving average keep theirs, bounded or not.
func TestMaterializeColumns_FrameClauseOnlyWhereRead(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "spend", Type: encoding.FieldTypeF64},
		{Name: "week", Type: encoding.FieldTypeU16},
	}}
	two, zero := 2, 0
	order := []types.OrderKey{{Field: "week"}}
	req := &types.Request{Windows: []*types.Window{
		{Type: types.WIN_EWMA, Field: "spend", Label: "e", Params: json.RawMessage(`{"decay":0.5}`), OrderBy: order,
			Frame: &types.FrameSpec{Mode: "rows", Preceding: &two, Following: &zero}},
		{Type: types.WIN_RUNNING_SUM, Field: "spend", Label: "rs", OrderBy: order, Frame: &types.FrameSpec{Mode: "rows"}},
		{Type: types.WIN_MOVING_AVG, Field: "spend", Label: "ma", OrderBy: order,
			Frame: &types.FrameSpec{Mode: "rows", Preceding: &two, Following: &zero}},
	}}
	cols, err := MaterializeColumns(req, schema, nil, "s.pulse")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"WIN_EWMA of spend (decay 0.5; order week) — derived from s.pulse",
		"WIN_RUNNING_SUM of spend (order week; frame unbounded preceding, unbounded following) — derived from s.pulse",
		"WIN_MOVING_AVG of spend (order week; frame 2 preceding, 0 following) — derived from s.pulse",
	}
	for i, w := range want {
		if cols[i].Description != w {
			t.Errorf("%s description = %q, want %q", cols[i].Name, cols[i].Description, w)
		}
	}
	if !windowIgnoresFrame(types.WIN_EWMA) || windowIgnoresFrame(types.WIN_RUNNING_SUM) || windowIgnoresFrame("WIN_X_UNKNOWN") {
		t.Error("windowIgnoresFrame disagrees with the capability table")
	}
}
