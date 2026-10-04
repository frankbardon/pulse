package processing

import (
	"testing"

	"github.com/frankbardon/pulse/internal/crosstabfuse"
	"github.com/frankbardon/pulse/types"
)

// TestBuiltinGrouperKeyableMatchesRegistry pins the static
// crosstabfuse.BuiltinGrouperKeyable table — which both the runtime
// fusion gate and the no-execute predict arm read — against the
// interfaces the constructed built-in instances actually implement.
// The table replaced a per-request factory probe; this test is what
// keeps it honest. A new built-in grouper fails here until it gets a
// table row, and a grouper that gains or loses a per-record keying
// interface fails until the row is flipped.
func TestBuiltinGrouperKeyableMatchesRegistry(t *testing.T) {
	fixtures := allGroupParityFixtures(t)
	for _, groupType := range types.AllGroupTypes() {
		declared, known := crosstabfuse.BuiltinGrouperKeyable(groupType)
		if !known {
			t.Errorf("grouper %s has no crosstabfuse.BuiltinGrouperKeyable row", groupType)
			continue
		}
		factory, ok := grouperRegistry[groupType]
		if !ok {
			t.Errorf("grouper %s not in registry", groupType)
			continue
		}
		fix, ok := fixtures[groupType]
		if !ok {
			t.Fatalf("no group parity fixture for %s — add one in allGroupParityFixtures", groupType)
		}
		instance, err := factory(&types.Group{Type: groupType, Field: fix.field, Interval: fix.interval, Params: fix.params}, fix.schema)
		if err != nil {
			t.Errorf("grouper %s factory error: %v", groupType, err)
			continue
		}
		ApplyGrouperExtensions(instance, nil)
		_, single := instance.(StreamableGrouper)
		_, multi := instance.(MultiKeyStreamingGrouper)
		if observed := single || multi; observed != declared {
			t.Errorf("grouper %s: BuiltinGrouperKeyable=%v but instance keys per record=%v", groupType, declared, observed)
		}
	}
	// GROUP_DATE and GROUP_QUANTILE are the two rows the table exists
	// for: wider than types.GroupType.Streamable() in one direction,
	// still refusing the finalize-time grouper in the other.
	if k, _ := crosstabfuse.BuiltinGrouperKeyable(types.GROUP_DATE); !k || types.GROUP_DATE.Streamable() {
		t.Errorf("GROUP_DATE: keyable=%v Process-streamable=%v, want keyable and not Process-streamable", k, types.GROUP_DATE.Streamable())
	}
	if k, _ := crosstabfuse.BuiltinGrouperKeyable(types.GROUP_QUANTILE); k {
		t.Error("GROUP_QUANTILE must not be keyable")
	}
}

// TestCanFuseCrosstab_HiddenBuiltinGrouperDeclines pins that a grouper
// the instance feature set hides declines fusion exactly as an unknown
// name does, through the registry facts.
func TestCanFuseCrosstab_HiddenBuiltinGrouperDeclines(t *testing.T) {
	reg := (*ExtensionRegistry)(nil).WithHidden(func(n string) bool { return n == string(types.GROUP_CATEGORY) })
	ok, reason := CanFuseCrosstab(happyPathCrosstabRequest(), crosstabFusedGateSchema(), reg)
	if ok || reason != "non-streamable grouper on row axis (GROUP_CATEGORY)" {
		t.Fatalf("CanFuseCrosstab = (%v, %q), want declined on the hidden row grouper", ok, reason)
	}
}

// TestCanFuseCrosstab_UnconstructableGrouperDeclines pins the one fact
// only the runtime arm knows: a keyable grouper whose factory refuses
// its params declines fusion so the buffered path reports the coded
// error.
func TestCanFuseCrosstab_UnconstructableGrouperDeclines(t *testing.T) {
	req := happyPathCrosstabRequest()
	req.Crosstab.Rows = []*types.Group{{Type: types.GROUP_DATE, Field: "ts", Params: []byte(`{"component":"fortnight"}`)}}
	ok, reason := CanFuseCrosstab(req, crosstabFusedGateSchema(), nil)
	if ok || reason != "non-streamable grouper on row axis (GROUP_DATE)" {
		t.Fatalf("CanFuseCrosstab = (%v, %q), want declined on the unconstructable GROUP_DATE", ok, reason)
	}
	if got := CrosstabFuseReasons(happyPathCrosstabRequest(), crosstabFusedGateSchema(), nil); len(got) != 0 {
		t.Fatalf("CrosstabFuseReasons(happy path) = %v, want none", got)
	}
}
