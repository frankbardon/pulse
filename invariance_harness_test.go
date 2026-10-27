package pulse_test

import (
	"context"
	"sync"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/internal/testutil/invariance"
	"github.com/frankbardon/pulse/observe"
	"github.com/spf13/afero"
)

// invariance_harness_test.go is the root package's plumbing for the
// shared worker-invariance harness (internal/testutil/invariance): one
// fresh engine per harness cell carrying the cell's worker knobs and an
// arm-recording hook (fresh, so parallel cells never share the record).
// The families: multi_grouper_invariance_test.go,
// grouped_components_invariance_test.go.

// invCohort is one layout's cohort: the file system it lives on (a
// MemMapFs, or the real disk for a layout the parallel decode reads —
// it needs mmap) and its path there.
type invCohort struct {
	fs   afero.Fs
	path string
}

// invEngine is a Pulse over c.fs under opts with k's worker knobs, and
// the arm its last operation took.
func invEngine(t *testing.T, opts pulse.Options, c invCohort, k invariance.Knobs) (*pulse.Pulse, func() observe.Arm) {
	t.Helper()
	var mu sync.Mutex
	var arm observe.Arm
	opts.FS = c.fs
	opts.ShardWorkers, opts.DecodeWorkers = k.ShardWorkers, k.DecodeWorkers
	opts.Hooks = &observe.Hooks{OnOperationEnd: func(_ context.Context, _ observe.OperationInfo, r observe.OperationResult) {
		mu.Lock()
		arm = r.Arm
		mu.Unlock()
	}}
	p, err := pulse.New(opts)
	if err != nil {
		t.Fatalf("pulse.New(%s): %v", k, err)
	}
	return p, func() observe.Arm {
		mu.Lock()
		defer mu.Unlock()
		return arm
	}
}
