package encoding

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// SchemaDocMagic is the four-byte marker that introduces the sharding
// metadata extension appended to a `_schema.pulse` entry after the
// standard schema block. Readers detect the marker and parse the
// extension; absence means the metadata is unavailable (the canonical
// fallback is to peek each shard's header for record counts).
var SchemaDocMagic = [4]byte{'S', 'H', 'R', 'D'}

// SchemaDoc carries the contents of a `_schema.pulse` entry: the
// canonical Schema plus the sharding metadata extension. AggregateRecordCount
// is the cached sum of every shard's record count at the time the
// archive was last written; ShardCount mirrors the zip central
// directory's entry count (excluding the reserved schema entry).
//
// Both metadata fields are a SANITY CHECK, not the source of truth.
// Per the design contract, the per-shard headers are authoritative —
// callers populate live `ShardEntry.RecordCount` values by peeking
// each shard's header. AggregateRecordCount is exposed so cheap
// `pulse inspect` calls can report an approximate total without
// opening every shard.
type SchemaDoc struct {
	Schema               *encoding.Schema
	AggregateRecordCount uint64
	ShardCount           uint16
}

// WriteSchemaDoc emits a `_schema.pulse` payload: standard Pulse
// header, standard schema block, then the sharding metadata
// extension (magic "SHRD" + u64 aggregate_record_count + u16
// shard_count, little-endian). The result is a valid single-file
// Pulse cohort with zero records, so legacy readers that don't know
// about the SHRD extension can still parse the header + schema and
// stop at the (empty) record region — the extension bytes live
// where records would otherwise begin and a header-only reader
// (`Inspect`) ignores them.
func WriteSchemaDoc(w io.Writer, schema *encoding.Schema, agg uint64, shardCount uint16) error {
	// The canonical schema carries the version its content requires, like
	// any shard payload — the SHRD trailer follows the (possibly
	// extension-bearing) schema block either way.
	if err := WritePreamble(w, schema); err != nil {
		return err
	}
	if _, err := w.Write(SchemaDocMagic[:]); err != nil {
		return errors.WrapCodedError(err, errors.ENCODING_IO,
			"writing schema-doc sharding magic")
	}
	if err := binary.Write(w, binary.LittleEndian, agg); err != nil {
		return errors.WrapCodedError(err, errors.ENCODING_IO,
			"writing schema-doc aggregate record count")
	}
	if err := binary.Write(w, binary.LittleEndian, shardCount); err != nil {
		return errors.WrapCodedError(err, errors.ENCODING_IO,
			"writing schema-doc shard count")
	}
	return nil
}

// ReadSchemaDoc parses a `_schema.pulse` payload. It always reads the
// Pulse header + standard schema block. The sharding metadata
// extension (magic "SHRD" + agg + shard count) is read when present
// and silently skipped when absent — older `_schema.pulse` artifacts
// or single-file cohorts addressed by mistake still parse cleanly,
// returning AggregateRecordCount=0 and ShardCount=0.
func ReadSchemaDoc(r io.Reader) (*SchemaDoc, error) {
	pulseVersion, err := encoding.ReadHeader(r)
	if err != nil {
		return nil, err
	}
	schema, err := encoding.ReadSchema(r, pulseVersion)
	if err != nil {
		return nil, err
	}
	// A grouped canonical schema is legal: every shard of the archive
	// shares its group layout (ValidateGroupCohesion) and its row indices
	// address these canonical dictionaries, of which each shard's own is a
	// prefix (see shard_groups.go). The read path decodes every shard with
	// this schema.
	doc := &SchemaDoc{Schema: schema}

	// Peek the next four bytes to detect the SHRD marker. A short read
	// or non-matching prefix means there is no extension — that is
	// not an error.
	var probe [4]byte
	n, perr := io.ReadFull(r, probe[:])
	if perr != nil {
		// Short tail is fine — metadata block is optional.
		if perr == io.EOF || perr == io.ErrUnexpectedEOF {
			return doc, nil
		}
		return nil, errors.WrapCodedError(perr, errors.ENCODING_INVALID,
			"reading schema-doc extension marker")
	}
	if n != len(SchemaDocMagic) || !bytes.Equal(probe[:], SchemaDocMagic[:]) {
		// Unknown trailing bytes — treat as absent metadata. Anything
		// past the schema block in a sharding-naïve `_schema.pulse`
		// belongs to records, which a header-only doc has none of.
		return doc, nil
	}
	if err := binary.Read(r, binary.LittleEndian, &doc.AggregateRecordCount); err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID,
			"reading schema-doc aggregate record count")
	}
	if err := binary.Read(r, binary.LittleEndian, &doc.ShardCount); err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID,
			"reading schema-doc shard count")
	}
	return doc, nil
}

// PeekShardRecordCount opens the named entry, reads its Pulse header
// and schema block, validates both, and computes the record count
// from the remaining bytes divided by the schema's per-record size.
// Used by `Pulse.Open` to populate `ShardEntry.RecordCount` from the
// per-shard headers (the source of truth — `_schema.pulse`'s
// AggregateRecordCount is only a sanity check).
//
// The schema block is read LAYOUT-ONLY (ReadSchemaLayout): every
// categorical / set dictionary and parent-group entry table is read
// past and discarded, never allocated — the stride needs only the field
// types, nullability, vector dims and group layout, all still read and
// validated. A full decode of a shard with 100,000-entry dictionaries
// allocated tens of megabytes only to throw them away
// (TestPeekShardRecordCount_SkipsDictionaries); the count is the one a
// full decode derives (TestPeekShardRecordCount_LayoutMatchesFullDecode).
//
// The records themselves are never read from a stored (Method 0) entry
// — every Pulse-written shard: the count is the central directory's
// entry size less the header and schema bytes just consumed, over the
// stride, so peeking every shard of an archive reads its headers, not
// its payload (TestPeekShardRecordCount_ReadsNoRecords). A deflated
// entry has no payload length short of inflating it, so it is drained.
//
// Returns PULSE_SHARD_HEADER_INVALID when the shard's bytes are not
// a valid single-file Pulse cohort (bad magic, unsupported version,
// truncated header or schema).
func (a *Archive) PeekShardRecordCount(name string) (int64, error) {
	rc, err := a.Open(name)
	if err != nil {
		return 0, err
	}
	defer rc.Close()

	cr := &byteCountingReader{r: rc}
	pulseVersion, err := encoding.ReadHeader(cr)
	if err != nil {
		return 0, errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_HEADER_INVALID,
			fmt.Sprintf("shard %q has an invalid header", name),
			map[string]any{"entry": name, "cause": err.Error()})
	}
	schema, err := ReadSchemaLayout(cr, pulseVersion)
	if err != nil {
		return 0, errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_HEADER_INVALID,
			fmt.Sprintf("shard %q has an invalid schema block", name),
			map[string]any{"entry": name, "cause": err.Error()})
	}

	recordSize := schema.RecordByteSize()
	if recordSize == 0 {
		return 0, nil
	}

	if a.IsStored(name) {
		size := int64(a.byName[name].UncompressedSize64)
		return max(size-cr.n, 0) / int64(recordSize), nil
	}
	// A deflated entry: drain the remaining bytes into io.Discard to
	// count without buffering the payload.
	remaining, err := io.Copy(io.Discard, rc)
	if err != nil {
		return 0, errors.WrapCodedError(err, errors.ENCODING_IO,
			fmt.Sprintf("reading shard %q to compute record count", name))
	}
	return remaining / int64(recordSize), nil
}

// byteCountingReader counts the bytes read through it.
type byteCountingReader struct {
	r io.Reader
	n int64
}

func (c *byteCountingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}
