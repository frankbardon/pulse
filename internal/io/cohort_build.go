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
	buildPhysPattern  = ".build-phys-*.tmp"
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
	// Groups declares parent groups exactly as ImportJob.Groups does:
	// the PULSE_GROUP_* declaration checks and the viability gate's
	// width screen run in NewCohortBuild, the encode and the ratio
	// assessment in Close. Strict is the gate's strictness too (as
	// `--strict` is for import).
	Groups []GroupDecl
	// ElideConstants stores every field holding one value on every row
	// once, as ImportJob.ElideConstants does — decided by a full pass
	// over the spooled rows at Close.
	ElideConstants bool
	// RatioFloor is the viability gate's rows-per-tuple floor
	// (ImportJob.DedupRatioFloor); 0 selects the default.
	RatioFloor float64
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
	// Warnings are the non-fatal findings: description quality, then
	// the viability gate's (PULSE_GROUP_TOO_NARROW from the width
	// screen, then PULSE_DEDUP_LOW_RATIO from the ratio assessment).
	Warnings []*errors.CodedError
	// Groups describes every declared group, in declaration order, as
	// ImportReport.Groups does. Nil when none was declared.
	Groups []GroupReport
	// ElidedConstants names the fields ElideConstants stored once.
	ElidedConstants []string
	// Replaced reports that a cohort already existed at Target and was
	// atomically replaced (Overwrite).
	Replaced bool
}

// CohortBuild writes a single-file cohort from typed rows appended one
// at a time — ungrouped (format 0x01) unless a declared group survives
// the viability gate or constant elision forms a group (0x02). It is the row-at-a-time twin of an
// explicit-schema ImportJob and runs the same code for everything that
// decides bytes — schema description checks, dictionary assignment
// with the declared rung as a ceiling, decimal precision, set cells and
// the row layout (cell_encode.go) — so the cohort is byte-identical to
// an import of the same rows with the same schema.
//
// Build model: every accepted row is encoded straight to a spool file
// beside the target on the build's afero filesystem (dictionaries
// precede records on the wire, so the preamble can only be written
// once the last row is in). The spool holds LOGICAL rows. With groups
// or elision, Close first runs import's full pass over it — the
// ConstantDetector (elision), then the GroupEncoder into a second,
// physical spool, then the gate's ratio assessment, all before
// anything is published. Close then writes the preamble plus the
// (logical or physical) spooled rows to a temp file beside the target,
// fsyncs it and renames it over the target; Abort, or any Close failure, removes the spool and the
// temp file and leaves the target untouched. A CohortBuild is not safe
// for concurrent use.
type CohortBuild struct {
	ctx    context.Context
	fs     afero.Fs
	target string
	opts   CohortBuildOptions
	schema *encoding.Schema
	warns  []*errors.CodedError

	// Declared groups: the specs the width screen admitted, its views
	// (one per declaration) and its warnings.
	specs      []encx.GroupSpec
	screen     []encx.GroupViability
	groupWarns []*errors.CodedError
	rejected   []RowError // rejected Append calls, for encode-error row mapping

	cells   rowCells
	staged  []*stagedDict // per field; nil for a dictionary-less type
	scratch bytes.Buffer
	bitmap  []byte

	spool     afero.File
	spoolName string
	phys      afero.File // the physical (grouped) spool, while one exists
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
// description (PULSE_IMPORT_DESCRIPTION_TOO_LONG); under Strict a
// low-quality description (PULSE_FIELD_DESCRIPTION_LOW_QUALITY); a bad
// group declaration (PULSE_GROUP_*, from the same encoder check import
// runs); and under Strict a too-narrow group (PULSE_GROUP_TOO_NARROW).
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
	var (
		specs      []encx.GroupSpec
		screen     []encx.GroupViability
		groupWarns []*errors.CodedError
	)
	if declared := groupSpecs(opts.Groups); len(declared) > 0 {
		// Import's pre-pass: the full declaration check, then the
		// schema-only width screen. An explicit schema never widens,
		// so this verdict is final (no strict re-screen).
		if _, err := encx.NewGroupEncoder(written, declared); err != nil {
			return nil, err
		}
		if specs, screen, groupWarns, err = buildGate(opts).ScreenWidths(written, declared); err != nil {
			return nil, err
		}
	}
	b := &CohortBuild{
		specs:      specs,
		screen:     screen,
		groupWarns: groupWarns,
		ctx:        ctx,
		fs:         fsys,
		target:     target,
		opts:       opts,
		schema:     written,
		warns:      warns,
		cells:      newRowCells(len(written.Fields)),
		staged:     make([]*stagedDict, len(written.Fields)),
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
		b.rejected = append(b.rejected, RowError{Row: int(b.calls)})
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
	lay, err := b.finalLayout()
	if err != nil {
		return nil, err
	}
	if err := b.publish(lay.schema, lay.payload); err != nil {
		return nil, err
	}
	rep := &CohortBuildReport{
		Target:          b.target,
		Records:         b.records,
		FormatVersion:   lay.schema.RequiredFormatVersion(),
		Schema:          lay.schema,
		Warnings:        append(append(append([]*errors.CodedError(nil), b.warns...), b.groupWarns...), lay.ratioWarns...),
		ElidedConstants: lay.elided,
		Replaced:        replaced,
	}
	rep.Groups = groupReports(lay.schema, b.opts.Groups, b.screen, lay.ratio)
	return rep, nil
}

// buildGate is the viability policy the options select.
func buildGate(opts CohortBuildOptions) encx.DedupGate {
	return encx.DedupGate{RatioFloor: opts.RatioFloor, Strict: opts.Strict}
}

// buildLayout is what Close publishes: the schema to write, the
// record payload to copy after it, and the full pass's findings.
type buildLayout struct {
	schema     *encoding.Schema
	payload    io.Reader
	elided     []string
	ratio      []encx.GroupViability
	ratioWarns []*errors.CodedError
}

// finalLayout decides the cohort's layout from every spooled row, as
// import's writeCohortPayload does from its buffered rows: with
// ElideConstants a ConstantDetector observes every logical row and the
// plan's constant group follows the admitted declared groups (whose
// members are reserved from elision); with any group at all the
// logical rows are encoded through one GroupEncoder into a physical
// spool and the declared groups' ratios are assessed — a Strict
// finding fails here, before anything is published. With no group the
// logical spool is the payload (0x01).
func (b *CohortBuild) finalLayout() (*buildLayout, error) {
	rewind := func() error {
		if _, err := b.spool.Seek(0, io.SeekStart); err != nil {
			return errors.WrapCodedError(err, errors.ENCODING_IO, "rewinding build spool")
		}
		return nil
	}
	if err := rewind(); err != nil {
		return nil, err
	}
	lay := &buildLayout{schema: b.schema, payload: b.spool}
	specs := append([]encx.GroupSpec(nil), b.specs...)
	if b.opts.ElideConstants {
		det, err := encx.NewConstantDetector(b.schema)
		if err != nil {
			return nil, err
		}
		in := ctxReader{ctx: b.ctx, r: bufio.NewReaderSize(b.spool, buildIOBuffer)}
		if _, err := encx.ForEachRecord(in, det.Stride(), det.Observe); err != nil {
			return nil, err
		}
		plan, err := encx.PlanConstantElision(det, groupMemberNames(b.specs))
		if err != nil {
			return nil, err
		}
		if plan.Spec != nil {
			lay.elided = plan.Fields
			specs = append(specs, *plan.Spec)
		}
		if err := rewind(); err != nil {
			return nil, err
		}
	}
	if len(specs) == 0 {
		return lay, nil
	}
	enc, err := encx.NewGroupEncoder(b.schema, specs)
	if err != nil {
		return nil, err
	}
	dir, base := filepath.Dir(b.target), filepath.Base(b.target)
	phys, err := afero.TempFile(b.fs, dir, base+buildPhysPattern)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_IO,
			fmt.Sprintf("creating physical spool beside %s", b.target))
	}
	b.phys = phys
	pw := bufio.NewWriterSize(phys, buildIOBuffer)
	in := ctxReader{ctx: b.ctx, r: bufio.NewReaderSize(b.spool, buildIOBuffer)}
	if _, err := enc.EncodeStream(pw, in); err != nil {
		return nil, withSourceRow(err, b.rejected)
	}
	if err := pw.Flush(); err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_IO,
			fmt.Sprintf("flushing physical spool for %s", b.target))
	}
	lay.schema = enc.Schema()
	// The ratio floor, measured over the dictionaries the full pass
	// built, before anything reaches the target (as import does).
	if lay.ratio, lay.ratioWarns, err = buildGate(b.opts).AssessRatios(lay.schema, b.specs, b.records); err != nil {
		return nil, err
	}
	if _, err := phys.Seek(0, io.SeekStart); err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_IO, "rewinding physical spool")
	}
	lay.payload = phys
	return lay, nil
}

// publish assembles preamble(schema) + payload into a temp file beside
// the target, fsyncs it and renames it over the target.
func (b *CohortBuild) publish(schema *encoding.Schema, payload io.Reader) error {
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
	if err := encx.WritePreamble(tw, schema); err != nil {
		return abort(err)
	}
	if _, err := io.Copy(tw, ctxReader{ctx: b.ctx, r: payload}); err != nil {
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
	if b.phys != nil {
		name := b.phys.Name()
		_ = b.phys.Close()
		b.phys = nil
		if err := b.fs.Remove(name); err != nil {
			if exists, _ := afero.Exists(b.fs, name); exists {
				return errors.WrapCodedError(err, errors.ENCODING_IO,
					fmt.Sprintf("removing build spool %s", name))
			}
		}
	}
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
