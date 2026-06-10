package encoding

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math/bits"

	"github.com/frankbardon/pulse/errors"
)

// RewriteShardCategoricals re-emits a single-file .pulse shard with
// its schema block replaced by targetSchema and its records' categorical
// indices translated according to remap. The on-wire byte layout of
// every non-categorical field is preserved exactly.
//
// targetSchema must be structurally identical to the shard's own
// schema (field count, names, types, byte offsets, bit positions) —
// only the per-categorical dictionaries differ.
//
// When remap is empty (every incoming dict was a prefix of canonical),
// the function still re-emits the shard with targetSchema so the
// shard's standalone dictionary matches the archive's canonical
// (callers reading the shard via the `archive.pulse#shard.pulse`
// anchor see the union dictionary). Record bytes are copied verbatim.
//
// Returns PULSE_SHARD_HEADER_INVALID if the input bytes are not a
// valid single-file Pulse cohort, and PULSE_SHARD_SCHEMA_MISMATCH if
// targetSchema is structurally incompatible with the shard's own
// schema.
func RewriteShardCategoricals(shardBytes []byte, targetSchema *Schema, remap map[int]DictRemap) ([]byte, error) {
	if targetSchema == nil {
		return nil, errors.NewCodedError(errors.PULSE_SHARD_SCHEMA_MISMATCH,
			"RewriteShardCategoricals requires a non-nil targetSchema")
	}

	src := bytes.NewReader(shardBytes)
	if err := ReadHeader(src); err != nil {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_HEADER_INVALID,
			"shard rewrite: invalid header",
			map[string]any{"cause": err.Error()})
	}
	srcSchema, err := ReadSchema(src)
	if err != nil {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_HEADER_INVALID,
			"shard rewrite: invalid schema block",
			map[string]any{"cause": err.Error()})
	}
	if len(srcSchema.Fields) != len(targetSchema.Fields) {
		return nil, errors.NewCodedError(errors.PULSE_SHARD_SCHEMA_MISMATCH,
			"shard rewrite: targetSchema field count differs from shard schema")
	}
	for i := range srcSchema.Fields {
		sf := &srcSchema.Fields[i]
		tf := &targetSchema.Fields[i]
		if sf.Name != tf.Name || sf.Type != tf.Type || sf.ByteOffset != tf.ByteOffset || sf.BitPosition != tf.BitPosition {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_SCHEMA_MISMATCH,
				fmt.Sprintf("shard rewrite: field %d structural mismatch", i),
				map[string]any{"field_index": i, "shard_name": sf.Name, "target_name": tf.Name})
		}
	}

	recordSize := srcSchema.RecordByteSize()

	remaining := shardBytes[len(shardBytes)-src.Len():]

	var out bytes.Buffer
	if err := WriteHeader(&out); err != nil {
		return nil, err
	}
	if err := WriteSchema(&out, targetSchema); err != nil {
		return nil, err
	}

	if len(remap) == 0 || recordSize == 0 {
		out.Write(remaining)
		return out.Bytes(), nil
	}

	if len(remaining)%recordSize != 0 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_HEADER_INVALID,
			"shard rewrite: payload length not a multiple of record size",
			map[string]any{"record_size": recordSize, "payload_bytes": len(remaining)})
	}

	rewritePlan := make([]rewriteEntry, 0, len(remap))
	for idx, m := range remap {
		f := &srcSchema.Fields[idx]
		rewritePlan = append(rewritePlan, rewriteEntry{
			offset: int(f.ByteOffset),
			ftype:  f.Type,
			remap:  m,
		})
	}

	rec := make([]byte, recordSize)
	for i := 0; i < len(remaining); i += recordSize {
		copy(rec, remaining[i:i+recordSize])
		for _, e := range rewritePlan {
			if err := applyRemap(rec, e); err != nil {
				return nil, err
			}
		}
		out.Write(rec)
	}
	return out.Bytes(), nil
}

type rewriteEntry struct {
	offset int
	ftype  FieldType
	remap  DictRemap
}

func applyRemap(rec []byte, e rewriteEntry) error {
	switch e.ftype {
	case FieldTypeCategoricalU8:
		old := uint32(rec[e.offset])
		if v, ok := e.remap[old]; ok {
			if v > 0xFF {
				return errors.NewCodedError(errors.PULSE_SHARD_DICT_WIDTH_OVERFLOW,
					"shard rewrite: remapped u8 categorical index exceeds 0xFF")
			}
			rec[e.offset] = byte(v)
		}
	case FieldTypeCategoricalU16:
		old := uint32(binary.LittleEndian.Uint16(rec[e.offset : e.offset+2]))
		if v, ok := e.remap[old]; ok {
			if v > 0xFFFF {
				return errors.NewCodedError(errors.PULSE_SHARD_DICT_WIDTH_OVERFLOW,
					"shard rewrite: remapped u16 categorical index exceeds 0xFFFF")
			}
			binary.LittleEndian.PutUint16(rec[e.offset:e.offset+2], uint16(v))
		}
	case FieldTypeCategoricalU32:
		old := binary.LittleEndian.Uint32(rec[e.offset : e.offset+4])
		if v, ok := e.remap[old]; ok {
			binary.LittleEndian.PutUint32(rec[e.offset:e.offset+4], v)
		}
	case FieldTypeSetU8:
		old := uint64(rec[e.offset])
		newMask, err := remapSetMask(old, e.remap, 8)
		if err != nil {
			return err
		}
		rec[e.offset] = byte(newMask)
	case FieldTypeSetU16:
		old := uint64(binary.LittleEndian.Uint16(rec[e.offset : e.offset+2]))
		newMask, err := remapSetMask(old, e.remap, 16)
		if err != nil {
			return err
		}
		binary.LittleEndian.PutUint16(rec[e.offset:e.offset+2], uint16(newMask))
	case FieldTypeSetU32:
		old := uint64(binary.LittleEndian.Uint32(rec[e.offset : e.offset+4]))
		newMask, err := remapSetMask(old, e.remap, 32)
		if err != nil {
			return err
		}
		binary.LittleEndian.PutUint32(rec[e.offset:e.offset+4], uint32(newMask))
	case FieldTypeSetU64:
		old := binary.LittleEndian.Uint64(rec[e.offset : e.offset+8])
		newMask, err := remapSetMask(old, e.remap, 64)
		if err != nil {
			return err
		}
		binary.LittleEndian.PutUint64(rec[e.offset:e.offset+8], newMask)
	default:
		return errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_SCHEMA_MISMATCH,
			"shard rewrite: non-dictionary field in remap plan",
			map[string]any{"type": e.ftype.String()})
	}
	return nil
}

// remapSetMask rewrites a set field's bitmask under a dict remap. For
// every set bit i, the new mask has bit remap[i] set (or bit i when no
// remap entry exists). width is the field type's bit capacity and
// gates the new bit position so a remap that promotes a value past the
// declared width surfaces as PULSE_SHARD_DICT_WIDTH_OVERFLOW — the
// dict-union path already enforces this at the schema level, but the
// rewrite stays defensive against a corrupted remap.
func remapSetMask(mask uint64, remap DictRemap, width uint32) (uint64, error) {
	var out uint64
	for mask != 0 {
		i := uint32(bits.TrailingZeros64(mask))
		newBit := i
		if v, ok := remap[i]; ok {
			newBit = v
		}
		if newBit >= width {
			return 0, errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_DICT_WIDTH_OVERFLOW,
				"shard rewrite: remapped set bit position exceeds field width",
				map[string]any{"width": width, "new_bit": newBit, "old_bit": i})
		}
		out |= uint64(1) << newBit
		mask &= mask - 1
	}
	return out, nil
}
