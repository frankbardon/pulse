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
	began    bool
	sources  []int64
}

func (r *recordingSink) Begin(_ *Cohort, cols []descx.MaterializeColumn) error {
	r.began, r.cols = true, cols
	return r.beginErr
}

func (r *recordingSink) Row(src int64, _ []any) error {
	r.sources = append(r.sources, src)
	return nil
}

// TestMaterialize_SinkContract: Begin sees the derived columns after
// every check passed; a Begin error ends the run before any Row; a
// refusal never reaches Begin.
func TestMaterialize_SinkContract(t *testing.T) {
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
