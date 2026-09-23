package encoding

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"path/filepath"

	"github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// widenTempPattern is the afero.TempFile pattern for the staging file a
// file-path widen builds into. It sits in the SAME directory as the
// cohort so the closing rename is a same-filesystem operation — a temp
// file under os.TempDir() can land on a different device, where rename
// degrades to a copy and stops being atomic.
const widenTempPattern = ".widen-*.tmp"

// WidenReport describes a completed widen. It is the value both
// callers of the engine (shard auto-widen and the `pulse widen` leaf)
// report from, so it carries the strides as well as the identities: the
// stride delta is the only observable difference between a widen that
// re-laid-out every record and one that did nothing.
type WidenReport struct {
	// Field is the name of the widened set field.
	Field string
	// From is the set rung the field carried before the rewrite.
	From FieldType
	// To is the set rung it carries after.
	To FieldType
	// Records is the number of records re-laid-out.
	Records int64
	// StrideBefore is the source record stride in bytes, bitmap included.
	StrideBefore int
	// StrideAfter is the rewritten record stride in bytes, bitmap included.
	StrideAfter int
}

// CheckSetWiden reports whether a set field of type from may be widened
// to type to, returning the refusal as a coded error otherwise. It is
// the whole admission policy, exported so a caller can pre-flight a
// widen (a CLI predict, a shard-admin decision) without touching bytes.
//
// Every refusal is ENCODING_TYPE_MISMATCH: each one is a caller asking
// for a type transition the format does not define, which is exactly
// what that code names. This effort introduces no new error codes.
func CheckSetWiden(from, to FieldType) error {
	if !from.IsSet() {
		return errors.NewCodedErrorWithDetails(errors.ENCODING_TYPE_MISMATCH,
			fmt.Sprintf("widen: %s is not a set type", from),
			map[string]any{"from": from.String(), "to": to.String()})
	}
	if !to.IsSet() {
		return errors.NewCodedErrorWithDetails(errors.ENCODING_TYPE_MISMATCH,
			fmt.Sprintf("widen: target %s is not a set type", to),
			map[string]any{"from": from.String(), "to": to.String()})
	}
	// Checked before the width comparison so "already at the top rung"
	// reports the fact the caller needs rather than the generic
	// "not wider", which would be true but useless.
	if from.MaxSetEntries() >= FieldTypeSetU256.MaxSetEntries() {
		return errors.NewCodedErrorWithDetails(errors.ENCODING_TYPE_MISMATCH,
			fmt.Sprintf("widen: %s is already the widest set rung", from),
			map[string]any{"from": from.String(), "to": to.String(),
				"capacity": from.MaxSetEntries()})
	}
	if to.MaxSetEntries() <= from.MaxSetEntries() {
		return errors.NewCodedErrorWithDetails(errors.ENCODING_TYPE_MISMATCH,
			fmt.Sprintf("widen: target %s is not wider than %s", to, from),
			map[string]any{"from": from.String(), "to": to.String(),
				"from_capacity": from.MaxSetEntries(), "to_capacity": to.MaxSetEntries()})
	}
	return nil
}

// WidenSchemaSetField returns a copy of s in which the named set field
// carries the wider rung target, with every field's ByteOffset
// recomputed for the new stride.
//
// It is the SCHEMA half of a widen, split out because the shard
// auto-widen path has to publish a widened canonical `_schema.pulse`
// that has no records of its own alongside shard payloads widened by
// WidenSetFieldBytes. Both go through this one function, so the
// canonical schema block and every shard's schema block cannot end up
// describing different layouts — a divergence that would pass
// ValidateStructuralCohesion nowhere and decode as garbage everywhere.
//
// Names, nullability, descriptions, CSV indices, decimal metadata and
// the inline dictionary pointer ride across by value, so bit i still
// means dictionary entry i. The input schema is never modified.
func WidenSchemaSetField(s *Schema, field string, target FieldType) (*Schema, error) {
	if s == nil {
		return nil, errors.NewCodedError(errors.ENCODING_INVALID,
			"widen: nil schema")
	}
	idx := -1
	for i := range s.Fields {
		if s.Fields[i].Name == field {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, errors.NewCodedErrorWithDetails(errors.ENCODING_INVALID,
			fmt.Sprintf("widen: no field named %q in the cohort schema", field),
			map[string]any{"field": field, "field_count": len(s.Fields)})
	}
	if err := CheckSetWiden(s.Fields[idx].Type, target); err != nil {
		return nil, err
	}
	out := &Schema{Fields: append([]Field(nil), s.Fields...)}
	out.Fields[idx].Type = target
	offsets, _ := widenFieldOffsets(out)
	for i := range out.Fields {
		out.Fields[i].ByteOffset = offsets[i]
	}
	return out, nil
}

// WidenSetFieldBytes rewrites a single-file .pulse cohort held in memory
// so the named set field is stored at the wider rung target, returning
// the new cohort bytes. The input slice is never modified.
//
// It is the entry point for callers that already hold a cohort as bytes
// — a shard payload lifted out of a Zip64 archive, most of all. Callers
// holding a path want WidenSetFieldFile, which adds the atomic
// temp/fsync/rename dance around the same engine.
func WidenSetFieldBytes(cohort []byte, field string, target FieldType) ([]byte, *WidenReport, error) {
	var out bytes.Buffer
	out.Grow(len(cohort))
	rep, err := widenSetFieldStream(&out, bytes.NewReader(cohort), field, target)
	if err != nil {
		return nil, nil, err
	}
	return out.Bytes(), rep, nil
}

// WidenSetFieldFile rewrites the cohort at path in place, widening the
// named set field to target.
//
// The rewrite is ATOMIC, and that is a correctness requirement rather
// than a nicety: widening changes the field's stride, so every record is
// re-laid-out and every field after the widened one moves. A half-
// written cohort is not a cohort that fails to open — it is one that
// opens and decodes every row past the failure point as garbage, with
// the schema block confidently describing a layout the tail does not
// have. So the new cohort is built into a temp file beside the original,
// fsynced, and only then renamed over it. Any failure before the rename
// removes the temp file and leaves the original byte-identical.
//
// Sidecars are NOT updated: a widened cohort changes length, so the
// point-lookup index (`cohort.pulse.<keyhash>.idx`) and the SPSS
// metadata sidecar both invalidate themselves through their own
// size+mtime fingerprints on the next read. Rebuilding them is the
// caller's business.
func WidenSetFieldFile(fsys afero.Fs, path, field string, target FieldType) (*WidenReport, error) {
	src, err := fsys.Open(path)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_IO,
			fmt.Sprintf("opening cohort for widen: %s", path))
	}

	tmp, err := afero.TempFile(fsys, filepath.Dir(path), filepath.Base(path)+widenTempPattern)
	if err != nil {
		_ = src.Close()
		return nil, errors.WrapCodedError(err, errors.ENCODING_IO,
			fmt.Sprintf("creating temp file for widen of %s", path))
	}
	tmpName := tmp.Name()
	abort := func(e error) (*WidenReport, error) {
		_ = tmp.Close()
		_ = fsys.Remove(tmpName)
		_ = src.Close()
		return nil, e
	}

	bw := bufio.NewWriter(tmp)
	rep, err := widenSetFieldStream(bw, src, field, target)
	if err != nil {
		return abort(err)
	}
	if err := bw.Flush(); err != nil {
		return abort(errors.WrapCodedError(err, errors.ENCODING_IO,
			fmt.Sprintf("flushing widened cohort for %s", path)))
	}
	// fsync BEFORE the rename: a rename that lands while the temp file's
	// contents are still only in the page cache publishes a name pointing
	// at bytes a crash can still lose.
	if err := tmp.Sync(); err != nil {
		return abort(errors.WrapCodedError(err, errors.ENCODING_IO,
			fmt.Sprintf("syncing widened cohort for %s", path)))
	}
	if err := tmp.Close(); err != nil {
		_ = fsys.Remove(tmpName)
		_ = src.Close()
		return nil, errors.WrapCodedError(err, errors.ENCODING_IO,
			fmt.Sprintf("closing widened cohort for %s", path))
	}
	// Close the source before the rename so the swap is safe on platforms
	// that refuse to replace an open file.
	if err := src.Close(); err != nil {
		_ = fsys.Remove(tmpName)
		return nil, errors.WrapCodedError(err, errors.ENCODING_IO,
			fmt.Sprintf("closing source cohort %s", path))
	}
	if err := fsys.Rename(tmpName, path); err != nil {
		_ = fsys.Remove(tmpName)
		return nil, errors.WrapCodedError(err, errors.ENCODING_IO,
			fmt.Sprintf("renaming %s onto %s", tmpName, path))
	}
	return rep, nil
}

// widenSetFieldStream is the engine both entry points share: it reads a
// single-file cohort from src and writes the widened cohort to dst,
// record by record, holding one source and one destination record in
// memory at a time.
func widenSetFieldStream(dst io.Writer, src io.Reader, field string, target FieldType) (*WidenReport, error) {
	if err := ReadHeader(src); err != nil {
		return nil, err
	}
	srcSchema, err := ReadSchema(src)
	if err != nil {
		return nil, err
	}

	idx := -1
	for i := range srcSchema.Fields {
		if srcSchema.Fields[i].Name == field {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, errors.NewCodedErrorWithDetails(errors.ENCODING_INVALID,
			fmt.Sprintf("widen: no field named %q in the cohort schema", field),
			map[string]any{"field": field, "field_count": len(srcSchema.Fields)})
	}
	from := srcSchema.Fields[idx].Type

	// The destination schema differs from the source in exactly two ways:
	// the widened field's type byte, and the byte offsets the new stride
	// implies. WidenSchemaSetField is the sole author of both — including
	// the admission check — so a schema-only widen and a bytes widen
	// cannot describe different layouts.
	dstSchema, err := WidenSchemaSetField(srcSchema, field, target)
	if err != nil {
		return nil, err
	}

	srcOffsets, srcPayload := widenFieldOffsets(srcSchema)
	dstOffsets, dstPayload := widenFieldOffsets(dstSchema)

	bitmapBytes := srcSchema.BitmapByteSize()
	srcStride := srcPayload + bitmapBytes
	dstStride := dstPayload + dstSchema.BitmapByteSize()
	if srcStride <= 0 {
		return nil, errors.NewCodedError(errors.ENCODING_INVALID,
			"widen: cohort schema has a zero-byte record stride")
	}

	srcWidth := from.ByteSize()
	dstWidth := target.ByteSize()
	srcSlot := srcOffsets[idx]
	dstSlot := dstOffsets[idx]

	if err := WriteHeader(dst); err != nil {
		return nil, err
	}
	if err := WriteSchema(dst, dstSchema); err != nil {
		return nil, err
	}

	srcRec := make([]byte, srcStride)
	dstRec := make([]byte, dstStride)
	var count int64
	for {
		n, err := io.ReadFull(src, srcRec)
		if err == io.EOF {
			break
		}
		if err == io.ErrUnexpectedEOF {
			return nil, errors.NewCodedErrorWithDetails(errors.ENCODING_INVALID,
				"widen: cohort payload ends mid-record",
				map[string]any{"record_size": srcStride, "trailing_bytes": n,
					"records_read": count})
		}
		if err != nil {
			return nil, errors.WrapCodedError(err, errors.ENCODING_IO, "widen: reading record")
		}

		// Prefix: every field before the widened one keeps its offset, so
		// this is a verbatim copy of the head of the record.
		copy(dstRec[:dstSlot], srcRec[:srcSlot])
		// The widened slot itself, through the rung-agnostic mask codec.
		mask, err := readSetMaskSlot(srcRec[srcSlot:srcSlot+srcWidth], from)
		if err != nil {
			return nil, err
		}
		if err := writeSetMaskSlot(dstRec[dstSlot:dstSlot+dstWidth], target, mask); err != nil {
			return nil, err
		}
		// Suffix: every field after the widened one, plus the trailing null
		// bitmap, shifted by the width delta and otherwise byte-identical.
		copy(dstRec[dstSlot+dstWidth:], srcRec[srcSlot+srcWidth:])

		if _, err := dst.Write(dstRec); err != nil {
			return nil, errors.WrapCodedError(err, errors.ENCODING_IO, "widen: writing record")
		}
		count++
	}

	return &WidenReport{
		Field:        field,
		From:         from,
		To:           target,
		Records:      count,
		StrideBefore: srcStride,
		StrideAfter:  dstStride,
	}, nil
}

// widenFieldOffsets derives each field's byte offset within the record
// payload from the field TYPES, together with the total payload width
// (the stride minus any trailing null bitmap).
//
// It deliberately ignores the Field.ByteOffset the schema block carries:
// stride is a pure function of the type bytes, the stored offset is a
// cache of that derivation, and a widen is precisely the operation that
// invalidates the cache. Bit-packed fields report ByteSize()==0 but
// occupy one whole byte each on the wire, matching Schema.RecordByteSize.
func widenFieldOffsets(s *Schema) (offsets []int, payload int) {
	offsets = make([]int, len(s.Fields))
	for i := range s.Fields {
		offsets[i] = payload
		ft := s.Fields[i].Type
		if ft.IsBitPacked() {
			payload++
			continue
		}
		payload += ft.ByteSize()
	}
	return offsets, payload
}

// readSetMaskSlot decodes a set field's on-wire payload at ANY rung into
// the shared SetMask. The wide rungs go through the normative wire API;
// the narrow rungs keep their uint64 storage and are lifted into words[0].
// src must be at least the rung's byte width.
func readSetMaskSlot(src []byte, ft FieldType) (SetMask, error) {
	if ft.IsWideSet() {
		return SetMaskFromBytes(ft, src)
	}
	if !ft.IsSet() {
		return SetMask{}, errors.NewCodedErrorWithDetails(errors.ENCODING_TYPE_MISMATCH,
			fmt.Sprintf("set mask decode requires a set type, got %s", ft),
			map[string]any{"type": ft.String()})
	}
	if len(src) < ft.ByteSize() {
		return SetMask{}, errors.NewCodedErrorWithDetails(errors.ENCODING_INVALID,
			fmt.Sprintf("source buffer too small for %s", ft),
			map[string]any{"type": ft.String(), "want_bytes": ft.ByteSize(), "got_bytes": len(src)})
	}
	return SetMaskFromUint64(decodeSetMask(ft, src)), nil
}

// writeSetMaskSlot encodes a SetMask as a set field's on-wire payload at
// ANY rung — the inverse of readSetMaskSlot, and the mirror decodeSetMask
// never had. A mask carrying a bit the rung cannot store is refused by
// checkSetMaskFits rather than silently truncated.
func writeSetMaskSlot(dst []byte, ft FieldType, m SetMask) error {
	if ft.IsWideSet() {
		return PutSetMask(dst, ft, m)
	}
	if !ft.IsSet() {
		return errors.NewCodedErrorWithDetails(errors.ENCODING_TYPE_MISMATCH,
			fmt.Sprintf("set mask encode requires a set type, got %s", ft),
			map[string]any{"type": ft.String()})
	}
	width := ft.ByteSize()
	if len(dst) < width {
		return errors.NewCodedErrorWithDetails(errors.ENCODING_INVALID,
			fmt.Sprintf("destination buffer too small for %s", ft),
			map[string]any{"type": ft.String(), "want_bytes": width, "got_bytes": len(dst)})
	}
	if err := checkSetMaskFits(ft, m); err != nil {
		return err
	}
	low, _ := m.Uint64()
	switch ft {
	case FieldTypeSetU8:
		dst[0] = byte(low)
	case FieldTypeSetU16:
		binary.LittleEndian.PutUint16(dst[:2], uint16(low))
	case FieldTypeSetU32:
		binary.LittleEndian.PutUint32(dst[:4], uint32(low))
	case FieldTypeSetU64:
		binary.LittleEndian.PutUint64(dst[:8], low)
	}
	return nil
}
