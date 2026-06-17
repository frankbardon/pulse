package pulse

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// TestFacade_DisableComponents_OptionsPropagates verifies that
// pulse.New(Options{DisableComponents: true}) wires the engine flag
// through Service so a default Process call yields nil
// Response.Components. Mirrors the SetDisableDefaults precedent in
// pulse_test.go for opt-out option wiring.
func TestFacade_DisableComponents_OptionsPropagates(t *testing.T) {
	memFs := afero.NewMemMapFs()
	createTestPulseFile(t, memFs, "test.pulse",
		[]string{"id", "score"},
		[][]string{
			{"1", "10"},
			{"2", "20"},
			{"3", "30"},
		},
	)

	p, err := New(Options{FS: memFs, DisableComponents: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "test.pulse"},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "score", Label: "total"},
		},
	}

	resp, err := p.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if resp.Components != nil {
		t.Errorf("Response.Components = %+v; want nil with Options.DisableComponents=true", resp.Components)
	}
	if got := resp.Data[0]["total"].(float64); got != 60.0 {
		t.Errorf("scalar result must be unaffected by opt-out; total = %v, want 60", got)
	}
}

// TestFacade_DisableComponents_OptionsOffEmits is the round-trip baseline:
// Options.DisableComponents=false (default) populates Components.
func TestFacade_DisableComponents_OptionsOffEmits(t *testing.T) {
	memFs := afero.NewMemMapFs()
	createTestPulseFile(t, memFs, "test.pulse",
		[]string{"id", "score"},
		[][]string{
			{"1", "10"},
			{"2", "20"},
		},
	)

	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "test.pulse"},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "score", Label: "total"},
		},
	}

	resp, err := p.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if resp.Components == nil {
		t.Fatal("Response.Components = nil; want populated under default Options")
	}
}
