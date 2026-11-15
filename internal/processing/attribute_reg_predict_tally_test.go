package processing

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// tallyScorer is a RowScorer keeping a GeoTally (a REG_MMM_BAYES geo
// panel's scorer, stubbed).
type tallyScorer struct{ t *model.GeoTally }

func (tallyScorer) Columns() []string               { return nil }
func (tallyScorer) Score(model.Row) (float64, bool) { return 0, true }
func (s tallyScorer) GeoTally() *model.GeoTally     { return s.t }
func newTallyScorer() tallyScorer                   { return tallyScorer{model.NewGeoTally("market", []string{"east"})} }

// TestScoringSlots_GeoTallyFoldsAndResets: an ATTR_REG_PREDICT slot and
// a params.fit slot surface their scorer's unseen-geo count as one
// PULSE_MMM_GEO_UNSEEN warning naming the slot and the fit, fold a
// partner partition's count (MergeTally) and clear it (resetTally); a
// scorer without a tally warns nothing.
func TestScoringSlots_GeoTallyFoldsAndResets(t *testing.T) {
	t.Parallel()

	pa, pb := newTallyScorer(), newTallyScorer()
	a := &regPredictAttribute{attr: &types.Attribute{Type: types.ATTR_REG_PREDICT, Label: "p", Params: json.RawMessage(`{"fit":"m"}`)}, scorer: pa, fit: "m"}
	b := &regPredictAttribute{attr: a.attr, scorer: pb, fit: "m"}
	pa.t.Add("north")
	pb.t.Add("north")
	pb.t.Add("south")
	if err := a.MergeTally(b); err != nil {
		t.Fatal(err)
	}
	ws := a.attributeWarnings()
	if len(ws) != 1 || ws[0].Code != string(errors.PULSE_MMM_GEO_UNSEEN) || ws[0].Details["attribute"] != "p" || ws[0].Details["fit"] != "m" ||
		ws[0].Details["rows"].([]int64)[0] != 2 || ws[0].Details["rows"].([]int64)[1] != 1 {
		t.Fatalf("warnings %+v", ws)
	}
	a.resetTally()
	if ws := a.attributeWarnings(); ws != nil {
		t.Fatalf("after reset: %+v", ws)
	}

	fa, fb := newTallyScorer(), newTallyScorer()
	f := &regFitScoreAttribute{attr: &types.Attribute{Type: types.ATTR_REG_FITTED, Label: "f"}, scorer: fa, fit: "m"}
	g := &regFitScoreAttribute{attr: f.attr, scorer: fb, fit: "m"}
	fb.t.Add("north")
	if err := f.MergeTally(g); err != nil {
		t.Fatal(err)
	}
	if ws := f.attributeWarnings(); len(ws) != 1 || ws[0].Details["attribute"] != "f" || ws[0].Details["type"] != string(types.ATTR_REG_FITTED) {
		t.Fatalf("params.fit warnings %+v", ws)
	}
	f.resetTally()
	if ws := f.attributeWarnings(); ws != nil {
		t.Fatalf("params.fit after reset: %+v", ws)
	}
	if err := a.MergeTally(f); err == nil {
		t.Fatal("a partner of another type merged")
	}
	plain := &regPredictAttribute{attr: a.attr, scorer: columnOnlyScorer{}}
	if ws := plain.attributeWarnings(); ws != nil {
		t.Fatalf("a tally-free scorer warned %+v", ws)
	}
}

type columnOnlyScorer struct{}

func (columnOnlyScorer) Columns() []string               { return nil }
func (columnOnlyScorer) Score(model.Row) (float64, bool) { return 0, true }
