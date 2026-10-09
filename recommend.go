package pulse

import (
	"bytes"
	"context"
	"io"

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
// Bound mode reads the header, the schema and the sidecar only — never
// a record of a single-file cohort; a shard archive (or an anchored
// shard) is read whole, as Predict reads it.
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
	opts := p.predictOptions(ctx, p.sidecarFacts(path))
	return guide.RecommendBound(inst, req, guide.Bound{
		Cohort: req.Cohort,
		Schema: src.schema,
		Predict: func(r *types.Request) *descriptor.Envelope {
			if _, err := src.rs.Seek(0, io.SeekStart); err != nil {
				return nil
			}
			return descx.Predict(src.rs, r, &opts)
		},
	})
}

// guideCohort is the cohort a guidance surface (bound Recommend,
// Explain) predicts against: a seekable source predict reads from the
// start, and its schema.
type guideCohort struct {
	rs     io.ReadSeeker
	schema *encoding.Schema
	close  func()
}

// openGuideCohort opens the cohort at path for a guidance surface; op
// ("recommend", "explain") prefixes its error messages. A
// single file stays an open file handle, so predict reads its header
// and schema and seeks to the end for the record count — never a
// record. A shard archive or an anchored shard is read into memory, as
// Predict reads it.
func (p *Pulse) openGuideCohort(path, op string) (*guideCohort, error) {
	readPath, entry := path, ""
	if archivePath, e, ok := service.SplitAnchorPath(path); ok {
		readPath, entry = archivePath, e
	}
	f, err := p.fsys.Open(readPath)
	if err != nil {
		return nil, errors.NewCodedErrorWithDetails(errors.DATA_FILE,
			op+": opening cohort: "+err.Error(), map[string]any{"path": path})
	}
	var magic [4]byte
	n, _ := io.ReadFull(f, magic[:])
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		_ = f.Close()
		return nil, errors.NewCodedErrorWithDetails(errors.DATA_FILE,
			op+": reading cohort: "+err.Error(), map[string]any{"path": path})
	}
	if entry == "" && (n < 4 || magic != [4]byte{'P', 'K', 0x03, 0x04}) {
		schema, err := readGuideSchema(f)
		if err == nil {
			_, err = f.Seek(0, io.SeekStart)
		}
		if err != nil {
			_ = f.Close()
			return nil, guideSchemaError(op, path, err)
		}
		return &guideCohort{rs: f, schema: schema, close: func() { _ = f.Close() }}, nil
	}
	data, err := io.ReadAll(f)
	_ = f.Close()
	if err != nil {
		return nil, errors.NewCodedErrorWithDetails(errors.DATA_FILE,
			op+": reading cohort: "+err.Error(), map[string]any{"path": path})
	}
	schemaBytes := data
	if entry != "" {
		if data, err = extractShardBytes(data, entry); err != nil {
			return nil, guideSchemaError(op, path, err)
		}
		schemaBytes = data
	} else if schemaBytes, err = extractShardBytes(data, encx.ReservedSchemaName); err != nil {
		return nil, guideSchemaError(op, path, err)
	}
	schema, err := readGuideSchema(bytes.NewReader(schemaBytes))
	if err != nil {
		return nil, guideSchemaError(op, path, err)
	}
	return &guideCohort{rs: bytes.NewReader(data), schema: schema, close: func() {}}, nil
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
