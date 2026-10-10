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
// resolver (internal/vectors) and adds a PULSE_VECTOR_UNREFERENCED
// warning per vector no operator slot references. A refused vector is
// reported by the field-reference pass, so nothing is echoed for it.
func predictVectors(env *descriptor.Envelope, result *descriptor.PredictResult, req *types.Request, schema *encoding.Schema) {
	if req == nil || len(req.Vectors) == 0 {
		return
	}
	resolved, err := vectors.Resolve(req.Vectors, schema)
	if err != nil {
		return
	}
	result.ResolvedVectors = make(map[string][]string, len(resolved))
	for _, r := range resolved {
		result.ResolvedVectors[r.Name] = append([]string(nil), r.Members...)
	}
	for _, name := range vectors.Unreferenced(req) {
		w := vectors.UnreferencedWarning(name)
		env.AddWarning(string(w.Code), w.Message, w.Details)
	}
}

// predictMatrices fills PredictResult.Matrices from the one resolver
// (internal/vectors.ResolveMatrices, the field-reference pass's own
// call) and the shared per-matrix rules (vectors.Matrix.PSDRisk /
// AccumulatorBytes, vectors.EstimateBuckets) the engine honours. A refused spec is reported by
// the field-reference pass, so nothing is echoed then.
//
// blocks is the merge-block count the run populates (limits.MergeBlocks
// over the record count, per shard for an archive; -1 when unknown).
// EstimatedBytes is the real state, limits.MatrixStateBytes: one
// CoMoment per populated block per bucket — omitted with the block
// count unknown.
//
// records is the cohort's record count (-1 when unknown): a buffered
// spec's RowBufferBytes is records × vectors.Matrix.RowBytes, added to
// EstimatedBytes.
func predictMatrices(result *descriptor.PredictResult, req *types.Request, schema *encoding.Schema, inst *InstanceSnapshot, blocks, records int64) {
	if req == nil || len(req.Matrices) == 0 {
		return
	}
	plans, err := vectors.ResolveMatrices(req, schema, func(t types.MatrixType) bool {
		return isBuiltinMatrixType(opRoute(inst, t))
	})
	if err != nil {
		return
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
		var rowBytes int64
		if m.RowBytes() > 0 && records >= 0 {
			rowBytes = limits.MatrixRowBufferBytes(records, m.RowBytes())
			rb := rowBytes
			mp.RowBufferBytes = &rb
		}
		if known {
			b := buckets
			cells := b * int64(p) * int64(p)
			mp.EstimatedBuckets, mp.EstimatedCells = &b, &cells
			if blocks >= 0 {
				bytes := limits.MatrixStateBytes(blocks, b, m.AccumulatorBytes())
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
}
