package encoding

import (
	"encoding/binary"
	"io"
	"strconv"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// UnionRowPlan rewrites one union member's LOGICAL records (the
// member's schema read through Logical / NewLogicalStream) into the
// layout of the merged union schema (MergeUnionSchema's fold plus the
// virtual source field appended last), so the merged schema's
// RecordReader decodes every member as the rows of one cohort — the
// record half of the shard-archive merge a union applies in memory:
//
//   - a categorical's index moves to its value's index in the merged
//     dictionary (the archive's insert-time RewriteShardCategoricals);
//   - a set's mask moves bit by bit to the merged dictionary's entries
//     and is written at the merged rung (the archive's set widening);
//   - every other field is copied byte for byte;
//   - the null bitmap carries each member field's bit to the same field
//     index, and a null field keeps its placeholder bytes (only a set
//     rung change rewrites a null's slot, as an all-zero mask);
//   - the source field holds the member's index in the label
//     dictionary and is never null.
//
// The plan is a pure function of the two schemas and the member index,
// built once per member and shared read-only by every scan.
type UnionRowPlan struct {
	srcStride, srcBody int
	dstStride, dstBody int
	srcBM, dstBM       int // bitmap byte sizes; 0 = no bitmap
	fields             []unionFieldOp
	srcField           int // source field index in the merged schema
	srcType            encoding.FieldType
	srcValue           uint32
	srcOff             int
}

// unionFieldOp is one member field's move into the merged row.
type unionFieldOp struct {
	field              int
	srcOff, dstOff     int
	from, to           encoding.FieldType
	cat                []uint32 // categorical index remap by member index (nil: identity)
	set                []uint32 // set bit remap by member bit (nil: identity)
	setRung            bool     // the set's rung differs, so the slot is re-encoded
	srcNullable        bool
	dstWidth, srcWidth int
}

// NewUnionRowPlan builds the plan moving member's logical records into
// merged's layout; memberIndex is the member's position in the union
// (its source field value). merged must be member's fields, in order,
// plus one trailing categorical source field — what MergeUnion returns
// — else ENCODING_INTERNAL: a plan over schemas the union merge did not
// produce is a programming error, never a data fault.
func NewUnionRowPlan(member, merged *encoding.Schema, memberIndex int) (*UnionRowPlan, error) {
	if member == nil || merged == nil {
		return nil, errors.NewCodedError(errors.ENCODING_INTERNAL, "union row plan requires both schemas")
	}
	m := member.Logical()
	n := len(m.Fields)
	if len(merged.Fields) != n+1 || !merged.Fields[n].Type.IsCategorical() {
		return nil, errors.NewCodedErrorWithDetails(errors.ENCODING_INTERNAL,
			"union row plan: the merged schema is not the member's fields plus one source field",
			map[string]any{"member_fields": n, "merged_fields": len(merged.Fields)})
	}
	if uint32(memberIndex) >= merged.Fields[n].Type.MaxDictEntries() {
		return nil, errors.NewCodedErrorWithDetails(errors.ENCODING_INTERNAL,
			"union row plan: member index exceeds the source field's width",
			map[string]any{"member": memberIndex, "type": merged.Fields[n].Type.String()})
	}
	p := &UnionRowPlan{srcField: n, srcType: merged.Fields[n].Type, srcValue: uint32(memberIndex)}
	srcOffs, srcBody := widenFieldOffsets(m)
	dstOffs, dstBody := widenFieldOffsets(merged)
	p.srcBody, p.dstBody = srcBody, dstBody
	p.srcBM, p.dstBM = m.BitmapByteSize(), merged.BitmapByteSize()
	p.srcStride, p.dstStride = srcBody+p.srcBM, dstBody+p.dstBM
	p.srcOff = dstOffs[n]
	for i := 0; i < n; i++ {
		sf, df := &m.Fields[i], &merged.Fields[i]
		if sf.Name != df.Name {
			return nil, errors.NewCodedErrorWithDetails(errors.ENCODING_INTERNAL,
				"union row plan: field "+strconv.Itoa(i)+" differs by name",
				map[string]any{"field_index": i, "member": sf.Name, "merged": df.Name})
		}
		op := unionFieldOp{
			field: i, srcOff: srcOffs[i], dstOff: dstOffs[i],
			from: sf.Type, to: df.Type, srcNullable: sf.Nullable,
			srcWidth: m.FieldWidth(i), dstWidth: merged.FieldWidth(i),
		}
		switch {
		case sf.Type.IsSet():
			if !df.Type.IsSet() {
				return nil, unionPlanTypeError(i, sf, df)
			}
			op.set = dictRemapTable(sf.Dictionary, df.Dictionary)
			op.setRung = sf.Type != df.Type
		case sf.Type.IsCategorical():
			if sf.Type != df.Type {
				return nil, unionPlanTypeError(i, sf, df)
			}
			op.cat = dictRemapTable(sf.Dictionary, df.Dictionary)
		default:
			if sf.Type != df.Type || op.srcWidth != op.dstWidth {
				return nil, unionPlanTypeError(i, sf, df)
			}
		}
		p.fields = append(p.fields, op)
	}
	return p, nil
}

func unionPlanTypeError(i int, sf, df *encoding.Field) error {
	return errors.NewCodedErrorWithDetails(errors.ENCODING_INTERNAL,
		"union row plan: field "+strconv.Quote(sf.Name)+" cannot move from "+sf.Type.String()+" to "+df.Type.String(),
		map[string]any{"field_index": i, "from": sf.Type.String(), "to": df.Type.String()})
}

// dictRemapTable maps each member dictionary index to the index of the
// same value in the merged dictionary; nil when every index maps to
// itself (the member's dictionary is a prefix of the merged one).
func dictRemapTable(member, merged *encoding.Dictionary) []uint32 {
	if member == nil || member.Count() == 0 {
		return nil
	}
	out := make([]uint32, member.Count())
	identity := true
	for i, v := range member.Values() {
		id, ok := uint32(i), false
		if merged != nil {
			id, ok = merged.IDFor(v)
		}
		if !ok {
			id = uint32(i)
		}
		out[i] = id
		identity = identity && id == uint32(i)
	}
	if identity {
		return nil
	}
	return out
}

// SourceStride is the byte length of one member logical record.
func (p *UnionRowPlan) SourceStride() int { return p.srcStride }

// Stride is the byte length of one merged record.
func (p *UnionRowPlan) Stride() int { return p.dstStride }

// Transcode writes the merged record for the member record src into
// dst (len(dst) >= Stride(), len(src) >= SourceStride()). dst is fully
// overwritten.
func (p *UnionRowPlan) Transcode(dst, src []byte) error {
	dst = dst[:p.dstStride]
	clear(dst)
	srcBM := src[p.srcBody : p.srcBody+p.srcBM]
	dstBM := dst[p.dstBody : p.dstBody+p.dstBM]
	for k := range p.fields {
		op := &p.fields[k]
		null := op.srcNullable && encoding.BitmapIsNull(srcBM, op.field)
		if null {
			encoding.BitmapSetNull(dstBM, op.field)
		}
		in := src[op.srcOff : op.srcOff+op.srcWidth]
		out := dst[op.dstOff : op.dstOff+op.dstWidth]
		switch {
		case op.from.IsSet() && (op.set != nil || op.setRung):
			if null {
				if !op.setRung {
					copy(out, in)
				}
				continue
			}
			mask, err := readSetMaskSlot(in, op.from)
			if err != nil {
				return err
			}
			if op.set != nil {
				mask, err = remapSetBits(mask, op.set, op.to.MaxSetEntries())
				if err != nil {
					return err
				}
			}
			if err := writeSetMaskSlot(out, op.to, mask); err != nil {
				return err
			}
		case op.cat != nil && !null:
			if err := remapCategorical(out, in, op.from, op.cat); err != nil {
				return err
			}
		default:
			copy(out, in)
		}
	}
	return putCategoricalIndex(dst[p.srcOff:], p.srcType, p.srcValue)
}

// remapSetBits moves every set bit i of mask to bit table[i] (i itself
// past the table — a bit with no dictionary entry is kept, as the
// archive rewrite keeps it).
func remapSetBits(mask encoding.SetMask, table []uint32, width uint32) (encoding.SetMask, error) {
	var out encoding.SetMask
	for bit, ok := mask.NextBit(0); ok; bit, ok = mask.NextBit(bit + 1) {
		nb := uint32(bit)
		if bit < len(table) {
			nb = table[bit]
		}
		if nb >= width {
			return encoding.SetMask{}, errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_DICT_WIDTH_OVERFLOW,
				"union row: remapped set bit position exceeds field width",
				map[string]any{"width": width, "new_bit": nb, "old_bit": bit})
		}
		out = out.WithBit(int(nb))
	}
	return out, nil
}

// remapCategorical writes in's categorical index, remapped through
// table (an index past the table is kept), into out.
func remapCategorical(out, in []byte, ft encoding.FieldType, table []uint32) error {
	var old uint32
	switch ft {
	case encoding.FieldTypeCategoricalU8:
		old = uint32(in[0])
	case encoding.FieldTypeCategoricalU16:
		old = uint32(binary.LittleEndian.Uint16(in))
	default:
		old = binary.LittleEndian.Uint32(in)
	}
	if int(old) < len(table) {
		old = table[old]
	}
	return putCategoricalIndex(out, ft, old)
}

// putCategoricalIndex writes v at a categorical rung's width.
func putCategoricalIndex(out []byte, ft encoding.FieldType, v uint32) error {
	switch ft {
	case encoding.FieldTypeCategoricalU8:
		if v > 0xFF {
			return errors.NewCodedError(errors.PULSE_SHARD_DICT_WIDTH_OVERFLOW, "union row: u8 categorical index exceeds 0xFF")
		}
		out[0] = byte(v)
	case encoding.FieldTypeCategoricalU16:
		if v > 0xFFFF {
			return errors.NewCodedError(errors.PULSE_SHARD_DICT_WIDTH_OVERFLOW, "union row: u16 categorical index exceeds 0xFFFF")
		}
		binary.LittleEndian.PutUint16(out, uint16(v))
	default:
		binary.LittleEndian.PutUint32(out, v)
	}
	return nil
}

// unionRowReader is the merged record stream over one member's logical
// record stream. Like logicalReader it reads exactly one member record
// per merged record and never reads ahead.
type unionRowReader struct {
	r   io.Reader
	p   *UnionRowPlan
	src []byte
	row []byte
	pos int
	off int64
}

// NewUnionRowStream returns the merged-layout record stream of the
// member records r yields (r positioned at the first LOGICAL member
// record — a grouped member read through NewLogicalStream first). It
// answers forward relative seeks, as the decode plan issues them.
func NewUnionRowStream(r io.Reader, p *UnionRowPlan) io.Reader {
	return &unionRowReader{r: r, p: p, src: make([]byte, p.srcStride), row: make([]byte, p.dstStride), pos: p.dstStride}
}

func (u *unionRowReader) next() error {
	if _, err := io.ReadFull(u.r, u.src); err != nil {
		return err // io.EOF on a clean boundary, io.ErrUnexpectedEOF mid-record
	}
	if err := u.p.Transcode(u.row, u.src); err != nil {
		return err
	}
	u.pos = 0
	return nil
}

func (u *unionRowReader) Read(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	if u.pos >= len(u.row) {
		if err := u.next(); err != nil {
			return 0, err
		}
	}
	n := copy(b, u.row[u.pos:])
	u.pos += n
	u.off += int64(n)
	return n, nil
}

// Seek supports only forward relative seeks (whence io.SeekCurrent,
// offset >= 0). Whole merged records are skipped as whole member
// records, never transcoded.
func (u *unionRowReader) Seek(offset int64, whence int) (int64, error) {
	if whence != io.SeekCurrent || offset < 0 {
		return 0, errors.NewCodedError(errors.ENCODING_INTERNAL,
			"union record stream supports only forward relative seeks")
	}
	for offset > 0 {
		if u.pos >= len(u.row) {
			ds := int64(u.p.dstStride)
			if offset >= ds {
				if err := advanceReader(u.r, u.p.srcStride); err != nil {
					return u.off, err
				}
				offset -= ds
				u.off += ds
				continue
			}
			if err := u.next(); err != nil {
				return u.off, err
			}
		}
		n := min(int64(len(u.row)-u.pos), offset)
		u.pos += int(n)
		u.off += n
		offset -= n
	}
	return u.off, nil
}
