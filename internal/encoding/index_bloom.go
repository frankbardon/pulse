package encoding

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"strings"

	"github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// index_bloom.go is the per-shard BLOOM sidecar of an archive point-lookup
// index (archive storage #493, part 2): a binary membership filter over
// the composite keys one shard's sidecar holds, consulted by a lookup
// AFTER the shard's recorded key range and BEFORE the shard's sidecar is
// opened. "No" is exact (a Bloom filter has no false negatives); "maybe"
// probes the sidecar as before.
//
// The file sits beside the shard's sidecar — the sidecar path with its
// ".idx" swapped for ".bloom" (ArchiveShardBloomPath) — never inside the
// zip and never in the manifest. Layout, every integer little-endian:
//
//	magic        8  "PULSEBLM"
//	version      1  ShardBloomFormatVersion (0x01)
//	hash_count   1  k probes per key (ShardBloomHashCount)
//	key_width    4  u32 byte length of one composite on-wire key
//	shard_size   8  u64 shard size  } the shard's identity, exactly
//	shard_crc32  4  u32 zip CRC32   } as the manifest records it
//	name_len     2  u16, then name_len bytes of the shard's entry name
//	word_count   4  u32; the filter has word_count*64 bits
//	words        8 × word_count, u64 each; bit i is word i/64, bit i%64
//	crc32        4  IEEE CRC32 of every preceding byte
//
// Hashing is integer-only and therefore bit-identical on every
// architecture: h = HashKey(key) (FNV-1a 64, the bucket hash); h1 =
// splitmix64(h), h2 = splitmix64(h ^ bloomSecondSeed) | 1; probe j sets
// or tests bit (h1 + j*h2) mod bits (Kirsch–Mitzenmacher double
// hashing, uint64 wraparound). Sizing is ShardBloomBitsPerKey bits per
// distinct key rounded up to a whole word (minimum one word), k = 7:
// a ~1% false-positive rate.
//
// A Bloom is only ever a skip decision. Missing, unreadable, malformed
// (magic, version, CRC, length), another shard identity or another key
// width all mean "probe this shard" (ShardBloomExcludes) — a stale or
// damaged filter costs speed, never a match.

// ShardBloomMagic opens every shard Bloom sidecar.
const ShardBloomMagic = "PULSEBLM"

// ShardBloomFormatVersion is the shard Bloom sidecar's own version — a
// separate axis from the .pulse version byte and the .idx version.
const ShardBloomFormatVersion byte = 0x01

// ShardBloomBitsPerKey and ShardBloomHashCount size the filter: 10 bits
// per distinct key and 7 probes give a ~1% false-positive rate.
const (
	ShardBloomBitsPerKey = 10
	ShardBloomHashCount  = 7
)

// bloomSecondSeed decorrelates the second double-hashing stream.
const bloomSecondSeed uint64 = 0x9e3779b97f4a7c15

// ShardBloomSuffix replaces a shard sidecar's ".idx" in its Bloom's path.
const ShardBloomSuffix = ".bloom"

// ShardBloom is a decoded shard Bloom sidecar.
type ShardBloom struct {
	// Shard, Size and CRC32 are the shard identity the filter was built
	// over — the manifest record's name + size + zip CRC32.
	Shard string
	Size  int64
	CRC32 uint32
	// KeyWidth is the byte length of one composite on-wire key.
	KeyWidth  uint32
	HashCount uint8
	Words     []uint64
}

// NewShardBloom returns an empty filter for distinctKeys keys of the
// shard identified by (shard, size, crc).
func NewShardBloom(shard string, size int64, crc uint32, keyWidth int, distinctKeys int) *ShardBloom {
	bits := distinctKeys * ShardBloomBitsPerKey
	words := (bits + 63) / 64
	if words < 1 {
		words = 1
	}
	return &ShardBloom{
		Shard: shard, Size: size, CRC32: crc,
		KeyWidth: uint32(keyWidth), HashCount: ShardBloomHashCount,
		Words: make([]uint64, words),
	}
}

// ShardBloomFromBuckets builds the filter over every key of a shard's
// sidecar buckets.
func ShardBloomFromBuckets(e ArchiveEntry, keys []IndexKeySpec, buckets []IndexBucket) *ShardBloom {
	width, distinct := 0, 0
	for _, k := range keys {
		width += IndexKeyComponentWidth(k.Type)
	}
	for _, b := range buckets {
		distinct += len(b.Entries)
	}
	f := NewShardBloom(e.Name, e.Size, e.CRC32, width, distinct)
	for _, b := range buckets {
		for _, en := range b.Entries {
			f.Add(en.Key)
		}
	}
	return f
}

func splitmix64(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}

// bloomBits returns the k bit positions key probes.
func (f *ShardBloom) bloomBits(key []byte, visit func(bit uint64) bool) {
	h := HashKey(key)
	h1 := splitmix64(h)
	h2 := splitmix64(h^bloomSecondSeed) | 1
	m := uint64(len(f.Words)) * 64
	for j := uint64(0); j < uint64(f.HashCount); j++ {
		if !visit((h1 + j*h2) % m) {
			return
		}
	}
}

// Add inserts key.
func (f *ShardBloom) Add(key []byte) {
	f.bloomBits(key, func(bit uint64) bool {
		f.Words[bit/64] |= 1 << (bit % 64)
		return true
	})
}

// MayContain reports whether key may be in the set: false is exact.
func (f *ShardBloom) MayContain(key []byte) bool {
	in := true
	f.bloomBits(key, func(bit uint64) bool {
		if f.Words[bit/64]&(1<<(bit%64)) == 0 {
			in = false
		}
		return in
	})
	return in
}

// MarshalShardBloom renders f in the sidecar layout.
func MarshalShardBloom(f *ShardBloom) []byte {
	var buf bytes.Buffer
	buf.WriteString(ShardBloomMagic)
	buf.WriteByte(ShardBloomFormatVersion)
	buf.WriteByte(f.HashCount)
	_ = binary.Write(&buf, binary.LittleEndian, f.KeyWidth)
	_ = binary.Write(&buf, binary.LittleEndian, uint64(f.Size))
	_ = binary.Write(&buf, binary.LittleEndian, f.CRC32)
	_ = binary.Write(&buf, binary.LittleEndian, uint16(len(f.Shard)))
	buf.WriteString(f.Shard)
	_ = binary.Write(&buf, binary.LittleEndian, uint32(len(f.Words)))
	_ = binary.Write(&buf, binary.LittleEndian, f.Words)
	_ = binary.Write(&buf, binary.LittleEndian, crc32.ChecksumIEEE(buf.Bytes()))
	return buf.Bytes()
}

// shardBloomFixedHeader is every header byte but the name.
const shardBloomFixedHeader = 8 + 1 + 1 + 4 + 8 + 4 + 2 + 4

// UnmarshalShardBloom decodes a sidecar, refusing (ENCODING_INVALID) a
// wrong magic or version, a length that disagrees with the header, a
// hash count of zero or a CRC32 mismatch.
func UnmarshalShardBloom(raw []byte) (*ShardBloom, error) {
	bad := func(why string) error {
		return errors.NewCodedErrorWithDetails(errors.ENCODING_INVALID,
			"shard Bloom sidecar is malformed: "+why, map[string]any{"reason": why})
	}
	if len(raw) < shardBloomFixedHeader+4 || string(raw[:8]) != ShardBloomMagic {
		return nil, bad("magic")
	}
	if raw[8] != ShardBloomFormatVersion {
		return nil, bad("version")
	}
	body, sum := raw[:len(raw)-4], binary.LittleEndian.Uint32(raw[len(raw)-4:])
	if crc32.ChecksumIEEE(body) != sum {
		return nil, bad("crc32")
	}
	f := &ShardBloom{HashCount: raw[9]}
	f.KeyWidth = binary.LittleEndian.Uint32(raw[10:])
	f.Size = int64(binary.LittleEndian.Uint64(raw[14:]))
	f.CRC32 = binary.LittleEndian.Uint32(raw[22:])
	nameLen := int(binary.LittleEndian.Uint16(raw[26:]))
	off := 28 + nameLen
	if off+4 > len(body) {
		return nil, bad("length")
	}
	f.Shard = string(raw[28:off])
	words := int(binary.LittleEndian.Uint32(raw[off:]))
	off += 4
	if words < 1 || f.HashCount == 0 || len(body)-off != words*8 {
		return nil, bad("length")
	}
	f.Words = make([]uint64, words)
	for i := range f.Words {
		f.Words[i] = binary.LittleEndian.Uint64(raw[off+8*i:])
	}
	return f, nil
}

// ArchiveShardBloomPath is the Bloom sidecar beside the shard sidecar
// ArchiveShardIndexPath derives. Pure.
func ArchiveShardBloomPath(archivePath, shardName string, keyFields []string) string {
	return ShardBloomPathFor(ArchiveShardIndexPath(archivePath, shardName, keyFields))
}

// ShardBloomPathFor maps a shard sidecar path (or basename) to its
// Bloom's: the trailing ".idx" becomes ".bloom".
func ShardBloomPathFor(indexPath string) string {
	return strings.TrimSuffix(indexPath, ".idx") + ShardBloomSuffix
}

// WriteShardBloomFile writes f to path.
func WriteShardBloomFile(fsys afero.Fs, path string, f *ShardBloom) error {
	if err := afero.WriteFile(fsys, path, MarshalShardBloom(f), 0o644); err != nil {
		return errors.WrapCodedError(err, errors.ENCODING_IO,
			fmt.Sprintf("writing shard Bloom sidecar: %s", path))
	}
	return nil
}

// ReadShardBloomFile reads and decodes the sidecar at path. present is
// false (with a nil error) when no file exists.
func ReadShardBloomFile(fsys afero.Fs, path string) (f *ShardBloom, present bool, err error) {
	raw, err := afero.ReadFile(fsys, path)
	if err != nil {
		if exists, xerr := afero.Exists(fsys, path); xerr == nil && !exists {
			return nil, false, nil
		}
		return nil, false, errors.WrapCodedError(err, errors.ENCODING_IO,
			fmt.Sprintf("reading shard Bloom sidecar: %s", path))
	}
	f, err = UnmarshalShardBloom(raw)
	return f, true, err
}

// Describes reports whether f was built over the shard sh identifies
// (name + size + CRC32) for keys of keyWidth bytes.
func (f *ShardBloom) Describes(sh IndexManifestShard, keyWidth int) bool {
	return f.Shard == sh.Name && f.Size == sh.Size && f.CRC32 == sh.CRC32 && int(f.KeyWidth) == keyWidth
}

// ShardBloomExcludes is the lookup's Bloom check: true only when the
// Bloom sidecar at path is present, well formed, built over exactly the
// covered shard sh, for keys of key's width, and answers "not present".
// Anything else — no file, a read error, a malformed or stale filter —
// is false: probe the shard.
func ShardBloomExcludes(fsys afero.Fs, path string, sh IndexManifestShard, key []byte) bool {
	f, present, err := ReadShardBloomFile(fsys, path)
	if err != nil || !present || !f.Describes(sh, len(key)) {
		return false
	}
	return !f.MayContain(key)
}
