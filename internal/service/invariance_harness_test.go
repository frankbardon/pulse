package service

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/frankbardon/pulse/encoding"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/testutil/invariance"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// invariance_harness_test.go is the service package's plumbing for the
// shared worker-invariance harness (internal/testutil/invariance): a
// layout's cohort, and one Process under a cell's worker knobs that
// reports the arm it took. The families: matrix_invariance_test.go
// (its own runner, for the CoMoment vehicle), wide_set_invariance_test.go,
// time_zone_invariance_heavy_test.go, multiplicity_fold_invariance_test.go,
// attribute_slots_heavy_test.go, matrix_sem_ordinal_test.go.

// svcInvCohort is one layout's cohort: the fs it lives on (a MemMap, or
// a real directory for a layout the parallel decode reads — it needs
// mmap) and its path there.
type svcInvCohort struct {
	cfg  *fs.Config
	path string
}

// svcInvDisk is an fs.Config over the real disk rooted at a fresh
// directory, for a BigFile layout; write the cohort with the returned
// afero.Fs at filepath.Join(dir, name) and read it by that path.
func svcInvDisk(t *testing.T) (cfg *fs.Config, osFs afero.Fs, dir string) {
	t.Helper()
	osFs, dir = afero.NewOsFs(), t.TempDir()
	cfg, err := fs.New(fs.WithFs(osFs), fs.WithDataDir(dir))
	if err != nil {
		t.Fatalf("fs.New: %v", err)
	}
	return cfg, osFs, dir
}

// svcInvProcess runs req (its cohort set to c.path) on a fresh Service
// over c under k's worker knobs — setup, when non-nil, configures the
// Service first — and returns the response (LayoutFree) and the arm.
func svcInvProcess(t *testing.T, c svcInvCohort, k invariance.Knobs, req *types.Request, setup func(*Service)) invariance.Result[*types.Response] {
	t.Helper()
	req.Cohort = &types.Cohort{Filename: c.path}
	svc := New(c.cfg)
	if setup != nil {
		setup(svc)
	}
	svc.SetDecodeWorkers(k.DecodeWorkers)
	svc.SetShardWorkers(k.ShardWorkers)
	ei := NewExecInfo(time.Now())
	resp, err := svc.Process(WithExecInfo(context.Background(), ei), req)
	if err != nil {
		t.Fatalf("%s: Process: %v", k, err)
	}
	return invariance.Result[*types.Response]{Answer: invariance.LayoutFree(resp), Arm: ei.Snapshot().Arm}
}

// svcInvArchive is a shard archive of rows split into shards of
// shardRows records each (a 0 is an empty shard), every shard written
// with writeNullablePulse; nullAt reads the GLOBAL row index.
func svcInvArchive(t *testing.T, schema *encoding.Schema, rows [][]uint64, nullAt func(r, f int) bool, shardRows []int) []byte {
	t.Helper()
	total := 0
	for _, n := range shardRows {
		total += n
	}
	if total != len(rows) {
		t.Fatalf("shards hold %d rows, have %d", total, len(rows))
	}
	var doc bytes.Buffer
	if err := encx.WriteSchemaDoc(&doc, schema, uint64(total), uint16(len(shardRows))); err != nil {
		t.Fatalf("WriteSchemaDoc: %v", err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	write := func(name string, b []byte) {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			t.Fatalf("zip.CreateHeader(%q): %v", name, err)
		}
		if _, err := w.Write(b); err != nil {
			t.Fatalf("zip write: %v", err)
		}
	}
	write(encx.ReservedSchemaName, doc.Bytes())
	offset := 0
	for i, n := range shardRows {
		off := offset
		write(fmt.Sprintf("s%d.pulse", i), writeNullablePulse(t, schema, rows[off:off+n], func(r, f int) bool { return nullAt(off+r, f) }))
		offset += n
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip.Close: %v", err)
	}
	return buf.Bytes()
}

// svcInvSerialArchiveGroup is invariance.ArchiveGroup for a request the
// merge gate refuses: every cell, parallel knobs included, must run on
// arm (the serial one) — the knobs never fan it out.
func svcInvSerialArchiveGroup(arm observe.Arm) invariance.Group {
	g := invariance.ArchiveGroup()
	for i := range g.Cells {
		g.Cells[i].Arm = arm
	}
	return g
}

// svcInvSerialDecodeGroup is invariance.DecodeGroup for a request the
// merge gate refuses: every DecodeWorkers cell runs on arm.
func svcInvSerialDecodeGroup(name string, layout invariance.Layout, arm observe.Arm) invariance.Group {
	g := invariance.DecodeGroup(name, layout)
	for i := range g.Cells {
		g.Cells[i].Arm = arm
	}
	return g
}
