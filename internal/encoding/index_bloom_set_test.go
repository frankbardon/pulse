package encoding

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

func bloomSetFixture() []*ShardBloom {
	a := NewShardBloom("a.pulse", 100, 0xaaaa, 8, 2)
	a.Add(bloomKey(1))
	a.Add(bloomKey(5))
	b := NewShardBloom("b.pulse", 200, 0xbbbb, 8, 1)
	b.Add(bloomKey(7))
	return []*ShardBloom{a, b}
}

// TestArchiveBloomSet_RoundTrip: write → read gives every filter back,
// each record byte-equal to its per-shard sidecar encoding, and the
// set's Excludes answers exactly as each filter does for its own shard.
func TestArchiveBloomSet_RoundTrip(t *testing.T) {
	t.Parallel()

	fsys := afero.NewMemMapFs()
	filters := bloomSetFixture()
	path := ArchiveBloomSetPath("arch.pulse", []string{"id"})
	if err := WriteArchiveBloomSetFile(fsys, path, filters); err != nil {
		t.Fatal(err)
	}
	s, present, err := ReadArchiveBloomSetFile(fsys, path)
	if err != nil || !present || s.Damaged {
		t.Fatalf("read = %+v, %v, %v", s, present, err)
	}
	if len(s.Filters) != 2 {
		t.Fatalf("filters = %d, want 2", len(s.Filters))
	}
	for _, f := range filters {
		if !bytes.Equal(s.Records[f.Shard], MarshalShardBloom(f)) {
			t.Errorf("%s: record is not its per-shard encoding", f.Shard)
		}
	}
	a := IndexManifestShard{Name: "a.pulse", Size: 100, CRC32: 0xaaaa}
	if s.Excludes(a, bloomKey(1)) || s.Excludes(a, bloomKey(5)) {
		t.Error("set excluded a key a.pulse holds")
	}
	if !s.Excludes(a, bloomKey(7)) {
		t.Error("set did not exclude a key a.pulse lacks (fixture has no false positive)")
	}
	b := IndexManifestShard{Name: "b.pulse", Size: 200, CRC32: 0xbbbb}
	if s.Excludes(b, bloomKey(7)) {
		t.Error("set excluded a key b.pulse holds")
	}
}

// TestArchiveBloomSet_PathBesideTheLogicalIndex: the set is the logical
// index path with .idx swapped for .blooms.
func TestArchiveBloomSet_PathBesideTheLogicalIndex(t *testing.T) {
	t.Parallel()

	logical := SidecarIndexPath("dir/arch.pulse", []string{"id"})
	got := ArchiveBloomSetPath("dir/arch.pulse", []string{"id"})
	if want := logical[:len(logical)-len(".idx")] + ".blooms"; got != want {
		t.Errorf("path = %s, want %s", got, want)
	}
}

// TestArchiveBloomSet_StaleOrMissingRecordProbes: a record stamped with
// another identity or key width, or no record at all, never excludes.
func TestArchiveBloomSet_StaleOrMissingRecordProbes(t *testing.T) {
	t.Parallel()

	s, err := UnmarshalArchiveBloomSet(MarshalArchiveBloomSet(bloomSetFixture()))
	if err != nil {
		t.Fatal(err)
	}
	for name, sh := range map[string]IndexManifestShard{
		"other size":  {Name: "a.pulse", Size: 101, CRC32: 0xaaaa},
		"other crc":   {Name: "a.pulse", Size: 100, CRC32: 0xaaab},
		"no record":   {Name: "c.pulse", Size: 100, CRC32: 0xaaaa},
		"other width": {Name: "a.pulse", Size: 100, CRC32: 0xaaaa},
	} {
		key := bloomKey(7)
		if name == "other width" {
			key = append(key, 0)
		}
		if s.Excludes(sh, key) {
			t.Errorf("%s: excluded", name)
		}
	}
	var none *ArchiveBloomSet
	if none.Excludes(IndexManifestShard{Name: "a.pulse", Size: 100, CRC32: 0xaaaa}, bloomKey(7)) {
		t.Error("nil set excluded")
	}
}

// TestArchiveBloomSet_DamagedRecordsStandAlone: one damaged record marks
// the set Damaged and drops only that record — the other still prunes;
// truncation and trailing bytes mark it Damaged too.
func TestArchiveBloomSet_DamagedRecordsStandAlone(t *testing.T) {
	t.Parallel()

	raw := MarshalArchiveBloomSet(bloomSetFixture())
	firstLen := int(binary.LittleEndian.Uint32(raw[archiveBloomSetHeader:]))

	flipped := append([]byte(nil), raw...)
	flipped[archiveBloomSetHeader+4+firstLen-6] ^= 0x01 // inside a's words
	s, err := UnmarshalArchiveBloomSet(flipped)
	if err != nil || !s.Damaged {
		t.Fatalf("flipped = %+v, %v; want damaged", s, err)
	}
	if _, ok := s.Filters["a.pulse"]; ok {
		t.Error("damaged a.pulse record kept")
	}
	if _, ok := s.Filters["b.pulse"]; !ok {
		t.Error("intact b.pulse record lost")
	}

	for name, mutated := range map[string][]byte{
		"truncated": raw[:len(raw)-3],
		"trailing":  append(append([]byte(nil), raw...), 0),
	} {
		s, err := UnmarshalArchiveBloomSet(mutated)
		if err != nil || !s.Damaged {
			t.Errorf("%s = %+v, %v; want damaged", name, s, err)
		}
	}
}

// TestArchiveBloomSet_BadHeaderRefused: a wrong magic, version or a
// short file is ENCODING_INVALID; a missing file is absent, not an error.
func TestArchiveBloomSet_BadHeaderRefused(t *testing.T) {
	t.Parallel()

	raw := MarshalArchiveBloomSet(bloomSetFixture())
	badMagic := append([]byte(nil), raw...)
	badMagic[0] = 'X'
	badVersion := append([]byte(nil), raw...)
	badVersion[8] = 0x7f
	for name, b := range map[string][]byte{"magic": badMagic, "version": badVersion, "short": raw[:5]} {
		if _, err := UnmarshalArchiveBloomSet(b); !errors.HasCode(err, errors.ENCODING_INVALID) {
			t.Errorf("%s: err = %v, want ENCODING_INVALID", name, err)
		}
	}
	fsys := afero.NewMemMapFs()
	if s, present, err := ReadArchiveBloomSetFile(fsys, "none.blooms"); s != nil || present || err != nil {
		t.Errorf("missing = %v, %v, %v", s, present, err)
	}
	if err := afero.WriteFile(fsys, "bad.blooms", badMagic, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, present, err := ReadArchiveBloomSetFile(fsys, "bad.blooms"); !present || err == nil {
		t.Errorf("bad header = present %v, err %v; want present with an error", present, err)
	}
}
