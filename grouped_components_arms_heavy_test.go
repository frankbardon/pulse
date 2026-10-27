//go:build heavy

package pulse_test

import (
	"path/filepath"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// TestGroupedComponents_DecodeWorkersAgree: serial ≡ DecodeWorkers=4
// over a single file above the parallel-decode threshold, extension
// slot included.
func TestGroupedComponents_DecodeWorkersAgree(t *testing.T) {
	dir := t.TempDir()
	writeParityCohort(t, afero.NewOsFs(), filepath.Join(dir, "large.pulse"), paritySchema(t), 0, parityLargeRows)
	probe := &parityProbe{}
	serial := gaOpen(t, pulse.Options{DataDir: dir, DecodeWorkers: 1}, probe)
	parallel := gaOpen(t, pulse.Options{DataDir: dir, DecodeWorkers: 4}, probe)
	for name, grp := range gaGroupers() {
		t.Run(name, func(t *testing.T) {
			req := &types.Request{Cohort: &types.Cohort{Filename: "large.pulse"},
				Groups: []*types.Group{grp}, Aggregations: gaAggregations()}
			base := gcProcess(t, serial, req)
			gaAssertGroups(t, base)
			probe.reset()
			if got, want := gaAnswer(t, gcProcess(t, parallel, req)), gaAnswer(t, base); got != want {
				t.Errorf("DecodeWorkers=4 answers differently from DecodeWorkers=1\n got  %.800s\n want %.800s", got, want)
			}
			if probe.merges.Load() == 0 {
				t.Error("the extension aggregator was never merged; the parallel decode arm did not run")
			}
		})
	}
}
