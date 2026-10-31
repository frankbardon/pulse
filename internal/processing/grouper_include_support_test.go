package processing

import (
	"testing"

	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
)

// TestIncludeGroupers_MatchRuntime pins the predict-side include set
// (descriptor.GrouperHonoursInclude, which refuses `include` on a
// `groups` slot of any other built-in grouper) to the runtime: a
// built-in grouper honours Group.Include iff its concrete type
// implements IncludeOrdered.
func TestIncludeGroupers_MatchRuntime(t *testing.T) {
	t.Parallel()

	concrete := map[types.GroupType]Grouper{
		types.GROUP_CATEGORY:        &categoryGrouper{},
		types.GROUP_DATE:            &dateGrouper{},
		types.GROUP_DATE_RANGES:     &dateRangesGrouper{},
		types.GROUP_SET_VALUE:       &setValueGrouper{},
		types.GROUP_SET_PER_ELEMENT: &setPerElementGrouper{},
		types.GROUP_RANGE:           &rangeGrouper{},
		types.GROUP_ROUNDED:         &roundedGrouper{},
		types.GROUP_QUANTILE:        &quantileGrouper{},
		types.GROUP_NUMERIC_RANGES:  &numericRangesGrouper{},
		types.GROUP_KMEANS:          &kmeansGrouper{},
	}
	for _, gt := range types.AllGroupTypes() {
		g, ok := concrete[gt]
		if !ok {
			t.Errorf("%s: add its concrete grouper type to this table", gt)
			continue
		}
		_, honours := g.(IncludeOrdered)
		if got := descx.GrouperHonoursInclude(gt); got != honours {
			t.Errorf("%s: descriptor.GrouperHonoursInclude = %v, runtime IncludeOrdered = %v", gt, got, honours)
		}
	}
}
