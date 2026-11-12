package encoding

import (
	"encoding/binary"
	"fmt"
	"io"

	"github.com/frankbardon/pulse/errors"
)

// Dictionary maps string values to sequential uint32 IDs.
// It is used for categorical field types to encode string categories
// as compact integer IDs in the binary record format.
type Dictionary struct {
	values []string
	lookup map[string]uint32
}

// NewDictionary creates an empty dictionary.
func NewDictionary() *Dictionary {
	return &Dictionary{
		lookup: make(map[string]uint32),
	}
}

// Add inserts a string into the dictionary, returning its ID.
// If the string already exists, the existing ID is returned.
// There is no capacity limit with Add; use AddWithLimit to enforce one.
func (d *Dictionary) Add(s string) (uint32, error) {
	if id, ok := d.lookup[s]; ok {
		return id, nil
	}
	id := uint32(len(d.values))
	d.values = append(d.values, s)
	d.lookup[s] = id
	return id, nil
}

// AddWithLimit inserts a string, enforcing a maximum entry count.
// Returns PULSE_IMPORT_CATEGORICAL_OVERFLOW if the dictionary is full.
func (d *Dictionary) AddWithLimit(s string, maxEntries uint32) (uint32, error) {
	if id, ok := d.lookup[s]; ok {
		return id, nil
	}
	if uint32(len(d.values)) >= maxEntries {
		return 0, errors.NewCodedErrorWithDetails(
			errors.PULSE_IMPORT_CATEGORICAL_OVERFLOW,
			fmt.Sprintf("categorical dictionary overflow: max %d entries", maxEntries),
			map[string]any{"max_entries": maxEntries, "value": s},
		)
	}
	id := uint32(len(d.values))
	d.values = append(d.values, s)
	d.lookup[s] = id
	return id, nil
}

// Resolve returns the string for a given ID.
// Returns "" if the ID is out of range.
func (d *Dictionary) Resolve(id uint32) string {
	if id >= uint32(len(d.values)) {
		return ""
	}
	return d.values[id]
}

// IDFor looks up the ID for a string.
// Returns the ID and true if found, or 0 and false otherwise.
func (d *Dictionary) IDFor(s string) (uint32, bool) {
	id, ok := d.lookup[s]
	return id, ok
}

// Count returns the number of entries in the dictionary.
func (d *Dictionary) Count() int {
	return len(d.values)
}

// Values returns a copy of all dictionary values in insertion order.
func (d *Dictionary) Values() []string {
	out := make([]string, len(d.values))
	copy(out, d.values)
	return out
}

// WriteTo serializes the dictionary to w.
// Format: u32 count + (u16 strlen + utf8 bytes) x count
func (d *Dictionary) WriteTo(w io.Writer) (int64, error) {
	var written int64
	count := uint32(len(d.values))
	if err := binary.Write(w, binary.LittleEndian, count); err != nil {
		return written, errors.WrapCodedError(err, errors.ENCODING_IO, "writing dictionary count")
	}
	written += 4
	for _, s := range d.values {
		b := []byte(s)
		slen := uint16(len(b))
		if err := binary.Write(w, binary.LittleEndian, slen); err != nil {
			return written, errors.WrapCodedError(err, errors.ENCODING_IO, "writing dictionary string length")
		}
		written += 2
		n, err := w.Write(b)
		written += int64(n)
		if err != nil {
			return written, errors.WrapCodedError(err, errors.ENCODING_IO, "writing dictionary string")
		}
	}
	return written, nil
}

// dictPreallocCap bounds the entries Dictionary.ReadFrom preallocates
// for from the declared count before any entry is read.
const dictPreallocCap = 4096

// ReadFrom deserializes a dictionary from r, replacing current contents.
// Format: u32 count + (u16 strlen + utf8 bytes) x count
//
// The count is a length the input merely DECLARES, so nothing is sized
// from it until the entries behind it have been read: the []string
// starts at min(count, dictPreallocCap) and doubles (never past count),
// and the lookup map is built once, presized to the entries actually
// read. A forged count over a short tail fails ENCODING_INVALID having
// allocated only for the bytes present, while an honest dictionary's
// map is sized exactly. Each label is read into one reused scratch
// buffer (grown to the longest label, at most 64 KiB) and copied once
// via the standard string([]byte) conversion (no unsafe), so callers
// may reuse r's buffers freely. limits.DictionaryDecodeBytes replays
// these allocations for the memory pre-flight.
func (d *Dictionary) ReadFrom(r io.Reader) (int64, error) {
	var read int64
	// Read count via a small fixed-size buffer to avoid binary.Read's
	// reflective path.
	var hdr [4]byte
	n, err := io.ReadFull(r, hdr[:])
	read += int64(n)
	if err != nil {
		return read, errors.WrapCodedError(err, errors.ENCODING_INVALID, "reading dictionary count")
	}
	count := binary.LittleEndian.Uint32(hdr[:])

	if count == 0 {
		d.values = make([]string, 0)
		d.lookup = make(map[string]uint32)
		return read, nil
	}

	values := make([]string, 0, min(count, dictPreallocCap))
	var scratch []byte
	var lenBuf [2]byte
	// A schema-block decode (schemaReader) is told what the block still
	// holds: this label, then at least a u16 length per entry to come.
	sr, _ := r.(*schemaReader)
	for i := range count {
		ln, err := io.ReadFull(r, lenBuf[:])
		read += int64(ln)
		if err != nil {
			return read, errors.WrapCodedError(err, errors.ENCODING_INVALID, "reading dictionary string length")
		}
		slen := int(binary.LittleEndian.Uint16(lenBuf[:]))
		if sr != nil {
			sr.ahead(int64(slen) + 2*int64(count-i-1))
		}
		if cap(scratch) < slen {
			scratch = make([]byte, max(cap(scratch)*2, slen, dictScratchMin))
		}
		label := scratch[:slen]
		if slen > 0 {
			rn, err := io.ReadFull(r, label)
			read += int64(rn)
			if err != nil {
				return read, errors.WrapCodedError(err, errors.ENCODING_INVALID, "reading dictionary string")
			}
		}
		if len(values) == cap(values) {
			// Double, never past the declared count: the entries read so
			// far vouch for the capacity, the count only caps it.
			grown := make([]string, len(values), min(uint32(cap(values))*2, count))
			copy(grown, values)
			values = grown
		}
		values = append(values, string(label))
	}

	lookup := make(map[string]uint32, len(values))
	for i, s := range values {
		lookup[s] = uint32(i)
	}
	d.values = values
	d.lookup = lookup
	return read, nil
}

// dictScratchMin is the smallest label scratch buffer ReadFrom allocates.
const dictScratchMin = 256
