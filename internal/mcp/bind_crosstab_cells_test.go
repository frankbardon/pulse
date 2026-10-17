package mcp

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/mcp/toolmeta"
	"github.com/frankbardon/pulse/types"
)

// crosstabCellsInstance is a crosstab profile with or without
// capability:crosstab_cells.
func crosstabCellsInstance(t *testing.T, with bool) (*pulse.Pulse, *descx.InstanceSnapshot) {
	t.Helper()
	features := []string{"capability:process", "capability:crosstab", "AGG_SUM", "AGG_COUNT", "GROUP_CATEGORY"}
	if with {
		features = append(features, descx.FeatureCrosstabCells)
	}
	return instanceFor(t, &pulse.FeatureProfile{Features: features}, pulse.Extensions{})
}

// boundCrosstabProps returns the bound process tool's crosstab
// properties for inst.
func boundCrosstabProps(t *testing.T, inst *descx.InstanceSnapshot) map[string]any {
	t.Helper()
	schemas, err := BindForInstance(makeBindSchema(), inst)
	if err != nil {
		t.Fatal(err)
	}
	var req map[string]any
	if err := json.Unmarshal(schemas[toolmeta.ToolProcess], &req); err != nil {
		t.Fatal(err)
	}
	if inner, ok := req["properties"].(map[string]any)["request"].(map[string]any); ok {
		req = inner
	}
	props, _ := req["properties"].(map[string]any)
	cross, _ := props["crosstab"].(map[string]any)
	if cross == nil {
		t.Fatal("crosstab schema missing from the bound request")
	}
	cp, _ := cross["properties"].(map[string]any)
	return cp
}

// TestBindForInstance_CrosstabCells: the bound crosstab schema carries
// the multi-measure `cells` property on an instance enabling
// capability:crosstab_cells and drops it on one hiding it — the property
// the payload schema drops and the request-slot gate refuses.
func TestBindForInstance_CrosstabCells(t *testing.T) {
	_, on := crosstabCellsInstance(t, true)
	_, off := crosstabCellsInstance(t, false)
	if _, ok := boundCrosstabProps(t, on)["cells"]; !ok {
		t.Error("crosstab_cells enabled: bound crosstab schema has no cells property")
	}
	if _, ok := boundCrosstabProps(t, off)["cells"]; ok {
		t.Error("crosstab_cells hidden: bound crosstab schema still carries cells")
	}
	if _, ok := boundCrosstabProps(t, off)["cell"]; !ok {
		t.Error("crosstab_cells hidden: the single cell property must stay")
	}
}

// TestCheckUnknownKeys_HiddenCrosstabCellsRefusedLikeLibrary: a
// `crosstab.cells` key sent over MCP to an instance hiding the
// capability is refused exactly as the library refuses the typed
// request.
func TestCheckUnknownKeys_HiddenCrosstabCellsRefusedLikeLibrary(t *testing.T) {
	_, off := crosstabCellsInstance(t, false)
	body := []byte(`{"crosstab":{"rows":[{"type":"GROUP_CATEGORY","field":"region"}],"cells":[{"type":"AGG_COUNT","field":"score"}]}}`)
	typed := &types.Request{Crosstab: &types.CrosstabSpec{
		Rows:  []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
		Cells: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "score"}},
	}}
	sameRefusal(t, "crosstab cells", checkUnknownRequestKeys(body, off), codedOf(t, descx.SlotRefusal(typed, off)))
	_, on := crosstabCellsInstance(t, true)
	if ce := checkUnknownRequestKeys(body, on); ce != nil {
		t.Errorf("crosstab_cells enabled: cells refused: %v", ce)
	}
}
