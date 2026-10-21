package descriptor

import (
	"bytes"
	"io"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
)

// archiveSource is a shard archive the no-execute surfaces read by
// random access: the zip central directory at its tail, the canonical
// schema entry and each shard's header — never a shard's records.
type archiveSource struct {
	r    io.ReaderAt
	size int64
}

// sniffArchive reports whether rs, at its current position, opens with
// the zip magic PK\x03\x04 that marks a Pulse shard archive. On a hit it
// returns the archive as a random-access source from that position to
// the end: rs itself when it is an io.ReaderAt (an open file, a section
// of one), so nothing is copied and only what is addressed is read;
// otherwise its remaining bytes, read once. On a miss (or any read
// fault) it restores the original position so the single-file path
// reads from exactly where the caller left the reader.
func sniffArchive(rs io.ReadSeeker) (archiveSource, bool) {
	start, err := rs.Seek(0, io.SeekCurrent)
	if err != nil {
		return archiveSource{}, false
	}
	var magic [4]byte
	n, _ := io.ReadFull(rs, magic[:])
	if _, err := rs.Seek(start, io.SeekStart); err != nil {
		return archiveSource{}, false
	}
	if n < 4 || magic != encx.ZipMagic {
		return archiveSource{}, false
	}
	if ra, ok := rs.(io.ReaderAt); ok {
		end, err := rs.Seek(0, io.SeekEnd)
		if _, serr := rs.Seek(start, io.SeekStart); err == nil && serr == nil {
			return archiveSource{r: io.NewSectionReader(ra, start, end-start), size: end - start}, true
		}
		_, _ = rs.Seek(start, io.SeekStart)
	}
	data, err := io.ReadAll(rs)
	if err != nil {
		_, _ = rs.Seek(start, io.SeekStart)
		return archiveSource{}, false
	}
	return archiveSource{r: bytes.NewReader(data), size: int64(len(data))}, true
}

// cohortHeadFault is a cohort that cannot be judged: the envelope entry
// a validator reports, with the code the Predict path raises for it.
type cohortHeadFault struct {
	code    errors.Code
	message string
	details map[string]any
}

// readCohortHead reads the schema and the header counts of the cohort
// fileData holds, for the predict roots that judge one cohort beside a
// request (facet, chain stage 0): a single file's header + schema and
// its length-derived record count (an anchor-extracted shard is one),
// or a shard archive's canonical schema with the per-shard header
// counts and its central-directory bytes — the figures the runtime pre-flight reads off the
// cohort Open returns. Never a record. A fault carries the code and
// message Predict reports for the same bytes.
func readCohortHead(fileData io.ReadSeeker) (*encoding.Schema, CohortCounts, *cohortHeadFault) {
	if src, ok := sniffArchive(fileData); ok {
		return readArchiveHead(src)
	}
	pulseVersion, err := encoding.ReadHeader(fileData)
	if err != nil {
		return nil, CohortCounts{}, &cohortHeadFault{code: headerErrorCode(err), message: "invalid pulse file header: " + err.Error()}
	}
	schema, err := encoding.ReadSchema(fileData, pulseVersion)
	if err != nil {
		return nil, CohortCounts{}, &cohortHeadFault{code: errors.ENCODING_INVALID, message: "invalid pulse schema: " + err.Error()}
	}
	counts := CohortCounts{Records: -1}
	if n, _, ok := deriveSingleFileRecordCount(fileData, schema); ok {
		counts.Records = n
	}
	return schema, counts, nil
}

// readArchiveHead is readCohortHead over a shard archive.
func readArchiveHead(src archiveSource) (*encoding.Schema, CohortCounts, *cohortHeadFault) {
	arch, err := encx.OpenArchive(src.r, src.size)
	if err != nil {
		return nil, CohortCounts{}, &cohortHeadFault{code: errors.PULSE_ARCHIVE_CORRUPT, message: "invalid pulse shard archive: " + err.Error()}
	}
	rc, err := arch.Open(encx.ReservedSchemaName)
	if err != nil {
		return nil, CohortCounts{}, &cohortHeadFault{code: errors.PULSE_SHARD_MISSING,
			message: "archive missing reserved schema entry " + encx.ReservedSchemaName + ": " + err.Error()}
	}
	defer rc.Close()
	pulseVersion, err := encoding.ReadHeader(rc)
	if err == nil {
		var schema *encoding.Schema
		if schema, err = encoding.ReadSchema(rc, pulseVersion); err == nil {
			counts := CohortCounts{DirectoryBytes: arch.DirectoryBytes()}
			for _, entry := range arch.Entries() {
				if entry.Name == encx.ReservedSchemaName {
					continue
				}
				n, perr := arch.PeekShardRecordCount(entry.Name)
				if perr != nil {
					return nil, CohortCounts{}, &cohortHeadFault{code: errors.PULSE_SHARD_HEADER_INVALID,
						message: "peeking record count for shard " + entry.Name + ": " + perr.Error(),
						details: map[string]any{"entry": entry.Name}}
				}
				counts.Records += n
				counts.ShardRecords = append(counts.ShardRecords, n)
			}
			return schema, counts, nil
		}
	}
	return nil, CohortCounts{}, &cohortHeadFault{code: errors.ENCODING_INVALID,
		message: "reading canonical schema entry " + encx.ReservedSchemaName + ": " + err.Error()}
}
