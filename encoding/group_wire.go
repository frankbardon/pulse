package encoding

import (
	"bytes"
	"encoding/binary"
	"io"

	"github.com/frankbardon/pulse/errors"
)

// The 0x02 schema extension block.
//
// It follows the 0x01 field descriptors (and their inline dictionaries):
//
//	u64 extension_length            bytes that follow, to the first record
//	payload (extension_length bytes):
//	  u16 section_count
//	  per section:
//	    u16 tag
//	    u8  flags                   bit 0 = REQUIRED; bits 1-7 reserved, 0
//	    u64 section_length
//	    section_length bytes
//
// A reader that meets a section tag it does not know REFUSES the file
// when the section is REQUIRED (its meaning changes how records decode)
// and skips it otherwise (advisory metadata). That, plus the reserved
// flag bits below, is what lets later additions be readable or refusable
// without another header version. A tag appearing twice, reserved bits
// set, or a payload not consumed exactly are all ENCODING_INVALID.
//
// Section tag 1, GROUPS (always REQUIRED):
//
//	u16 group_count                 >= 1
//	per group:
//	  u8  kind                      0 = indexed, 1 = constant
//	  u8  index_width               4 for indexed, 0 for constant
//	  u8  flags                     reserved, 0
//	  u16 member_count              >= 1
//	  per member:
//	    u16 field_index             LOGICAL position, strictly ascending
//	    u8  member_flags            bit 0 = key member; bits 1-7 reserved, 0
//	  u32 entry_width               must equal the width the members imply
//	  u32 entry_count               constant: exactly 1
//	  entry_count × entry_width bytes
//
// An entry is the members' logical on-wire bytes in member order
// (bit-packed = one whole byte, exactly as the 0x01 row carries it),
// followed by a ceil(member_count/8)-byte member null bitmap when any
// member is nullable (bit k = member k, LSB-first, 1 = null).
const (
	sectionTagGroups uint16 = 1

	sectionFlagRequired uint8 = 1 << 0
	memberFlagKey       uint8 = 1 << 0
)

// writeSchemaExtension writes the 0x02 extension block for s.
func writeSchemaExtension(w io.Writer, s *Schema) error {
	if err := s.ValidateGroups(); err != nil {
		return err
	}
	var payload bytes.Buffer
	var sections uint16
	var groups bytes.Buffer
	if s.HasGroups() {
		sections++
		writeGroupsSection(&groups, s)
	}
	le := binary.LittleEndian
	payload.Write(le.AppendUint16(nil, sections))
	if s.HasGroups() {
		payload.Write(le.AppendUint16(nil, sectionTagGroups))
		payload.WriteByte(sectionFlagRequired)
		payload.Write(le.AppendUint64(nil, uint64(groups.Len())))
		payload.Write(groups.Bytes())
	}
	if _, err := w.Write(le.AppendUint64(nil, uint64(payload.Len()))); err != nil {
		return errors.WrapCodedError(err, errors.ENCODING_IO, "writing schema extension length")
	}
	if _, err := w.Write(payload.Bytes()); err != nil {
		return errors.WrapCodedError(err, errors.ENCODING_IO, "writing schema extension")
	}
	return nil
}

// groupDescriptorBytes is the on-wire size of one group's descriptor in
// the GROUPS section, dictionary entries excluded: kind, index_width
// and flags (3), member_count (2), 3 per member, entry_width and
// entry_count (8). It is what a group costs beyond its entries, and it
// must track writeGroupsSection exactly (TestGroupDescriptorBytes).
func groupDescriptorBytes(members int) int { return 13 + 3*members }

func writeGroupsSection(b *bytes.Buffer, s *Schema) {
	le := binary.LittleEndian
	b.Write(le.AppendUint16(nil, uint16(len(s.Groups))))
	for g := range s.Groups {
		grp := &s.Groups[g]
		b.WriteByte(byte(grp.Kind))
		if grp.Kind == GroupKindIndexed {
			b.WriteByte(GroupIndexWidth)
		} else {
			b.WriteByte(0)
		}
		b.WriteByte(0) // group flags, reserved
		b.Write(le.AppendUint16(nil, uint16(len(grp.Members))))
		for _, m := range grp.Members {
			b.Write(le.AppendUint16(nil, uint16(m.Field)))
			var fl uint8
			if m.Key {
				fl |= memberFlagKey
			}
			b.WriteByte(fl)
		}
		w := s.GroupEntryWidth(g)
		b.Write(le.AppendUint32(nil, uint32(w)))
		b.Write(le.AppendUint32(nil, uint32(len(grp.Entries)/w)))
		b.Write(grp.Entries)
	}
}

// extReader reads the extension payload through a limit, turning every
// short read into ENCODING_INVALID.
type extReader struct {
	r   *io.LimitedReader
	buf [8]byte
}

func (x *extReader) read(n int, what string) ([]byte, error) {
	if _, err := io.ReadFull(x.r, x.buf[:n]); err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID, "reading schema extension: "+what)
	}
	return x.buf[:n], nil
}

func (x *extReader) u8(what string) (uint8, error) {
	b, err := x.read(1, what)
	if err != nil {
		return 0, err
	}
	return b[0], nil
}

func (x *extReader) u16(what string) (uint16, error) {
	b, err := x.read(2, what)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint16(b), nil
}

func (x *extReader) u32(what string) (uint32, error) {
	b, err := x.read(4, what)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(b), nil
}

func (x *extReader) u64(what string) (uint64, error) {
	b, err := x.read(8, what)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint64(b), nil
}

// bytesN reads n bytes, growing the destination as data actually
// arrives so a corrupt length cannot force a huge up-front allocation.
func (x *extReader) bytesN(n uint64, what string) ([]byte, error) {
	if n > uint64(x.r.N) {
		return nil, errors.NewCodedErrorWithDetails(errors.ENCODING_INVALID,
			"reading schema extension: "+what+" runs past its section",
			map[string]any{"bytes": n, "remaining": x.r.N})
	}
	if n <= exactAllocLimit {
		out := make([]byte, n)
		if _, err := io.ReadFull(x.r, out); err != nil {
			return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID, "reading schema extension: "+what)
		}
		return out, nil
	}
	var b bytes.Buffer
	if _, err := io.CopyN(&b, x.r, int64(n)); err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID, "reading schema extension: "+what)
	}
	// Trim to the exact size: the dictionary stays resident for the
	// cohort's lifetime, so buffer growth slack would be a permanent cost.
	return bytes.Clone(b.Bytes()), nil
}

// exactAllocLimit is the largest extension read allocated up front at
// its declared size. Anything larger grows as bytes actually arrive, so
// a corrupt length cannot force a huge allocation before the read fails.
const exactAllocLimit = 64 << 20

// readSchemaExtension reads the 0x02 extension block into s and
// validates the resulting groups against s.Fields.
func readSchemaExtension(r io.Reader, s *Schema) error {
	var lenBuf [8]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return errors.WrapCodedError(err, errors.ENCODING_INVALID, "reading schema extension length")
	}
	extLen := binary.LittleEndian.Uint64(lenBuf[:])
	if extLen > 1<<62 {
		return errors.NewCodedErrorWithDetails(errors.ENCODING_INVALID,
			"schema extension length is implausible",
			map[string]any{"extension_length": extLen, "version": FormatVersionV2})
	}
	lr := &io.LimitedReader{R: r, N: int64(extLen)}
	x := &extReader{r: lr}
	sections, err := x.u16("section count")
	if err != nil {
		return err
	}
	seen := map[uint16]bool{}
	for i := 0; i < int(sections); i++ {
		tag, err := x.u16("section tag")
		if err != nil {
			return err
		}
		flags, err := x.u8("section flags")
		if err != nil {
			return err
		}
		secLen, err := x.u64("section length")
		if err != nil {
			return err
		}
		if flags&^sectionFlagRequired != 0 {
			return errors.NewCodedErrorWithDetails(errors.ENCODING_INVALID,
				"schema extension section sets reserved flag bits",
				map[string]any{"tag": tag, "flags": flags})
		}
		if seen[tag] {
			return errors.NewCodedErrorWithDetails(errors.ENCODING_INVALID,
				"schema extension section appears twice",
				map[string]any{"tag": tag})
		}
		seen[tag] = true
		if secLen > uint64(lr.N) {
			return errors.NewCodedErrorWithDetails(errors.ENCODING_INVALID,
				"schema extension section runs past the extension block",
				map[string]any{"tag": tag, "section_length": secLen, "remaining": lr.N})
		}
		sec := &extReader{r: &io.LimitedReader{R: lr, N: int64(secLen)}}
		switch tag {
		case sectionTagGroups:
			if err := readGroupsSection(sec, s); err != nil {
				return err
			}
		default:
			if flags&sectionFlagRequired != 0 {
				return errors.NewCodedErrorWithDetails(errors.ENCODING_INVALID,
					"schema extension carries a required section this binary does not understand (a newer Pulse wrote it — upgrade Pulse)",
					map[string]any{"tag": tag, "version": FormatVersionV2})
			}
			if _, err := sec.bytesN(secLen, "skipped section"); err != nil {
				return err
			}
		}
		if sec.r.N != 0 {
			return errors.NewCodedErrorWithDetails(errors.ENCODING_INVALID,
				"schema extension section carries trailing bytes",
				map[string]any{"tag": tag, "trailing": sec.r.N})
		}
	}
	if lr.N != 0 {
		return errors.NewCodedErrorWithDetails(errors.ENCODING_INVALID,
			"schema extension block carries trailing bytes",
			map[string]any{"extension_length": extLen, "trailing": lr.N, "version": FormatVersionV2})
	}
	return s.ValidateGroups()
}

func readGroupsSection(x *extReader, s *Schema) error {
	n, err := x.u16("group count")
	if err != nil {
		return err
	}
	if n == 0 {
		return groupErr("groups section declares no group", nil)
	}
	groups := make([]Group, n)
	for g := range groups {
		kind, err := x.u8("group kind")
		if err != nil {
			return err
		}
		idxW, err := x.u8("group index width")
		if err != nil {
			return err
		}
		flags, err := x.u8("group flags")
		if err != nil {
			return err
		}
		grp := &groups[g]
		grp.Kind = GroupKind(kind)
		switch {
		case grp.Kind == GroupKindIndexed && idxW == GroupIndexWidth:
		case grp.Kind == GroupKindConstant && idxW == 0:
		default:
			return groupErr("unsupported group kind / index width", map[string]any{"group": g, "kind": kind, "index_width": idxW})
		}
		if flags != 0 {
			return groupErr("group sets reserved flag bits", map[string]any{"group": g, "flags": flags})
		}
		mc, err := x.u16("member count")
		if err != nil {
			return err
		}
		grp.Members = make([]GroupMember, mc)
		for k := range grp.Members {
			fi, err := x.u16("member field index")
			if err != nil {
				return err
			}
			mf, err := x.u8("member flags")
			if err != nil {
				return err
			}
			if mf&^memberFlagKey != 0 {
				return groupErr("group member sets reserved flag bits", map[string]any{"group": g, "field_index": fi, "flags": mf})
			}
			grp.Members[k] = GroupMember{Field: int(fi), Key: mf&memberFlagKey != 0}
		}
		ew, err := x.u32("entry width")
		if err != nil {
			return err
		}
		ec, err := x.u32("entry count")
		if err != nil {
			return err
		}
		// Validate members before trusting the width they imply.
		probe := &Schema{Fields: s.Fields, Groups: []Group{*grp}}
		for _, m := range grp.Members {
			if m.Field >= len(s.Fields) {
				return groupErr("group member names no field", map[string]any{"group": g, "field_index": m.Field, "field_count": len(s.Fields)})
			}
		}
		if want := probe.GroupEntryWidth(0); int(ew) != want {
			return groupErr("entry width disagrees with the members' widths", map[string]any{"group": g, "entry_width": ew, "want": want})
		}
		grp.Entries, err = x.bytesN(uint64(ec)*uint64(ew), "group entries")
		if err != nil {
			return err
		}
	}
	s.Groups = groups
	return nil
}
