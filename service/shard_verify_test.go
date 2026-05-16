package service

import (
	"archive/zip"
	"bytes"
	"context"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/fs"
	"github.com/spf13/afero"
)

// TestShardArchiveVerify exercises the structured verify path covering
// the five outcomes documented in §6 (CLI: verify):
//
//  1. Healthy archive — no errors, no warnings.
//  2. Structural schema mismatch — PULSE_SHARD_SCHEMA_MISMATCH error.
//  3. Dictionary prefix divergence — PULSE_SHARD_DICT_DIVERGENCE error.
//  4. Description divergence — warning only, no error.
//  5. Corrupt shard header bytes — PULSE_SHARD_HEADER_INVALID error.
func TestShardArchiveVerify(t *testing.T) {
	t.Run("healthy_archive", func(t *testing.T) {
		cfg := fs.NewMemMap()
		fsys := cfg.Fs()
		svc := New(cfg)

		_, shardA, shardB := adminTwoSimpleShards(t, fsys)
		if err := svc.CreateShardArchive(context.Background(), "arch.pulse",
			[]string{shardA, shardB}); err != nil {
			t.Fatalf("CreateShardArchive: %v", err)
		}

		result, err := svc.VerifyShardArchive(context.Background(), "arch.pulse")
		if err != nil {
			t.Fatalf("VerifyShardArchive: %v", err)
		}
		if len(result.Errors) != 0 {
			t.Errorf("healthy verify: errors = %v, want none", result.Errors)
		}
		if len(result.Warnings) != 0 {
			t.Errorf("healthy verify: warnings = %v, want none", result.Warnings)
		}
	})

	t.Run("structural_mismatch", func(t *testing.T) {
		cfg := fs.NewMemMap()
		fsys := cfg.Fs()
		svc := New(cfg)

		// Canonical: u32 id + f64 score.
		// Synthesized divergent shard: u32 id + f64 amount (different name).
		canonical := adminScoreSchema()
		divergent := &encoding.Schema{
			Fields: []encoding.Field{
				{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
				{Name: "amount", Type: encoding.FieldTypeF64, ByteOffset: 4, CsvColumnIdx: 1},
			},
		}
		archive := synthesizeArchive(t,
			canonical,
			[]synthShard{
				{Name: "a.pulse", Schema: canonical, Records: [][]uint64{{1, math.Float64bits(1.0)}}},
				{Name: "b.pulse", Schema: divergent, Records: [][]uint64{{2, math.Float64bits(2.0)}}},
			},
		)
		if err := afero.WriteFile(fsys, "arch.pulse", archive, 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		result, err := svc.VerifyShardArchive(context.Background(), "arch.pulse")
		if err != nil {
			t.Fatalf("VerifyShardArchive: %v", err)
		}
		if !verifyHasErrorCode(result, errors.PULSE_SHARD_SCHEMA_MISMATCH) {
			t.Errorf("structural mismatch: expected PULSE_SHARD_SCHEMA_MISMATCH, got %+v", result.Errors)
		}
	})

	t.Run("dict_divergence", func(t *testing.T) {
		cfg := fs.NewMemMap()
		fsys := cfg.Fs()
		svc := New(cfg)

		// Canonical: categorical_u8 region {US, CA, MX}.
		canonical := newCategoricalSchema(t, []string{"US", "CA", "MX"})
		// Divergent: categorical_u8 region {US, CA, BR} — neither
		// prefix of the canonical nor extended-by-it.
		divergent := newCategoricalSchema(t, []string{"US", "CA", "BR"})

		archive := synthesizeArchive(t,
			canonical,
			[]synthShard{
				{Name: "a.pulse", Schema: canonical, Records: [][]uint64{{0}}},
				{Name: "b.pulse", Schema: divergent, Records: [][]uint64{{1}}},
			},
		)
		if err := afero.WriteFile(fsys, "arch.pulse", archive, 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		result, err := svc.VerifyShardArchive(context.Background(), "arch.pulse")
		if err != nil {
			t.Fatalf("VerifyShardArchive: %v", err)
		}
		if !verifyHasErrorCode(result, errors.PULSE_SHARD_DICT_DIVERGENCE) {
			t.Errorf("dict divergence: expected PULSE_SHARD_DICT_DIVERGENCE, got %+v", result.Errors)
		}
	})

	t.Run("description_divergence_warns", func(t *testing.T) {
		cfg := fs.NewMemMap()
		fsys := cfg.Fs()
		svc := New(cfg)

		canonical := adminScoreSchema()
		divergent := &encoding.Schema{
			Fields: []encoding.Field{
				{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0, Description: "respondent id"},
				{Name: "score", Type: encoding.FieldTypeF64, ByteOffset: 4, CsvColumnIdx: 1, Description: "primary score"},
			},
		}

		archive := synthesizeArchive(t,
			canonical,
			[]synthShard{
				{Name: "a.pulse", Schema: canonical, Records: [][]uint64{{1, math.Float64bits(1.0)}}},
				{Name: "b.pulse", Schema: divergent, Records: [][]uint64{{2, math.Float64bits(2.0)}}},
			},
		)
		if err := afero.WriteFile(fsys, "arch.pulse", archive, 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		result, err := svc.VerifyShardArchive(context.Background(), "arch.pulse")
		if err != nil {
			t.Fatalf("VerifyShardArchive: %v", err)
		}
		if len(result.Errors) != 0 {
			t.Errorf("description divergence: errors = %v, want none", result.Errors)
		}
		if !verifyHasWarningCode(result, string(errors.PULSE_SHARD_DESCRIPTION_DIVERGENCE)) {
			t.Errorf("description divergence: expected PULSE_SHARD_DESCRIPTION_DIVERGENCE warning, got %+v",
				result.Warnings)
		}
	})

	t.Run("corrupt_shard_header", func(t *testing.T) {
		cfg := fs.NewMemMap()
		fsys := cfg.Fs()
		svc := New(cfg)

		// Real canonical, real shard A, and a synthetic shard B whose
		// payload bytes do NOT carry the Pulse magic. The reserved
		// `_schema.pulse` is left intact so the archive opens cleanly
		// at the zip level; the verify step is the one that detects
		// the bogus header.
		canonical := adminScoreSchema()
		archive := synthesizeArchiveWithRawPayload(t,
			canonical,
			[]rawShard{
				{Name: "a.pulse", Payload: writeSinglePulse(t, canonical,
					[][]uint64{{1, math.Float64bits(1.0)}})},
				{Name: "b.pulse", Payload: []byte("not a pulse file at all")},
			},
		)
		if err := afero.WriteFile(fsys, "arch.pulse", archive, 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		result, err := svc.VerifyShardArchive(context.Background(), "arch.pulse")
		if err != nil {
			t.Fatalf("VerifyShardArchive: %v", err)
		}
		if !verifyHasErrorCode(result, errors.PULSE_SHARD_HEADER_INVALID) {
			t.Errorf("corrupt header: expected PULSE_SHARD_HEADER_INVALID, got %+v", result.Errors)
		}
	})
}

// --- helpers ---

type synthShard struct {
	Name    string
	Schema  *encoding.Schema
	Records [][]uint64
}

type rawShard struct {
	Name    string
	Payload []byte
}

// synthesizeArchive composes an archive directly via archive/zip,
// bypassing Service.CreateShardArchive's cohesion gates. Lets tests
// inject shards whose schemas disagree with canonical.
func synthesizeArchive(t *testing.T, canonical *encoding.Schema, shards []synthShard) []byte {
	t.Helper()

	var schemaDoc bytes.Buffer
	var total uint64
	for _, sh := range shards {
		total += uint64(len(sh.Records))
	}
	if err := encoding.WriteSchemaDoc(&schemaDoc, canonical, total, uint16(len(shards))); err != nil {
		t.Fatalf("WriteSchemaDoc: %v", err)
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	write := func(name string, payload []byte) {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			t.Fatalf("zip.CreateHeader(%q): %v", name, err)
		}
		if _, err := w.Write(payload); err != nil {
			t.Fatalf("zip write(%q): %v", name, err)
		}
	}

	write(encoding.ReservedSchemaName, schemaDoc.Bytes())
	for _, sh := range shards {
		write(sh.Name, writeSinglePulse(t, sh.Schema, sh.Records))
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip.Close: %v", err)
	}
	return buf.Bytes()
}

// synthesizeArchiveWithRawPayload is like synthesizeArchive but writes
// raw bytes for shard payloads (bypassing the WriteSinglePulse header
// + schema emission). Used to inject corrupt-header fixtures.
func synthesizeArchiveWithRawPayload(t *testing.T, canonical *encoding.Schema, shards []rawShard) []byte {
	t.Helper()

	var schemaDoc bytes.Buffer
	if err := encoding.WriteSchemaDoc(&schemaDoc, canonical, 0, uint16(len(shards))); err != nil {
		t.Fatalf("WriteSchemaDoc: %v", err)
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	write := func(name string, payload []byte) {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			t.Fatalf("zip.CreateHeader(%q): %v", name, err)
		}
		if _, err := w.Write(payload); err != nil {
			t.Fatalf("zip write(%q): %v", name, err)
		}
	}

	write(encoding.ReservedSchemaName, schemaDoc.Bytes())
	for _, sh := range shards {
		write(sh.Name, sh.Payload)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip.Close: %v", err)
	}
	return buf.Bytes()
}

// newCategoricalSchema returns a one-field schema (categorical_u8
// region) populated with the supplied dictionary entries.
func newCategoricalSchema(t *testing.T, values []string) *encoding.Schema {
	t.Helper()
	dict := encoding.NewDictionary()
	for _, v := range values {
		if _, err := dict.Add(v); err != nil {
			t.Fatalf("dict.Add(%q): %v", v, err)
		}
	}
	return &encoding.Schema{
		Fields: []encoding.Field{
			{
				Name:         "region",
				Type:         encoding.FieldTypeCategoricalU8,
				ByteOffset:   0,
				CsvColumnIdx: 0,
				Dictionary:   dict,
			},
		},
	}
}

// verifyHasErrorCode reports whether any error in the result carries
// the supplied code.
func verifyHasErrorCode(result *VerifyResult, code errors.Code) bool {
	for _, e := range result.Errors {
		if e.Code == code {
			return true
		}
	}
	return false
}

// verifyHasWarningCode reports whether any warning in the result
// carries the supplied code.
func verifyHasWarningCode(result *VerifyResult, code string) bool {
	for _, w := range result.Warnings {
		if w.Code == code {
			return true
		}
	}
	return false
}
