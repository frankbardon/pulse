package service

import (
	"archive/zip"
	"bytes"
	"context"
	"math"
	"testing"

	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/spf13/afero"
)

// TestShardArchiveVerify_AggregateDrift: a canonical schema whose cached
// aggregate record count disagrees with the shards' own counts verifies
// clean of errors and warns the registered PULSE_SHARD_AGGREGATE_DRIFT,
// with both counts in details.
func TestShardArchiveVerify_AggregateDrift(t *testing.T) {
	t.Parallel()
	cfg := fs.NewMemMap()
	fsys := cfg.Fs()
	svc := New(cfg)

	schema := adminScoreSchema()
	var doc bytes.Buffer
	if err := encx.WriteSchemaDoc(&doc, schema, 99, 1); err != nil {
		t.Fatalf("WriteSchemaDoc: %v", err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, entry := range []struct {
		name    string
		payload []byte
	}{
		{encx.ReservedSchemaName, doc.Bytes()},
		{"a.pulse", writeSinglePulse(t, schema, [][]uint64{{1, math.Float64bits(1.0)}, {2, math.Float64bits(2.0)}})},
	} {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: entry.name, Method: zip.Store})
		if err != nil {
			t.Fatalf("zip.CreateHeader(%q): %v", entry.name, err)
		}
		if _, err := w.Write(entry.payload); err != nil {
			t.Fatalf("zip write(%q): %v", entry.name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip.Close: %v", err)
	}
	if err := afero.WriteFile(fsys, "arch.pulse", buf.Bytes(), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	result, err := svc.VerifyShardArchive(context.Background(), "arch.pulse")
	if err != nil {
		t.Fatalf("VerifyShardArchive: %v", err)
	}
	if len(result.Errors) != 0 {
		t.Fatalf("errors = %v, want none", result.Errors)
	}
	if len(result.Warnings) != 1 || result.Warnings[0].Code != string(errors.PULSE_SHARD_AGGREGATE_DRIFT) {
		t.Fatalf("warnings = %+v, want one PULSE_SHARD_AGGREGATE_DRIFT", result.Warnings)
	}
	d := result.Warnings[0].Details
	if d["canonical_aggregate"] != uint64(99) || d["live_aggregate"] != uint64(2) {
		t.Errorf("details = %v, want canonical_aggregate 99, live_aggregate 2", d)
	}
	if _, ok := errors.MetadataFor(errors.PULSE_SHARD_AGGREGATE_DRIFT); !ok {
		t.Error("PULSE_SHARD_AGGREGATE_DRIFT has no lookup metadata")
	}
}
