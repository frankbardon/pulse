package pulse

import (
	"context"
	"math"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/types"
)

// newSeriesOverlayTestPulse builds the minimal engine the facade needs. The
// method touches no cohort and no filesystem — the memory FS is here only
// because New requires one.
func newSeriesOverlayTestPulse(t *testing.T) *Pulse {
	t.Helper()
	p, err := New(Options{FS: afero.NewMemMapFs()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func deltaVsPriorSpec() types.OverlaySpec {
	return types.OverlaySpec{
		Name:  "qoq_delta",
		Kind:  types.OverlayKindDeltaVsPrior,
		Scope: types.OverlayScopeGroup,
	}
}

// TestApplySeriesOverlays_StoredCellSeries is the case the facade exists for:
// values that were READ, not aggregated. Four stored metric cells, one per
// fiscal quarter, in axis order — exactly the shape a point-lookup caller
// holds — decorated with a period-over-period delta.
func TestApplySeriesOverlays_StoredCellSeries(t *testing.T) {
	p := newSeriesOverlayTestPulse(t)

	req := &SeriesOverlayRequest{
		Points: []SeriesPoint{
			{Key: types.AxisKey{"FY2025 Q2"}, Value: 94.10, Present: true},
			{Key: types.AxisKey{"FY2025 Q3"}, Value: 95.80, Present: true},
			{Key: types.AxisKey{"FY2025 Q4"}, Value: 96.40, Present: true},
			{Key: types.AxisKey{"FY2026 Q1"}, Value: 97.90, Present: true},
		},
		Overlays: []types.OverlaySpec{deltaVsPriorSpec()},
	}

	res, err := p.ApplySeriesOverlays(context.Background(), req)
	if err != nil {
		t.Fatalf("ApplySeriesOverlays: %v", err)
	}
	if len(res.Layers) != 1 {
		t.Fatalf("len(Layers) = %d, want 1", len(res.Layers))
	}
	entries := res.Layers[0].Payload.Series.Entries
	if len(entries) != 4 {
		t.Fatalf("len(Entries) = %d, want 4", len(entries))
	}

	// First quarter has no prior in this window.
	if entries[0].Summary.Statistic == nil || !math.IsNaN(*entries[0].Summary.Statistic) {
		t.Fatalf("entries[0] statistic = %v, want NaN (no prior in window)", entries[0].Summary.Statistic)
	}

	const tol = 1e-6
	wants := []float64{95.80 - 94.10, 96.40 - 95.80, 97.90 - 96.40}
	for i, want := range wants {
		got := entries[i+1].Summary.Statistic
		if got == nil {
			t.Fatalf("entries[%d] statistic nil, want %v", i+1, want)
		}
		if math.Abs(*got-want) > tol {
			t.Errorf("entries[%d] statistic = %v, want %v", i+1, *got, want)
		}
	}

	// The key survives onto the layer so a caller can align the delta back
	// onto the period it belongs to. A delta column that cannot be joined to
	// its period is not usable output.
	if len(entries[3].Key) != 1 || entries[3].Key[0] != "FY2026 Q1" {
		t.Errorf("entries[3].Key = %v, want [FY2026 Q1]", entries[3].Key)
	}
}

// TestApplySeriesOverlays_AbsentPeriodIsNotZero pins the distinction the
// Present flag exists for. A period holding no stored row must not read as a
// measured zero: it emits no statistic and does NOT advance the lag carrier,
// so the next present period differences against the last period that
// actually had data.
func TestApplySeriesOverlays_AbsentPeriodIsNotZero(t *testing.T) {
	p := newSeriesOverlayTestPulse(t)

	req := &SeriesOverlayRequest{
		Points: []SeriesPoint{
			{Key: types.AxisKey{"q1"}, Value: 50.0, Present: true},
			{Key: types.AxisKey{"q2"}, Present: false},
			{Key: types.AxisKey{"q3"}, Value: 56.0, Present: true},
		},
		Overlays: []types.OverlaySpec{deltaVsPriorSpec()},
	}

	res, err := p.ApplySeriesOverlays(context.Background(), req)
	if err != nil {
		t.Fatalf("ApplySeriesOverlays: %v", err)
	}
	entries := res.Layers[0].Payload.Series.Entries

	if entries[1].Summary.Statistic != nil {
		t.Errorf("absent period carried a statistic %v; want none", *entries[1].Summary.Statistic)
	}
	// 56 - 50 = 6, NOT 56 - 0 = 56.
	got := entries[2].Summary.Statistic
	if got == nil {
		t.Fatal("entries[2] statistic nil")
	}
	if math.Abs(*got-6.0) > 1e-9 {
		t.Errorf("entries[2] statistic = %v, want 6 (differenced against the last PRESENT period)", *got)
	}
}

// TestApplySeriesOverlays_NoOverlaysIsNotAnError mirrors
// processing.ApplyOverlaysSeries' unconditional-call contract.
func TestApplySeriesOverlays_NoOverlaysIsNotAnError(t *testing.T) {
	p := newSeriesOverlayTestPulse(t)

	res, err := p.ApplySeriesOverlays(context.Background(), &SeriesOverlayRequest{
		Points: []SeriesPoint{{Key: types.AxisKey{"a"}, Value: 1, Present: true}},
	})
	if err != nil {
		t.Fatalf("ApplySeriesOverlays with no specs: %v", err)
	}
	if len(res.Layers) != 0 {
		t.Errorf("len(Layers) = %d, want 0", len(res.Layers))
	}
}

// TestApplySeriesOverlays_NonGroupScopeRefused pins the one predict-time rule
// that survives onto this path: an ordered series has exactly one meaningful
// overlay footprint.
func TestApplySeriesOverlays_NonGroupScopeRefused(t *testing.T) {
	p := newSeriesOverlayTestPulse(t)

	spec := deltaVsPriorSpec()
	spec.Scope = types.OverlayScopeCell

	_, err := p.ApplySeriesOverlays(context.Background(), &SeriesOverlayRequest{
		Points:   []SeriesPoint{{Key: types.AxisKey{"a"}, Value: 1, Present: true}},
		Overlays: []types.OverlaySpec{spec},
	})
	if err == nil {
		t.Fatal("cell-scoped overlay on a series host was accepted; want a coded refusal")
	}
}

// TestApplySeriesOverlays_IndexTwinReachableToo proves the facade opens the
// SERIES catalog rather than one kind: the ratio twin runs over the same
// materialised series, so a caller can ask for either reading of the same
// stored cells.
func TestApplySeriesOverlays_IndexTwinReachableToo(t *testing.T) {
	p := newSeriesOverlayTestPulse(t)

	spec := deltaVsPriorSpec()
	spec.Kind = types.OverlayKindIndexVsPrior
	spec.Name = "qoq_index"

	res, err := p.ApplySeriesOverlays(context.Background(), &SeriesOverlayRequest{
		Points: []SeriesPoint{
			{Key: types.AxisKey{"a"}, Value: 50.0, Present: true},
			{Key: types.AxisKey{"b"}, Value: 75.0, Present: true},
		},
		Overlays: []types.OverlaySpec{spec},
	})
	if err != nil {
		t.Fatalf("ApplySeriesOverlays: %v", err)
	}
	got := res.Layers[0].Payload.Series.Entries[1].Summary.Statistic
	if got == nil {
		t.Fatal("index entry statistic nil")
	}
	if math.Abs(*got-150.0) > 1e-9 {
		t.Errorf("index statistic = %v, want 150", *got)
	}
}
