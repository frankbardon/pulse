package encoding

import (
	"bytes"
	"encoding/hex"

	"github.com/frankbardon/pulse/encoding"
)

// index_key_range.go is the per-shard key RANGE an archive index
// records in its manifest (IndexManifestShard.KeyMin / KeyMax, archive
// storage #493) so a lookup can skip a shard whose range excludes the
// probed key without opening that shard's sidecar.
//
// The range is kept per key COMPONENT (a composite key prunes when any
// one component falls outside its own range — a bounding box, never a
// lexicographic tuple range) and each bound is an ORDER-PRESERVING byte
// string, spelled as fixed-width lowercase hex in the manifest:
//
//   - unsigned words (u4, u8..u64, packed_bool, categorical_* dictionary
//     ids): the word big-endian;
//   - signed words (date int32, datetime int64, decimal128's 128-bit
//     two's-complement mantissa): big-endian with the sign bit flipped;
//   - floats (f32, f64): the IEEE-754 total-order transform of the raw
//     bit pattern (sign bit flipped when clear, every bit flipped when
//     set) — the index keys on raw bits, so bit-equal keys stay inside
//     the range and -0.0 / NaN payloads order consistently.
//
// Byte order of these strings equals the field's value order, so a
// bytes.Compare decides membership; u64 words above 2^53 stay exact
// (no JSON number anywhere). A categorical id range is an id range:
// shard dictionaries are a prefix of the canonical one, so ids mean
// the same in every shard and in the resolved literal.
//
// Pruning is only ever a skip decision: an absent, malformed or
// wrong-arity range means "probe this shard" — a manifest written or
// rewritten by a binary that predates the fields drops them, and that
// must cost speed, never a match.

// IndexKeyComponentWidth is the byte width of one key component in a
// composite index key (processing.KeyFieldOnWireBytes): the field's
// stored width, with the bit-packed u4 / packed_bool given their
// one-byte key slot.
func IndexKeyComponentWidth(ft encoding.FieldType) int {
	switch ft {
	case encoding.FieldTypeU4, encoding.FieldTypePackedBool:
		return 1
	}
	return ft.ByteSize()
}

// IndexKeyOrderComponents splits the composite on-wire key bytes into
// one order-preserving byte string per component of types. ok is false
// when the key's length does not match the types' widths.
func IndexKeyOrderComponents(types []encoding.FieldType, wire []byte) ([][]byte, bool) {
	out := make([][]byte, len(types))
	off := 0
	for i, ft := range types {
		w := IndexKeyComponentWidth(ft)
		if w <= 0 || off+w > len(wire) {
			return nil, false
		}
		out[i] = orderKeyComponent(ft, wire[off:off+w])
		off += w
	}
	if off != len(wire) {
		return nil, false
	}
	return out, true
}

// orderKeyComponent maps one little-endian on-wire component to its
// order-preserving big-endian form.
func orderKeyComponent(ft encoding.FieldType, le []byte) []byte {
	be := make([]byte, len(le))
	for i := range le {
		be[len(le)-1-i] = le[i]
	}
	switch ft {
	case encoding.FieldTypeDate, encoding.FieldTypeDateTime, encoding.FieldTypeDecimal128:
		be[0] ^= 0x80
	case encoding.FieldTypeF32, encoding.FieldTypeF64:
		if be[0]&0x80 != 0 {
			for i := range be {
				be[i] = ^be[i]
			}
		} else {
			be[0] ^= 0x80
		}
	}
	return be
}

// IndexKeySpecTypes returns the field types of keys, in order.
func IndexKeySpecTypes(keys []IndexKeySpec) []encoding.FieldType {
	out := make([]encoding.FieldType, len(keys))
	for i, k := range keys {
		out[i] = k.Type
	}
	return out
}

// IndexKeyRange returns the per-component minimum and maximum of every
// key in buckets as order-preserving hex (see the file comment), or
// nil, nil when the buckets hold no key — an empty shard records no
// range and is probed. A key whose length does not split into keys'
// widths also yields nil, nil: no range is safer than a wrong one.
func IndexKeyRange(keys []IndexKeySpec, buckets []IndexBucket) (keyMin, keyMax []string) {
	types := IndexKeySpecTypes(keys)
	var lo, hi [][]byte
	for _, b := range buckets {
		for _, e := range b.Entries {
			comps, ok := IndexKeyOrderComponents(types, e.Key)
			if !ok {
				return nil, nil
			}
			if lo == nil {
				lo = append([][]byte(nil), comps...)
				hi = append([][]byte(nil), comps...)
				continue
			}
			for i, c := range comps {
				if bytes.Compare(c, lo[i]) < 0 {
					lo[i] = c
				}
				if bytes.Compare(c, hi[i]) > 0 {
					hi[i] = c
				}
			}
		}
	}
	if lo == nil {
		return nil, nil
	}
	keyMin = make([]string, len(lo))
	keyMax = make([]string, len(hi))
	for i := range lo {
		keyMin[i] = hex.EncodeToString(lo[i])
		keyMax[i] = hex.EncodeToString(hi[i])
	}
	return keyMin, keyMax
}

// HasKeyRange reports whether the shard record carries a usable key
// range: both bounds present with the same arity.
func (s IndexManifestShard) HasKeyRange() bool {
	return len(s.KeyMin) > 0 && len(s.KeyMin) == len(s.KeyMax)
}

// KeyRangeExcludes reports whether the shard's recorded key range
// PROVES the key (its order-preserving components, from
// IndexKeyOrderComponents) absent from the shard. It answers false —
// probe — whenever it cannot prove that: no range recorded, an arity
// mismatch, or a bound that is not hex of the component's width.
func (s IndexManifestShard) KeyRangeExcludes(key [][]byte) bool {
	if !s.HasKeyRange() || len(s.KeyMin) != len(key) {
		return false
	}
	excluded := false
	for i, c := range key {
		lo, err1 := hex.DecodeString(s.KeyMin[i])
		hi, err2 := hex.DecodeString(s.KeyMax[i])
		if err1 != nil || err2 != nil || len(lo) != len(c) || len(hi) != len(c) {
			return false
		}
		if bytes.Compare(c, lo) < 0 || bytes.Compare(c, hi) > 0 {
			excluded = true
		}
	}
	return excluded
}
