package service

import (
	"context"
	"fmt"
	"io"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// WidenSetField widens the set column named field in the single-file
// cohort at path to the wider set rung target, rewriting the cohort in
// place, and returns the report describing what moved.
//
// The byte work is entirely encoding.WidenSetFieldFile: it builds the
// re-strided cohort into a temp file beside the original, fsyncs it and
// renames it over the original, so a failure at any point leaves the
// original byte-identical. What this layer adds is the LAYOUT DISPATCH
// a `.pulse` path needs and the engine deliberately does not perform —
// the extension covers two formats (single-file, PULSE magic; shard
// archive, zip magic PK\x03\x04) and the widen engine speaks only the
// first. An archive handed to it is refused by ReadHeader before a byte
// is written, so the archive survives, but the caller is told the file
// is not a cohort rather than told what is actually true: that widening
// an archive is a different operation, performed across the canonical
// `_schema.pulse` and every shard payload at once. This method names
// that distinction instead.
//
// Refusals are coded errors, each reusing an existing code:
//
//   - SERVICE_RESOURCE — the cohort does not exist / cannot be opened.
//   - SERVICE_VALIDATION — the path is a shard archive, or an anchored
//     shard inside one (`archive.pulse#shard.pulse`).
//   - ENCODING_INVALID — no field of that name in the schema.
//   - ENCODING_TYPE_MISMATCH — the field is not a set, the target is
//     not wider, or the field is already at the widest rung
//     (encoding.CheckSetWiden owns that whole policy).
//
// Sidecars are not rebuilt; see encoding.WidenSetFieldFile for why a
// widened cohort invalidates them through their own fingerprints.
func (s *Service) WidenSetField(_ context.Context, path, field string, target encoding.FieldType) (*encoding.WidenReport, error) {
	if archivePath, anchor, ok := splitAnchorPath(path); ok {
		return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("widen does not accept an anchored shard path (%s): a shard inside an archive cannot be widened alone, because every shard in an archive shares the canonical schema", path),
			map[string]any{"cohort": path, "archive": archivePath, "shard": anchor})
	}

	archive, err := s.pathIsShardArchive(path)
	if err != nil {
		return nil, err
	}
	if archive {
		return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("widen does not support shard archive cohorts: %s is a shard archive (zip magic), and widening one means rewriting the canonical schema plus every shard payload together", path),
			map[string]any{"cohort": path, "layout": "shard_archive"})
	}

	return encoding.WidenSetFieldFile(s.fs.Fs(), path, field, target)
}

// pathIsShardArchive reports whether the file at path begins with the
// zip magic, i.e. whether it is a shard archive rather than a
// single-file cohort. It reads four bytes and nothing else — the same
// magic dispatch Service.Open performs, without paying for the whole
// archive slurp Open's archive branch needs, since the answer here is
// used to REFUSE rather than to read.
func (s *Service) pathIsShardArchive(path string) (bool, error) {
	f, err := s.fs.Fs().Open(path)
	if err != nil {
		return false, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("opening cohort file: %s", path))
	}
	defer f.Close()

	var magic [4]byte
	n, err := io.ReadFull(f, magic[:])
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return false, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("reading magic prefix from cohort file: %s", path))
	}
	return n >= 4 && magic == encoding.ZipMagic, nil
}
