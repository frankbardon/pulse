package encoding

import (
	"bytes"
	"testing"
)

func FuzzPulseFileHeader(f *testing.F) {
	// Seed with valid header.
	var buf bytes.Buffer
	WriteHeader(&buf)
	f.Add(buf.Bytes())

	// Seed with truncated.
	f.Add([]byte{0x50})

	// Seed with wrong magic.
	f.Add(make([]byte, HeaderSize))

	f.Fuzz(func(t *testing.T, data []byte) {
		ReadHeader(bytes.NewReader(data))
	})
}

func FuzzDynamicSchemaReader(f *testing.F) {
	// Seed with a valid schema blob.
	schema := &Schema{
		Fields: []Field{
			{Name: "x", Type: FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0, Description: "test"},
		},
	}
	var buf bytes.Buffer
	WriteHeader(&buf)
	WriteSchema(&buf, schema)
	f.Add(buf.Bytes())

	// Seed empty schema.
	var buf2 bytes.Buffer
	WriteHeader(&buf2)
	WriteSchema(&buf2, &Schema{})
	f.Add(buf2.Bytes())

	f.Fuzz(func(t *testing.T, data []byte) {
		r := bytes.NewReader(data)
		if err := ReadHeader(r); err != nil {
			return
		}
		ReadSchema(r)
	})
}

func FuzzCategoricalDictionary(f *testing.F) {
	// Seed with valid dictionary.
	d := NewDictionary()
	d.Add("hello")
	d.Add("world")
	var buf bytes.Buffer
	d.WriteTo(&buf) //nolint:errcheck
	f.Add(buf.Bytes())

	// Seed empty.
	d2 := NewDictionary()
	var buf2 bytes.Buffer
	d2.WriteTo(&buf2) //nolint:errcheck
	f.Add(buf2.Bytes())

	f.Fuzz(func(t *testing.T, data []byte) {
		dict := NewDictionary()
		dict.ReadFrom(bytes.NewReader(data)) //nolint:errcheck
	})
}
