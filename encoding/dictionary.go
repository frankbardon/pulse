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

// ReadFrom deserializes a dictionary from r, replacing current contents.
// Format: u32 count + (u16 strlen + utf8 bytes) x count
//
// Performance: a single byte buffer is grown to hold all string payloads
// across the dictionary, instead of allocating one []byte per entry. Each
// resolved string is copied once via the standard string([]byte) conversion
// (no unsafe), so we drop one allocation per entry while preserving the
// safety contract that callers can mutate the underlying buffer afterwards
// without affecting the stored strings.
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

	d.values = make([]string, 0, count)
	d.lookup = make(map[string]uint32, count)

	if count == 0 {
		return read, nil
	}

	// Single shared byte buffer for all string payloads. We can't precompute
	// the exact total without consuming the stream first, so we grow on demand
	// from a heuristic initial capacity of 64 bytes per entry (typical for
	// short categorical labels).
	const avgBytesPerEntry = 64
	bufCap := max(int(count)*avgBytesPerEntry, 256)
	buf := make([]byte, 0, bufCap)

	var lenBuf [2]byte
	for i := range count {
		ln, err := io.ReadFull(r, lenBuf[:])
		read += int64(ln)
		if err != nil {
			return read, errors.WrapCodedError(err, errors.ENCODING_INVALID, "reading dictionary string length")
		}
		slen := int(binary.LittleEndian.Uint16(lenBuf[:]))

		// Grow shared buffer to fit this entry, then read directly into the
		// new tail slice. This keeps all string bytes in one backing array.
		start := len(buf)
		end := start + slen
		if cap(buf) < end {
			// Double capacity until it fits, like append's growth strategy.
			newCap := max(cap(buf)*2, end)
			grown := make([]byte, len(buf), newCap)
			copy(grown, buf)
			buf = grown
		}
		buf = buf[:end]

		if slen > 0 {
			rn, err := io.ReadFull(r, buf[start:end])
			read += int64(rn)
			if err != nil {
				return read, errors.WrapCodedError(err, errors.ENCODING_INVALID, "reading dictionary string")
			}
		}

		// string([]byte) copies once; that's the only per-entry alloc now.
		s := string(buf[start:end])
		d.values = append(d.values, s)
		d.lookup[s] = i
	}
	return read, nil
}
