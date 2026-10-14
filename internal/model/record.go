package model

import (
	"github.com/frankbardon/pulse/errors"
)

// record.go carries the stored records a comparison slot's {record:
// {cohort?, keys}} reference reads (ATTR_PROFILE_SIMILARITY /
// ATTR_VEC_DISTANCE). The Service resolves each one by a point lookup
// over its cohort's sidecar index BEFORE any processor builds and binds
// it — or the lookup's refusal — on the request's Set
// (Set.WithRecords), keyed by vectors.ComparisonReference.RecordKey;
// every execution arm builds its processors from that Set, so each
// reads the one resolution and a refusal surfaces where the slot binds
// (after the request's own validation), never as a fallback.

// StoredRecord is one resolved stored record, read by member column
// name. The engine implements it over the decoded record (this package
// stays a leaf: it never sees a schema).
type StoredRecord interface {
	// Member reads member column name: its value, present false for a
	// null (a missing element); known false when the record's cohort
	// holds no column a vector member may be under that name.
	Member(name string) (value float64, present, known bool)
	// Cohort is the cohort path the record was read from.
	Cohort() string
}

// RecordBinding is one {record} reference's resolution: the record, or
// the refusal its lookup raised (exactly one is set).
type RecordBinding struct {
	Record StoredRecord
	Err    error
}

// WithRecords returns a copy of s (nil s: an empty set) carrying s's
// records plus recs (a key in both takes recs'); s is never mutated, and
// every other binding — models, vector fits, the capture — is shared.
func (s *Set) WithRecords(recs map[string]RecordBinding) *Set {
	out := &Set{}
	if s != nil {
		out.byName, out.vectors, out.capture = s.byName, s.vectors, s.capture
		if len(s.records) > 0 {
			out.records = make(map[string]RecordBinding, len(s.records)+len(recs))
			for k, v := range s.records {
				out.records[k] = v
			}
		}
	}
	if len(recs) > 0 && out.records == nil {
		out.records = make(map[string]RecordBinding, len(recs))
	}
	for k, v := range recs {
		out.records[k] = v
	}
	return out
}

// LookupRecord returns the stored record bound under key, or its
// lookup's refusal. A key the Service never resolved (a processor
// driven without it) is PROCESSING_INTERNAL. Nil-receiver-safe.
func (s *Set) LookupRecord(key string) (StoredRecord, error) {
	if s != nil {
		if b, ok := s.records[key]; ok {
			if b.Err != nil {
				return nil, b.Err
			}
			return b.Record, nil
		}
	}
	return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
		"a {record} comparison reference was not resolved before the processor built",
		map[string]any{"record": key})
}
