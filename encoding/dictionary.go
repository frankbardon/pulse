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
func (d *Dictionary) ReadFrom(r io.Reader) (int64, error) {
	var read int64
	var count uint32
	if err := binary.Read(r, binary.LittleEndian, &count); err != nil {
		return read, errors.WrapCodedError(err, errors.ENCODING_INVALID, "reading dictionary count")
	}
	read += 4

	d.values = make([]string, 0, count)
	d.lookup = make(map[string]uint32, count)

	for i := uint32(0); i < count; i++ {
		var slen uint16
		if err := binary.Read(r, binary.LittleEndian, &slen); err != nil {
			return read, errors.WrapCodedError(err, errors.ENCODING_INVALID, "reading dictionary string length")
		}
		read += 2
		buf := make([]byte, slen)
		n, err := io.ReadFull(r, buf)
		read += int64(n)
		if err != nil {
			return read, errors.WrapCodedError(err, errors.ENCODING_INVALID, "reading dictionary string")
		}
		s := string(buf)
		d.values = append(d.values, s)
		d.lookup[s] = i
	}
	return read, nil
}
