package service

import (
	"context"
	"math"
	"strconv"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
)

// Sample returns up to n rows from the cohort as maps of field name to
// value. Streams from disk — stops reading as soon as n rows are
// collected. For shard archives the rows span the union of shards in
// central-directory (insertion) order; offset+limit apply globally
// across the union, never per-shard (sharding design contract §5.4).
func (s *Service) Sample(ctx context.Context, path string, n int) ([]map[string]any, error) {
	return s.sampleOffsetLimit(ctx, path, 0, n)
}

// SampleWithRequest is the labelled variant. The req struct carries
// the cohort path (via Cohort.Filename), the row cap (N), and any
// LabelBinding entries that translate categorical values to display
// labels in the returned rows. Path resolution mirrors the rest of
// the facade — Cohort.Filename is resolved against the Service's
// data-dir / fs. Returns the rows plus the resolver-side warnings
// (PULSE_LABEL_COLLISION / PULSE_LABEL_LOOKUP_MISS) so callers can
// fold them into the response envelope at their boundary.
func (s *Service) SampleWithRequest(ctx context.Context, req *types.SampleRequest, path string) ([]map[string]any, []processing.ResolverWarning, error) {
	rows, warns, err := s.sampleWithRequest(ctx, req, path)
	return rows, warns, s.scopeRefusal(err)
}

func (s *Service) sampleWithRequest(ctx context.Context, req *types.SampleRequest, path string) ([]map[string]any, []processing.ResolverWarning, error) {
	if req == nil {
		return nil, nil, errors.NewCodedError(errors.SERVICE_VALIDATION, "sample request is required")
	}
	// The union shape rules, before anything is opened.
	if err := descx.UnionShapeRefusalOf(req.Cohort, req.Union, req.UnionSourceField); err != nil {
		return nil, nil, err
	}
	if req.N <= 0 {
		return nil, nil, nil
	}

	var rows []map[string]any
	if len(req.Union) > 0 {
		// A union: its members merged into one archive-shaped cohort,
		// sampled in union order (members in declared order) — the
		// source field a column like any other. path is unused.
		cohort, err := s.openUnion(ctx, req.Union, req.UnionSourceField)
		if err != nil {
			return nil, nil, err
		}
		if len(s.autoLabels) > 0 {
			s.applyAutoLabels(&req.Labels, cohort.Schema(), nil, nil, nil)
		}
		if err := s.validateSampleLabelsSchema(cohort.Schema(), req); err != nil {
			return nil, nil, err
		}
		if rows, err = s.sampleCohort(ctx, cohort, "", 0, req.N); err != nil {
			return nil, nil, err
		}
	} else {
		// Inject configured default label bindings (schema-filtered) so
		// registered tables render in sampled rows without per-call bindings.
		// A failed open here is non-fatal — the real error surfaces in
		// validateSampleLabels / sampleOffsetLimit below.
		if len(s.autoLabels) > 0 {
			if cohort, err := s.Open(ctx, path); err == nil {
				s.applyAutoLabels(&req.Labels, cohort.Schema(), nil, nil, nil)
			}
		}

		// Header-only predict-time validation. Reads the schema once, lets
		// descriptor.ValidateLabels surface every binding-shape failure
		// (unknown field, non-categorical, unknown table, augment
		// collision) before any record bytes are touched.
		if err := s.validateSampleLabels(ctx, path, req); err != nil {
			return nil, nil, err
		}

		var err error
		if rows, err = s.sampleOffsetLimit(ctx, path, 0, req.N); err != nil {
			return nil, nil, err
		}
	}
	if len(req.Labels) == 0 || len(rows) == 0 {
		return rows, nil, nil
	}

	resolver, err := processing.BuildLabelResolver(req.Labels, s.extensions)
	if err != nil {
		return nil, nil, err
	}
	if resolver == nil {
		return rows, nil, nil
	}
	applyLabelResolverToRows(rows, resolver)
	return rows, resolver.Warnings(), nil
}

// validateSampleLabels runs descriptor.ValidateLabels against the
// cohort header + schema so per-binding failures surface as
// SERVICE_VALIDATION / PULSE_LABEL_* before record bytes are read.
// Sample has no projected output columns beyond the schema, so the
// extraFields set is empty.
func (s *Service) validateSampleLabels(ctx context.Context, path string, req *types.SampleRequest) error {
	if len(req.Labels) == 0 {
		return nil
	}
	cohort, err := s.Open(ctx, path)
	if err != nil {
		return err
	}
	return s.validateSampleLabelsSchema(cohort.Schema(), req)
}

// validateSampleLabelsSchema is validateSampleLabels over an opened
// cohort's schema.
func (s *Service) validateSampleLabelsSchema(schema *encoding.Schema, req *types.SampleRequest) error {
	if len(req.Labels) == 0 {
		return nil
	}
	// Reuse the descriptor validator. Build a minimal envelope and
	// promote the first error to a SERVICE_VALIDATION CodedError so
	// the facade returns a single typed error rather than a slice.
	env := descriptor.NewEnvelope(nil)
	descx.ValidateLabels(env, req.Labels, schema, s.ExtensionsSnapshot(), s.InstanceSnapshot(), nil)
	if len(env.Errors) == 0 {
		return nil
	}
	first := env.Errors[0]
	code, ok := errors.ParseCode(first.Code)
	if !ok {
		code = errors.SERVICE_VALIDATION
	}
	return errors.NewCodedErrorWithDetails(code, first.Message, first.Details)
}

// applyLabelResolverToRows walks the row maps in place, applying the
// resolver to every bound field. Replace overwrites the field value;
// augment leaves the original and writes a sibling "<field>_label".
//
// rawAsString is a small helper that coerces the typed row value to
// its string form for resolver lookup. Categorical fields decode to
// their dictionary string in AllValues; the helper handles the rare
// non-string sneak-in (nil) by skipping it.
func applyLabelResolverToRows(rows []map[string]any, r *processing.LabelResolver) {
	if r == nil {
		return
	}
	for _, row := range rows {
		for field, v := range row {
			if !r.Has(field) {
				continue
			}
			raw, ok := labelKeyOf(v, r.Mode(field))
			if !ok {
				continue
			}
			out, sibling, hit := r.Apply(field, raw)
			switch r.Mode(field) {
			case types.LabelModeAugment:
				if hit {
					row[field+"_label"] = sibling
				} else {
					row[field+"_label"] = nil
				}
			case types.LabelModeReplace:
				if hit {
					row[field] = out
				}
				// On miss the value stays as the raw resolved string.
			}
		}
	}
}

// rawAsString coerces a typed row value to a string for resolver
// lookup. Returns false when the value is nil or not a string —
// label tables key on the categorical dictionary value, which
// AllValues returns as a string.
func rawAsString(v any) (string, bool) {
	if v == nil {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

// labelKeyOf is the table key a bound row cell is looked up by. A
// string — a categorical's dictionary text, or an integer-coded field's
// group key, already its decimal spelling — is the key as is. Under
// augment mode a whole, non-negative number is too, as its decimal
// spelling: a sample cell of an integer-coded u4 / u8 / u16 / u32 field
// (labelcodes; such a binding is augment-only, PULSE_LABEL_REPLACE_NUMERIC,
// so replace never meets a number). A nil cell (null) and any other
// value report false: no lookup, no sibling — the categorical rule.
func labelKeyOf(v any, mode types.LabelMode) (string, bool) {
	if s, ok := rawAsString(v); ok || mode != types.LabelModeAugment {
		return s, ok
	}
	switch x := v.(type) {
	case float64:
		if x >= 0 && x <= 1<<53 && x == math.Trunc(x) {
			return strconv.FormatUint(uint64(x), 10), true
		}
	case uint64:
		return strconv.FormatUint(x, 10), true
	case int64:
		if x >= 0 {
			return strconv.FormatInt(x, 10), true
		}
	case int:
		if x >= 0 {
			return strconv.Itoa(x), true
		}
	}
	return "", false
}

// sampleOffsetLimit is the internal helper that powers Sample and the
// shard-aware tests. offset>0 walks rows from the head of the union
// stream until offset rows have been seen, then collects up to limit
// rows. For single-file cohorts this iterates a streamingIterator; for
// shard archives the same logic runs over a shardIter that surfaces
// the union row stream in insertion order — Service.newScanIter picks
// the right one.
//
// limit<=0 returns an empty slice (matches the historical
// "Sample(path, 0)" no-op behavior). offset<0 is normalized to zero.
// When offset+limit exceeds the available row count, the result is
// truncated naturally — no error.
func (s *Service) sampleOffsetLimit(ctx context.Context, path string, offset, limit int) ([]map[string]any, error) {
	if limit <= 0 {
		return nil, nil
	}
	if offset < 0 {
		offset = 0
	}

	cohort, err := s.Open(ctx, path)
	if err != nil {
		return nil, err
	}
	return s.sampleCohort(ctx, cohort, path, offset, limit)
}

// sampleCohort is sampleOffsetLimit over an opened cohort (path names
// it; "" for a union).
func (s *Service) sampleCohort(ctx context.Context, cohort *Cohort, path string, offset, limit int) ([]map[string]any, error) {
	iter := s.newScanIter(cohort, path)
	defer iter.Close()

	// Skip past offset rows. We don't decode-and-discard via the
	// scanIterator's projection hook because Sample's contract is "give
	// me whole rows" and the user-facing payload is the field map; the
	// per-row decode cost is the same either way, only the post-decode
	// retention changes.
	skipped := 0
	for skipped < offset && iter.Next() {
		skipped++
	}
	if err := iter.Err(); err != nil {
		return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE, "sampling cohort")
	}
	if skipped < offset {
		// offset past end of union — no rows to return.
		return nil, nil
	}

	rows := make([]map[string]any, 0, limit)
	for iter.Next() && len(rows) < limit {
		cells, err := processing.RenderRecordCells(iter.Record())
		if err != nil {
			return nil, err
		}
		rows = append(rows, cells)
	}
	if err := iter.Err(); err != nil {
		return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE, "sampling cohort")
	}
	return rows, nil
}
