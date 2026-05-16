package service

import (
	"archive/zip"
	"bytes"
	"context"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/fs"
	"github.com/spf13/afero"
)

// TestShardArchiveCompactReclaimsOrphans exercises the §7.1 reclaim
// path. Two sub-tests:
//
//  1. Standard-path: build an archive via Create, Remove one shard,
//     then Compact. v1's Remove already uses temp+rename so there are
//     no orphan bytes — Compact must still produce a clean archive
//     with refreshed canonical metadata and a size <= the
//     pre-Compact bytes.
//
//  2. Synthetic orphan: hand-build a zip whose payload region carries
//     extra junk bytes that are not referenced from the central
//     directory (the canonical orphan-bytes shape). Compact must
//     strip the orphan bytes; the resulting archive is byte-shorter
//     than the input.
func TestShardArchiveCompactReclaimsOrphans(t *testing.T) {
	t.Run("removes_then_compacts", func(t *testing.T) {
		cfg := fs.NewMemMap()
		fsys := cfg.Fs()
		svc := New(cfg)

		schema := adminScoreSchema()
		shardA := "a.pulse"
		shardB := "b.pulse"
		shardC := "c.pulse"
		adminWriteShardFile(t, fsys, shardA, schema, [][]uint64{
			{1, math.Float64bits(10.0)},
			{2, math.Float64bits(20.0)},
		})
		adminWriteShardFile(t, fsys, shardB, schema, [][]uint64{
			{3, math.Float64bits(30.0)},
			{4, math.Float64bits(40.0)},
		})
		adminWriteShardFile(t, fsys, shardC, schema, [][]uint64{
			{5, math.Float64bits(50.0)},
			{6, math.Float64bits(60.0)},
		})

		archive := "arch.pulse"
		if err := svc.CreateShardArchive(context.Background(), archive,
			[]string{shardA, shardB, shardC}); err != nil {
			t.Fatalf("CreateShardArchive: %v", err)
		}

		if err := svc.RemoveShard(context.Background(), archive, shardB); err != nil {
			t.Fatalf("RemoveShard: %v", err)
		}

		preBytes, err := afero.ReadFile(fsys, archive)
		if err != nil {
			t.Fatalf("ReadFile pre: %v", err)
		}

		if err := svc.CompactShardArchive(context.Background(), archive); err != nil {
			t.Fatalf("CompactShardArchive: %v", err)
		}

		postBytes, err := afero.ReadFile(fsys, archive)
		if err != nil {
			t.Fatalf("ReadFile post: %v", err)
		}

		// v1 Remove already used temp+rename so the post-Compact
		// archive should be no larger than the pre-Compact archive.
		if len(postBytes) > len(preBytes) {
			t.Errorf("Compact grew archive: pre=%d post=%d", len(preBytes), len(postBytes))
		}

		// Shard manifest survives the rewrite (A, C, in order).
		shards, err := svc.ListShards(context.Background(), archive)
		if err != nil {
			t.Fatalf("ListShards post: %v", err)
		}
		if len(shards) != 2 {
			t.Fatalf("post-Compact shards = %d, want 2", len(shards))
		}
		if shards[0].Filename != shardA || shards[1].Filename != shardC {
			t.Errorf("post-Compact order = [%s,%s], want [%s,%s]",
				shards[0].Filename, shards[1].Filename, shardA, shardC)
		}

		// Canonical aggregate_record_count refreshed: 2 + 2 = 4.
		arch, err := encoding.OpenArchive(bytes.NewReader(postBytes), int64(len(postBytes)))
		if err != nil {
			t.Fatalf("OpenArchive post: %v", err)
		}
		doc, err := readSchemaDocEntry(arch)
		if err != nil {
			t.Fatalf("readSchemaDocEntry post: %v", err)
		}
		if doc.AggregateRecordCount != 4 {
			t.Errorf("post-Compact aggregate_record_count = %d, want 4",
				doc.AggregateRecordCount)
		}
		if doc.ShardCount != 2 {
			t.Errorf("post-Compact shard_count = %d, want 2", doc.ShardCount)
		}
	})

	t.Run("synthetic_orphan_bytes", func(t *testing.T) {
		cfg := fs.NewMemMap()
		fsys := cfg.Fs()
		svc := New(cfg)

		schema := adminScoreSchema()
		archive := "arch.pulse"

		// Build a real archive in-memory.
		realBytes := buildSimpleArchive(t, schema, struct {
			Name    string
			Records [][]uint64
		}{Name: "a.pulse", Records: [][]uint64{{1, math.Float64bits(10.0)}}})

		// Splice in 1024 orphan bytes between the last shard payload
		// and the central directory. The zip central directory is
		// located via the EOCD at the tail, so its offsets stay
		// referentially valid even though the orphan bytes shift the
		// physical position of the directory. zip.NewReader is
		// tolerant of this — it locates the EOCD by scanning the
		// trailing 64 KiB, then reads the central directory at the
		// pre-recorded offset (which still points at the original
		// directory location). The injected orphan bytes therefore
		// look exactly like the kind of garbage an interrupted
		// in-place AddShard would leave behind.
		corrupt := injectOrphanBytesBeforeCentralDirectory(t, realBytes, 1024)

		// Sanity: the corrupted archive still opens (orphan bytes do
		// not move the EOCD-recorded offsets).
		if _, err := encoding.OpenArchive(bytes.NewReader(corrupt), int64(len(corrupt))); err != nil {
			t.Fatalf("OpenArchive on synthetic orphan archive: %v", err)
		}

		if err := afero.WriteFile(fsys, archive, corrupt, 0o644); err != nil {
			t.Fatalf("WriteFile corrupt: %v", err)
		}

		if err := svc.CompactShardArchive(context.Background(), archive); err != nil {
			t.Fatalf("CompactShardArchive on synthetic orphan archive: %v", err)
		}

		postBytes, err := afero.ReadFile(fsys, archive)
		if err != nil {
			t.Fatalf("ReadFile post: %v", err)
		}

		if len(postBytes) >= len(corrupt) {
			t.Errorf("Compact did not reclaim orphan bytes: pre=%d post=%d",
				len(corrupt), len(postBytes))
		}
		if len(postBytes) > len(realBytes)+64 {
			// realBytes is the canonical no-orphan baseline; the
			// post-Compact bytes should be roughly the same size.
			// 64-byte slack allows for zip-writer alignment / extra-
			// field differences without flaking on minor encoding
			// quirks.
			t.Errorf("post-Compact archive larger than canonical baseline: post=%d baseline=%d",
				len(postBytes), len(realBytes))
		}
	})
}

// injectOrphanBytesBeforeCentralDirectory inserts n zero-bytes into
// archiveBytes at the byte offset of the central directory's start,
// without rewriting the EOCD's recorded central-directory offset. The
// result mimics an interrupted in-place AddShard that wrote shard
// bytes between the prior tail and the (untouched) central directory.
//
// Returns the spliced byte slice. The function locates the central
// directory by parsing the EOCD record at the tail of the archive —
// the four-byte signature 0x06054b50 — and reads the offset from the
// EOCD's fixed-position field.
func injectOrphanBytesBeforeCentralDirectory(t *testing.T, archiveBytes []byte, n int) []byte {
	t.Helper()
	// Locate EOCD by scanning the trailing 64 KiB (zip spec maximum).
	const eocdSig = 0x06054b50
	const eocdSize = 22 // minimum size; trailing comment may extend it
	if len(archiveBytes) < eocdSize {
		t.Fatalf("archive too small for EOCD: %d bytes", len(archiveBytes))
	}
	scanStart := len(archiveBytes) - 64*1024
	if scanStart < 0 {
		scanStart = 0
	}
	eocdOffset := -1
	for i := len(archiveBytes) - eocdSize; i >= scanStart; i-- {
		if archiveBytes[i] == 0x50 &&
			archiveBytes[i+1] == 0x4b &&
			archiveBytes[i+2] == 0x05 &&
			archiveBytes[i+3] == 0x06 {
			eocdOffset = i
			break
		}
	}
	if eocdOffset < 0 {
		t.Fatalf("EOCD signature not found in archive")
	}
	// The central-directory offset is at EOCD[16..20], little-endian.
	cdOffset := int(archiveBytes[eocdOffset+16]) |
		int(archiveBytes[eocdOffset+17])<<8 |
		int(archiveBytes[eocdOffset+18])<<16 |
		int(archiveBytes[eocdOffset+19])<<24
	if cdOffset < 0 || cdOffset >= len(archiveBytes) {
		t.Fatalf("EOCD central-directory offset %d out of range", cdOffset)
	}
	junk := make([]byte, n)
	for i := range junk {
		junk[i] = 0xAA
	}

	// Splice junk between [cdOffset:] payload and the central
	// directory. Importantly, leave the EOCD's recorded cdOffset and
	// cdSize fields untouched — zip.NewReader reads them as the
	// canonical pointers, so the central directory still resolves to
	// its original location (i.e., the bytes from cdOffset onward in
	// the new slice, which still point at the original directory).
	//
	// To make that work we must keep cdOffset valid: the simplest way
	// is to insert junk AT cdOffset (pushing the central directory to
	// cdOffset+n in the new slice) AND simultaneously shift the EOCD's
	// recorded cdOffset to cdOffset+n. But the test purpose is the
	// orphan reclaim path, not EOCD shifting. We instead append junk
	// AFTER the central directory but BEFORE the EOCD, and shift the
	// EOCD's recorded cdOffset forward, leaving orphan bytes between
	// the original central directory location and the new one.
	//
	// Re-design: write [up-to-cdOffset bytes] + [junk] + [central dir]
	// + [eocd with cdOffset+=n]. The central directory now starts at
	// cdOffset+n; we update the EOCD's recorded cdOffset accordingly.
	// Central-directory entry offsets (which point at local file
	// headers) stay valid because we did not move the payload region.
	out := make([]byte, 0, len(archiveBytes)+n)
	out = append(out, archiveBytes[:cdOffset]...)
	out = append(out, junk...)
	out = append(out, archiveBytes[cdOffset:]...)
	// Patch EOCD cdOffset += n in the new slice. The EOCD's offset
	// inside `out` is eocdOffset + n.
	newEocdOff := eocdOffset + n
	newCdOff := uint32(cdOffset + n)
	out[newEocdOff+16] = byte(newCdOff)
	out[newEocdOff+17] = byte(newCdOff >> 8)
	out[newEocdOff+18] = byte(newCdOff >> 16)
	out[newEocdOff+19] = byte(newCdOff >> 24)
	return out
}

// buildSimpleArchive composes a 1-shard archive via the same code path
// as production builds (encoding.WriteSchemaDoc + zip Store). Used as
// the canonical baseline against which the orphan-reclaim variant is
// compared.
func buildSimpleArchive(t *testing.T, schema *encoding.Schema, shards struct {
	Name    string
	Records [][]uint64
}) []byte {
	t.Helper()

	var schemaDoc bytes.Buffer
	if err := encoding.WriteSchemaDoc(&schemaDoc, schema, uint64(len(shards.Records)), 1); err != nil {
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
	write(shards.Name, writeSinglePulse(t, schema, shards.Records))

	if err := zw.Close(); err != nil {
		t.Fatalf("zip.Close: %v", err)
	}
	return buf.Bytes()
}
