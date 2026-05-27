package pulse

import (
	"context"
	"testing"
	"time"

	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

func TestProcessStreamResult_Completes(t *testing.T) {
	memFs := afero.NewMemMapFs()
	createTestPulseFile(t, memFs, "rows.pulse", []string{"age", "name"}, [][]string{
		{"10", "a"},
		{"20", "b"},
		{"30", "c"},
	})
	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	req := &Request{
		Cohort:       &types.Cohort{Filename: "rows.pulse"},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "age"}},
	}

	res, err := p.ProcessStreamResult(context.Background(), req)
	if err != nil {
		t.Fatalf("ProcessStreamResult: %v", err)
	}
	if res.Header.RequestHash == "" {
		t.Errorf("header missing request hash")
	}
	if res.Header.StartedAt.IsZero() {
		t.Errorf("header missing start time")
	}

	var seen int
	for chunk := range res.Chunks {
		seen++
		if chunk.Sequence != seen-1 {
			t.Errorf("chunk %d has bad sequence %d", seen-1, chunk.Sequence)
		}
	}
	term := <-res.Done
	if term.Status != StreamCompleted {
		t.Fatalf("status = %v, want completed (err=%v)", term.Status, term.Error)
	}
	if term.TotalRows != int64(seen) {
		t.Errorf("terminator TotalRows = %d, want %d", term.TotalRows, seen)
	}
}

func TestProcessStreamResult_Cancellation(t *testing.T) {
	memFs := afero.NewMemMapFs()
	createTestPulseFile(t, memFs, "rows.pulse", []string{"age"}, [][]string{
		{"1"}, {"2"}, {"3"}, {"4"}, {"5"},
	})
	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	req := &Request{
		Cohort:       &types.Cohort{Filename: "rows.pulse"},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "age"}},
	}
	res, err := p.ProcessStreamResult(ctx, req)
	if err != nil {
		t.Fatalf("ProcessStreamResult: %v", err)
	}
	cancel()
	// Drain whatever made it through pre-cancel and look at terminator.
	for range res.Chunks {
	}
	select {
	case term := <-res.Done:
		if term.Status != StreamCancelled && term.Status != StreamCompleted {
			t.Fatalf("status = %v, want cancelled or completed (err=%v)", term.Status, term.Error)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for terminator")
	}
}
