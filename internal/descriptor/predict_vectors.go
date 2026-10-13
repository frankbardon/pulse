package descriptor

import (
	"math"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/limits"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// predictVectors fills PredictResult.ResolvedVectors from the one
// resolver (internal/vectors), adds a PULSE_VECTOR_UNREFERENCED
// warning per vector no operator slot references, and reports each
// PULSE_VECTOR_METRIC_UNSUITED issue (vectors.MetricUnsuitedIssues, the
// list the runtime reads) as a warning — an error under strict. A
// refused vector is reported by the field-reference pass, so nothing is
// echoed for it.
func predictVectors(env *descriptor.Envelope, result *descriptor.PredictResult, req *types.Request, schema *encoding.Schema, strict bool) {
	if req == nil {
		return
	}
	if len(req.Vectors) > 0 {
		resolved, err := vectors.Resolve(req.Vectors, schema)
		if err != nil {
			return
		}
		// The echo is the request's own vectors; the native fields Resolve
		// appends after them are schema metadata, not request content.
		resolved = resolved[:len(req.Vectors)]
		result.ResolvedVectors = make(map[string][]string, len(resolved))
		for _, r := range resolved {
			result.ResolvedVectors[r.Name] = append([]string(nil), r.Members...)
		}
		for _, name := range vectors.Unreferenced(req) {
			w := vectors.UnreferencedWarning(name)
			env.AddWarning(string(w.Code), w.Message, w.Details)
		}
	}
	// A native vector field named directly carries its schema kind, so a
	// request with no Request.Vectors can still pair a metric poorly.
	for _, w := range vectors.MetricUnsuitedIssues(req, schema) {
		if strict {
			env.AddError(string(w.Code), w.Message, w.Details)
		} else {
			env.AddWarning(string(w.Code), w.Message, w.Details)
		}
	}
}

// predictReferences reports every reference-vector issue of the
// request's expressions (vectors.ReferenceIssues — the rules the runtime
// build refuses with, its first issue failing the build) as an error:
// an unregistered table or key, a table argument that is not a literal,
// and a ref() whose labels are not the compared vector's. The named
// vectors bind as the runtime binds them; a request whose vectors do not
// resolve is judged without them (the field-reference pass reports
// that).
func predictReferences(env *descriptor.Envelope, req *types.Request, schema *encoding.Schema, snap *ExtensionsSnapshot) {
	if req == nil || (len(req.Filterers) == 0 && len(req.Attributes) == 0) {
		return
	}
	// Resolve also yields the schema's native vector fields (nil, nil
	// when there are no vectors at all).
	resolved, _ := vectors.Resolve(req.Vectors, schema)
	for _, ce := range vectors.ReferenceIssues(req, schema, resolved, snap.ReferenceTables()) {
		env.AddError(string(ce.Code), ce.Message, ce.Details)
	}
}

// predictMatrices fills PredictResult.Matrices from the one resolver
// (internal/vectors.ResolveMatrices, the field-reference pass's own
// call) and the shared per-matrix rules (vectors.Matrix.PSDRisk /
// AccumulatorBytes, vectors.EstimateBuckets) the engine honours. A refused spec is reported by
// the field-reference pass, so nothing is echoed then.
//
// blocks is the merge-fragment count the run populates
// (limits.MergeBlocks over the record count, over the per-shard counts
// for an archive; -1 when unknown) and buffered the rows non-head
// fragments keep (limits.MergeBufferedRows; 0 on a single file).
// EstimatedBytes is the real state, limits.MatrixStateBytes: one
// CoMoment per populated fragment per bucket, plus the buffered rows
// (vectors.Matrix.FragmentRowBytes each, per bucket) — omitted with the
// block count unknown.
//
// records is the cohort's record count (-1 when unknown): a buffered
// spec's RowBufferBytes is records × vectors.Matrix.RowBytes, added to
// EstimatedBytes.
//
// It returns the resolved plans, one per result.Matrices entry in the
// same order (nil when nothing was echoed), for the response size model.
func predictMatrices(result *descriptor.PredictResult, req *types.Request, schema *encoding.Schema, snap *ExtensionsSnapshot, inst *InstanceSnapshot, blocks, buffered, records int64) []vectors.Matrix {
	if req == nil || len(req.Matrices) == 0 {
		return nil
	}
	plans, err := resolveMatricesFor(req, schema, snap, inst)
	if err != nil {
		return nil
	}
	buckets, basis, known := vectors.EstimateBuckets(req.Groups, schema)
	out := make([]descriptor.MatrixPredict, 0, len(plans))
	for _, m := range plans {
		// The output axis: every member, or under a
		// MAT_PARTIAL_CORRELATION control list the non-control members.
		axis, axisLabels := m.OutputMembers()
		p := len(axis)
		mp := descriptor.MatrixPredict{
			Name:             m.Name,
			Type:             m.Type,
			Shape:            [2]int{p, p},
			AxisKeys:         axis,
			Missing:          vectors.MissingListwise,
			Encoding:         m.Encoding,
			AccumulatorBytes: m.AccumulatorBytes(),
			Streamable:       m.Streamable,
			Mergeable:        m.Mergeable,
			PairwisePSDRisk:  m.PSDRisk(),
			BucketBasis:      basis,
		}
		if m.Type == types.MAT_PCA && m.Components.Rule == vectors.PCAComponentsFixed {
			// The p × k loadings: k is known before the run only for an
			// integer params.components; a data-driven rule keeps the
			// upper bound [p, p].
			mp.Shape[1] = m.Components.K
		}
		if m.Type == types.MAT_FACTOR && !m.Factors.Kaiser() {
			// The p × k loadings of an integer params.factors; the
			// Kaiser rule keeps the [p, p] bound.
			mp.Shape[1] = m.Factors.K
		}
		side := int64(p)
		if m.Type == types.MAT_DISTANCE {
			// The k × k bucket distances: the bucket labels are the
			// data's, so no axis key is known before the run; k is the
			// between grouper's bucket bound when the schema fixes it
			// (EstimateBuckets), else 0.
			side = 0
			if k, _, ok := vectors.EstimateBuckets([]*types.Group{m.Between}, schema); ok {
				side = k
			}
			mp.Shape = [2]int{int(side), int(side)}
			mp.AxisKeys = []string{}
			axisLabels = nil
			m.ExplicitLabels = false
		}
		var rowBytes int64
		if m.RowBytes() > 0 && records >= 0 {
			rowBytes = limits.MatrixRowBufferBytes(records, m.RowBytes())
			rb := rowBytes
			mp.RowBufferBytes = &rb
		}
		if known {
			b := buckets
			cells := b * side * side
			mp.EstimatedBuckets, mp.EstimatedCells = &b, &cells
			if blocks >= 0 {
				bytes := limits.MatrixStateBytes(blocks, b, m.AccumulatorBytes()*m.StateCount(schema))
				if buffered > 0 {
					fb := limits.MatrixStateBytes(buffered, b, m.FragmentRowBytes()*m.StateCount(schema))
					if bytes > math.MaxInt64-fb {
						bytes = math.MaxInt64
					} else {
						bytes += fb
					}
				}
				if bytes > math.MaxInt64-rowBytes {
					bytes = math.MaxInt64
				} else {
					bytes += rowBytes
				}
				mp.EstimatedBytes = &bytes
			}
		}
		if m.Pairwise {
			mp.Missing = vectors.MissingPairwise
		}
		if m.ExplicitLabels {
			mp.Labels = axisLabels
		}
		out = append(out, mp)
	}
	result.Matrices = out
	return plans
}
