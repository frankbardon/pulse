package vectors

import (
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// matrix_rules.go holds the per-matrix facts predict reports BEFORE a
// run and the engine honours DURING one, so the two cannot drift: both
// read them off the one resolved Matrix. Schema-only, no execution —
// importable from internal/descriptor.

// PSDRisk reports whether the matrix can come back NOT positive
// semidefinite — the only shapes the engine runs its
// PULSE_MATRIX_NOT_PSD check on, so a false here is a guarantee the
// warning never fires. Listwise matrices are Gram matrices over one row
// set and always PSD. Under pairwise each cell rests on its own rows:
//
//   - MAT_COVARIANCE at p ≥ 2 — a variance and a covariance over
//     different rows need not satisfy |C_ij| ≤ √(V_i·V_j);
//   - MAT_CORRELATION at p ≥ 3 — a 2 × 2 correlation is clamped to
//     [−1, 1] with a unit diagonal, which is always PSD, but three
//     pairwise r's need not be mutually consistent.
func (m Matrix) PSDRisk() bool {
	if !m.Pairwise {
		return false
	}
	p := len(m.Members.Members)
	if m.Type == types.MAT_CORRELATION {
		return p >= 3
	}
	return p >= 2
}

// Accumulator byte layout (linalg.CoMoment): the shared header — n and
// nInvalid (int64), w and w2 (float64) — then listwise p means plus the
// packed upper-triangle co-moments, or pairwise one per-pair state
// (n int64; w, mi, mj, mii, mjj, cij float64) per packed cell.
const (
	coMomentHeaderBytes = 4 * 8
	pairMomentBytes     = 7 * 8
)

// AccumulatorBytes estimates the payload bytes of one co-moment state
// over the matrix's members: 32 + 8·(p + p(p+1)/2) listwise,
// 32 + 56·p(p+1)/2 pairwise. The engine holds one such state per
// populated merge block (linalg.MergeBlockSize rows) until finalize, so
// a run's matrix state is this times its block count. Go headers and
// map overhead are not counted.
func (m Matrix) AccumulatorBytes() int64 {
	p := int64(len(m.Members.Members))
	cells := p * (p + 1) / 2
	if m.Pairwise {
		return coMomentHeaderBytes + pairMomentBytes*cells
	}
	return coMomentHeaderBytes + 8*(p+cells)
}

// How a bucket estimate was derived (MatrixPredict.bucket_basis).
const (
	// BucketBasisUngrouped: no Request.Groups — one result per spec.
	BucketBasisUngrouped = "ungrouped"
	// BucketBasisDictionary: GROUP_CATEGORY over a categorical field or
	// GROUP_SET_PER_ELEMENT over a set field — its dictionary size.
	BucketBasisDictionary = "dictionary"
	// BucketBasisBoolean: GROUP_CATEGORY over packed_bool — keys "0"
	// and "1".
	BucketBasisBoolean = "boolean"
	// BucketBasisInclude: Group.Include restricts the keys — its
	// distinct values (those the field can produce, when that is known).
	BucketBasisInclude = "include"
	// BucketBasisQuantileBins: GROUP_QUANTILE — its bin count
	// (Interval, default 4).
	BucketBasisQuantileBins = "quantile_bins"
	// BucketBasisUnknown: the grouper's keys depend on the data (ranges,
	// dates, numeric categories, set compositions) — no estimate.
	BucketBasisUnknown = "unknown"
)

// EstimateBuckets is the schema-only upper bound on the number of
// matrix buckets — the grouped Response.Data rows a request can emit —
// and how it was derived; known is false when the keys depend on the
// data. The engine executes Groups[0] only (U35 owns multi-entry
// Groups), so the estimate reads Groups[0] only. It is an upper bound:
// a dictionary entry no row carries (or that a filter removes) yields
// no bucket, so a run emits AT MOST this many results per spec.
// Header + schema only — importable from internal/descriptor.
func EstimateBuckets(groups []*types.Group, schema *encoding.Schema) (buckets int64, basis string, known bool) {
	if len(groups) == 0 || groups[0] == nil {
		return 1, BucketBasisUngrouped, true
	}
	g := groups[0]
	var field *encoding.Field
	if schema != nil {
		field = schema.Field(g.Field)
	}
	// possible is the key universe when the schema fixes it.
	var possible []string
	basis = BucketBasisUnknown
	switch g.Type {
	case types.GROUP_CATEGORY:
		switch {
		case field != nil && field.Type.IsCategorical() && field.Dictionary != nil:
			possible, basis = field.Dictionary.Values(), BucketBasisDictionary
		case field != nil && field.Type == encoding.FieldTypePackedBool:
			possible, basis = []string{"0", "1"}, BucketBasisBoolean
		}
	case types.GROUP_SET_PER_ELEMENT:
		if field != nil && field.Type.IsSet() && field.Dictionary != nil {
			possible, basis = field.Dictionary.Values(), BucketBasisDictionary
		}
	case types.GROUP_QUANTILE:
		bins := int64(g.Interval)
		if bins <= 0 {
			bins = 4
		}
		return bins, BucketBasisQuantileBins, true
	}
	includable := g.Type == types.GROUP_CATEGORY || g.Type == types.GROUP_SET_VALUE || g.Type == types.GROUP_SET_PER_ELEMENT
	if includable && len(g.Include) > 0 {
		var universe map[string]bool
		if basis != BucketBasisUnknown {
			universe = make(map[string]bool, len(possible))
			for _, k := range possible {
				universe[k] = true
			}
		}
		seen := make(map[string]bool, len(g.Include))
		for _, k := range g.Include {
			if universe == nil || universe[k] {
				seen[k] = true
			}
		}
		return int64(len(seen)), BucketBasisInclude, true
	}
	if basis == BucketBasisUnknown {
		return 0, basis, false
	}
	return int64(len(possible)), basis, true
}
