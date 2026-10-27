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
// grouped_components_invariance_test.go, vector_invariance_test.go,
// extensions_regression_invariance_test.go,
// sem_worker_invariance_test.go, previous_state_invariance_test.go,
// bit_exact_invariance_test.go (the
// extension matrix family, in package pulse, carries its own engine:
// extensions_matrix_invariance_test.go).

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

// invSerialDecodeGroup is the decode group for a request the merge
// gate refuses: it never reaches the fan-out, so two DecodeWorkers
// counts (an odd one and a wide one) pin that, each cell on arm.
func invSerialDecodeGroup(name string, layout invariance.Layout, arm observe.Arm) invariance.Group {
	g := invariance.Group{Name: name, Ref: invariance.Cell{Knobs: invariance.Knobs{Layout: layout, DecodeWorkers: 1, ShardWorkers: 1}}}
	for _, dw := range []int{3, 8} {
		g.Cells = append(g.Cells, invariance.Cell{Knobs: invariance.Knobs{Layout: layout, DecodeWorkers: dw, ShardWorkers: 1}, Arm: arm})
	}
	return g
}

// invSerialSpotArchiveGroup is the archive group for a costly
// refused-merge request (heavy tier): the archive under the widest
// knobs ≡ its single-file twin, and a rerun of the reference knobs (run
// to run), each cell on arm.
func invSerialSpotArchiveGroup(arm observe.Arm) invariance.Group {
	ref := invariance.Knobs{Layout: invariance.File, DecodeWorkers: 1, ShardWorkers: 1}
	return invariance.Group{Name: "archive", Ref: invariance.Cell{Knobs: ref},
		Cells: []invariance.Cell{
			{Knobs: invariance.Knobs{Layout: invariance.Archive, DecodeWorkers: 7, ShardWorkers: 8}, Arm: arm},
			{Knobs: ref, Arm: arm},
		}}
}
