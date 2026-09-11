package service

import (
	"bytes"
	"context"
	"math"
	"os"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/fs"
	"github.com/spf13/afero"
)

// TestCountRecords_SingleFile validates that CountRecords returns the
// same value as the existing payload-decoding RecordCount() helper on
// a single-file cohort.
func TestCountRecords_SingleFile(t *testing.T) {
	schema := testSchema()
	cfg := setupTestFS(t, "test.pulse", schema, testRecords())
	svc := New(cfg)

	got, err := svc.CountRecords(context.Background(), "test.pulse")
	if err != nil {
		t.Fatalf("CountRecords: %v", err)
	}
	if got != 5 {
		t.Errorf("got %d, want 5", got)
	}
}

// TestCountRecords_ShardArchive verifies that the SHRD trailer's
// aggregate_record_count drives the result on a multi-shard archive.
func TestCountRecords_ShardArchive(t *testing.T) {
	schema, shards, _ := canonicalThreeShards()
	svc, _ := setupShardArchive(t, "arch.pulse", schema, shards, [][]uint64{})

	got, err := svc.CountRecords(context.Background(), "arch.pulse")
	if err != nil {
		t.Fatalf("CountRecords archive: %v", err)
	}
	if got != 12 {
		t.Errorf("got %d, want 12", got)
	}
}

// TestCountRecords_Anchor validates that an anchor path returns the
// named shard's record count alone.
func TestCountRecords_Anchor(t *testing.T) {
	schema, shards, _ := canonicalThreeShards()
	svc, _ := setupShardArchive(t, "arch.pulse", schema, shards, [][]uint64{})

	got, err := svc.CountRecords(context.Background(), "arch.pulse#"+shards[0].Name)
	if err != nil {
		t.Fatalf("CountRecords anchor: %v", err)
	}
	if int(got) != len(shards[0].Records) {
		t.Errorf("anchor count = %d, want %d", got, len(shards[0].Records))
	}
}

// TestCountRecords_HeaderOnly proves O(1) read behaviour on
// single-file cohorts. The byte-budget gate wraps the in-memory fs in
// a counting afero.Fs and asserts that the CountRecords path reads
// fewer bytes than a small constant (header + schema bytes), even
// against a payload-heavy file.
func TestCountRecords_HeaderOnly(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
			{Name: "score", Type: encoding.FieldTypeF64, ByteOffset: 4, CsvColumnIdx: 1},
		},
	}

	// 10_000 rows ⇒ payload >= 120 KB, vastly larger than any plausible
	// header+schema budget. If the byte counter exceeds the budget we
	// are paying payload-decode cost — fail.
	const rowCount = 10_000
	records := make([][]uint64, rowCount)
	for i := 0; i < rowCount; i++ {
		records[i] = []uint64{uint64(i), math.Float64bits(float64(i))}
	}

	// Determine a tight budget by measuring header+schema serialisation.
	hsBuf := &bytes.Buffer{}
	if err := encoding.WriteHeader(hsBuf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(hsBuf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	const slack = 32 // afero may overshoot by a small read buffer
	budget := int64(hsBuf.Len()) + slack

	memFS := afero.NewMemMapFs()
	counter := &byteCountingFs{Fs: memFS}
	data := writePulseFile(t, schema, records)
	if err := afero.WriteFile(memFS, "big.pulse", data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := fs.New(fs.WithFs(counter))
	if err != nil {
		t.Fatalf("fs.New: %v", err)
	}
	svc := New(cfg)

	got, err := svc.CountRecords(context.Background(), "big.pulse")
	if err != nil {
		t.Fatalf("CountRecords: %v", err)
	}
	if got != rowCount {
		t.Errorf("got %d, want %d", got, rowCount)
	}
	if counter.bytes > budget {
		t.Errorf("CountRecords read %d bytes, exceeds header+schema budget %d (payload-decode cost paid)",
			counter.bytes, budget)
	}
}

// TestCountRecords_MissingFile validates error path on stat failure.
func TestCountRecords_MissingFile(t *testing.T) {
	svc := New(fs.NewMemMap())
	_, err := svc.CountRecords(context.Background(), "nope.pulse")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !errors.HasCode(err, errors.SERVICE_RESOURCE) {
		t.Errorf("expected SERVICE_RESOURCE, got: %v", err)
	}
}

// byteCountingFs wraps an afero.Fs and tallies bytes pulled through
// every File returned by Open / OpenFile. Used by the
// TestCountRecords_HeaderOnly gate.
type byteCountingFs struct {
	afero.Fs
	bytes int64
}

func (c *byteCountingFs) Open(name string) (afero.File, error) {
	f, err := c.Fs.Open(name)
	if err != nil {
		return nil, err
	}
	return &countingFile{File: f, ctr: c}, nil
}

func (c *byteCountingFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	f, err := c.Fs.OpenFile(name, flag, perm)
	if err != nil {
		return nil, err
	}
	return &countingFile{File: f, ctr: c}, nil
}

type countingFile struct {
	afero.File
	ctr *byteCountingFs
}

func (cf *countingFile) Read(p []byte) (int, error) {
	n, err := cf.File.Read(p)
	cf.ctr.bytes += int64(n)
	return n, err
}

func (cf *countingFile) ReadAt(p []byte, off int64) (int, error) {
	n, err := cf.File.ReadAt(p, off)
	cf.ctr.bytes += int64(n)
	return n, err
}

// TestCountRecords_TruncatedTailAgreesWithInspect drives ONE truncated
// cohort's bytes down both record-count arms and asserts the contract
// they were lifted onto encoding.Schema.RecordCountForPayload to make
// enforceable: the NUMBER is identical, and the observability is not.
//
// CountRecords floors silently — it has no warning channel, and it
// feeds the parallel-decode eligibility gate as well as the facade, so
// a half-written trailing record must not stop a cohort that still
// processes from reporting its whole-record count. descriptor.Inspect
// floors to the same number and raises the ENCODING_INVALID warning
// naming the leftover bytes, because it has an envelope and its whole
// job is to describe the file.
func TestCountRecords_TruncatedTailAgreesWithInspect(t *testing.T) {
	schema := testSchema()
	stride := int64(schema.RecordByteSize())
	whole := writePulseFile(t, schema, testRecords())

	// Three bytes of a fourth-record tail that will never complete.
	truncated := append(append([]byte{}, whole...), 0x01, 0x02, 0x03)

	cfg := fs.NewMemMap()
	if err := afero.WriteFile(cfg.Fs(), "truncated.pulse", truncated, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	svc := New(cfg)

	got, err := svc.CountRecords(context.Background(), "truncated.pulse")
	if err != nil {
		t.Fatalf("CountRecords over a truncated tail must not fail: %v", err)
	}
	if got != 5 {
		t.Errorf("CountRecords = %d, want the floor 5", got)
	}

	env := descriptor.InspectFromBytes(truncated, nil)
	result, ok := env.Data.(*descriptor.InspectResult)
	if !ok {
		t.Fatalf("inspect data = %T, want *descriptor.InspectResult", env.Data)
	}
	if result.RecordCount != int64(got) {
		t.Errorf("arms disagree on the count: inspect %d, CountRecords %d",
			result.RecordCount, got)
	}

	// Only the inspect arm says the tail is there, and it names the
	// leftover byte count so the reader can tell a truncation from an
	// empty cohort.
	var warned bool
	for _, w := range env.Warnings {
		if w.Code == string(errors.ENCODING_INVALID) {
			warned = true
			if w.Details["trailing_bytes"] != int64(3) {
				t.Errorf("trailing_bytes = %v (%T), want int64(3)",
					w.Details["trailing_bytes"], w.Details["trailing_bytes"])
			}
			if w.Details["record_stride"] != int(stride) {
				t.Errorf("record_stride = %v, want %d", w.Details["record_stride"], stride)
			}
		}
	}
	if !warned {
		t.Error("inspect raised no ENCODING_INVALID warning for a truncated tail")
	}
}
