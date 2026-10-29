package service

import (
	"context"
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
)

// recordingSink records a materialize run's calls; beginErr fails Begin.
type recordingSink struct {
	beginErr error
	cols     []descx.MaterializeColumn
	groups   *descx.MaterializeGroups
	began    bool
	sources  []int64
}

func (r *recordingSink) Begin(_ *Cohort, cols []descx.MaterializeColumn, _ []descx.MaterializeOutputField, groups *descx.MaterializeGroups) error {
	r.began, r.cols, r.groups = true, cols, groups
	return r.beginErr
}

func (r *recordingSink) Row(src int64, _ []any) error {
	r.sources = append(r.sources, src)
	return nil
}

// TestMaterialize_SinkContract: Begin sees the derived columns and the
// group plan after every check passed; a Begin error ends the run before any Row; a
// refusal never reaches Begin.
func TestMaterialize_SinkContract(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc := New(setupTestFS(t, "wide.pulse", wideSchema(), wideRecords()))
	req := func() *types.Request {
		return &types.Request{
			Cohort:    &types.Cohort{Filename: "wide.pulse"},
			Features:  []*types.Feature{{Type: types.FEAT_LOG, Field: "x"}},
			Filterers: []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "color", Values: []string{"red"}}},
		}
	}
	sink := &recordingSink{}
	if err := svc.Materialize(ctx, req(), sink); err != nil {
		t.Fatal(err)
	}
	if len(sink.cols) != 1 || sink.cols[0].Name != "LOG_x" {
		t.Fatalf("columns = %+v", sink.cols)
	}
	// The pre-flight's group plan reaches Begin: non-nil, empty without
	// an output block.
	if g := sink.groups; g == nil || len(g.Decls) != 0 || len(g.Plan) != 0 || len(g.Warnings) != 0 {
		t.Fatalf("groups = %+v, want the empty plan of a request without an output block", sink.groups)
	}
	if want := []int64{0, 4, 8}; len(sink.sources) != 3 || sink.sources[1] != want[1] || sink.sources[2] != want[2] {
		t.Fatalf("sources = %v, want %v", sink.sources, want)
	}

	stop := stderrors.New("no target")
	sink = &recordingSink{beginErr: stop}
	if err := svc.Materialize(ctx, req(), sink); err != stop || len(sink.sources) != 0 {
		t.Fatalf("err = %v, rows %v; want the Begin error and no row", err, sink.sources)
	}

	sink = &recordingSink{}
	bad := req()
	bad.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "color"}}
	var ce *errors.CodedError
	if err := svc.Materialize(ctx, bad, sink); !stderrors.As(err, &ce) || ce.Code != errors.PULSE_MATERIALIZE_SLOT_UNSUPPORTED || sink.began {
		t.Fatalf("err = %v began=%v; want a slot refusal before Begin", err, sink.began)
	}
	if err := svc.Materialize(ctx, nil, sink); !stderrors.As(err, &ce) || ce.Code != errors.SERVICE_VALIDATION {
		t.Fatalf("nil request: %v", err)
	}
}

// TestMaterialize_SortOrdersRows: a sort reaches the sink as the Row
// order (source indices still global), and a sort key naming no
// pipeline column is refused by the field-reference rule before Begin.
func TestMaterialize_SortOrdersRows(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc := New(setupTestFS(t, "wide.pulse", wideSchema(), wideRecords()))
	req := &types.Request{
		Cohort:    &types.Cohort{Filename: "wide.pulse"},
		Features:  []*types.Feature{{Type: types.FEAT_LOG, Field: "x"}},
		Filterers: []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "color", Values: []string{"red", "green"}}},
		Sort:      []types.OrderKey{{Field: "color", Desc: true}, {Field: "LOG_x", Desc: true}},
	}
	sink := &recordingSink{}
	if err := svc.Materialize(ctx, req, sink); err != nil {
		t.Fatal(err)
	}
	// color desc: red (0, 4, 8) before green (1, 5, 9); LOG_x desc within.
	want := []int64{8, 4, 0, 9, 5, 1}
	if len(sink.sources) != len(want) {
		t.Fatalf("sources = %v, want %v", sink.sources, want)
	}
	for i := range want {
		if sink.sources[i] != want[i] {
			t.Fatalf("sources = %v, want %v", sink.sources, want)
		}
	}

	sink = &recordingSink{}
	req.Sort = []types.OrderKey{{Field: "nope"}}
	var ce *errors.CodedError
	if err := svc.Materialize(ctx, req, sink); !stderrors.As(err, &ce) || ce.Code != errors.SERVICE_VALIDATION || sink.began {
		t.Fatalf("unknown sort key: err = %v began=%v; want a field-ref refusal before Begin", err, sink.began)
	}
}
