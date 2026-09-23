package encoding

import (
	"encoding/binary"
	"fmt"
	"io"
	"iter"
	"math/bits"

	"github.com/frankbardon/pulse/errors"
)

// SetMaskWords is the number of uint64 words backing a SetMask.
const SetMaskWords = 4

// SetMaskBits is the hard ceiling on set membership across every set
// rung: SetMaskWords × 64 = 256. The ceiling is what lets SetMask be a
// fixed array instead of a slice — see the SetMask doc comment.
const SetMaskBits = SetMaskWords * 64

// SetMask is the single in-memory representation of a set field's
// membership bitmask, for every rung from set_u8 through set_u256.
// Bit i corresponds to dictionary entry i; an all-zero mask is a valid
// "no selection" and is distinct from null (set nulls ride the per-record
// null bitmap only, with no in-band sentinel).
//
// # Word order — normative wire contract
//
// words[0] holds bits 0–63 (the LOW 64 bits), words[1] bits 64–127,
// words[2] bits 128–191, words[3] bits 192–255. On the wire the words are
// laid down in that same order — LITTLE-ENDIAN word order — with each
// word itself written little-endian. The payload of a wide set field is
// therefore exactly the little-endian byte image of the 128- or 256-bit
// integer, and the low 8 bytes of a set_u128 are byte-identical to the
// set_u64 payload holding the same selections. Reverse either the word
// order or the intra-word byte order and every cohort written since the
// wide rungs landed decodes as a different, plausible-looking selection.
//
// # Value semantics
//
// SetMask is a fixed [SetMaskWords]uint64 VALUE. It never allocates, is
// copied by plain assignment, and can be retained across records without
// a defensive copy — no method aliases the receiver's backing array and
// every mutator returns a new value rather than writing through a
// pointer. That is a deliberate consequence of the 256-bit ceiling and is
// why the wide-set surface carries no reuse contract.
type SetMask struct {
	words [SetMaskWords]uint64
}

// SetMaskFromWords builds a SetMask from raw words in the documented
// order: words[0] is the low 64 bits.
func SetMaskFromWords(words [SetMaskWords]uint64) SetMask {
	return SetMask{words: words}
}

// SetMaskFromUint64 lifts a narrow-rung mask (set_u8..set_u64, all of
// which store their bitmask in a uint64) into the shared wide type. The
// value lands in words[0]; the higher words stay zero.
func SetMaskFromUint64(low uint64) SetMask {
	return SetMask{words: [SetMaskWords]uint64{low}}
}

// Words returns a copy of the backing words. Mutating the result cannot
// reach the receiver — arrays are values in Go, and that is the point.
func (m SetMask) Words() [SetMaskWords]uint64 {
	return m.words
}

// Uint64 returns the low 64 bits and reports whether the whole mask fits
// in them. ok=false means the caller is about to truncate: the returned
// value is the low word, but bits at or above 64 exist and would be lost.
// Every narrowing hand-off to the uint64 value API must check ok.
func (m SetMask) Uint64() (uint64, bool) {
	for w := 1; w < SetMaskWords; w++ {
		if m.words[w] != 0 {
			return m.words[0], false
		}
	}
	return m.words[0], true
}

// Has reports whether bit i is set. Out-of-range indices (negative, or at
// or above SetMaskBits) report false rather than panicking — a corrupt or
// mid-remap payload must not take the decoder down.
func (m SetMask) Has(i int) bool {
	if i < 0 || i >= SetMaskBits {
		return false
	}
	return m.words[i/64]&(uint64(1)<<uint(i%64)) != 0
}

// WithBit returns a copy of m with bit i set. An out-of-range i is a
// no-op. The receiver is untouched.
func (m SetMask) WithBit(i int) SetMask {
	if i < 0 || i >= SetMaskBits {
		return m
	}
	m.words[i/64] |= uint64(1) << uint(i%64)
	return m
}

// WithoutBit returns a copy of m with bit i cleared. An out-of-range i is
// a no-op. The receiver is untouched.
func (m SetMask) WithoutBit(i int) SetMask {
	if i < 0 || i >= SetMaskBits {
		return m
	}
	m.words[i/64] &^= uint64(1) << uint(i%64)
	return m
}

// PopCount returns the number of set bits — the size of the selection.
func (m SetMask) PopCount() int {
	n := 0
	for w := 0; w < SetMaskWords; w++ {
		n += bits.OnesCount64(m.words[w])
	}
	return n
}

// IsEmpty reports whether no bit is set. An empty mask is a valid "no
// selection", NOT a null.
func (m SetMask) IsEmpty() bool {
	for w := 0; w < SetMaskWords; w++ {
		if m.words[w] != 0 {
			return false
		}
	}
	return true
}

// HighestBit returns the index of the highest set bit, or -1 when the
// mask is empty. Used by the write path to check a mask against the
// declared rung's capacity.
func (m SetMask) HighestBit() int {
	for w := SetMaskWords - 1; w >= 0; w-- {
		if m.words[w] != 0 {
			return w*64 + 63 - bits.LeadingZeros64(m.words[w])
		}
	}
	return -1
}

// Union returns the bitwise OR of m and o. Neither operand is modified.
func (m SetMask) Union(o SetMask) SetMask {
	for w := 0; w < SetMaskWords; w++ {
		m.words[w] |= o.words[w]
	}
	return m
}

// Intersect returns the bitwise AND of m and o. Neither operand is
// modified.
func (m SetMask) Intersect(o SetMask) SetMask {
	for w := 0; w < SetMaskWords; w++ {
		m.words[w] &= o.words[w]
	}
	return m
}

// Equal reports whether two masks carry exactly the same bits.
func (m SetMask) Equal(o SetMask) bool {
	return m.words == o.words
}

// NextBit returns the lowest set bit at index >= from, and ok=false when
// there is none. It is the allocation-free primitive Bits rides on, and
// the form to use on a hot path:
//
//	for bit, ok := m.NextBit(0); ok; bit, ok = m.NextBit(bit + 1) { … }
func (m SetMask) NextBit(from int) (int, bool) {
	if from < 0 {
		from = 0
	}
	for w := from / 64; w < SetMaskWords; w++ {
		word := m.words[w]
		if w == from/64 {
			// Clear the bits below `from` within its own word.
			word &^= (uint64(1) << uint(from%64)) - 1
		}
		if word != 0 {
			return w*64 + bits.TrailingZeros64(word), true
		}
	}
	return 0, false
}

// Bits returns an iterator over the set bit indices in ASCENDING order,
// for `for bit := range mask.Bits()`. Stopping early stops the walk.
func (m SetMask) Bits() iter.Seq[int] {
	return func(yield func(int) bool) {
		for bit, ok := m.NextBit(0); ok; bit, ok = m.NextBit(bit + 1) {
			if !yield(bit) {
				return
			}
		}
	}
}

// Labels resolves the set bits to dictionary labels in ascending bit
// order. The walk is bounded by dict.Count(), NOT by the mask width or
// the field type's capacity: a bit beyond the dictionary is a corrupt or
// mid-remap payload and is skipped, never resolved and never fatal.
// A nil or empty dictionary yields an empty, non-nil slice.
func (m SetMask) Labels(dict *Dictionary) []string {
	out := []string{}
	if dict == nil {
		return out
	}
	count := dict.Count()
	if count <= 0 {
		return out
	}
	// Size the result up front. The walk yields at most one label per set
	// bit and at most one per dictionary entry, so the smaller of the two
	// is an exact upper bound — without it the append chain reallocates
	// log2(n) times per call, which Record.AllValues pays once per row
	// per set field under expression evaluation.
	capHint := m.PopCount()
	if capHint > count {
		capHint = count
	}
	out = make([]string, 0, capHint)
	for bit, ok := m.NextBit(0); ok && bit < count; bit, ok = m.NextBit(bit + 1) {
		label := dict.Resolve(uint32(bit))
		if label == "" {
			continue
		}
		out = append(out, label)
	}
	return out
}

// FitsFieldType reports whether every set bit is within the capacity of
// the given set field type (ft.MaxSetEntries()). A non-set type has
// capacity 0, so only an empty mask fits one.
func (m SetMask) FitsFieldType(ft FieldType) bool {
	return m.HighestBit() < int(ft.MaxSetEntries())
}

// wideSetWidth returns the on-wire byte width of a wide set rung, or an
// ENCODING_TYPE_MISMATCH for anything else. The narrow rungs keep their
// uint64 storage and Read/WriteFieldValue path; routing one through the
// wide API would write a different number of bytes than the schema stride
// reserves, so it is refused here rather than silently re-laid-out.
func wideSetWidth(ft FieldType) (int, error) {
	if !ft.IsWideSet() {
		return 0, errors.NewCodedErrorWithDetails(errors.ENCODING_TYPE_MISMATCH,
			fmt.Sprintf("wide-set API requires set_u128 or set_u256, got %s", ft),
			map[string]any{"type": ft.String()})
	}
	return ft.ByteSize(), nil
}

// checkSetMaskFits rejects a mask carrying a bit the declared rung cannot
// store. Without this the excess bits would simply not be written, and a
// dropped selection is indistinguishable from one never made.
func checkSetMaskFits(ft FieldType, m SetMask) error {
	if m.FitsFieldType(ft) {
		return nil
	}
	return errors.NewCodedErrorWithDetails(errors.ENCODING_INVALID,
		fmt.Sprintf("set mask has bit %d beyond the capacity of %s", m.HighestBit(), ft),
		map[string]any{
			"type":         ft.String(),
			"capacity":     ft.MaxSetEntries(),
			"highest_bit":  m.HighestBit(),
			"population_n": m.PopCount(),
		})
}

// PutSetMask writes m into dst as the on-wire payload of a wide set
// field. Exactly ft.ByteSize() bytes are written, starting at dst[0], in
// the little-endian word order documented on SetMask; dst may be longer
// (a record buffer sliced at the field offset) and nothing past the field
// width is touched. A dst shorter than the field width, a non-wide field
// type, or a mask wider than the rung's capacity is an error and leaves
// dst untouched.
func PutSetMask(dst []byte, ft FieldType, m SetMask) error {
	width, err := wideSetWidth(ft)
	if err != nil {
		return err
	}
	if len(dst) < width {
		return errors.NewCodedErrorWithDetails(errors.ENCODING_INVALID,
			fmt.Sprintf("destination buffer too small for %s", ft),
			map[string]any{"type": ft.String(), "want_bytes": width, "got_bytes": len(dst)})
	}
	if err := checkSetMaskFits(ft, m); err != nil {
		return err
	}
	for w := 0; w < width/8; w++ {
		binary.LittleEndian.PutUint64(dst[w*8:w*8+8], m.words[w])
	}
	return nil
}

// SetMaskFromBytes decodes the on-wire payload of a wide set field. It
// reads exactly ft.ByteSize() bytes from the front of src and ignores
// anything past that, so a caller may hand it a record buffer sliced at
// the field offset without a neighbouring column bleeding in. Words the
// rung does not carry stay zero.
func SetMaskFromBytes(ft FieldType, src []byte) (SetMask, error) {
	width, err := wideSetWidth(ft)
	if err != nil {
		return SetMask{}, err
	}
	if len(src) < width {
		return SetMask{}, errors.NewCodedErrorWithDetails(errors.ENCODING_INVALID,
			fmt.Sprintf("source buffer too small for %s", ft),
			map[string]any{"type": ft.String(), "want_bytes": width, "got_bytes": len(src)})
	}
	var m SetMask
	for w := 0; w < width/8; w++ {
		m.words[w] = binary.LittleEndian.Uint64(src[w*8 : w*8+8])
	}
	return m, nil
}

// WriteSetMask writes a wide set field's payload to the record stream.
// It is the streaming mirror of PutSetMask and the API the uint64
// WriteFieldValue points at when it refuses set_u128 / set_u256.
func WriteSetMask(w io.Writer, ft FieldType, m SetMask) error {
	width, err := wideSetWidth(ft)
	if err != nil {
		return err
	}
	var buf [SetMaskWords * 8]byte
	if err := PutSetMask(buf[:width], ft, m); err != nil {
		return err
	}
	if _, err := w.Write(buf[:width]); err != nil {
		return errors.WrapCodedError(err, errors.ENCODING_IO, "writing set mask")
	}
	return nil
}

// ReadSetMask reads a wide set field's payload from the record stream.
// Null state rides the per-record null bitmap, not the payload bytes, so
// this function has no null channel — an all-zero mask is an empty
// selection, not a null.
func ReadSetMask(r io.Reader, ft FieldType) (SetMask, error) {
	width, err := wideSetWidth(ft)
	if err != nil {
		return SetMask{}, err
	}
	var buf [SetMaskWords * 8]byte
	if _, err := io.ReadFull(r, buf[:width]); err != nil {
		return SetMask{}, errors.WrapCodedError(err, errors.ENCODING_IO, "reading set mask")
	}
	return SetMaskFromBytes(ft, buf[:width])
}
