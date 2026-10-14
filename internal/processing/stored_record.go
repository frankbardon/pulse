package processing

import (
	"math"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// storedRecord is model.StoredRecord over one decoded record: the row a
// comparison slot's {record} reference reads, resolved by the Service's
// point lookup before any processor builds.
type storedRecord struct {
	rec    *Record
	schema *encoding.Schema
	cohort string
}

// NewStoredRecord wraps rec (decoded over schema, read from cohort) as
// a stored record a {record} comparison reference binds.
func NewStoredRecord(rec *Record, schema *encoding.Schema, cohort string) model.StoredRecord {
	return &storedRecord{rec: rec, schema: schema, cohort: cohort}
}

// Member reads member column name the way a row's member reads
// (Record.MemberValue): a scalar column an integer, float or packed_bool
// (0 / 1 — values that mean the same in every cohort, unlike a
// categorical code), or an element "v[k]" of a vec_* column. Any other
// name is unknown.
func (s *storedRecord) Member(name string) (float64, bool, bool) {
	if _, _, ok := vectors.ParseElementMember(name, s.schema); !ok {
		f := s.schema.Field(name)
		if f == nil || !vectors.MemberTypeAllowed(f.Type, types.VectorCoerceBinary) {
			return math.NaN(), false, false
		}
	}
	v, ok := s.rec.MemberValue(name)
	if !ok {
		return math.NaN(), false, true
	}
	return v, true, true
}

// Cohort is the cohort path the record was read from.
func (s *storedRecord) Cohort() string { return s.cohort }
