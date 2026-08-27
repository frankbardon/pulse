package spss

// The `.sav` DATA SECTION encoder: everything after the record type 999
// terminator, in either of the two encodings this writer emits.
//
// # It is the E3-S1 decoder, run backwards
//
// The command table, the bias arithmetic and the block framing are NOT
// restated here. bytecode.go owns them, in both directions: [commandValue]
// decodes an integer command and [valueCommand] encodes one, and they are
// each other's inverse by construction. This file calls the encode half and
// spells no command byte of its own — the constants cmdPad, cmdEOF, cmdRaw,
// cmdSpaces and cmdSysmis are the only names it uses, exactly as the decoder
// does. An encoder that disagreed with its decoder by one about the
// compressible range, or that rounded the bias differently, would be a silent
// corruption bug that round-tripping through our own codec need not catch —
// both halves would share the mistake and agree.
//
// The same reasoning is why [NewDataEncoder] PARSES the dictionary it is
// about to write cases behind, rather than reading the plan's own summary of
// it. The element kinds the command check needs, the byte order, the
// compression bias and — critically — the system-missing sentinel are all
// taken from [parseDictionary] and [buildDataPlan] over plan.Bytes, so the
// encoder's idea of what the file says is, by construction, the idea a reader
// will form. That is not a theoretical nicety: a sidecar-driven dictionary
// re-emits the source's record 7/4, and a source declaring a non-default
// sysmis has its declaration ADOPTED by any conforming reader (see
// applyMachineFloat) while [DictionaryPlan.Sysmis] still reports the spec
// default. Writing the plan's number would put a value into every null that
// reads back as data.
//
// # Bytecode is the default, and why
//
// SPSS's own SAVE writes compression flag 1, so almost every `.sav` in the
// world is bytecode-compressed and every tool that opens one is exercised on
// that path daily. It is lossless — the two encodings carry byte-identical
// case data — and it is smaller, because survey data is mostly small whole
// numbers that fit in one command byte instead of eight payload bytes.
// Uncompressed is available behind [WriterOptions.Uncompressed] for the cases
// where a human wants to read the bytes.
//
// ZSAV (flag 2) is NOT emitted; [BuildDictionary] refuses it, and this file
// never sees it.
//
// # Buffering and the two case counts
//
// The section is built in memory. A caller that does not know the case count
// up front passes Cases: -1 to [BuildDictionary] and calls [DataEncoder.Finish],
// which patches the count in. Patching goes through
// [DictionaryPlan.SetCaseCount] and never through two separate writes,
// because a `.sav` states its case count TWICE — the header's int32 ncases
// and the record 7/16 int64 — and patching one without the other leaves a
// file whose two counts disagree. Which of the two a reader believes is not
// something the file can settle.
//
// # Derived columns, and why nothing has to filter them out
//
// The encoder is driven by [DictionaryPlan.Columns] and by nothing else. A
// cohort field that no column names — every entry of
// [DictionaryPlan.UnboundFields], which for a sidecar-driven plan is exactly
// the derived `<var>_missing` siblings and `set_*` convenience columns — is
// still DECODED, because the record stride demands it, and is then simply not
// written anywhere. So a derived column can never leak out as an SPSS
// variable, and E5-S5's fold-back does not have to filter the case stream
// before the encoder sees it: it decides what a derived column MEANS for the
// variable it belongs to, which is a question about plan construction, not
// about this pass.

import (
	"bytes"
	"encoding/binary"
	"io"
	"math"
	"strconv"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// ---------------------------------------------------------------------------
// The case a value arrives in
// ---------------------------------------------------------------------------

// CaseValue is one cohort field's value in the raw form the encoder consumes:
// storage as the `.pulse` record holds it, with no rendering applied.
//
// It is raw rather than text because every mapping the encoder performs is
// defined on the stored value. A categorical's Num is its DICTIONARY ID, which
// is what indexes [ColumnPlan.Categories] to reach the SPSS code the file must
// carry; a rendered "1" would have to be looked up again, and a rendered
// label could not be looked up at all.
type CaseValue struct {
	// Num is the numeric reading of the field:
	//
	//   - u4 / u8 / u16 / u32 / u64 — the integer.
	//   - f32 / f64 — the float, widened exactly.
	//   - decimal128 — the value scaled by the field's Scale.
	//   - packed_bool — 0 or 1.
	//   - date — whole epoch DAYS, read unsigned, matching io/export.go.
	//   - datetime — whole epoch SECONDS, read SIGNED, so an instant before
	//     1970 stays before 1970 (the on-wire u64 is two's complement, which
	//     is exactly what encoding.FormatDateTime reinterprets).
	//   - categorical_* — the dictionary ID.
	//   - set_* — the mask, echoed as a float; see Mask.
	Num float64

	// Mask is a set_* column's bitmask at full width. Num echoes it, but a
	// set_u64 mask above 2^53 does not survive the float, and bit 63 of a
	// selection is data.
	Mask uint64

	// Null reports that the cohort's null bitmap marks this field missing for
	// this record. It is authoritative: Num and Mask are zero when it is set,
	// and carry no information.
	Null bool
}

// Case is one cohort record: one [CaseValue] per schema field, indexed by
// field index. It is index-keyed rather than name-keyed because
// [ColumnPlan.Field] is an index, so the encoder resolves a value with a
// slice load and never a map lookup on a per-case path.
type Case []CaseValue

// NewCase allocates a case buffer for a schema. Callers reuse ONE across
// every record; [DataEncoder.WriteCase] retains nothing.
func NewCase(s *encoding.Schema) Case { return make(Case, len(s.Fields)) }

// ---------------------------------------------------------------------------
// The encoder
// ---------------------------------------------------------------------------

// DataEncoder writes the data section of a `.sav`, one case at a time.
//
// It is single-threaded state: one encoder, one file, cases in order.
type DataEncoder struct {
	plan   *DictionaryPlan
	schema *encoding.Schema

	// bo, bias and sysmisBits are what a READER of plan.Bytes resolves,
	// not what the plan reports. See the file comment.
	bo         binary.ByteOrder
	bias       float64
	sysmisBits uint64

	// compressed selects the encoding. It mirrors plan.Compression, which
	// the header has already declared, so the two cannot disagree.
	compressed bool

	// kinds is one entry per 8-byte element of a case, from the same
	// buildDataPlan the decoder uses. The bytecode command check is defined
	// against it.
	kinds []elementKind

	// stride is the byte width of one case.
	stride int

	// caseBuf is the flat uncompressed form of the case being assembled. It
	// is the SAME buffer for both encodings: uncompressed appends it,
	// bytecode walks it element by element. That is what makes the two modes
	// carry identical data by construction rather than by agreement.
	caseBuf []byte

	// out is the emitted section, and bw the block framer that feeds it when
	// compressed.
	out []byte
	bw  bytecodeWriter

	cases    int64
	finished bool
}

// NewDataEncoder prepares a data-section encoder for an emitted dictionary.
//
// schema is the cohort schema the plan was built from; it is what a [Case] is
// indexed by, so a mismatch is caught here rather than as a wrong column.
func NewDataEncoder(plan *DictionaryPlan, schema *encoding.Schema) (*DataEncoder, error) {
	if plan == nil {
		return nil, errors.NewCodedError(errors.DATA_FILE,
			"spss.NewDataEncoder: no dictionary plan; the data section cannot be written without the dictionary it follows")
	}
	if schema == nil {
		return nil, errors.NewCodedError(errors.DATA_FILE,
			"spss.NewDataEncoder: no .pulse schema; the encoder resolves a case by field index and has nothing to index")
	}
	switch plan.Compression {
	case compressionNone, compressionBytecode:
	case compressionZSAV:
		return nil, errors.NewCodedError(errors.PULSE_SPSS_COMPRESSION_UNSUPPORTED,
			"spss.NewDataEncoder: ZSAV emission is out of scope; write an uncompressed or bytecode-compressed file instead")
	default:
		return nil, errors.NewCodedError(errors.PULSE_SPSS_COMPRESSION_INVALID,
			"spss.NewDataEncoder: the dictionary declares compression flag "+
				strconv.Itoa(int(plan.Compression))+", which is not one this writer emits")
	}

	// Read the dictionary back with the reader that will read the finished
	// file. Everything the encoding depends on comes from here.
	d, err := parseDictionary(plan.Bytes)
	if err != nil {
		return nil, err
	}
	dp, err := buildDataPlan(d)
	if err != nil {
		return nil, err
	}
	if want := int(plan.ElementCount) * elementSize; dp.stride != want {
		return nil, errors.NewCodedError(errors.PULSE_SPSS_DICT_INVALID,
			"spss.NewDataEncoder: the emitted dictionary declares a case of "+strconv.Itoa(dp.stride)+
				" byte(s) but the plan expects "+strconv.Itoa(want))
	}

	e := &DataEncoder{
		plan:       plan,
		schema:     schema,
		bo:         dp.bo,
		bias:       d.header.bias,
		sysmisBits: dp.sysmisBits,
		compressed: plan.Compression == compressionBytecode,
		kinds:      dp.elemKinds,
		stride:     dp.stride,
		caseBuf:    make([]byte, dp.stride),
	}
	if e.compressed && !usableBias(e.bias) {
		return nil, errors.NewCodedError(errors.PULSE_SPSS_COMPRESSION_INVALID,
			"spss.NewDataEncoder: the emitted dictionary declares a compression bias that is not a usable number; every integer command would decode to NaN")
	}
	if err := e.checkColumns(); err != nil {
		return nil, err
	}
	return e, nil
}

// checkColumns bounds every column against the case stride and against the
// schema, once, so the per-case path can index without checking.
func (e *DataEncoder) checkColumns() error {
	for i := range e.plan.Columns {
		col := &e.plan.Columns[i]
		if col.Encoding == EncodeUnbound || col.Field < 0 {
			return cannotWrite(col, "no cohort field backs it, so there is no value to write for it")
		}
		if col.Field >= len(e.schema.Fields) {
			return cannotWrite(col, "it names cohort field index "+strconv.Itoa(col.Field)+
				" but the schema has only "+strconv.Itoa(len(e.schema.Fields))+" field(s)")
		}
		at := (int(col.Index) - 1) * elementSize
		span := col.Elements * elementSize
		if col.Index < 1 || span <= 0 || at+span > e.stride {
			return cannotWrite(col, "it occupies bytes "+strconv.Itoa(at)+".."+strconv.Itoa(at+span)+
				" of a case that is only "+strconv.Itoa(e.stride)+" byte(s) wide")
		}
		if col.Width == 0 && col.Elements != 1 {
			return cannotWrite(col, "it is numeric but claims "+strconv.Itoa(col.Elements)+" element(s)")
		}
		if col.Width > 0 {
			content := 0
			for j := range col.Segments {
				content += col.Segments[j].Content
			}
			if content != col.Width {
				return cannotWrite(col, "its segments carry "+strconv.Itoa(content)+
					" byte(s) but it declares a width of "+strconv.Itoa(col.Width))
			}
		}
	}
	return nil
}

// Cases is how many cases have been written so far.
func (e *DataEncoder) Cases() int64 { return e.cases }

// WriteCase encodes one cohort record.
//
// c is indexed by cohort schema field, and is not retained.
func (e *DataEncoder) WriteCase(c Case) error {
	if e.finished {
		return errors.NewCodedError(errors.DATA_FILE,
			"spss.DataEncoder.WriteCase: the data section is already finished")
	}
	if len(c) != len(e.schema.Fields) {
		return errors.NewCodedError(errors.DATA_FILE,
			"spss.DataEncoder.WriteCase: the case carries "+strconv.Itoa(len(c))+
				" value(s) but the cohort schema declares "+strconv.Itoa(len(e.schema.Fields))+" field(s)")
	}

	for i := range e.caseBuf {
		e.caseBuf[i] = 0
	}
	for i := range e.plan.Columns {
		col := &e.plan.Columns[i]
		if err := e.writeColumn(col, c[col.Field]); err != nil {
			return err
		}
	}

	if e.compressed {
		e.emitCompressed()
	} else {
		e.out = append(e.out, e.caseBuf...)
	}
	e.cases++
	return nil
}

// writeColumn lays one variable's value into the flat case buffer.
func (e *DataEncoder) writeColumn(col *ColumnPlan, v CaseValue) error {
	switch col.Encoding {
	case EncodeNumeric:
		e.putNumeric(col, v, v.Num)

	case EncodeDateDays:
		// A `date` cell is whole epoch DAYS; SPSS counts SECONDS from its
		// own epoch. Both halves of the conversion are exact in a double
		// for every date the format can express.
		e.putNumeric(col, v, v.Num*encoding.SecondsPerDay+float64(spssEpochOffsetSeconds))

	case EncodeDateTimeSeconds:
		e.putNumeric(col, v, v.Num+float64(spssEpochOffsetSeconds))

	case EncodeCategoricalCode:
		if v.Null {
			e.putSysmis(col)
			return nil
		}
		entry, err := e.category(col, v.Num)
		if err != nil {
			return err
		}
		if !entry.Known {
			return cannotWrite(col, "the cohort holds dictionary ID "+strconv.FormatFloat(v.Num, 'g', -1, 64)+
				", for which no SPSS code was recorded; writing a code derived from the ID would re-point every reference to that category")
		}
		e.putNumber(col, entry.Code)

	case EncodeText:
		if v.Null {
			// A blank string IS the missing state of an SPSS string
			// variable — the format has no sentinel for one — and it reads
			// back as null, so the round trip is closed.
			return e.putText(col, nil, "")
		}
		entry, err := e.category(col, v.Num)
		if err != nil {
			return err
		}
		// Encoded, not Text. The value goes on the wire in the file's own
		// charset, and it was encoded once at plan time — see
		// CategoryCode.Encoded and charset_write.go.
		return e.putText(col, entry.Encoded, entry.Text)

	case EncodeSetMember:
		if v.Null {
			// Every member of a null set goes out system-missing. That is
			// what makes the null / empty-mask distinction survive: the
			// import reads a set as null exactly when NO constituent
			// carried a value, and as an empty mask when one did and
			// selected nothing.
			e.putSysmis(col)
			return nil
		}
		if col.SetBit < 0 || col.SetBit >= 64 {
			return cannotWrite(col, "it is a set member standing for bit "+strconv.Itoa(col.SetBit)+
				", which is not a bit a set_* mask has")
		}
		if v.Mask&(1<<uint(col.SetBit)) != 0 {
			e.putNumber(col, col.CountedValue)
		} else {
			e.putNumber(col, 0)
		}

	default:
		return cannotWrite(col, "its value encoding is "+col.Encoding.String()+", which the data encoder cannot write")
	}
	return nil
}

// category resolves a cohort dictionary ID to the SPSS value recorded for it.
func (e *DataEncoder) category(col *ColumnPlan, id float64) (CategoryCode, error) {
	if id != math.Trunc(id) || id < 0 || id >= float64(len(col.Categories)) {
		return CategoryCode{}, cannotWrite(col, "the cohort holds dictionary ID "+
			strconv.FormatFloat(id, 'g', -1, 64)+" but the plan records values for "+
			strconv.Itoa(len(col.Categories))+" ID(s); the value has no SPSS form")
	}
	entry := col.Categories[int(id)]
	if entry.Ambiguous {
		return CategoryCode{}, cannotWrite(col, "dictionary ID "+strconv.FormatFloat(id, 'g', -1, 64)+
			" is the collapse of two distinct source values (PULSE_SPSS_VALUE_COLLISION), so re-emitting either one would be a guess about which row meant which")
	}
	return entry, nil
}

// putNumeric writes a numeric variable's element, honouring the null bitmap.
func (e *DataEncoder) putNumeric(col *ColumnPlan, v CaseValue, out float64) {
	if v.Null {
		e.putSysmis(col)
		return
	}
	e.putNumber(col, out)
}

// putNumber writes one double at a column's element.
func (e *DataEncoder) putNumber(col *ColumnPlan, v float64) {
	at := (int(col.Index) - 1) * elementSize
	e.bo.PutUint64(e.caseBuf[at:at+elementSize], math.Float64bits(v))
}

// putSysmis writes the system-missing sentinel at a column's element.
func (e *DataEncoder) putSysmis(col *ColumnPlan) {
	at := (int(col.Index) - 1) * elementSize
	e.bo.PutUint64(e.caseBuf[at:at+elementSize], e.sysmisBits)
}

// putText lays an ENCODED string value out across a variable's physical
// segments. text is the same value as UTF-8, carried for the diagnostics.
//
// Each segment's region is space-filled first and the value's next Content
// bytes copied over the front of it. The bytes between a segment's content
// and its 8-byte round-up are padding a reader skips; SPSS writes spaces
// there, so this does too, which also lets an all-blank segment compress to
// one command byte.
//
// The bytes arrive already encoded, and that ordering is the whole of E5-S4
// on this path: a very long string is sliced on a fixed 252-byte stride, so
// a multi-byte character can straddle a segment boundary, and the reader
// joins the pieces before it decodes them (dataPlan.stringBytes). Segmenting
// the UTF-8 form and encoding each piece would encode a partial character.
// Encode whole, measure, then slice.
//
// The width check that remains is a bound, not a policy: applyCharsetWrite
// has already recomputed col.Width from these very bytes, so nothing that
// reaches here can overflow. It stays because the alternative to a refusal
// is a silent truncation, and a plan and an encoder that had drifted apart
// should say so rather than cut a value.
func (e *DataEncoder) putText(col *ColumnPlan, encoded []byte, text string) error {
	if len(encoded) > col.Width {
		return cannotWrite(col, "the cohort holds a value that is "+strconv.Itoa(len(encoded))+
			" byte(s) once encoded ("+strconv.Quote(text)+") for a variable declared "+strconv.Itoa(col.Width)+
			" byte(s) wide; truncating it would cut a value, and a multi-byte character with it")
	}
	rest := encoded
	for i := range col.Segments {
		sg := &col.Segments[i]
		at := (int(sg.Index) - 1) * elementSize
		region := e.caseBuf[at : at+sg.Elements*elementSize]
		for j := range region {
			region[j] = ' '
		}
		n := sg.Content
		if n > len(rest) {
			n = len(rest)
		}
		copy(region, rest[:n])
		rest = rest[n:]
	}
	if len(rest) > 0 {
		return cannotWrite(col, "its segments have no room for the last "+strconv.Itoa(len(rest))+
			" byte(s) of the value")
	}
	return nil
}

// ---------------------------------------------------------------------------
// Bytecode emission
// ---------------------------------------------------------------------------

// emitCompressed turns the assembled case into commands.
//
// One element, one command — the same one-to-one the decoder walks, in the
// same order, checked against the same [elementKind.allows] table.
func (e *DataEncoder) emitCompressed() {
	for elem := 0; elem*elementSize < e.stride; elem++ {
		at := elem * elementSize
		seg := e.caseBuf[at : at+elementSize]
		e.bw.put(e.command(e.kinds[elem], seg), seg)
	}
}

// command picks the cheapest command that reproduces seg EXACTLY.
//
// The integer arm is verified rather than trusted: [valueCommand] says the
// value is encodable, and the result is then decoded back through
// [commandValue] and compared BIT for bit. That check costs nothing per
// element and buys two things. It makes byte-identity between the two write
// modes a property of the code rather than of an argument about floating
// point under an unusual bias. And it keeps negative zero: -0.0 truncates to
// itself and encodes to the command for 0, which decodes to +0.0 — a
// different double — so the comparison sends it down the verbatim arm, where
// the sign survives.
func (e *DataEncoder) command(kind elementKind, seg []byte) byte {
	if kind == elemString {
		if bytes.Equal(seg, spacesElement[:]) {
			return cmdSpaces
		}
		return cmdRaw
	}
	bits := e.bo.Uint64(seg)
	if bits == e.sysmisBits {
		return cmdSysmis
	}
	if cmd, ok := valueCommand(math.Float64frombits(bits), e.bias); ok &&
		math.Float64bits(commandValue(cmd, e.bias)) == bits {
		return cmd
	}
	return cmdRaw
}

// bytecodeWriter frames commands into blocks: eight command bytes, then the
// payloads those eight asked for, in command order.
//
// It is the mirror of [bytecodeStream], which reads exactly this shape.
type bytecodeWriter struct {
	out []byte

	cmds [commandBlockSize]byte
	n    int

	// payload accumulates the verbatim values the current block's commands
	// named. They cannot be written as they are produced: they belong AFTER
	// all eight command bytes, and a command later in the block may still be
	// coming.
	payload []byte
}

// put appends one command. seg is the element it stands for and is copied
// only when the command is cmdRaw.
func (w *bytecodeWriter) put(cmd byte, seg []byte) {
	w.cmds[w.n] = cmd
	w.n++
	if cmd == cmdRaw {
		w.payload = append(w.payload, seg...)
	}
	if w.n == commandBlockSize {
		w.flush()
	}
}

// flush writes the pending block, padding it out with cmdPad. A writer
// filling the tail of the last block with 0 is exactly what "0 = ignore"
// is for.
func (w *bytecodeWriter) flush() {
	if w.n == 0 {
		return
	}
	for i := w.n; i < commandBlockSize; i++ {
		w.cmds[i] = cmdPad
	}
	w.out = append(w.out, w.cmds[:]...)
	w.out = append(w.out, w.payload...)
	w.payload = w.payload[:0]
	w.n = 0
}

// finish terminates the stream with cmdEOF and flushes the partial block.
//
// The terminator is written even though the decoder also accepts a stream
// that simply stops on a case boundary: 252 is what the format defines for
// the purpose, and a reader that trusts it stops without having to reason
// about whether the remaining bytes are a truncated block.
func (w *bytecodeWriter) finish() {
	w.put(cmdEOF, nil)
	w.flush()
}

// ---------------------------------------------------------------------------
// Finishing, and the two case counts
// ---------------------------------------------------------------------------

// Finish closes the data section and patches the case count into the
// dictionary.
//
// It returns the section's bytes; the complete file is plan.Bytes followed by
// them, in that order, and plan.Bytes has been mutated by the time this
// returns. Calling it twice is an error rather than a second flush.
func (e *DataEncoder) Finish() ([]byte, error) {
	if e.finished {
		return nil, errors.NewCodedError(errors.DATA_FILE,
			"spss.DataEncoder.Finish: the data section is already finished")
	}
	e.finished = true
	if e.compressed {
		e.bw.finish()
		e.out = append(e.out, e.bw.out...)
		e.bw.out = nil
	}
	if err := e.plan.SetCaseCount(e.cases); err != nil {
		return nil, err
	}
	return e.out, nil
}

// SetCaseCount patches the case count into an already-emitted dictionary.
//
// A `.sav` states its case count twice — the header's int32 ncases field and
// the record 7/16 int64 — so this writes BOTH or neither. There is
// deliberately no way to write one: a file whose two counts disagree is one
// no reader can adjudicate, and the whole reason a writer patches at all is
// that it did not know the number when it emitted the header.
//
// The 7/16 record is present only when [BuildDictionary] was given a
// non-negative Cases, because a record whose whole content is a case count
// cannot be emitted "to be filled in later" without also declaring a count.
// So a plan built with Cases: -1 carries the header field alone, and a count
// past the int32 ceiling lands there as -1 — the format's "unknown", which
// every reader answers by counting the cases it finds. That is honest, and it
// is the only shape available; a wrapped int32 would be a plausible wrong
// answer.
func (p *DictionaryPlan) SetCaseCount(n int64) error {
	if n < 0 {
		return errors.NewCodedError(errors.DATA_FILE,
			"spss: a case count of "+strconv.FormatInt(n, 10)+" cannot be written; -1 is declared by emitting no count, not by patching one in")
	}
	if p.CaseCountOffset < 0 || p.CaseCountOffset+4 > len(p.Bytes) {
		return errors.NewCodedError(errors.PULSE_SPSS_DICT_INVALID,
			"spss: the dictionary plan does not say where its header case count sits, so it cannot be patched")
	}
	if p.CaseCount64Offset >= 0 && p.CaseCount64Offset+8 > len(p.Bytes) {
		return errors.NewCodedError(errors.PULSE_SPSS_DICT_INVALID,
			"spss: the dictionary plan places its record 7/16 case count past the end of the dictionary")
	}

	p.ByteOrder.PutUint32(p.Bytes[p.CaseCountOffset:p.CaseCountOffset+4],
		uint32(headerCaseCount(n)))
	if p.CaseCount64Offset >= 0 {
		p.ByteOrder.PutUint64(p.Bytes[p.CaseCount64Offset:p.CaseCount64Offset+8], uint64(n))
	}
	p.CaseCount = n
	return nil
}

// ---------------------------------------------------------------------------
// Reading the cohort
// ---------------------------------------------------------------------------

// WriteCohort encodes every record of a `.pulse` record stream.
//
// r must be positioned at the first record — after [encoding.ReadHeader] and
// [encoding.ReadSchema] — and is read to EOF. A record that ends part way
// through is a truncated cohort and is reported rather than written as a
// short case.
func (e *DataEncoder) WriteCohort(r io.Reader) error {
	c := NewCase(e.schema)
	for {
		err := readCohortCase(r, e.schema, c)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if err := e.WriteCase(c); err != nil {
			return err
		}
	}
}

// readCohortCase decodes one `.pulse` record into c.
//
// It reads STORAGE, not text: a categorical yields its dictionary ID and a
// set_* its mask, which is what the plan is indexed by. io/export.go's row
// loop is the same walk with a rendering step where this one has none, and
// the field-order-then-bitmap layout is [encoding.Schema]'s, not this
// package's.
//
// It returns io.EOF, unwrapped, at a clean record boundary.
func readCohortCase(r io.Reader, s *encoding.Schema, c Case) error {
	for i := range s.Fields {
		f := &s.Fields[i]
		c[i] = CaseValue{}

		switch {
		case f.Type == encoding.FieldTypePackedBool:
			v, err := encoding.ReadBit(r, uint(f.BitPosition))
			if err != nil {
				return cohortReadError(err, i, len(s.Fields))
			}
			if v {
				c[i].Num = 1
			}

		case f.Type == encoding.FieldTypeU4:
			v, err := encoding.ReadNibble(r, f.BitPosition > 0)
			if err != nil {
				return cohortReadError(err, i, len(s.Fields))
			}
			c[i].Num = float64(v)

		case f.Type.IsDecimal():
			d, err := encoding.ReadDecimal128(r)
			if err != nil {
				return cohortReadError(err, i, len(s.Fields))
			}
			c[i].Num = d.Float64(f.Scale)

		default:
			raw, err := encoding.ReadFieldValue(r, f.Type)
			if err != nil {
				return cohortReadError(err, i, len(s.Fields))
			}
			c[i].Num = cohortNumber(f.Type, raw)
			if f.Type.IsSet() {
				c[i].Mask = raw
			}
		}
	}

	if bmSize := s.BitmapByteSize(); bmSize > 0 {
		bitmap, err := encoding.ReadBitmap(r, bmSize)
		if err != nil {
			return cohortReadError(err, len(s.Fields), len(s.Fields))
		}
		for i := range s.Fields {
			if s.Fields[i].Nullable && encoding.BitmapIsNull(bitmap, i) {
				c[i] = CaseValue{Null: true}
			}
		}
	}
	return nil
}

// cohortNumber is the numeric reading of a raw storage value. See
// [CaseValue.Num] for why date is unsigned and datetime signed.
func cohortNumber(ft encoding.FieldType, raw uint64) float64 {
	switch ft {
	case encoding.FieldTypeF32:
		return float64(math.Float32frombits(uint32(raw)))
	case encoding.FieldTypeF64:
		return math.Float64frombits(raw)
	case encoding.FieldTypeDateTime:
		return float64(int64(raw))
	default:
		return float64(raw)
	}
}

// cohortReadError turns a record-stream fault into either a clean EOF or a
// truncation diagnostic.
//
// EOF at field 0, before any byte of a record was consumed, is the end of the
// cohort. EOF anywhere else is a record that stops part way through, which is
// a damaged cohort and not a case to write.
func cohortReadError(err error, at, fields int) error {
	if !isCohortEOF(err) {
		return err
	}
	if at == 0 {
		return io.EOF
	}
	return errors.NewCodedError(errors.DATA_FILE,
		"spss: the .pulse cohort ends "+strconv.Itoa(at)+" field(s) into a record of "+
			strconv.Itoa(fields)+"; a partial record cannot be written as a case")
}

// isCohortEOF reports whether a record-stream fault is the stream running
// out. The `.pulse` value readers wrap their io errors in coded ones, and
// io.ErrUnexpectedEOF is what a short read of a fixed-width value produces,
// so neither an == comparison nor errors.Is against io.EOF alone would see
// every shape of "the cohort ended".
func isCohortEOF(err error) bool {
	for err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// cannotWrite names a variable a value could not be written for. It reuses
// PULSE_SPSS_EXPORT_UNSUPPORTED, the writer's "this cannot be expressed in a
// .sav" code, rather than degrading the value quietly.
func cannotWrite(col *ColumnPlan, why string) error {
	return errors.NewCodedErrorWithDetails(errors.PULSE_SPSS_EXPORT_UNSUPPORTED,
		"spss: the variable "+strconv.Quote(col.Name)+" cannot be written: "+why,
		map[string]any{errors.DetailSPSSVariable: col.Name})
}
