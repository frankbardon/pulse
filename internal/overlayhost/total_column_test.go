package overlayhost

import (
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func TestHeaderGrouperCount(t *testing.T) {
	t.Parallel()

	total := (&types.CrosstabSpec{}).ColumnHeader()
	if got := HeaderGrouperCount(total); got != 0 {
		t.Errorf("implicit Total header counts %d groupers, want 0", got)
	}
	two := types.AxisHeader{Fields: []string{"a", "b"}, Types: []string{"GROUP_CATEGORY", "GROUP_CATEGORY"}}
	if got := HeaderGrouperCount(two); got != 2 {
		t.Errorf("two-grouper header counts %d, want 2", got)
	}
}

func TestTotalColumnRefusal(t *testing.T) {
	t.Parallel()

	for _, k := range []types.OverlayKind{types.OverlayKindChiSqMatrix, types.OverlayKindChiSqRow,
		types.OverlayKindStdResidual, types.OverlayKindCorrespondence} {
		cerr := TotalColumnRefusal("OVERLAY_X_ALIAS", k, 2)
		if cerr == nil || cerr.Code != errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE {
			t.Fatalf("%s: refusal = %v, want PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE", k, cerr)
		}
		if cerr.Details["kind"] != "OVERLAY_X_ALIAS" || cerr.Details["index"] != 2 {
			t.Errorf("%s: details = %v, want the authored kind and index", k, cerr.Details)
		}
	}
	if cerr := TotalColumnRefusal(types.OverlayKindShareOfCol, types.OverlayKindShareOfCol, -1); cerr != nil {
		t.Errorf("SHARE_OF_COL refused on the Total column: %v", cerr)
	}
	if cerr := TotalColumnRefusal(types.OverlayKindChiSqMatrix, types.OverlayKindChiSqMatrix, -1); cerr == nil {
		t.Fatal("no refusal")
	} else if _, ok := cerr.Details["index"]; ok {
		t.Errorf("index -1 still stamped: %v", cerr.Details)
	}
}
