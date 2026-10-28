package encoding

import (
	"bufio"
	"fmt"
	"io"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// archiveRecordsBuffer bounds the read-ahead used while parsing one
// shard's header + schema block. Only the preamble is read through it;
// the record region is addressed by a fresh SectionReader.
const archiveRecordsBuffer = 64 << 10

// ArchiveRecords is a shard archive opened for ONE sequential pass over
// its records, in archive (central-directory) order: the read a
// whole-cohort export makes.
//
// Schema is the archive's CANONICAL schema (`_schema.pulse`) — the one
// whose set rungs every shard was widened to and whose dictionaries
// every shard's own are a prefix of — so a code read from any shard
// resolves against it. Reader yields the PHYSICAL records of every
// shard back to back, positioned at the first: exactly the byte stream
// a single-file cohort with that schema carries after its preamble.
// Each shard contributes only WHOLE records; a truncated shard tail is
// dropped there rather than shifting every later shard off its stride.
// Records is their total.
type ArchiveRecords struct {
	Schema  *encoding.Schema
	Reader  io.Reader
	Records int64
	// Shards names the shards read, in order.
	Shards []string
}

// OpenArchiveRecords parses the archive in ra (size bytes) and plans
// the sequential record pass. It reads the central directory, the
// canonical entry and every shard's preamble — never a record. A shard
// that does not decode under the canonical schema is refused with the
// verify code (ShardDecodeCompatible), its name on details["entry"]; a
// deflated shard entry is PULSE_ARCHIVE_CORRUPT; the canonical entry's
// absence is PULSE_SHARD_MISSING. ra must outlive the returned Reader.
func OpenArchiveRecords(ra io.ReaderAt, size int64) (*ArchiveRecords, error) {
	return openArchiveRecords(ra, size, "", false)
}

// OpenArchiveEntryRecords is OpenArchiveRecords restricted to ONE
// shard: the read an anchored (`archive.pulse#entry.pulse`) export
// makes. Schema is still the archive's CANONICAL schema and the shard
// passes the same ShardDecodeCompatible gate, so its records are
// exactly the ones the whole-archive pass yields for that entry —
// byte-for-byte the record region of the single file a flatten of the
// archive filtered to that shard would write.
//
// The anchor refusals are pulse.Open's own, raised in its order: the
// reserved canonical entry is PULSE_SHARD_RESERVED_NAME before the
// archive is read (RefuseReservedAnchor); a non-archive keeps the
// OpenArchive code (PULSE_ARCHIVE_MAGIC_INVALID, ...); an entry the
// central directory lacks is Archive.OpenAt's PULSE_SHARD_MISSING with
// details.entry. A deflated entry is PULSE_ARCHIVE_CORRUPT exactly as
// in the whole-archive pass.
func OpenArchiveEntryRecords(ra io.ReaderAt, size int64, entry string) (*ArchiveRecords, error) {
	if err := RefuseReservedAnchor(entry); err != nil {
		return nil, err
	}
	return openArchiveRecords(ra, size, entry, true)
}

// openArchiveRecords plans the pass over every shard, or — anchored —
// over the one shard only names (an empty name is an entry like any
// other, so it is refused as missing, never read as "every shard").
func openArchiveRecords(ra io.ReaderAt, size int64, only string, anchored bool) (*ArchiveRecords, error) {
	arch, err := OpenArchive(ra, size)
	if err != nil {
		return nil, err
	}
	if anchored {
		if _, err := arch.OpenAt(only); err != nil {
			return nil, err // PULSE_SHARD_MISSING, details.entry
		}
	}
	rc, err := arch.Open(ReservedSchemaName)
	if err != nil {
		return nil, err
	}
	doc, err := ReadSchemaDoc(rc)
	_ = rc.Close()
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID,
			"reading the canonical schema "+ReservedSchemaName+" of a shard archive")
	}
	canonical := doc.Schema
	stride := int64(canonical.RecordByteSize())

	out := &ArchiveRecords{Schema: canonical}
	var parts []io.Reader
	for _, e := range arch.Entries() {
		if e.Name == ReservedSchemaName || (anchored && e.Name != only) {
			continue
		}
		if !arch.IsStored(e.Name) {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_ARCHIVE_CORRUPT,
				"shard entry is not store-only, so its records cannot be read by offset",
				map[string]any{"entry": e.Name})
		}
		sect, err := arch.OpenAt(e.Name)
		if err != nil {
			return nil, err
		}
		own, start, err := readShardPreamble(&sect)
		if err != nil {
			return nil, errors.WrapCodedError(err, errors.PULSE_SHARD_HEADER_INVALID,
				fmt.Sprintf("reading the header of shard %q", e.Name))
		}
		if err := ShardDecodeCompatible(canonical, own); err != nil {
			return nil, withEntry(err, e.Name)
		}
		n := int64(0)
		if stride > 0 {
			n = (sect.Size() - start) / stride
		}
		parts = append(parts, io.NewSectionReader(&sect, start, n*stride))
		out.Records += n
		out.Shards = append(out.Shards, e.Name)
	}
	out.Reader = io.MultiReader(parts...)
	return out, nil
}

// readShardPreamble reads one shard's header + schema block and returns
// the schema with the record region's start offset within the shard.
func readShardPreamble(sect *io.SectionReader) (*encoding.Schema, int64, error) {
	cr := &preambleCounter{r: sect}
	br := bufio.NewReaderSize(cr, archiveRecordsBuffer)
	v, err := encoding.ReadHeader(br)
	if err != nil {
		return nil, 0, err
	}
	own, err := encoding.ReadSchema(br, v)
	if err != nil {
		return nil, 0, err
	}
	return own, cr.n - int64(br.Buffered()), nil
}

type preambleCounter struct {
	r io.Reader
	n int64
}

func (c *preambleCounter) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// withEntry names the offending shard on a coded error's details.
func withEntry(err error, name string) error {
	ce, ok := err.(*errors.CodedError)
	if !ok {
		return err
	}
	if ce.Details == nil {
		ce.Details = map[string]any{}
	}
	if _, present := ce.Details["entry"]; !present {
		ce.Details["entry"] = name
	}
	return ce
}

// ShardDecodeCompatible reports whether a shard written with header
// schema own decodes to its own values under canonical. It checks, via
// ValidateStructuralCohesion, the same field names, types (set rungs
// included), vector dims, byte offsets, bit positions, group layout and
// per-field nullability under its one-directional rule (a field
// canonical declares non-nullable may not be nullable in own; a
// nullable canonical field admits a non-nullable one only with an
// equal-width bitmap and outside any group — details.dimension
// "nullable"); then the record stride; then that every dictionary
// (categorical, set, group entries) is a PREFIX of canonical's. A
// dictionary LONGER than canonical's could hold IDs the canonical
// dictionary cannot resolve and is PULSE_SHARD_DICT_DIVERGENCE.
//
// The one rule every whole-archive record read applies: the
// index-addressed CohortRecordReader and the sequential export pass
// (OpenArchiveRecords).
func ShardDecodeCompatible(canonical, own *encoding.Schema) error {
	if _, err := ValidateStructuralCohesion(canonical, own); err != nil {
		return err
	}
	if own.RecordByteSize() != canonical.RecordByteSize() {
		return errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_SCHEMA_MISMATCH,
			"shard record stride differs from the canonical schema",
			map[string]any{"canonical_stride": canonical.RecordByteSize(), "shard_stride": own.RecordByteSize()})
	}
	ext, err := ValidateDictPrefixRule(canonical, own)
	if err != nil {
		return err
	}
	if ext != canonical {
		return errors.NewCodedError(errors.PULSE_SHARD_DICT_DIVERGENCE,
			"shard dictionary extends past the canonical dictionary")
	}
	return nil
}
