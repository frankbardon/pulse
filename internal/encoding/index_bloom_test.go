package encoding

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

func bloomKey(i uint64) []byte {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], i)
	return b[:]
}

// TestShardBloom_NoFalseNegativesAndBoundedFPR: every inserted key is
// reported present; absent keys are reported present at about the
// designed ~1% rate (bounded well under 3%).
func TestShardBloom_NoFalseNegativesAndBoundedFPR(t *testing.T) {
	t.Parallel()

	const n = 5000
	f := NewShardBloom("s.pulse", 10, 20, 8, n)
	for i := uint64(0); i < n; i++ {
		f.Add(bloomKey(i * 3))
	}
	for i := uint64(0); i < n; i++ {
		if !f.MayContain(bloomKey(i * 3)) {
			t.Fatalf("false negative for key %d", i*3)
		}
	}
	fp := 0
	for i := uint64(0); i < 20000; i++ {
		if f.MayContain(bloomKey(1_000_000 + i*3 + 1)) {
			fp++
		}
	}
	if fp > 600 {
		t.Errorf("false positives %d / 20000, want about 1%%", fp)
	}
	if len(f.Words) != (n*ShardBloomBitsPerKey+63)/64 {
		t.Errorf("words = %d, want 10 bits per key rounded to a word", len(f.Words))
	}
}

// TestShardBloom_RoundTripAndFrozenBytes: marshal → unmarshal is the
// identity, and the bytes for a fixed key set are frozen — the hashing
// is integer-only, so this digest is the same on every architecture.
func TestShardBloom_RoundTripAndFrozenBytes(t *testing.T) {
	t.Parallel()

	f := NewShardBloom("a.pulse", 1234, 0xdeadbeef, 8, 3)
	for _, k := range []uint64{1, 5, 1 << 53} {
		f.Add(bloomKey(k))
	}
	raw := MarshalShardBloom(f)
	got, err := UnmarshalShardBloom(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, f) {
		t.Errorf("round trip = %+v, want %+v", got, f)
	}
	sum := sha256.Sum256(raw)
	if d := hex.EncodeToString(sum[:]); d != frozenBloomDigest {
		t.Errorf("bloom bytes digest = %s, want %s (layout or hashing moved)", d, frozenBloomDigest)
	}
}

const frozenBloomDigest = "3dd662293118811223a95d13829f40348c5854d748447414fae8cf6fdcea2fe9"

// TestShardBloom_MalformedRefused: every damaged form is ENCODING_INVALID.
func TestShardBloom_MalformedRefused(t *testing.T) {
	t.Parallel()

	f := NewShardBloom("a.pulse", 1, 2, 8, 4)
	f.Add(bloomKey(9))
	good := MarshalShardBloom(f)
	mut := func(edit func(b []byte) []byte) []byte {
		return edit(append([]byte(nil), good...))
	}
	for name, raw := range map[string][]byte{
		"empty":     nil,
		"magic":     mut(func(b []byte) []byte { b[0] = 'X'; return b }),
		"version":   mut(func(b []byte) []byte { b[8] = 0x02; return b }),
		"bit flip":  mut(func(b []byte) []byte { b[len(b)-6] ^= 1; return b }),
		"truncated": mut(func(b []byte) []byte { return b[:len(b)-9] }),
	} {
		if _, err := UnmarshalShardBloom(raw); !errors.HasCode(err, errors.ENCODING_INVALID) {
			t.Errorf("%s: err = %v, want ENCODING_INVALID", name, err)
		}
	}
}

// TestShardBloom_ExcludesOnlyWhenFreshAndAbsent: the lookup check skips
// only on a present, well-formed filter for exactly the covered shard
// identity and key width that answers "no".
func TestShardBloom_ExcludesOnlyWhenFreshAndAbsent(t *testing.T) {
	t.Parallel()

	fsys := afero.NewMemMapFs()
	e := ArchiveEntry{Name: "a.pulse", Size: 100, CRC32: 7}
	keys := []IndexKeySpec{{Name: "id", Type: encoding.FieldTypeU64}}
	buckets := []IndexBucket{{Entries: []IndexEntry{{Key: bloomKey(1)}, {Key: bloomKey(2)}}}}
	path := ArchiveShardBloomPath("arch.pulse", "a.pulse", []string{"id"})
	if idx := ArchiveShardIndexPath("arch.pulse", "a.pulse", []string{"id"}); path != idx[:len(idx)-len(".idx")]+".bloom" {
		t.Fatalf("bloom path = %s", path)
	}
	sh := IndexManifestShard{Name: "a.pulse", Size: 100, CRC32: 7}
	absent := bloomKey(424242)

	if ShardBloomExcludes(fsys, path, sh, absent) {
		t.Error("a missing filter excluded the shard")
	}
	if err := WriteShardBloomFile(fsys, path, ShardBloomFromBuckets(e, keys, buckets)); err != nil {
		t.Fatal(err)
	}
	if !ShardBloomExcludes(fsys, path, sh, absent) {
		t.Error("a fresh filter did not exclude an absent key")
	}
	for _, k := range []uint64{1, 2} {
		if ShardBloomExcludes(fsys, path, sh, bloomKey(k)) {
			t.Errorf("a fresh filter excluded present key %d", k)
		}
	}
	for name, other := range map[string]IndexManifestShard{
		"name":  {Name: "b.pulse", Size: 100, CRC32: 7},
		"size":  {Name: "a.pulse", Size: 101, CRC32: 7},
		"crc32": {Name: "a.pulse", Size: 100, CRC32: 8},
	} {
		if ShardBloomExcludes(fsys, path, other, absent) {
			t.Errorf("a filter for another shard %s excluded the shard", name)
		}
	}
	if ShardBloomExcludes(fsys, path, sh, absent[:4]) {
		t.Error("a filter for another key width excluded the shard")
	}
	if err := afero.WriteFile(fsys, path, []byte("PULSEBLM garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ShardBloomExcludes(fsys, path, sh, absent) {
		t.Error("a malformed filter excluded the shard")
	}
}
