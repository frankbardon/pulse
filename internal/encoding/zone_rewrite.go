package encoding

import (
	"bufio"
	"bytes"
	stderrors "errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// The SET / CLEAR writer of a cohort's DEFAULT zone field (format 0x02
// ZONE section, encoding.Schema.ZoneField) on an EXISTING cohort.
//
// It is a SCHEMA-ONLY rewrite: the ZONE section neither moves a field
// nor changes the stride, so every byte after the schema block — the
// record region of a single-file cohort, of a shard payload — is copied
// verbatim. Only the header's version byte and the schema block are
// re-emitted (through WritePreamble, so the version stays a function of
// schema content: a flat cohort gains 0x02 with its default and returns
// to 0x01, byte-identical, when the default is cleared).

// zoneTempPattern is the afero.TempFile pattern of the staging file a
// file-path zone rewrite builds into — beside the cohort, so the closing
// rename is a same-filesystem (atomic) one.
const zoneTempPattern = ".zone-*.tmp"

// zoneOptionDetail is the details key naming the option an option
// refusal is about — the value of internal/io.DetailOption, restated
// here because internal/io imports this package (never the reverse).
const zoneOptionDetail = "option"

// ZoneFieldOption is the details.option value every zone-field option
// refusal carries.
const ZoneFieldOption = "zone_field"

// ZoneFieldOptionRefusal re-codes encoding.Schema.ValidateZoneField's
// STRUCTURAL refusal (ENCODING_INVALID: the name resolves to no field,
// or to a non-categorical one) as the OPTION refusal it is at every
// surface that declares a default — import, convert and the set
// writer: SERVICE_VALIDATION, details.option "zone_field", every
// original detail (details.zone_field among them) kept. A bad dictionary
// entry keeps its own PULSE_TIMEZONE_UNKNOWN (the data, not the option,
// is at fault); any other error passes through unchanged. nil stays nil.
func ZoneFieldOptionRefusal(err error) error {
	var ce *errors.CodedError
	if err == nil || !stderrors.As(err, &ce) || ce.Code != errors.ENCODING_INVALID {
		return err
	}
	details := make(map[string]any, len(ce.Details)+1)
	for k, v := range ce.Details {
		details[k] = v
	}
	details[zoneOptionDetail] = ZoneFieldOption
	return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION, ce.Message, details)
}

// WithZoneField returns a copy of s declaring field as its default zone
// field ("" clears it). Every other schema member is shared, never
// mutated: the ZONE section changes no layout, so the copy describes the
// same records. It validates nothing; callers run ValidateZoneField.
func WithZoneField(s *encoding.Schema, field string) *encoding.Schema {
	cp := *s
	cp.ZoneField = field
	return &cp
}

// ZoneRewrite reports one schema-only zone rewrite: the default before
// and after, and whether any byte moved (Changed false: the cohort
// already declared exactly that default, so nothing was written).
type ZoneRewrite struct {
	Previous string
	Current  string
	Changed  bool
}

// SetZoneFieldStream reads a single-file cohort (a cohort file or a
// shard payload) from src and writes it to dst declaring field as its
// default zone field ("" clears it). The schema is judged by
// ValidateZoneField BEFORE a byte is written, its structural refusal
// re-coded by ZoneFieldOptionRefusal. When the default is unchanged
// nothing is written and Changed is false.
func SetZoneFieldStream(dst io.Writer, src io.Reader, field string) (*ZoneRewrite, error) {
	pulseVersion, err := encoding.ReadHeader(src)
	if err != nil {
		return nil, err
	}
	schema, err := encoding.ReadSchema(src, pulseVersion)
	if err != nil {
		return nil, err
	}
	rep := &ZoneRewrite{Previous: schema.ZoneField, Current: field}
	if schema.ZoneField == field {
		return rep, nil
	}
	next := WithZoneField(schema, field)
	if err := ZoneFieldOptionRefusal(next.ValidateZoneField()); err != nil {
		return nil, err
	}
	if err := WritePreamble(dst, next); err != nil {
		return nil, err
	}
	if _, err := io.Copy(dst, src); err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_IO,
			"copying the record region of a zone rewrite")
	}
	rep.Changed = true
	return rep, nil
}

// SetZoneFieldBytes is SetZoneFieldStream over a cohort held in memory
// (a shard payload lifted out of an archive). Unchanged, it returns the
// input slice itself; the input is never modified.
func SetZoneFieldBytes(cohort []byte, field string) ([]byte, *ZoneRewrite, error) {
	var out bytes.Buffer
	out.Grow(len(cohort) + len(field) + 32)
	rep, err := SetZoneFieldStream(&out, bytes.NewReader(cohort), field)
	if err != nil {
		return nil, nil, err
	}
	if !rep.Changed {
		return cohort, rep, nil
	}
	return out.Bytes(), rep, nil
}

// SetZoneFieldFile rewrites the single-file cohort at path in place,
// declaring field as its default zone field ("" clears it).
//
// ATOMIC: the new cohort is built in a temp file beside the original,
// fsynced, the source closed, and only then renamed over it; any failure
// before the rename removes the temp file and leaves the original
// byte-identical. An unchanged default writes nothing (no temp file, no
// rename, the cohort's mtime untouched).
func SetZoneFieldFile(fsys afero.Fs, path, field string) (*ZoneRewrite, error) {
	src, err := fsys.Open(path)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_IO,
			fmt.Sprintf("opening cohort for a zone rewrite: %s", path))
	}
	// A cheap pre-read decides the no-op before any temp file exists.
	pulseVersion, err := encoding.ReadHeader(src)
	if err != nil {
		_ = src.Close()
		return nil, err
	}
	schema, err := encoding.ReadSchema(src, pulseVersion)
	if err != nil {
		_ = src.Close()
		return nil, err
	}
	if schema.ZoneField == field {
		_ = src.Close()
		return &ZoneRewrite{Previous: field, Current: field}, nil
	}
	if _, err := src.Seek(0, io.SeekStart); err != nil {
		_ = src.Close()
		return nil, errors.WrapCodedError(err, errors.ENCODING_IO,
			fmt.Sprintf("rewinding cohort for a zone rewrite: %s", path))
	}

	tmp, err := afero.TempFile(fsys, filepath.Dir(path), filepath.Base(path)+zoneTempPattern)
	if err != nil {
		_ = src.Close()
		return nil, errors.WrapCodedError(err, errors.ENCODING_IO,
			fmt.Sprintf("creating temp file for a zone rewrite of %s", path))
	}
	tmpName := tmp.Name()
	abort := func(e error) (*ZoneRewrite, error) {
		_ = tmp.Close()
		_ = fsys.Remove(tmpName)
		_ = src.Close()
		return nil, e
	}
	bw := bufio.NewWriter(tmp)
	rep, err := SetZoneFieldStream(bw, bufio.NewReader(src), field)
	if err != nil {
		return abort(err)
	}
	if err := bw.Flush(); err != nil {
		return abort(errors.WrapCodedError(err, errors.ENCODING_IO,
			fmt.Sprintf("flushing the zone rewrite of %s", path)))
	}
	// fsync BEFORE the rename, so the published name never points at
	// bytes a crash can still lose.
	if err := tmp.Sync(); err != nil {
		return abort(errors.WrapCodedError(err, errors.ENCODING_IO,
			fmt.Sprintf("syncing the zone rewrite of %s", path)))
	}
	if err := tmp.Close(); err != nil {
		_ = fsys.Remove(tmpName)
		_ = src.Close()
		return nil, errors.WrapCodedError(err, errors.ENCODING_IO,
			fmt.Sprintf("closing the zone rewrite of %s", path))
	}
	// The source is released before the rename (a platform that refuses
	// to replace an open file would otherwise fail the swap).
	if err := src.Close(); err != nil {
		_ = fsys.Remove(tmpName)
		return nil, errors.WrapCodedError(err, errors.ENCODING_IO,
			fmt.Sprintf("closing source cohort %s", path))
	}
	if err := fsys.Rename(tmpName, path); err != nil {
		_ = fsys.Remove(tmpName)
		return nil, errors.WrapCodedError(err, errors.ENCODING_IO,
			fmt.Sprintf("renaming %s onto %s", tmpName, path))
	}
	return rep, nil
}
