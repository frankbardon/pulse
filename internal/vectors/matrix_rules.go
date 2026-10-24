package vectors

import (
	"math"

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
// set and always PSD. Under pairwise each cell rests on its own rows,
// and the risk is decided PER TYPE — a type this switch does not name
// carries none (an operator that assembles a pairwise matrix and can
// come back non-PSD must add its own case):
//
//   - MAT_COVARIANCE at p ≥ 2 — a variance and a covariance over
//     different rows need not satisfy |C_ij| ≤ √(V_i·V_j);
//   - MAT_CORRELATION at p ≥ 3 — a 2 × 2 correlation is clamped to
//     [−1, 1] with a unit diagonal, which is always PSD, but three
//     pairwise r's need not be mutually consistent;
//   - MAT_PARTIAL_CORRELATION at p ≥ 3 folded columns (members plus
//     outside controls) — its input is the pairwise correlation over
//     every column. A decomposition operator (Decomposition): there the
//     risk is a FATAL PULSE_MATRIX_NOT_PSD, or the repair warning under
//     params.repair "nearest", never the detection-only warning;
//   - MAT_RELIABILITY at p ≥ 3 — omega factors the pairwise inter-item
//     correlation (a decomposition operator); there an unrepaired
//     non-PSD input nulls omega with a PULSE_MATRIX_NOT_PSD warning
//     (alpha needs no PSD input and still computes);
//   - MAT_PCA — a decomposition operator over the pairwise correlation
//     at p ≥ 3 (params.on "correlation") or the pairwise covariance at
//     p ≥ 2 ("covariance", the MAT_COVARIANCE rule); the risk is the
//     fatal refusal or the repair warning;
//   - MAT_COLLINEARITY at p ≥ 3 — a decomposition operator over the
//     pairwise correlation (VIF inverts it; Belsley's diagnostics
//     decompose it, or the uncentered moment matrix rebuilt from it,
//     which is PSD whenever it is); the risk is the fatal refusal or
//     the repair warning;
//   - MAT_FACTOR at p ≥ 3 — a decomposition operator over the pairwise
//     correlation; the risk is the fatal refusal or the repair warning.
func (m Matrix) PSDRisk() bool {
	if !m.Pairwise {
		return false
	}
	p := len(m.Columns())
	switch m.Type {
	case types.MAT_COVARIANCE:
		return p >= 2
	case types.MAT_PCA:
		if m.On == PCAOnCovariance {
			return p >= 2
		}
		return p >= 3
	case types.MAT_CORRELATION, types.MAT_PARTIAL_CORRELATION, types.MAT_RELIABILITY, types.MAT_COLLINEARITY, types.MAT_FACTOR:
		return p >= 3
	}
	return false
}

// Accumulator byte layout (linalg.CoMoment): the shared header — n,
// nMass and nInvalid (int64), w and w2 (float64) — then listwise p means
// plus the packed upper-triangle co-moments, or pairwise one per-pair
// state (n and nMass int64; w, w2, mi, mj, mii, mjj, cij float64) per
// packed cell. TestAccumulatorBytesMatchesCoMomentLayout (linalg) derives
// both word counts from the struct by reflection, so a field added to
// either fails there instead of drifting the estimate silently.
const (
	coMomentHeaderBytes = 5 * 8
	pairMomentBytes     = 9 * 8
)

// AccumulatorBytes estimates the payload bytes of one co-moment state
// over the matrix's folded columns (Columns: the members plus any
// outside control): 40 + 8·(p + p(p+1)/2) listwise,
// 40 + 72·p(p+1)/2 pairwise. The engine holds one such state per
// populated merge block (linalg.MergeBlockSize rows) until finalize, so
// a run's matrix state is this times its block count. Go headers and
// map overhead are not counted.
func (m Matrix) AccumulatorBytes() int64 {
	p := int64(len(m.Columns()))
	cells := p * (p + 1) / 2
	if m.Pairwise {
		return coMomentHeaderBytes + pairMomentBytes*cells
	}
	return coMomentHeaderBytes + 8*(p+cells)
}

// FragmentRowBytes is one row a non-head merge fragment buffers (a
// shard archive row whose shard starts inside its merge block, kept
// for the finalize to replay): the folded columns plus the weight,
// 8 bytes each. Slice headers are not counted.
func (m Matrix) FragmentRowBytes() int64 {
	return 8 * int64(len(m.Columns())+1)
}

// StateCount is the co-moment states the matrix holds per merge block:
// 1, or on MAT_DISTANCE its base state plus one per between bucket
// when the bucket count is known from the schema (EstimateBuckets over
// params.between; 1 otherwise, the per-bucket states then uncounted),
// or on a grouped MAT_SEM (params.group) its base state plus one per
// level, at most (SEMPlan.groupStateBound: the schema's levels, else
// the integer field's value range).
func (m Matrix) StateCount(schema *encoding.Schema) int64 {
	if m.Type == types.MAT_SEM && m.SEM.Grouped() {
		return 1 + m.SEM.groupStateBound()
	}
	if m.Type != types.MAT_DISTANCE || m.Between == nil {
		return 1
	}
	if k, _, known := EstimateBuckets([]*types.Group{m.Between}, schema); known {
		return 1 + k
	}
	return 1
}

// RowBytes is the bytes a BUFFERED matrix (not Streamable — a rank
// method) keeps per admitted row until finalize: the p member values
// and the row weight, 8·(p + 1). 0 on a streamable matrix, which keeps
// no rows. The row store is shared by every bucket of a grouped run
// (each row lands in its own bucket's store), so a run's buffer is this
// times the admitted rows, at most the record count.
func (m Matrix) RowBytes() int64 {
	if m.Streamable {
		return 0
	}
	return 8 * (int64(len(m.Columns())) + 1)
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
	// BucketBasisClusters: GROUP_KMEANS — its cluster count params.k.
	BucketBasisClusters = "clusters"
	// BucketBasisUnknown: the grouper's keys depend on the data (ranges,
	// dates, numeric categories, set compositions) — no estimate.
	BucketBasisUnknown = "unknown"
	// BucketBasisProduct: two or more groupers, every one sized — the
	// product of their bounds (an upper bound on the observed
	// combinations).
	BucketBasisProduct = "product"
)

// EstimateBuckets is the schema-only upper bound on the number of
// matrix buckets — the grouped Response.Data rows a request can emit —
// and how it was derived; known is false when the keys depend on the
// data. One grouper reports its own bound and basis; two or more report
// the PRODUCT of every grouper's bound (saturating at math.MaxInt64)
// with basis BucketBasisProduct, and are unknown when ANY grouper is —
// the composite keys a multi-grouper run mints are a subset of that
// cartesian product. It is an upper bound: a key no row carries (or
// that a filter removes, or a combination never observed) yields no
// bucket, so a run emits AT MOST this many results per spec. Unclamped
// by the record count (callers clamp). Header + schema only —
// importable from internal/descriptor.
func EstimateBuckets(groups []*types.Group, schema *encoding.Schema) (buckets int64, basis string, known bool) {
	var live []*types.Group
	for _, g := range groups {
		if g != nil {
			live = append(live, g)
		}
	}
	switch len(live) {
	case 0:
		return 1, BucketBasisUngrouped, true
	case 1:
		return estimateGrouperBuckets(live[0], schema)
	}
	buckets = 1
	for _, g := range live {
		b, _, ok := estimateGrouperBuckets(g, schema)
		if !ok {
			return 0, BucketBasisUnknown, false
		}
		buckets = mulSaturating(buckets, b)
	}
	return buckets, BucketBasisProduct, true
}

// mulSaturating multiplies two non-negative estimates, saturating at
// math.MaxInt64 rather than wrapping.
func mulSaturating(a, b int64) int64 {
	if a == 0 || b == 0 {
		return 0
	}
	if a > math.MaxInt64/b {
		return math.MaxInt64
	}
	return a * b
}

// estimateGrouperBuckets is one grouper's schema-only bucket bound and
// basis (EstimateBuckets' per-position rule). It reads a dictionary's
// size and membership, never a copy of its values: predict and the
// limit pre-flight call it per grouper, over dictionaries of any size.
func estimateGrouperBuckets(g *types.Group, schema *encoding.Schema) (buckets int64, basis string, known bool) {
	var field *encoding.Field
	if schema != nil {
		field = schema.Field(g.Field)
	}
	// size is the key universe's size and member its membership when
	// the schema fixes it (basis != BucketBasisUnknown).
	var size int64
	var member func(string) bool
	basis = BucketBasisUnknown
	switch g.Type {
	case types.GROUP_CATEGORY:
		switch {
		case field != nil && field.Type.IsCategorical() && field.Dictionary != nil:
			dict := field.Dictionary
			size, basis = int64(dict.Count()), BucketBasisDictionary
			member = func(k string) bool { _, ok := dict.IDFor(k); return ok }
		case field != nil && field.Type == encoding.FieldTypePackedBool:
			size, basis = 2, BucketBasisBoolean
			member = func(k string) bool { return k == "0" || k == "1" }
		}
	case types.GROUP_SET_PER_ELEMENT:
		if field != nil && field.Type.IsSet() && field.Dictionary != nil {
			dict := field.Dictionary
			size, basis = int64(dict.Count()), BucketBasisDictionary
			member = func(k string) bool { _, ok := dict.IDFor(k); return ok }
		}
	case types.GROUP_QUANTILE:
		bins := int64(g.Interval)
		if bins <= 0 {
			bins = 4
		}
		return bins, BucketBasisQuantileBins, true
	case types.GROUP_KMEANS:
		if k, ok := KMeansK(g); ok {
			return int64(k), BucketBasisClusters, true
		}
		return 0, BucketBasisUnknown, false
	}
	includable := g.Type == types.GROUP_CATEGORY || g.Type == types.GROUP_SET_VALUE || g.Type == types.GROUP_SET_PER_ELEMENT ||
		g.Type == types.GROUP_DATE || g.Type == types.GROUP_DATE_RANGES
	if includable && len(g.Include) > 0 {
		seen := make(map[string]bool, len(g.Include))
		for _, k := range g.Include {
			if member == nil || member(k) {
				seen[k] = true
			}
		}
		return int64(len(seen)), BucketBasisInclude, true
	}
	if basis == BucketBasisUnknown {
		return 0, basis, false
	}
	return size, basis, true
}
