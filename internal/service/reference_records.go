package service

import (
	"context"

	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// reference_records.go resolves the stored records a comparison slot's
// {record: {cohort?, keys}} reference reads (ATTR_PROFILE_SIMILARITY /
// ATTR_VEC_DISTANCE, U30 E3-S2): once per distinct (cohort, keys) per
// request, by the point lookup Service.Lookup runs (the cohort's sidecar
// index, assert-unique), BEFORE any processor builds, and bound on the
// request's model.Set (model.Set.WithRecords). Every execution arm builds
// its processors from that Set, so serial, streaming, parallel decode,
// shard fan-out, crosstab, join, Compose slots and chain stages all read
// the one resolution.
//
// A lookup's refusal — PULSE_INDEX_MISSING, PULSE_INDEX_STALE,
// PULSE_INDEX_UNSUPPORTED_SHARDED, PULSE_LOOKUP_NOT_FOUND,
// PULSE_LOOKUP_AMBIGUOUS, a key field the cohort lacks — is bound in
// the record's place and raised, unchanged but located on the slot,
// where the slot binds (after the request's own validation, so a
// request predict refuses is refused by the runtime with predict's
// error first). Never a fallback. A cancelled context returns at once.

// bindReferenceRecords returns ctx carrying the request's bound-model
// set plus every comparison slot's resolved {record} reference; cohort
// is the cohort an omitted record.cohort reads (the request's own —
// every caller has one: a comparison slot never rides a chain stage
// after stage 0, whose rows are not a cohort's, because the chain gate
// refuses it). A request with no {record} reference returns ctx
// unchanged.
func (s *Service) bindReferenceRecords(ctx context.Context, req *types.Request, cohort *types.Cohort) (context.Context, error) {
	if req == nil {
		return ctx, nil
	}
	var recs map[string]model.RecordBinding
	for _, a := range req.Attributes {
		if a == nil {
			continue
		}
		ref, ok := vectors.ComparisonRecord(a.Type, a)
		if !ok {
			continue
		}
		key := ref.RecordKey()
		if _, done := recs[key]; done {
			continue
		}
		if recs == nil {
			recs = map[string]model.RecordBinding{}
		}
		src := ref.Cohort
		if src == nil {
			src = cohort
		}
		matches, _, err := s.lookupRecords(ctx, &types.LookupRequest{Cohort: src, Keys: ref.Keys,
			Multiplicity: types.LookupMultiplicityAssertUnique})
		if cerr := ctx.Err(); cerr != nil {
			return ctx, cerr
		}
		if err != nil {
			recs[key] = model.RecordBinding{Err: err}
			continue
		}
		m := matches[0]
		recs[key] = model.RecordBinding{Record: processing.NewStoredRecord(m.rec, m.schema, m.path)}
	}
	if recs == nil {
		return ctx, nil
	}
	return model.WithSet(ctx, model.SetFrom(ctx).WithRecords(recs)), nil
}
