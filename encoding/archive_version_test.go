package encoding_test

import (
	"bytes"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// buildVersionedShard returns a u32-only cohort with three records whose
// preamble is written at version v.
func buildVersionedShard(t *testing.T, v byte) []byte {
	t.Helper()
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32},
	}}
	var buf bytes.Buffer
	if err := encoding.WritePreambleVersion(&buf, schema, v); err != nil {
		t.Fatalf("WritePreambleVersion(0x%02x): %v", v, err)
	}
	for _, id := range []byte{1, 2, 3} {
		buf.Write([]byte{id, 0, 0, 0})
	}
	return buf.Bytes()
}

// TestArchive_ShardFormatVersions: a shard archive admits 0x02 shards
// (header peek + record count thread the version, so the 4-byte
// extension block is not counted as record bytes) and refuses a version
// outside the supported set with PULSE_SHARD_HEADER_INVALID.
func TestArchive_ShardFormatVersions(t *testing.T) {
	v2 := buildVersionedShard(t, encoding.FormatVersionV2)
	future := append([]byte{}, v2...)
	future[encoding.HeaderSize-1] = 0x03

	data := buildArchive(t, buildSinglePulse(t), map[string][]byte{
		"v1.pulse":     buildVersionedShard(t, encoding.FormatVersionV1),
		"v2.pulse":     v2,
		"future.pulse": future,
	}, []string{"v1.pulse", "v2.pulse", "future.pulse"})
	arch, err := encoding.OpenArchive(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("OpenArchive: %v", err)
	}

	for _, name := range []string{"v1.pulse", "v2.pulse"} {
		if err := arch.PeekShardHeader(name); err != nil {
			t.Errorf("PeekShardHeader(%s): %v", name, err)
		}
		n, err := arch.PeekShardRecordCount(name)
		if err != nil {
			t.Fatalf("PeekShardRecordCount(%s): %v", name, err)
		}
		if n != 3 {
			t.Errorf("PeekShardRecordCount(%s) = %d, want 3", name, n)
		}
	}

	if err := arch.PeekShardHeader("future.pulse"); !errors.HasCode(err, errors.PULSE_SHARD_HEADER_INVALID) {
		t.Errorf("PeekShardHeader(future) = %v, want PULSE_SHARD_HEADER_INVALID", err)
	}
	if _, err := arch.PeekShardRecordCount("future.pulse"); !errors.HasCode(err, errors.PULSE_SHARD_HEADER_INVALID) {
		t.Errorf("PeekShardRecordCount(future) = %v, want PULSE_SHARD_HEADER_INVALID", err)
	}
}
