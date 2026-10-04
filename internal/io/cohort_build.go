package io

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"path/filepath"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/spf13/afero"
)

// Spool / staging name patterns, beside the target (afero.TempFile
// replaces the '*').
const (
	buildSpoolPattern = ".build-spool-*.tmp"
	buildTempPattern  = ".build-*.tmp"
	buildIOBuffer     = 256 << 10
)

// CohortBuildOptions configures a CohortBuild.
type CohortBuildOptions struct {
	// Strict turns every PULSE_FIELD_DESCRIPTION_LOW_QUALITY finding
	// into a fatal error raised by NewCohortBuild.
	Strict bool
	// Overwrite lets the build replace an existing target. Without it
	// an existing target is refused (SERVICE_VALIDATION).
	Overwrite bool
}

// CohortBuildReport is what a successful CohortBuild.Close wrote.
type CohortBuildReport struct {
	// Target is the cohort path written.
	Target string
	// Records is the number of rows written.
	Records int64
	// FormatVersion is the header version byte written — a function of
	// the schema's content, never a flag.
	FormatVersion byte
	// Schema is the schema written, dictionaries final.
	Schema *encoding.Schema
	// Warnings are the non-fatal findings (description quality).
	Warnings []*errors.CodedError
	// Replaced reports that a cohort already existed at Target and was
	// atomically replaced (Overwrite).
	Replaced bool
}

// CohortBuild writes a single-file, ungrouped (format 0x01) cohort from
// typed rows appended one at a time. It is the row-at-a-time twin of an
// explicit-schema ImportJob and runs the same code for everything that
// decides bytes — schema description checks, dictionary assignment
// with the declared rung as a ceiling, decimal precision, set cells and
// the row layout (cell_encode.go) — so the cohort is byte-identical to
// an import of the same rows with the same schema.
//
// Build model: every accepted row is encoded straight to a spool file
// beside the target on the build's afero filesystem (dictionaries
// precede records on the wire, so the preamble can only be written
// once the last row is in). Close writes the preamble plus the spooled
// rows to a temp file beside the target, fsyncs it and renames it over
// the target; Abort, or any Close failure, removes the spool and the
// temp file and leaves the target untouched. A CohortBuild is not safe
// for concurrent use.
type CohortBuild struct {
	ctx    context.Context
	fs     afero.Fs
	target string
	opts   CohortBuildOptions
	schema *encoding.Schema
	warns  []*errors.CodedError

	cells   rowCells
	staged  []*stagedDict // per field; nil for a dictionary-less type
	scratch bytes.Buffer
	bitmap  []byte

	spool     afero.File
	spoolName string
	sw        *bufio.Writer
	ioErr     error // sticky: a spool write failed

	calls   int64 // Append calls, the 1-based row number of errors
	records int64
	done    bool
}

// NewCohortBuild validates target and schema and opens the spool.
//
// Refusals (SERVICE_VALIDATION unless noted): an empty target, an
// anchored target (`archive.pulse#shard.pulse`), a `.zst` transfer
// target, an existing target without Overwrite or a directory at the
// target; a malformed schema (no fields, empty or duplicate names, an
// unknown type, a bad decimal precision/scale, a pre-seeded dictionary
// longer than its rung, parent groups in the schema); an over-long
// description (PULSE_IMPORT_DESCRIPTION_TOO_LONG); and under Strict a
// low-quality description (PULSE_FIELD_DESCRIPTION_LOW_QUALITY).
func NewCohortBuild(ctx context.Context, fsys afero.Fs, target string, schema *encoding.Schema, opts CohortBuildOptions) (*CohortBuild, error) {
	if fsys == nil {
		return nil, errors.NewCodedError(errors.SERVICE_VALIDATION, "cohort build needs a filesystem")
	}
	if err := checkBuildTarget(fsys, target, opts.Overwrite); err != nil {
		return nil, err
	}
	written, warns, err := prepareBuildSchema(schema, opts.Strict)
	if err != nil {
		return nil, err
	}
	b := &CohortBuild{
		ctx:    ctx,
		fs:     fsys,
		target: target,
		opts:   opts,
		schema: written,
		warns:  warns,
		cells:  newRowCells(len(written.Fields)),
		staged: make([]*stagedDict, len(written.Fields)),
	}
	for i := range written.Fields {
		if d := written.Fields[i].Dictionary; d != nil {
			b.staged[i] = &stagedDict{dict: d}
		}
	}
	if written.HasBitmap() {
		b.bitmap = make([]byte, written.BitmapByteSize())
	}
	dir, base := filepath.Dir(target), filepath.Base(target)
	spool, err := afero.TempFile(fsys, dir, base+buildSpoolPattern)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_IO,
			fmt.Sprintf("creating build spool beside %s", target))
	}
	b.spool, b.spoolName = spool, spool.Name()
	b.sw = bufio.NewWriterSize(spool, buildIOBuffer)
	return b, nil
}

// checkBuildTarget refuses a target the builder cannot write.
func checkBuildTarget(fsys afero.Fs, target string, overwrite bool) error {
	invalid := func(reason, msg string) error {
		return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION, msg,
			map[string]any{"target": target, "reason": reason})
	}
	switch {
	case target == "":
		return invalid("empty_target", "cohort build target is empty")
	case strings.Contains(target, "#"):
		return invalid("anchored_target",
			"an anchored target (archive.pulse#shard.pulse) is not a single-file cohort path")
	case strings.HasSuffix(strings.ToLower(target), ".zst"):
		return invalid("compressed_target",
			"a .zst file is a transfer artifact, never a cohort: build the .pulse, then ExportTransfer it")
	}
	return checkTargetFree(fsys, target, overwrite)
}

// checkTargetFree refuses a directory at target, and an existing target
// unless overwrite.
func checkTargetFree(fsys afero.Fs, target string, overwrite bool) error {
	st, err := fsys.Stat(target)
	if err != nil {
		return nil // absent (or unreadable: the final rename reports it)
	}
	if st.IsDir() {
		return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			"cohort build target is a directory",
			map[string]any{"target": target, "reason": "target_is_directory"})
	}
	if !overwrite {
		return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			"cohort build target already exists; set Overwrite to replace it",
			map[string]any{"target": target, "reason": "target_exists"})
	}
	return nil
}

// Schema returns the schema the build writes (layout recomputed,
// dictionaries as grown so far).
func (b *CohortBuild) Schema() *encoding.Schema { return b.schema }

// Append converts row — one typed value per schema field, in field
// order, using the CohortRow type mapping — and spools it. A row that
// does not convert is rejected with PULSE_IMPORT_ROW_ERROR (details:
// row, field, reason ∈ arity / type / null / overflow) and leaves NO
// trace: nothing is spooled and no dictionary entry it introduced is
// kept, so the build continues as if the call never happened. A spool
// write failure is sticky: every later Append and Close returns it.
func (b *CohortBuild) Append(row []any) error {
	if b.done {
		return errors.NewCodedError(errors.SERVICE_RESOURCE, "cohort builder is closed")
	}
	if b.ioErr != nil {
		return b.ioErr
	}
	if err := b.ctx.Err(); err != nil {
		return err
	}
	b.calls++
	if err := b.convert(row); err != nil {
		b.rollback()
		return err
	}
	b.scratch.Reset()
	if err := b.cells.writeFields(&b.scratch, b.schema); err != nil {
		b.rollback()
		return err
	}
	if b.bitmap != nil {
		clear(b.bitmap)
		b.cells.fillBitmap(b.bitmap)
		b.scratch.Write(b.bitmap)
	}
	if _, err := b.sw.Write(b.scratch.Bytes()); err != nil {
		b.rollback()
		b.ioErr = errors.WrapCodedError(err, errors.ENCODING_IO,
			fmt.Sprintf("writing build spool for %s", b.target))
		return b.ioErr
	}
	for _, s := range b.staged {
		if s != nil {
			s.commit()
		}
	}
	b.records++
	return nil
}

func (b *CohortBuild) rollback() {
	for _, s := range b.staged {
		if s != nil {
			s.rollback()
		}
	}
}

// convert fills b.cells from row. Dictionary-bearing fields intern
// through their staged view; the caller commits or rolls back.
func (b *CohortBuild) convert(row []any) error {
	b.cells.reset()
	if len(row) != len(b.schema.Fields) {
		return b.rowError(nil, "arity",
			fmt.Sprintf("row has %d values, schema has %d fields", len(row), len(b.schema.Fields)), nil)
	}
	for i := range b.schema.Fields {
		f := &b.schema.Fields[i]
		v := row[i]
		if v == nil {
			if !f.Nullable {
				return b.rowError(f, "null", "null value in non-nullable field", nil)
			}
			b.cells.setNull(i, f.Type)
			continue
		}
		if err := b.convertCell(i, f, v); err != nil {
			return err
		}
	}
	return nil
}

// convertCell converts one present value under the CohortRow mapping:
// integers uint64, f32/f64 float32/float64, date int32 epoch days,
// datetime int64 epoch seconds, decimal128 encoding.Decimal128 (the
// mantissa at the field's scale), categorical string, set_* []string,
// packed_bool bool. No coercion: any other Go type is a type error.
func (b *CohortBuild) convertCell(i int, f *encoding.Field, v any) error {
	typeErr := func(want string) error {
		return b.rowError(f, "type", fmt.Sprintf("want %s for %s, got %T", want, f.Type, v),
			map[string]any{"expected": want, "got": fmt.Sprintf("%T", v)})
	}
	overflow := func(err error) error {
		return b.rowError(f, "overflow", err.Error(), nil)
	}
	switch {
	case f.Type == encoding.FieldTypeU4 || f.Type == encoding.FieldTypeU8 || f.Type == encoding.FieldTypeU16 ||
		f.Type == encoding.FieldTypeU32 || f.Type == encoding.FieldTypeU64:
		u, ok := v.(uint64)
		if !ok {
			return typeErr("uint64")
		}
		if u > intRungMax(f.Type) {
			return overflow(fmt.Errorf("value %d exceeds %s range (max %d)", u, f.Type, intRungMax(f.Type)))
		}
		b.cells.vals[i] = u
	case f.Type == encoding.FieldTypeF32:
		x, ok := v.(float32)
		if !ok {
			return typeErr("float32")
		}
		b.cells.vals[i] = uint64(math.Float32bits(x))
	case f.Type == encoding.FieldTypeF64:
		x, ok := v.(float64)
		if !ok {
			return typeErr("float64")
		}
		b.cells.vals[i] = math.Float64bits(x)
	case f.Type == encoding.FieldTypeDate:
		d, ok := v.(int32)
		if !ok {
			return typeErr("int32 (epoch days)")
		}
		b.cells.vals[i] = uint64(uint32(d))
	case f.Type == encoding.FieldTypeDateTime:
		s, ok := v.(int64)
		if !ok {
			return typeErr("int64 (epoch seconds)")
		}
		b.cells.vals[i] = uint64(s)
	case f.Type == encoding.FieldTypePackedBool:
		x, ok := v.(bool)
		if !ok {
			return typeErr("bool")
		}
		b.cells.vals[i] = 0
		if x {
			b.cells.vals[i] = 1
		}
	case f.Type == encoding.FieldTypeDecimal128:
		d, ok := v.(encoding.Decimal128)
		if !ok {
			return typeErr("encoding.Decimal128")
		}
		wb, err := decimalCell(d, f.Scale, *f, d.String(f.Scale))
		if err != nil {
			return overflow(err)
		}
		b.cells.setWide(i, wb)
	case f.Type.IsCategorical():
		s, ok := v.(string)
		if !ok {
			return typeErr("string")
		}
		id, err := internCategorical(b.staged[i], s, f.Type)
		if err != nil {
			return overflow(err)
		}
		b.cells.vals[i] = id
	case f.Type.IsSet():
		labels, ok := v.([]string)
		if !ok {
			return typeErr("[]string")
		}
		mask, err := internSetTokens(b.staged[i], labels, f.Type)
		if err != nil {
			return overflow(err)
		}
		if f.Type.IsWideSet() {
			wb, err := wideSetCell(mask, f.Type)
			if err != nil {
				return overflow(err)
			}
			b.cells.setWide(i, wb)
			return nil
		}
		w, err := narrowSetWord(mask, f.Type)
		if err != nil {
			return overflow(err)
		}
		b.cells.vals[i] = w
	default:
		return b.rowError(f, "type", fmt.Sprintf("unsupported field type %s", f.Type), nil)
	}
	return nil
}

// rowError is the PULSE_IMPORT_ROW_ERROR an import row failure carries,
// with the builder's row number (the 1-based Append call), the field
// and a reason.
func (b *CohortBuild) rowError(f *encoding.Field, reason, msg string, extra map[string]any) error {
	details := map[string]any{"row": b.calls, "reason": reason}
	text := fmt.Sprintf("row %d: %s", b.calls, msg)
	if f != nil {
		details["field"] = f.Name
		text = fmt.Sprintf("row %d, field %q: %s", b.calls, f.Name, msg)
	}
	for k, v := range extra {
		details[k] = v
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_IMPORT_ROW_ERROR, text, details)
}

// Close writes the cohort: the preamble (schema with the final
// dictionaries) then the spooled rows into a temp file beside the
// target, fsynced and renamed over it. Whatever the outcome the spool
// is removed and the build is finished; on failure the temp file is
// removed too and the target is untouched. Without Overwrite a target
// that appeared since NewCohortBuild is refused.
func (b *CohortBuild) Close() (*CohortBuildReport, error) {
	if b.done {
		return nil, errors.NewCodedError(errors.SERVICE_RESOURCE, "cohort builder is closed")
	}
	b.done = true
	defer b.dropSpool()
	if b.ioErr != nil {
		return nil, b.ioErr
	}
	if err := b.sw.Flush(); err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_IO,
			fmt.Sprintf("flushing build spool for %s", b.target))
	}
	if err := checkTargetFree(b.fs, b.target, b.opts.Overwrite); err != nil {
		return nil, err
	}
	replaced, _ := afero.Exists(b.fs, b.target)
	if _, err := b.spool.Seek(0, io.SeekStart); err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_IO, "rewinding build spool")
	}
	if err := b.publish(); err != nil {
		return nil, err
	}
	return &CohortBuildReport{
		Target:        b.target,
		Records:       b.records,
		FormatVersion: b.schema.RequiredFormatVersion(),
		Schema:        b.schema,
		Warnings:      b.warns,
		Replaced:      replaced,
	}, nil
}

// publish assembles preamble + spool into a temp file beside the
// target, fsyncs it and renames it over the target.
func (b *CohortBuild) publish() error {
	dir, base := filepath.Dir(b.target), filepath.Base(b.target)
	tmp, err := afero.TempFile(b.fs, dir, base+buildTempPattern)
	if err != nil {
		return errors.WrapCodedError(err, errors.ENCODING_IO,
			fmt.Sprintf("creating temp file for %s", b.target))
	}
	tmpName := tmp.Name()
	abort := func(e error) error {
		_ = tmp.Close()
		_ = b.fs.Remove(tmpName)
		return e
	}
	tw := bufio.NewWriterSize(tmp, buildIOBuffer)
	if err := encx.WritePreamble(tw, b.schema); err != nil {
		return abort(err)
	}
	if _, err := io.Copy(tw, ctxReader{ctx: b.ctx, r: b.spool}); err != nil {
		return abort(errors.WrapCodedError(err, errors.ENCODING_IO,
			fmt.Sprintf("writing cohort %s", b.target)))
	}
	if err := tw.Flush(); err != nil {
		return abort(errors.WrapCodedError(err, errors.ENCODING_IO,
			fmt.Sprintf("flushing cohort %s", b.target)))
	}
	// fsync BEFORE the rename, so the published name never points at
	// bytes a crash can still lose.
	if err := tmp.Sync(); err != nil {
		return abort(errors.WrapCodedError(err, errors.ENCODING_IO,
			fmt.Sprintf("syncing cohort %s", b.target)))
	}
	if err := tmp.Close(); err != nil {
		_ = b.fs.Remove(tmpName)
		return errors.WrapCodedError(err, errors.ENCODING_IO,
			fmt.Sprintf("closing cohort %s", b.target))
	}
	if err := b.ctx.Err(); err != nil {
		_ = b.fs.Remove(tmpName)
		return err
	}
	if err := b.fs.Rename(tmpName, b.target); err != nil {
		_ = b.fs.Remove(tmpName)
		return errors.WrapCodedError(err, errors.ENCODING_IO,
			fmt.Sprintf("renaming %s onto %s", tmpName, b.target))
	}
	return nil
}

// Abort discards the build: the spool is removed and the target is
// untouched. It is idempotent and a no-op after Close.
func (b *CohortBuild) Abort() error {
	if b.done {
		return nil
	}
	b.done = true
	return b.dropSpool()
}

func (b *CohortBuild) dropSpool() error {
	_ = b.spool.Close()
	if err := b.fs.Remove(b.spoolName); err != nil {
		if exists, _ := afero.Exists(b.fs, b.spoolName); exists {
			return errors.WrapCodedError(err, errors.ENCODING_IO,
				fmt.Sprintf("removing build spool %s", b.spoolName))
		}
	}
	return nil
}

// stagedDict interns labels against a field's dictionary without
// mutating it: a label already in the dictionary keeps its ID, a new
// one is assigned the next ID provisionally under the same rung ceiling
// Dictionary.AddWithLimit enforces. commit appends the provisional
// labels in assignment order — so the IDs match — once the whole row
// converted and spooled; rollback forgets them.
type stagedDict struct {
	dict    *encoding.Dictionary
	pending []string
	ids     map[string]uint32
}

func (s *stagedDict) AddWithLimit(label string, maxEntries uint32) (uint32, error) {
	if id, ok := s.dict.IDFor(label); ok {
		return id, nil
	}
	if id, ok := s.ids[label]; ok {
		return id, nil
	}
	n := uint64(s.dict.Count()) + uint64(len(s.pending))
	if n >= uint64(maxEntries) {
		return 0, errors.NewCodedErrorWithDetails(
			errors.PULSE_IMPORT_CATEGORICAL_OVERFLOW,
			fmt.Sprintf("categorical dictionary overflow: max %d entries", maxEntries),
			map[string]any{"max_entries": maxEntries, "value": label},
		)
	}
	if s.ids == nil {
		s.ids = map[string]uint32{}
	}
	id := uint32(n)
	s.pending = append(s.pending, label)
	s.ids[label] = id
	return id, nil
}

func (s *stagedDict) commit() {
	for _, l := range s.pending {
		_, _ = s.dict.Add(l)
	}
	s.rollback()
}

func (s *stagedDict) rollback() {
	s.pending = s.pending[:0]
	clear(s.ids)
}
