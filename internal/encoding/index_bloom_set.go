package encoding

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strings"

	"github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// index_bloom_set.go is the ARCHIVE BLOOM SET of an archive point-lookup
// index (archive storage #721): every covered shard's Bloom filter in ONE
// file per index, so a lookup reads the Bloom data once — one GET on
// object storage — however many shards its key range leaves in play,
// instead of one per-shard ".bloom" sidecar per in-range shard.
//
// The file sits beside the archive at the index's LOGICAL path with
// ".idx" swapped for ".blooms" (ArchiveBloomSetPath —
// "archive.pulse.<keyhash>.blooms"), never inside the zip and never in
// the manifest. Layout, every integer little-endian:
//
//	magic     8  "PULSEBLS"
//	version   1  ArchiveBloomSetFormatVersion (0x01)
//	count     4  u32 number of records
//	count × { len u32, then len bytes: one shard Bloom in the per-shard
//	          sidecar layout (MarshalShardBloom) — its own magic,
//	          version, shard identity stamp and CRC32 }
//
// The records are in archive order. Each record stands or falls alone:
// it is trusted only when it decodes (its own CRC32) and its stamp
// names the covered shard's exact name + size + CRC32 and key width.
// A record that does not decode, a length that overruns the file, a
// duplicate name or bytes after the last record mark the set Damaged;
// the records that did decode still prune, every other shard is probed.
// A bad header (magic, version, short file) makes the whole set
// unusable: every shard is probed. Nothing here can lose a match.

// ArchiveBloomSetMagic opens every archive Bloom set file.
const ArchiveBloomSetMagic = "PULSEBLS"

// ArchiveBloomSetFormatVersion is the archive Bloom set's own version —
// a separate axis from the .pulse byte, the .idx version, the per-shard
// Bloom version and the manifest's format_version.
const ArchiveBloomSetFormatVersion byte = 0x01

// ArchiveBloomSetSuffix replaces an archive index's logical ".idx" in
// its Bloom set's path.
const ArchiveBloomSetSuffix = ".blooms"

// archiveBloomSetHeader is magic + version + count.
const archiveBloomSetHeader = 8 + 1 + 4

// ArchiveBloomSet is a decoded archive Bloom set.
type ArchiveBloomSet struct {
	// Filters holds every record that decoded, by shard entry name.
	Filters map[string]*ShardBloom
	// Records holds each decoded record's exact bytes, by shard name
	// (VerifyIndex compares them with a rebuild).
	Records map[string][]byte
	// Damaged reports a record that did not decode, a duplicate name,
	// a length past the end or trailing bytes: some shard's filter may
	// be missing from Filters.
	Damaged bool
}

// MarshalArchiveBloomSet renders filters, in the order given, in the
// archive Bloom set layout.
func MarshalArchiveBloomSet(filters []*ShardBloom) []byte {
	var buf bytes.Buffer
	buf.WriteString(ArchiveBloomSetMagic)
	buf.WriteByte(ArchiveBloomSetFormatVersion)
	_ = binary.Write(&buf, binary.LittleEndian, uint32(len(filters)))
	for _, f := range filters {
		rec := MarshalShardBloom(f)
		_ = binary.Write(&buf, binary.LittleEndian, uint32(len(rec)))
		buf.Write(rec)
	}
	return buf.Bytes()
}

// UnmarshalArchiveBloomSet decodes a set. A wrong magic or version, or
// a file shorter than the header, is ENCODING_INVALID; every later
// fault only marks the set Damaged (see the file comment).
func UnmarshalArchiveBloomSet(raw []byte) (*ArchiveBloomSet, error) {
	bad := func(why string) error {
		return errors.NewCodedErrorWithDetails(errors.ENCODING_INVALID,
			"archive Bloom set is malformed: "+why, map[string]any{"reason": why})
	}
	if len(raw) < archiveBloomSetHeader || string(raw[:8]) != ArchiveBloomSetMagic {
		return nil, bad("magic")
	}
	if raw[8] != ArchiveBloomSetFormatVersion {
		return nil, bad("version")
	}
	count := binary.LittleEndian.Uint32(raw[9:])
	s := &ArchiveBloomSet{Filters: map[string]*ShardBloom{}, Records: map[string][]byte{}}
	off := archiveBloomSetHeader
	for i := uint32(0); i < count; i++ {
		if len(raw)-off < 4 {
			s.Damaged = true
			return s, nil
		}
		n := int(binary.LittleEndian.Uint32(raw[off:]))
		off += 4
		if n > len(raw)-off {
			s.Damaged = true
			return s, nil
		}
		rec := raw[off : off+n]
		off += n
		f, err := UnmarshalShardBloom(rec)
		if err != nil {
			s.Damaged = true
			continue
		}
		if _, dup := s.Filters[f.Shard]; dup {
			s.Damaged = true
			continue
		}
		s.Filters[f.Shard] = f
		s.Records[f.Shard] = append([]byte(nil), rec...)
	}
	if off != len(raw) {
		s.Damaged = true
	}
	return s, nil
}

// Fresh returns the decoded filter for the covered shard sh when its
// stamp is exactly sh's identity for keys of keyWidth bytes; nil
// otherwise (no record, or a stale one).
func (s *ArchiveBloomSet) Fresh(sh IndexManifestShard, keyWidth int) *ShardBloom {
	if s == nil {
		return nil
	}
	if f := s.Filters[sh.Name]; f != nil && f.Describes(sh, keyWidth) {
		return f
	}
	return nil
}

// Excludes is the lookup's Bloom check against the set: true only when
// the set holds a fresh filter for the covered shard sh that answers
// "not present" for key. A nil set, no record or a stale one is false:
// probe the shard.
func (s *ArchiveBloomSet) Excludes(sh IndexManifestShard, key []byte) bool {
	f := s.Fresh(sh, len(key))
	return f != nil && !f.MayContain(key)
}

// ArchiveBloomSetPath is the Bloom set of the archive index over the
// ordered keyFields: the index's logical path (SidecarIndexPath over
// the archive) with ".idx" swapped for ".blooms". Pure.
func ArchiveBloomSetPath(archivePath string, keyFields []string) string {
	return ArchiveBloomSetPathFor(SidecarIndexPath(archivePath, keyFields))
}

// ArchiveBloomSetPathFor maps an archive index's logical path (or a
// manifest entry's index_path basename) to its Bloom set's.
func ArchiveBloomSetPathFor(indexPath string) string {
	return strings.TrimSuffix(indexPath, ".idx") + ArchiveBloomSetSuffix
}

// WriteArchiveBloomSetFile writes filters, in order, to path.
func WriteArchiveBloomSetFile(fsys afero.Fs, path string, filters []*ShardBloom) error {
	if err := afero.WriteFile(fsys, path, MarshalArchiveBloomSet(filters), 0o644); err != nil {
		return errors.WrapCodedError(err, errors.ENCODING_IO,
			fmt.Sprintf("writing archive Bloom set: %s", path))
	}
	return nil
}

// ReadArchiveBloomSetFile reads and decodes the set at path in ONE read.
// No file is (nil, false, nil); a read failure is (nil, false,
// ENCODING_IO); a present file with a bad header is (nil, true,
// ENCODING_INVALID).
func ReadArchiveBloomSetFile(fsys afero.Fs, path string) (s *ArchiveBloomSet, present bool, err error) {
	raw, err := afero.ReadFile(fsys, path)
	if err != nil {
		if exists, xerr := afero.Exists(fsys, path); xerr == nil && !exists {
			return nil, false, nil
		}
		return nil, false, errors.WrapCodedError(err, errors.ENCODING_IO,
			fmt.Sprintf("reading archive Bloom set: %s", path))
	}
	s, err = UnmarshalArchiveBloomSet(raw)
	return s, true, err
}
