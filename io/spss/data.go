package spss

// The data section reader.
//
// A `.sav` data section is a flat run of 8-byte elements: case after case,
// and within a case, element after element in dictionary order. There is no
// per-case framing, no length prefix and no terminator — the only thing that
// says where one case ends and the next begins is the case stride the
// dictionary declares. That is why dictionary.elementCount, counted from the
// record type 2 stream, is load-bearing here and the header's
// nominal_case_size (a writer's claim) is not used at all.
//
// Only the uncompressed encoding is decoded today. Bytecode and ZSAV
// compression are E3; until they land, a compressed file is refused with
// PULSE_SPSS_COMPRESSION_UNSUPPORTED rather than read as though its command
// bytes were doubles, which would yield plausible-looking garbage.
//
// # Rendering to strings
//
// pio.Reader is a string-only contract, so every SPSS datum is rendered to a
// canonical string here. The rendering is driven by the schema mapping
// (mapping.go), not by the raw bytes alone, because a cell and the field type
// declared for it in PulseSchema must agree — a `date` field whose cells
// arrived as raw seconds-since-1582 would fail every row of an import:
//
//   - A plain numeric, a TIME / DTIME duration and any temporal column the
//     mapping had to drop to f64 render as the shortest decimal string that
//     round-trips back to the same float64.
//   - A `date` column renders as a "2006-01-02" literal, and a `datetime`
//     column as encoding.CanonicalDateTimeLayout — the exact layouts
//     encoding.ParseDate and encoding.ParseDateTime read back.
//   - A string is its declared-width bytes with trailing spaces removed,
//     reassembled across its 8-byte segments. It is NOT re-rendered from the
//     value label: the categorical dictionary the mapping builds holds the
//     source VALUE, so the cell and the dictionary entry are the same text.
//   - system-missing renders as the empty string — the house null token that
//     io/import.go's isNullToken recognises — so a sysmis datum is seen as a
//     null and never as a finite value of about -1.8e308.
//
// User-missing values are NOT treated as null here. They are ordinary data
// with a declared meaning, and collapsing them would destroy exactly the
// information the missing-value spec exists to carry.

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/io"
)

// recordData is the DetailSPSSRecord value for a fault in the data section.
// The section carries no record tag of its own, so it names itself.
const recordData = "data"

// dataColumn is the pre-resolved geometry of one column within a case.
//
// It is computed once per ReadRows pass rather than per case: the offsets are
// a pure function of the dictionary, and a file of a million cases would
// otherwise re-derive them a million times.
type dataColumn struct {
	// offset is the byte offset of the column's first element, measured
	// from the start of the case.
	offset int

	// span is the number of bytes the column occupies: 8 for a numeric,
	// segments*8 for a string.
	span int

	// width is 0 for a numeric variable, else the declared byte width.
	// A string's span rounds up to the segment boundary, so span and
	// width differ whenever the width is not a multiple of 8, and the
	// bytes between them are padding that must not reach the caller.
	width int

	// kind is the resolved schema mapping for the column, which selects
	// the cell rendering. buildDataPlan leaves it at the zero value
	// (kindNumeric, the raw shortest-round-tripping decimal); the
	// mapping pass overwrites it with the kind it settled on, so a cell
	// and the field type declared for it can never disagree.
	kind columnKind
}

// dataPlan is everything the per-case decode needs, resolved once.
type dataPlan struct {
	cols []dataColumn

	// stride is the byte width of one case: elementCount * 8.
	stride int

	// sysmis and sysmisBits are the system-missing sentinel in both
	// forms. The bit comparison is what makes the check exact for a
	// declared sentinel that is a NaN, where == is false against itself;
	// the value comparison covers a sentinel written with a different but
	// numerically equal encoding.
	sysmis     float64
	sysmisBits uint64

	// bo is the file's byte order, which governs the data section exactly
	// as it governs the dictionary.
	bo binary.ByteOrder
}

// buildDataPlan resolves the per-case geometry from the dictionary.
//
// It also bounds-checks that geometry against the case stride. Nothing in a
// well-formed file can fail that check — elementCount is counted from the same
// record stream the variable indices come from — but a hand-mutated file can
// declare a variable extending past the end of its own case, and a decode
// trusting that would index out of the case slice.
func buildDataPlan(d *dictionary) (*dataPlan, error) {
	stride := int(d.elementCount) * elementSize
	if stride <= 0 {
		return nil, dataError(errors.PULSE_SPSS_DICT_INVALID, d.dataOffset,
			"the dictionary declares %d element(s) per case; a case must hold at least one",
			d.elementCount)
	}

	cols := make([]dataColumn, len(d.vars))
	for i, v := range d.vars {
		off := (int(v.index) - 1) * elementSize
		span := v.segments * elementSize
		if off < 0 || span <= 0 || off+span > stride {
			return nil, dataError(errors.PULSE_SPSS_DICT_INVALID, d.dataOffset,
				"variable %q occupies bytes %d..%d of a case, but a case is only %d byte(s) wide",
				v.fieldName(), off, off+span, stride)
		}
		width := v.width
		if width > span {
			return nil, dataError(errors.PULSE_SPSS_DICT_INVALID, d.dataOffset,
				"variable %q declares width %d but occupies only %d byte(s)",
				v.fieldName(), width, span)
		}
		cols[i] = dataColumn{offset: off, span: span, width: width}
	}

	return &dataPlan{
		cols:       cols,
		stride:     stride,
		sysmis:     d.sysmis,
		sysmisBits: math.Float64bits(d.sysmis),
		bo:         d.byteOrder,
	}, nil
}

// decodeCase renders one case into row, which must have one slot per column.
func (p *dataPlan) decodeCase(c []byte, row []string) {
	for i := range p.cols {
		col := &p.cols[i]
		seg := c[col.offset : col.offset+col.span]
		if col.width > 0 {
			row[i] = trimStringDatum(seg[:col.width])
			continue
		}
		switch col.kind {
		case kindDate:
			row[i] = p.formatDate(seg)
		case kindDateTime:
			row[i] = p.formatDateTime(seg)
		default:
			row[i] = p.formatNumeric(seg)
		}
	}
}

// isSysmis reports whether a raw 8-byte element is the system-missing
// sentinel. Both comparisons are deliberate: the bit test is exact for a
// declared sentinel that is a NaN, where == is false against itself, and
// the value test covers a sentinel written with a different but
// numerically equal encoding.
func (p *dataPlan) isSysmis(bits uint64) bool {
	return bits == p.sysmisBits || math.Float64frombits(bits) == p.sysmis
}

// isSysmisValue is the isSysmis test for a value already decoded, used by
// the schema mapping when it checks a declared value label against the
// sentinel.
func (p *dataPlan) isSysmisValue(v float64) bool {
	return math.Float64bits(v) == p.sysmisBits || v == p.sysmis
}

// formatNumeric renders one 8-byte numeric element.
func (p *dataPlan) formatNumeric(seg []byte) string {
	bits := p.bo.Uint64(seg)
	if p.isSysmis(bits) {
		return ""
	}
	return formatNumericValue(math.Float64frombits(bits))
}

// formatNumericValue is the canonical numeric rendering: the shortest
// decimal string that parses back to the same float64. It is the one
// place the rule lives, so the schema mapping's dictionary keys and the
// cells ReadRows emits are the same text by construction.
//
// A non-finite datum that is not the system-missing sentinel renders
// "NaN" / "+Inf" / "-Inf" rather than a null token. That is deliberate:
// SPSS's missing state is the sentinel, so a bare NaN is data this reader
// has no licence to reinterpret, and strconv.ParseFloat reads all three
// back exactly, so an f64 column round-trips them.
func formatNumericValue(v float64) string {
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// formatDate renders a day-resolution temporal element as a date literal
// in encoding.DateFormats[0], the layout encoding.ParseDate reads back to
// the epoch day this mapping intended.
//
// The mapping only assigns kindDate to a column whose every value is a
// whole, finite, midnight-aligned second count at or after the Unix
// epoch, so the conversion here cannot lose anything. The guard is
// nonetheless real rather than an assertion: a plan built by hand, or a
// future caller setting kinds itself, must fall back to the lossless raw
// rendering rather than emit a fabricated calendar date.
func (p *dataPlan) formatDate(seg []byte) string {
	bits := p.bo.Uint64(seg)
	if p.isSysmis(bits) {
		return ""
	}
	sec, ok := spssSecondsExact(math.Float64frombits(bits))
	if !ok || sec < 0 {
		return p.formatNumeric(seg)
	}
	return time.Unix(sec, 0).UTC().Format(dateLayout)
}

// formatDateTime renders a temporal element as a datetime literal through
// encoding.FormatDateTime, the exact inverse of the encoding.ParseDateTime
// call io/import.go makes — so the instant survives the round trip,
// including before 1970.
func (p *dataPlan) formatDateTime(seg []byte) string {
	bits := p.bo.Uint64(seg)
	if p.isSysmis(bits) {
		return ""
	}
	sec, ok := spssSecondsExact(math.Float64frombits(bits))
	if !ok {
		return p.formatNumeric(seg)
	}
	return encoding.FormatDateTime(uint64(sec))
}

// trimStringDatum renders a string variable's declared-width bytes.
//
// SPSS space-pads a string value out to its declared width and then on out to
// the 8-byte segment boundary; the caller has already cut the segment padding
// away, and this removes the value padding. Trailing spaces are therefore not
// recoverable from a `.sav` — the format cannot distinguish "AB" from "AB  "
// — so trimming loses nothing that was ever there. Only spaces are trimmed,
// matching valueLabel.text, so a data value compares equal to the value-label
// key naming it.
func trimStringDatum(b []byte) string {
	return strings.TrimRight(string(b), " ")
}

// ---------------------------------------------------------------------------
// The pio.Reader surface
// ---------------------------------------------------------------------------

// ReadHeader returns one column name per SPSS variable, in file order.
//
// The name is the record 7/13 long name where the file declares one and the
// 8-byte short name otherwise — see variable.fieldName. String continuation
// records are not variables and contribute no column.
func (r *Reader) ReadHeader() ([]string, error) {
	d, err := r.loadDictionary()
	if err != nil {
		return nil, err
	}
	if r.header != nil {
		return r.header, nil
	}
	names := make([]string, len(d.vars))
	for i, v := range d.vars {
		names[i] = v.fieldName()
	}
	r.header = names
	return names, nil
}

// ReadRows streams the data section, calling fn once per case.
//
// The row slice handed to fn is REUSED between cases, matching the
// io/parquet adapter: a callback that needs to keep a row past its call must
// copy it. The strings themselves are immutable and safe to retain.
//
// ctx is checked before every case, so cancellation is observed within one
// case rather than at the end of the file. Returning pio.ErrStopIteration
// from fn ends the pass without an error, which is how the inference sampler
// stops after its sample window.
func (r *Reader) ReadRows(ctx context.Context, fn func(row []string) error) error {
	d, err := r.loadDictionary()
	if err != nil {
		return err
	}
	if _, err := r.ReadHeader(); err != nil {
		return err
	}
	// The mapping is what says how each column renders, so it is resolved
	// before the first cell rather than alongside it. It also subsumes the
	// compression check and the case geometry, and it is memoised, so a
	// second pass pays for neither.
	m, err := r.loadMapping()
	if err != nil {
		return err
	}
	plan := m.plan
	cases := m.cases

	// Each pass rebuilds its own diagnostics rather than appending to the
	// last pass's: an infer-then-import sequence reads the same file twice
	// and would otherwise report every warning twice.
	r.dataWarnings = nil

	start := d.dataOffset

	if declared, ok := declaredCaseCount(d); ok && declared != int64(cases) {
		r.dataWarnings = append(r.dataWarnings, caseCountMismatch(d, declared, cases))
	}

	row := make([]string, len(plan.cols))
	for i := 0; i < cases; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		off := start + i*plan.stride
		plan.decodeCase(r.data[off:off+plan.stride], row)

		if err := fn(row); err != nil {
			if err == pio.ErrStopIteration() {
				return nil
			}
			return err
		}
	}
	return nil
}

// Reset rewinds the reader to the start of the data section.
//
// The dictionary parse is deliberately kept: it is a pure function of bytes
// that have not changed, and re-walking it would make an infer-then-import
// sequence pay for the dictionary twice. What Reset drops is the derived
// per-pass state, so the next ReadRows starts from the first case with a
// clean diagnostics slate.
func (r *Reader) Reset() error {
	r.header = nil
	r.dataWarnings = nil
	return nil
}

// Warnings returns the non-fatal diagnostics raised so far, in the order
// they became knowable: the dictionary parse's own (unrecognised or
// malformed record type 7 extension subtypes), then the schema mapping's
// (a widened or downcast temporal column, a near-unique categorical, a
// value collision, a measurement level that disagrees with the mapped
// type), then the data section's (a case count disagreeing with the
// file's declaration).
//
// The first two channels are memoised with the parse and the mapping, and
// the third is rebuilt per pass, so nothing accumulates across repeated
// reads.
//
// It is a pure accessor and never triggers a parse: called before
// ReadHeader or ReadRows it returns nothing, because nothing has been read.
// The returned slice is freshly allocated, so a caller may retain it across
// a Reset that clears the reader's own.
func (r *Reader) Warnings() []*errors.CodedError {
	var out []*errors.CodedError
	if r.dict != nil {
		out = append(out, r.dict.warnings...)
	}
	if r.mapped != nil {
		out = append(out, r.mapped.warnings...)
	}
	out = append(out, r.dataWarnings...)
	return out
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// caseSpan returns the number of whole cases the data section holds,
// refusing a section that does not divide evenly into the case stride.
//
// It is the one place the case count is derived, so the mapping scan and
// the ReadRows pass can never disagree about how many cases there are.
func caseSpan(d *dictionary, data []byte, plan *dataPlan) (int, error) {
	end := len(data)
	start := d.dataOffset
	if start > end {
		// Unreachable for a parsed dictionary — the walk cannot end past
		// the buffer — but the arithmetic below would silently produce a
		// negative count, so it is checked rather than assumed.
		return 0, dataError(errors.PULSE_SPSS_DATA_TRUNCATED, end,
			"the dictionary ends at byte offset %d but the file is only %d byte(s) long",
			start, end)
	}
	avail := end - start
	if rem := avail % plan.stride; rem != 0 {
		return 0, dataError(errors.PULSE_SPSS_DATA_TRUNCATED, end-rem,
			"the data section holds %d byte(s), which is %d whole case(s) of %d byte(s) plus %d trailing byte(s)",
			avail, avail/plan.stride, plan.stride, rem)
	}
	return avail / plan.stride, nil
}

// checkCompression refuses a data section this reader cannot decode.
//
// The dictionary of a compressed file parses identically to an uncompressed
// one, so the flag is the ONLY thing separating a readable data section from
// a stream of command bytes. Reading the latter as doubles produces numbers,
// not an error, which is why this is a hard failure rather than a warning.
func checkCompression(d *dictionary) error {
	switch d.header.compression {
	case compressionNone:
		return nil
	case compressionBytecode:
		return dataError(errors.PULSE_SPSS_COMPRESSION_UNSUPPORTED, d.dataOffset,
			"the file uses SPSS bytecode compression (header compression flag %d), which this reader cannot yet decode; only the uncompressed encoding is read today",
			d.header.compression)
	case compressionZSAV:
		return dataError(errors.PULSE_SPSS_COMPRESSION_UNSUPPORTED, d.dataOffset,
			"the file uses ZSAV zlib block compression (header compression flag %d), which this reader cannot yet decode; only the uncompressed encoding is read today",
			d.header.compression)
	default:
		// The header parse rejects any other value, so this is
		// defence in depth rather than a reachable branch.
		return dataError(errors.PULSE_SPSS_COMPRESSION_UNSUPPORTED, d.dataOffset,
			"the file declares compression flag %d, which this reader does not recognise",
			d.header.compression)
	}
}

// declaredCaseCount returns the case count the file declares, and whether it
// declares one at all.
//
// The record 7/16 64-bit count wins where the file carries one: the header
// field is an int32 and therefore cannot express a file of more than 2^31-1
// cases, which is the entire reason 7/16 exists. A header count of -1 is the
// documented "the writer did not know" marker and is not a declaration.
func declaredCaseCount(d *dictionary) (int64, bool) {
	if d.hasCaseCount64 {
		return d.caseCount64, true
	}
	if d.header.caseCount < 0 {
		return 0, false
	}
	return int64(d.header.caseCount), true
}

// caseCountMismatch builds the warning for a declared count that disagrees
// with what the data section holds.
func caseCountMismatch(d *dictionary, declared int64, actual int) *errors.CodedError {
	source := "the file header"
	if d.hasCaseCount64 {
		source = "the record 7/16 64-bit case count"
	}
	ce := dataError(errors.PULSE_SPSS_DATA_CASE_COUNT_MISMATCH, d.dataOffset,
		"%s declares %d case(s) but the data section holds %d; every whole case present is read",
		source, declared, actual)
	ce.Details[errors.DetailSPSSDeclaredCases] = declared
	ce.Details[errors.DetailSPSSActualCases] = actual
	return ce
}

// dataError builds a coded data-section fault carrying the two details every
// PULSE_SPSS_* diagnostic names: the record it was reading and the byte
// offset it was reading at.
func dataError(code errors.Code, off int, format string, args ...any) *errors.CodedError {
	msg := "spss: data section: " + fmt.Sprintf(format, args...) +
		fmt.Sprintf(" [at byte offset %d (0x%X)]", off, off)
	return errors.NewCodedErrorWithDetails(code, msg, map[string]any{
		errors.DetailSPSSRecord: recordData,
		errors.DetailSPSSOffset: off,
	})
}

// The adapter contract this package satisfies. Reset is what lets the shared
// import path infer a schema from a sample and then re-read the same source
// for the row pass.
// SchemaAwareReader is the third: a `.sav` carries an authoritative
// dictionary, so the import path takes the schema off this reader instead
// of sampling rows and guessing. E2-S5 could not state this assertion from
// package io — asserting it there would import io/spss into its own
// parent — so it lives here, at the implementation.
var (
	_ pio.Reader            = (*Reader)(nil)
	_ pio.ResetReader       = (*Reader)(nil)
	_ pio.SchemaAwareReader = (*Reader)(nil)
)
