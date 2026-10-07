package service

import (
	"context"
	stderrors "errors"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/limits"
	"github.com/frankbardon/pulse/types"
)

// TestLimitsPreflight_MatrixDimRefusesBeforeDecode: a matrix whose
// dimension exceeds MaxMatrixDim is refused with PULSE_LIMIT_EXCEEDED
// by the pre-flight — no handle ever drains the cohort — while the
// same request under the defaults reads it (the control that keeps the
// read assertion from being vacuous).
func TestLimitsPreflight_MatrixDimRefusesBeforeDecode(t *testing.T) {
	req := func() *types.Request {
		return &types.Request{
			Cohort:       &types.Cohort{Filename: "bench.pulse"},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "a", Label: "s"}},
			Matrices:     []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Name: "cov", Fields: []string{"a", "b", "c"}}},
		}
	}
	run := func(l *limits.Limits) (fsCounts, int64, error) {
		mem := afero.NewMemMapFs()
		makeSyntheticCohort(t, mem, "bench.pulse")
		wrapped := newCountingFs(mem)
		cfg, err := fs.New(fs.WithFs(wrapped))
		if err != nil {
			t.Fatal(err)
		}
		svc := New(cfg)
		if l != nil {
			svc.SetLimits(*l)
		}
		_, perr := svc.Process(context.Background(), req())
		fi, err := mem.Stat("bench.pulse")
		if err != nil {
			t.Fatal(err)
		}
		return wrapped.snapshot(), fi.Size(), perr
	}

	if snap, _, err := run(nil); err != nil {
		t.Fatalf("defaults: %v", err)
	} else if snap.FullReads["bench.pulse"] == 0 {
		t.Fatal("control: a run under the defaults never drained the cohort — the read assertion below would be vacuous")
	}

	l := limits.Defaults()
	l.MaxMatrixDim = 2
	snap, size, err := run(&l)
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_LIMIT_EXCEEDED {
		t.Fatalf("err = %v, want PULSE_LIMIT_EXCEEDED", err)
	}
	if ce.Details["limit"] != "max_matrix_dim" || ce.Details["configured"] != int64(2) || ce.Details["observed"] != int64(3) {
		t.Fatalf("details = %v", ce.Details)
	}
	if fr := snap.FullReads["bench.pulse"]; fr != 0 {
		t.Fatalf("refused run drained the cohort %d time(s); the pre-flight must refuse before decode", fr)
	}
	if got := snap.BytesRead["bench.pulse"]; got >= size {
		t.Fatalf("refused run read %d of %d bytes; want header + schema only", got, size)
	}
}
