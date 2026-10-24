package vectors

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/internal/exprslots"
	"github.com/frankbardon/pulse/types"
)

// indexRe strips the array indices off an ExprSlot path.
var indexRe = regexp.MustCompile(`\[[0-9]+\]`)

// TestTemplateExprSplice_EngineExprSlotsMatch: every request expression
// the engine's reference / kind / length checks read (ExprSlots) — a
// filterer, a formula, a fitted attribute's params.fit_on filterers, a
// comparison centroid's filter — sits at a key path the shared
// expression-slot list (internal/exprslots) matches, so the template
// renderer refuses a `{{token}}` splice into each of them.
func TestTemplateExprSplice_EngineExprSlotsMatch(t *testing.T) {
	fitOn := json.RawMessage(`{"fit_on":[{"type":"FILTER_EXPRESSION","expression":"b1 > 0"}]}`)
	req := &types.Request{
		Filterers: []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "a1 > 0"}},
		Attributes: []*types.Attribute{
			{Type: types.ATTR_FORMULA, Label: "f", Expression: "a1 + 1"},
			{Type: types.ATTR_MAHALANOBIS, Fields: []string{"b1", "b2"}, Params: fitOn},
			{Type: types.ATTR_VEC_DISTANCE, Fields: []string{"b1", "b2"}, Params: json.RawMessage(`{"metric":"euclidean","fit_on":[{"type":"FILTER_EXPRESSION","expression":"b2 > 0"}],"reference":{"centroid":{"filter":[{"type":"FILTER_EXPRESSION","expression":"a1 > 0"}]}}}`)},
		},
	}
	slots := ExprSlots(req)
	if len(slots) < 5 {
		t.Fatalf("ExprSlots = %+v, want the filterer, the formula, both fit_on lists and the centroid filter", slots)
	}
	for _, s := range slots {
		keys := strings.Split(indexRe.ReplaceAllString(s.Path, ""), ".")
		if got, ok := exprslots.Match(keys); !ok || got.Name != exprslots.SlotExpression {
			t.Errorf("engine expression %s (%q): exprslots.Match = (%q, %v), want %s", s.Path, s.Src, got.Name, ok, exprslots.SlotExpression)
		}
	}
}
