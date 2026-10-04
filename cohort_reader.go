package pulse

import (
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/service"
)

// CohortRow is one cohort record as exact typed values, one slot per
// field in schema LOGICAL order (index it by Schema().Fields). It is the
// row type of CohortReader and is distinct from Record, the
// name-keyed map Sample returns.
//
// Per-type Go values (the EXACT stored value, never a float64 echo):
//
//	u4, u8, u16, u32, u64        uint64
//	f32 / f64                    float32 / float64
//	date                         int32 epoch days
//	datetime                     int64 epoch seconds
//	decimal128                   encoding.Decimal128
//	categorical_*                string (the dictionary label)
//	set_* (every rung)           []string labels in dictionary order;
//	                             an empty non-nil slice is "no selection"
//	packed_bool                  bool
//	null (any type)              nil
type CohortRow []any

// CohortReader reads an opened cohort record by record, by index. Obtain
// one from Cohort.Reader and release it with Close.
//
// RecordAt is safe for concurrent use from multiple goroutines; every
// returned row is freshly allocated and owned by the caller.
type CohortReader struct {
	inner *service.CohortRecordReader
}

// Reader opens an index-addressed record reader over the cohort. It
// supports single-file cohorts, ungrouped (format 0x01) and grouped
// (0x02) alike — a grouped cohort reads identically to its ungrouped
// twin. A whole shard archive is refused with SERVICE_VALIDATION. The
// caller must Close the returned reader.
func (c *Cohort) Reader() (*CohortReader, error) {
	inner, err := c.inner.OpenRecordReader()
	if err != nil {
		return nil, err
	}
	return &CohortReader{inner: inner}, nil
}

// Schema returns the schema the reader decodes against; CohortRow slot
// i holds Schema().Fields[i].
func (r *CohortReader) Schema() *encoding.Schema { return r.inner.Schema() }

// Len returns the number of records the reader addresses.
func (r *CohortReader) Len() int64 { return r.inner.Len() }

// RecordAt returns record i (0-based) as exact typed values. An index
// outside [0, Len()) returns a SERVICE_VALIDATION coded error and a
// call after Close a SERVICE_RESOURCE coded error.
func (r *CohortReader) RecordAt(i int64) (CohortRow, error) {
	row, err := r.inner.RecordAt(i)
	if err != nil {
		return nil, err
	}
	return CohortRow(row), nil
}

// Close releases the reader's file handle, waiting for in-flight
// RecordAt calls. It is idempotent.
func (r *CohortReader) Close() error { return r.inner.Close() }
