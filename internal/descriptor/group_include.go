package descriptor

import (
	"strings"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// includeGroupers are the built-in groupers that keep a Group.Include
// inclusion list (allow-list + emission order) — exactly the runtime
// groupers implementing processing.IncludeOrdered, pinned by
// TestIncludeGroupers_MatchRuntime (internal/processing). Listed in
// refusal order.
var includeGroupers = []types.GroupType{
	types.GROUP_CATEGORY,
	types.GROUP_DATE,
	types.GROUP_DATE_RANGES,
	types.GROUP_SET_PER_ELEMENT,
	types.GROUP_SET_VALUE,
}

// GrouperHonoursInclude reports whether built-in grouper t keeps a
// Group.Include list. False for every other name (extension groupers
// included — they read the Group themselves).
func GrouperHonoursInclude(t types.GroupType) bool {
	for _, g := range includeGroupers {
		if g == t {
			return true
		}
	}
	return false
}

// groupIncludeRefusal refuses a non-empty `include` on a `groups` slot
// (path is the slot, e.g. "groups[0]") whose built-in grouper keeps no
// inclusion list: the list would otherwise be dropped silently and every
// bucket returned. A crosstab axis is not checked — its include orders
// the axis on every grouper. Extension and hidden groupers pass (the
// hidden name is refused as unknown elsewhere).
func groupIncludeRefusal(path string, g *types.Group, inst *InstanceSnapshot) *errors.CodedError {
	if g == nil || len(g.Include) == 0 || g.Type == "" || GrouperHonoursInclude(g.Type) || inst.Hidden(string(g.Type)) {
		return nil
	}
	if !isBuiltinGroupType(g.Type) {
		return nil
	}
	var offered []string
	for _, t := range includeGroupers {
		if !inst.Hidden(string(t)) {
			offered = append(offered, string(t))
		}
	}
	advice := "no grouper this instance offers takes an include list"
	if len(offered) > 0 {
		advice = "only " + strings.Join(offered, ", ") + " take an include list"
	}
	return errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
		path+": operator "+string(g.Type)+" does not accept `include`; "+advice+" — drop it, or narrow the rows with a filterer",
		map[string]any{"slot": path, "operator": string(g.Type), "path": path + ".include"})
}

func isBuiltinGroupType(t types.GroupType) bool {
	for _, b := range types.AllGroupTypes() {
		if b == t {
			return true
		}
	}
	return false
}
