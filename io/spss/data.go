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
// All three encodings the format defines are decoded: uncompressed, SPSS's
// default bytecode compression (see bytecode.go for the command table), and
// ZSAV zlib block compression (see zsav.go, which inflates the blocks and
// then runs the SAME bytecode decoder over what comes out — ZSAV is two
// layers, not a third encoding). readCaseData is the one place the header
// compression flag is acted on. Whichever encoding the file used, everything
// below this point sees the same flat run of elements at the same fixed
// stride.
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
// # User-missing values, and why a case may yield more cells than variables
//
// A USER-missing datum — a `refused` / `don't know` / `not applicable`
// code a variable's record type 2 specification names — also renders as
// the empty string, so it too imports as null. Leaving the code in place
// as ordinary data would let AGG_SUM add 99999 for every refusal, which
// is a silently wrong answer rather than a visibly missing one.
//
// The REASON is not thrown away with it. Under the default missing mode
// each such variable contributes a SECOND cell to the row, its generated
// `<var>_missing` sibling, carrying the reason as text: "sysmis", the
// value label the file declared for that code, or the code itself. So a
// row is one cell per dataPlan.out slot, NOT one per variable, and
// nothing may assume the two counts are equal. See missing.go.

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"strconv"
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

	// name is the variable's Pulse field name, carried so a per-cell
	// fault — today only an undecodable string datum — can name the
	// variable it came from. The data section has no other coordinate: a
	// byte offset into a case says nothing a reader of the message could
	// act on.
	name string

	// missing is the variable's USER-missing specification compiled to a
	// predicate, nil when it declares none this reader acts on. Non-nil
	// only for a non-dictionary-bearing numeric column.
	//
	// It is on the plan and not consulted from the dictionary per cell
	// because deciding, for every datum of every case, which slots of a
	// signed-count missing specification are range bounds would put the
	// format's most easily misread field on the hot path.
	missing *missingTest

	// sibling is the generated `<var>_missing` reason column derived
	// from this variable, nil when it has none. The sibling has no
	// geometry of its own — it reads the SAME bytes this column does and
	// renders them as a reason instead of a value.
	sibling *missingSibling

	// pieces is the record 7/14 very-long-string reassembly plan: the
	// byte ranges within a case that together hold the logical value, in
	// order, with each physical segment's unused tail and its round-up
	// padding already excluded.
	//
	// It is nil for every column that is not a very long string, and the
	// nil case takes a path that is byte-identical to the pre-7/14 one —
	// a plain string still reads as one contiguous slice.
	pieces []dataPiece
}

// dataPiece is one contiguous run of a very long string's logical value
// within a case: the offset of a physical segment's first byte and how many
// of that segment's bytes belong to the value.
//
// length is NOT the segment's declared width. A non-final segment declares
// 255 bytes and carries 252; the other three are unused padding that must
// never reach the caller, and a further one to seven bytes of round-up
// padding sit past them. See longstring.go.
type dataPiece struct {
	offset int
	length int
}

// dataPlan is everything the per-case decode needs, resolved once.
type dataPlan struct {
	cols []dataColumn

	// out is the cohort's COLUMN LAYOUT: one entry per emitted cell, in
	// row order. It is not one entry per variable — a variable carrying
	// user-missing values contributes its own cell and its reason
	// sibling's.
	//
	// buildDataPlan leaves it nil, which decodeCase reads as the
	// identity layout (one cell per column, no siblings); the mapping
	// pass installs the real one. That keeps a plan assembled by hand in
	// a test working unchanged.
	out []outputSlot

	// mrSets are the derived multiple-dichotomy set_* columns, indexed by
	// outputSlot.mrIndex. Like a reason sibling, such a column has no
	// storage of its own: it is a second reading of its constituents'
	// bytes, which stay in the cohort under their own field names. See
	// mrset.go.
	mrSets []*mrSetColumn

	// stride is the byte width of one case: elementCount * 8.
	stride int

	// elemKinds says what the dictionary declares occupies each 8-byte
	// element position within a case, one entry per element rather than
	// one per variable. It is what the bytecode decoder checks each
	// command against, and it is indexed by element position because
	// that is the only coordinate a command stream has — the stream
	// knows nothing of variables.
	elemKinds []elementKind

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

	// cs decodes a string datum from the file's declared character
	// encoding into UTF-8. Never nil for a plan built by buildDataPlan;
	// a nil one falls back to the raw bytes, which is what a plan
	// assembled by hand in a test gets.
	cs *charsetDecoder

	// vlsBuf is the scratch buffer very-long-string reassembly joins
	// segments into. It is on the plan rather than per call because a
	// file of a million cases would otherwise allocate a million copies
	// of a value that is thrown away as soon as it has been decoded.
	//
	// It makes a plan single-pass state, so one plan must not decode two
	// cases concurrently. Nothing does: ReadRows and scanCases are both
	// sequential, and the mapping is memoised per reader.
	vlsBuf []byte
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
	kinds := make([]elementKind, d.elementCount)
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
		cols[i] = dataColumn{offset: off, span: span, width: width, name: v.fieldName()}

		// A very long string is several PHYSICAL variables laid end to
		// end inside the case. Each contributes only its content bytes;
		// the tail of a non-final segment and the round-up padding of
		// every segment are skipped, so the pieces do not tile the span.
		if v.vls != nil {
			at := off
			pieces := make([]dataPiece, 0, len(v.vls.segments))
			for _, sg := range v.vls.segments {
				pieces = append(pieces, dataPiece{offset: at, length: sg.content})
				at += sg.elements * elementSize
			}
			if at-off != span {
				return nil, dataError(errors.PULSE_SPSS_DICT_INVALID, d.dataOffset,
					"the very long string %q occupies %d byte(s) across its %d segment(s) but its variable claims %d",
					v.fieldName(), at-off, len(v.vls.segments), span)
			}
			cols[i].pieces = pieces
		}

		// Every element the variable occupies takes its kind. A
		// string's continuation elements are string segments in
		// their own right, which is exactly what the command check
		// needs to know about them.
		kind := elemNumeric
		if width > 0 {
			kind = elemString
		}
		for e := 0; e < v.segments; e++ {
			kinds[int(v.index)-1+e] = kind
		}
	}

	return &dataPlan{
		cols:       cols,
		stride:     stride,
		elemKinds:  kinds,
		sysmis:     d.sysmis,
		sysmisBits: math.Float64bits(d.sysmis),
		bo:         d.byteOrder,
		cs:         d.charset.dec,
	}, nil
}

// decodeCase renders one case into row, which must have one slot per column.
//
// It fails only on a string datum the file's declared character encoding
// cannot decode. That is a per-CELL fault rather than a per-file one — the
// bytes are only wrong for this one value — but it is still fatal to the
// read, because the alternative is a U+FFFD substitution that no later stage
// could tell from data. See charset.go.
func (p *dataPlan) decodeCase(c []byte, row []string) *errors.CodedError {
	for i, slot := range p.layout() {
		if slot.mrSet {
			cell, err := p.renderMRSet(p.mrSets[slot.mrIndex], c)
			if err != nil {
				return err
			}
			row[i] = cell
			continue
		}
		col := &p.cols[slot.col]
		if slot.sibling {
			row[i] = p.missingReason(col, c)
			continue
		}
		if col.width > 0 {
			text, err := p.decodeStringDatum(col, p.stringBytes(col, c))
			if err != nil {
				return err
			}
			row[i] = text
			continue
		}
		seg := c[col.offset : col.offset+col.span]
		// A USER-missing datum renders as the house null token under
		// every missing mode. The reason it is missing rides the sibling
		// column where one exists; what must never happen is that a
		// refusal code of 99999 reaches an f64 field and is summed.
		if col.missing != nil && !p.isSysmis(p.bo.Uint64(seg)) &&
			col.missing.match(math.Float64frombits(p.bo.Uint64(seg))) {
			row[i] = ""
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
	return nil
}

// layout returns the plan's column layout, defaulting to one cell per
// column for a plan the mapping pass has not installed one on.
func (p *dataPlan) layout() []outputSlot {
	if p.out != nil {
		return p.out
	}
	out := make([]outputSlot, len(p.cols))
	for i := range p.cols {
		out[i] = outputSlot{col: i, name: p.cols[i].name}
	}
	p.out = out
	return out
}

// missingReason renders one generated `<var>_missing` cell.
//
// It reads the SOURCE variable's bytes: a sibling has no storage of its
// own, it is a second reading of the same eight bytes. A present value
// renders as the empty string, which the import path reads as null — the
// empty reason IS the null bitmap bit, and materialising it as a
// dictionary entry would create an ID no record could reference.
//
// System-missing is tested BEFORE the user-missing predicate, and that
// order is load-bearing: SPSS spells an open-ended range with its LOWEST
// sentinel, which is the same double as the default sysmis sentinel, so
// the other order reports every sysmis datum as a user-missing one.
func (p *dataPlan) missingReason(col *dataColumn, c []byte) string {
	if col.sibling == nil {
		return ""
	}
	seg := c[col.offset : col.offset+col.span]
	bits := p.bo.Uint64(seg)
	if p.isSysmis(bits) {
		return SysmisReason
	}
	value := math.Float64frombits(bits)
	if !col.missing.match(value) {
		return ""
	}
	return col.sibling.reasonFor(value)
}

// stringBytes returns one string column's RAW wire bytes within a case: the
// declared-width run for a plain string, and the reassembled logical value
// for a record 7/14 very long string.
//
// Reassembly happens HERE, on raw bytes, and not after decoding, because a
// segment boundary falls at a fixed byte offset that knows nothing about
// character boundaries. Decode each segment separately and any multi-byte
// character straddling the boundary is destroyed — its leading bytes fail to
// decode at the end of one segment and its trailing bytes fail at the start
// of the next. Join first, decode once.
//
// The returned slice aliases either the case or the plan's scratch buffer.
// It is valid only until the next call.
func (p *dataPlan) stringBytes(col *dataColumn, c []byte) []byte {
	if len(col.pieces) == 0 {
		return c[col.offset : col.offset+col.width]
	}
	p.vlsBuf = p.vlsBuf[:0]
	for _, piece := range col.pieces {
		p.vlsBuf = append(p.vlsBuf, c[piece.offset:piece.offset+piece.length]...)
	}
	return p.vlsBuf
}

// decodeStringDatum renders one string datum: the declared-width bytes
// stripped of their padding, then decoded out of the file's charset.
//
// The trim happens FIRST, on raw bytes, because the declared width is a
// BYTE count and the padding SPSS writes is the byte 0x20 — both are
// statements about the wire form, and applying them after a decode that has
// changed the byte length would be applying them to the wrong string.
func (p *dataPlan) decodeStringDatum(col *dataColumn, b []byte) (string, *errors.CodedError) {
	raw := trimStringDatum(b)
	if p.cs == nil {
		return string(raw), nil
	}
	text, at := p.cs.decode(raw)
	if at >= 0 {
		return "", charsetInvalid(p.cs, "a data value of variable "+strconv.Quote(col.name),
			col.name, raw, at)
	}
	return text, nil
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

// trimStringDatum strips a string variable's padding, on the RAW bytes.
//
// SPSS space-pads a string value out to its declared width and then on out to
// the 8-byte segment boundary; the caller has already cut the segment padding
// away, and this removes the value padding. Trailing spaces are therefore not
// recoverable from a `.sav` — the format cannot distinguish "AB" from "AB  "
// — so trimming loses nothing that was ever there. Only spaces are trimmed,
// matching valueLabel.text, so a data value compares equal to the value-label
// key naming it.
//
// It returns bytes rather than a string, and it runs BEFORE the charset
// decode rather than after, because both the declared width and the 0x20
// padding are statements about the wire form. Applying them to a decoded
// string would be applying a byte-count rule to text whose byte length the
// decode has already changed.
func trimStringDatum(b []byte) []byte {
	return bytes.TrimRight(b, " ")
}

// ---------------------------------------------------------------------------
// The pio.Reader surface
// ---------------------------------------------------------------------------

// ReadHeader returns the cohort's column names, in file order.
//
// The name is the record 7/13 long name where the file declares one and the
// 8-byte short name otherwise — see variable.fieldName. String continuation
// records are not variables and contribute no column.
//
// It is NOT one name per SPSS variable. A numeric variable declaring
// user-missing values contributes a second, GENERATED column immediately
// after its own — `<var>_missing`, carrying why each value is missing —
// unless the reader was built with spss.WithMissingMode(spss.MissingNull).
// See missing.go. A multiple-DICHOTOMY response set contributes one more,
// a `set_*` convenience column named after the set (without its leading
// '$') placed after the last of its constituents — which are all still
// here, because that column is additive. See mrset.go.
//
// The set-planning warnings this call raises are discarded: they are
// memoised with the mapping, which raises the same ones, and Warnings
// reads them from there. A header read is not where a diagnostic about
// the mapping should first appear, and appending them here would double
// them for any caller that reads the header and then the rows.
func (r *Reader) ReadHeader() ([]string, error) {
	d, err := r.loadDictionary()
	if err != nil {
		return nil, err
	}
	if r.header != nil {
		return r.header, nil
	}
	// planOutputs reads the dictionary only, never the data section, so
	// ReadHeader stays a dictionary-cheap call even though the cohort's
	// columns are no longer one per variable. It is also the SAME
	// function the schema and the row decoder use, which is what
	// guarantees the names returned here are the fields those two
	// declare — a second derivation would silently disagree the first
	// time the two rules drifted.
	slots, _, _, err := planOutputs(d, r.opts)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(slots))
	for i, slot := range slots {
		names[i] = slot.name
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
	body := m.body

	// Each pass rebuilds its own diagnostics rather than appending to the
	// last pass's: an infer-then-import sequence reads the same file twice
	// and would otherwise report every warning twice.
	r.dataWarnings = nil

	if declared, ok := declaredCaseCount(d); ok && declared != int64(cases) {
		r.dataWarnings = append(r.dataWarnings, caseCountMismatch(d, declared, cases))
	}

	// One cell per LAYOUT slot, not per variable: a user-missing
	// variable contributes its own value and its reason sibling's.
	row := make([]string, len(plan.layout()))
	for i := 0; i < cases; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		off := i * plan.stride
		if err := plan.decodeCase(body[off:off+plan.stride], row); err != nil {
			return err
		}

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

// readCaseData resolves the data section into the flat case bytes everything
// downstream decodes, whatever encoding the file wrote it in.
//
// This is the ONE place the header compression flag is acted on, and it is a
// dispatch rather than a refusal because the flag is also the only thing
// separating a readable data section from a stream of command bytes: the
// dictionary of a compressed file parses identically to an uncompressed one,
// and reading command bytes as doubles produces numbers rather than an
// error. Getting the branch wrong is therefore silent, which is why there is
// exactly one branch point.
//
// The uncompressed case returns a SUBSLICE of the file bytes and copies
// nothing — that encoding already is the flat form. A compressed case
// materialises the expansion; see decodeBytecode for why it is materialised
// rather than streamed.
func readCaseData(d *dictionary, data []byte, plan *dataPlan) ([]byte, int, error) {
	switch d.header.compression {
	case compressionNone:
		cases, err := caseSpan(d, data, plan)
		if err != nil {
			return nil, 0, err
		}
		return data[d.dataOffset : d.dataOffset+cases*plan.stride], cases, nil
	case compressionBytecode:
		return decodeBytecode(d, data, plan)
	case compressionZSAV:
		// ZSAV is TWO layers, not one: the zlib blocks inflate to a
		// bytecode command stream, which is then decoded exactly as a
		// flag-1 file's would be. decodeZSAV does the inflation and
		// hands off; see zsav.go, where the nesting is spelled out,
		// because a reader that assumed one layer would read command
		// bytes as doubles and produce numbers from every file.
		return decodeZSAV(d, data, plan)
	default:
		// The header parse rejects any value outside 0..2, so this is
		// defence in depth rather than a reachable branch. It is kept
		// as the named refusal for a data-section encoding this
		// reader cannot decode: all three the format defines are read
		// today, so reaching it means a flag the format does not
		// define got past the header check.
		return nil, 0, dataError(errors.PULSE_SPSS_COMPRESSION_UNSUPPORTED, d.dataOffset,
			"the file declares compression flag %d, which this reader does not recognise; the format defines 0 (uncompressed), 1 (bytecode) and 2 (ZSAV zlib blocks), and all three are read",
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
