package pulse

import (
	"bytes"
	"context"
	stderrors "errors"
	"io"
	"sync"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/guide"
	"github.com/frankbardon/pulse/internal/service"
	"github.com/frankbardon/pulse/types"
)

// Recommend turns a question kind into ranked draft requests. req.Intent
// is an intent-taxonomy ID (Intents); an unknown one is the coded error
// PULSE_RECOMMEND_INTENT_UNKNOWN, whose details.valid lists every ID.
//
// Without a cohort the answer is UNBOUND (Bound false): for each
// operator the instance offers that serves the intent, a request
// skeleton whose caller-supplied values are "<placeholder>" strings
// named after their wire keys, a why sentence from the operator's
// purpose, the intent's field shapes, and the alternatives and
// follow-ups its guidance declares. Nothing is predict-run. A
// non-analytic intent (prepare, simulate, lookup) or one no operator
// serves yet returns an empty Recommendations list plus RoutesTo, the
// tooling that answers it instead — that is not an error.
//
// With req.Cohort the answer is BOUND: each serving operator's slots
// are bound to the cohort's fields — req.Fields hints pin roles, the
// rest take schema order, at most three bindings per operator — and
// every draft is validated by an in-process predict carrying the
// instance's options and the cohort's sidecar facts. A draft predict
// refuses is dropped; a survivor carries predict's advisories. A draft
// that still needs a value only the caller can choose (a success
// value, a reference mean, a model family, a percentile) keeps its
// bound fields with Bound false and Needs naming each value, and ranks
// after a fully bound draft of the same hint, level and advisory
// standing. A hint that is not a cohort field, or whose kind no role of
// the intent takes, is SERVICE_VALIDATION; a cohort that cannot be read
// is DATA_FILE (ENCODING_INVALID for a malformed header or schema).
// A weight-only operator (AGG_WEIGHTED_MEAN) binds its weight_field to
// the cohort's suggested weight (Inspect's suggested_weight, from the
// SPSS sidecar), else asks for it unless an instance default weight
// already resolves.
// Bound mode reads the header, the schema and the sidecar only — never
// a record: a shard archive is read by random access (its central
// directory, canonical schema entry and shard headers), an anchored
// shard is that one entry's header and schema, as Predict reads them.
//
// Under a feature profile no hidden operator or capability appears in
// a recommendation, an alternative, a follow-up or a route. Recommend
// is not an observed operation: its draft predicts run in-process and
// fire no predict hooks or metrics.
func (p *Pulse) Recommend(ctx context.Context, req descriptor.RecommendRequest) (*descriptor.RecommendResult, error) {
	inst := p.svc.InstanceSnapshot()
	if req.Cohort == nil {
		return guide.Recommend(inst, req)
	}
	if err := guide.ValidateRequest(req); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path := resolveCohortPath(req.Cohort)
	src, err := p.openGuideCohort(path, "recommend")
	if err != nil {
		return nil, err
	}
	defer src.close()
	facts := p.sidecarFacts(path)
	opts := p.predictOptions(ctx, facts)
	// The cohort's suggested weight — what Inspect reports as
	// suggested_weight — is the weight a weight-only operator
	// (AGG_WEIGHTED_MEAN) binds.
	var weight string
	if sw := descx.SuggestWeight(src.schema, facts.weightVariable, inst); sw != nil {
		weight = sw.Field
	}
	return guide.RecommendBound(inst, req, guide.Bound{
		Cohort: req.Cohort,
		Schema: src.schema,
		Weight: weight,
		Predict: func(r *types.Request) *descriptor.Envelope {
			if _, err := src.rs.Seek(0, io.SeekStart); err != nil {
				return nil
			}
			return descx.Predict(src.rs, r, &opts)
		},
	})
}

// guideCohort is the cohort a guidance surface (bound Recommend,
// Explain, the non-Request predict roots) predicts against: a seekable
// source predict reads from the start, and its schema.
type guideCohort struct {
	rs     io.ReadSeeker
	schema *encoding.Schema
	close  func()
}

// cohortSource is a stored cohort opened for a no-execute read: rs is
// the open file of a single file or a shard archive (an io.ReaderAt,
// so predict and inspect read an archive's central directory, schema
// entry and shard headers in place — never a record, never a copy), or
// the one entry an `archive.pulse#shard.pulse` anchor addresses (a
// section of the open archive when the entry is stored; only that
// entry is read). close releases the file.
type cohortSource struct {
	rs       io.ReadSeeker
	anchored bool
	close    func()
}

// anchorFault is an anchor that does not resolve: the file is not a
// shard archive, or names no such entry.
type anchorFault struct{ err error }

func (a *anchorFault) Error() string { return a.err.Error() }
func (a *anchorFault) Unwrap() error { return a.err }

// asAnchorFault reports whether err is an unresolvable anchor.
func asAnchorFault(err error) (*anchorFault, bool) {
	var af *anchorFault
	ok := stderrors.As(err, &af)
	return af, ok
}

// openCohortSource opens the cohort at path through the instance Fs for
// a no-execute read (Predict, Inspect, the guidance surfaces). A
// returned *anchorFault is an unresolvable anchor; any other error is
// the open itself. An anchored shard is predicted as the single file it
// is: its run reads only that shard (U51), so no archive term applies.
func (p *Pulse) openCohortSource(path string) (*cohortSource, error) {
	readPath, entry := path, ""
	if archivePath, e, ok := service.SplitAnchorPath(path); ok {
		readPath, entry = archivePath, e
	}
	f, err := p.fsys.Open(readPath)
	if err != nil {
		return nil, err
	}
	closeFile := func() { _ = f.Close() }
	if entry == "" {
		return &cohortSource{rs: f, close: closeFile}, nil
	}
	size, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		closeFile()
		return nil, err
	}
	rs, err := archiveEntryReader(f, size, entry)
	if err != nil {
		closeFile()
		return nil, &anchorFault{err: err}
	}
	return &cohortSource{rs: rs, anchored: true, close: closeFile}, nil
}

// archiveEntryReader returns the named entry of the shard archive r
// (size bytes) as a seekable reader: a section of r for a stored entry
// (every Pulse-written shard), else the entry inflated into memory —
// that entry alone.
func archiveEntryReader(r io.ReaderAt, size int64, entry string) (io.ReadSeeker, error) {
	arch, err := encx.OpenArchive(r, size)
	if err != nil {
		return nil, err
	}
	if arch.IsStored(entry) {
		sr, err := arch.OpenAt(entry)
		if err != nil {
			return nil, err
		}
		return &sr, nil
	}
	rc, err := arch.Open(entry)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(data), nil
}

// openGuideCohort opens the cohort at path for a guidance surface; op
// ("recommend", "explain", "predict") prefixes its error messages. It
// reads what predict reads — a single file's header and schema, a shard
// archive's central directory and canonical schema entry, an anchored
// shard's own header and schema — never a record.
func (p *Pulse) openGuideCohort(path, op string) (*guideCohort, error) {
	src, err := p.openCohortSource(path)
	if err != nil {
		if af, ok := asAnchorFault(err); ok {
			return nil, guideSchemaError(op, path, af.err)
		}
		return nil, errors.NewCodedErrorWithDetails(errors.DATA_FILE,
			op+": opening cohort: "+err.Error(), map[string]any{"path": path})
	}
	schema, err := guideSchema(src)
	if err == nil {
		_, err = src.rs.Seek(0, io.SeekStart)
	}
	if err != nil {
		src.close()
		return nil, guideSchemaError(op, path, err)
	}
	rs := src.rs
	if ra, ok := rs.(io.ReaderAt); ok {
		// A guidance surface predicts the same cohort many times over
		// (one predict per Recommend draft): each re-reads the header —
		// an archive's central directory and shard headers — so the
		// spans read are kept, and the cohort is read from storage once.
		if size, err := rs.Seek(0, io.SeekEnd); err == nil {
			rs = io.NewSectionReader(&readCache{r: ra}, 0, size)
		}
	}
	return &guideCohort{rs: rs, schema: schema, close: src.close}, nil
}

// readCache is an io.ReaderAt that keeps every span it reads, so a
// repeated header-only read of one cohort — the same spans each time —
// reaches storage once. It never reads past what was asked: a single
// file's records stay unread.
type readCache struct {
	r     io.ReaderAt
	mu    sync.Mutex
	spans []readSpan
}

type readSpan struct {
	off  int64
	data []byte
	err  error
}

func (c *readCache) ReadAt(p []byte, off int64) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	end := off + int64(len(p))
	for _, s := range c.spans {
		if off >= s.off && end <= s.off+int64(len(s.data)) {
			return copy(p, s.data[off-s.off:]), nil
		}
		if off == s.off && len(p) == len(s.data) && s.err != nil {
			return copy(p, s.data), s.err
		}
	}
	n, err := c.r.ReadAt(p, off)
	if err == nil || err == io.EOF {
		c.spans = append(c.spans, readSpan{off: off, data: append([]byte(nil), p[:n]...), err: err})
	}
	return n, err
}

// guideSchema reads src's schema: a shard archive's from its canonical
// entry, anything else (a single file, an anchored shard) from its own
// header.
func guideSchema(src *cohortSource) (*encoding.Schema, error) {
	if !src.anchored {
		var magic [4]byte
		n, _ := io.ReadFull(src.rs, magic[:])
		if _, err := src.rs.Seek(0, io.SeekStart); err != nil {
			return nil, err
		}
		if ra, ok := src.rs.(io.ReaderAt); ok && n == 4 && magic == encx.ZipMagic {
			size, err := src.rs.Seek(0, io.SeekEnd)
			if err != nil {
				return nil, err
			}
			schemaEntry, err := archiveEntryReader(ra, size, encx.ReservedSchemaName)
			if err != nil {
				return nil, err
			}
			return readGuideSchema(schemaEntry)
		}
	}
	return readGuideSchema(src.rs)
}

// readGuideSchema reads a header and the schema block after it.
func readGuideSchema(r io.Reader) (*encoding.Schema, error) {
	version, err := encoding.ReadHeader(r)
	if err != nil {
		return nil, err
	}
	return encoding.ReadSchema(r, version)
}

func guideSchemaError(op, path string, err error) error {
	return errors.NewCodedErrorWithDetails(errors.ENCODING_INVALID,
		op+": reading cohort schema: "+err.Error(), map[string]any{"path": path})
}
