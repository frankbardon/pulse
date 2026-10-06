package descriptor

import (
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
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
func predictMatrices(result *descriptor.PredictResult, req *types.Request, schema *encoding.Schema, inst *InstanceSnapshot) {
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
		p := len(m.Members.Members)
		mp := descriptor.MatrixPredict{
			Name:             m.Name,
			Type:             m.Type,
			Shape:            [2]int{p, p},
			AxisKeys:         append([]string{}, m.Members.Members...),
			Missing:          vectors.MissingListwise,
			Encoding:         m.Encoding,
			AccumulatorBytes: m.AccumulatorBytes(),
			Streamable:       m.Type.Streamable(),
			PairwisePSDRisk:  m.PSDRisk(),
			BucketBasis:      basis,
		}
		if known {
			b := buckets
			cells := b * int64(p) * int64(p)
			bytes := b * m.AccumulatorBytes()
			mp.EstimatedBuckets, mp.EstimatedCells, mp.EstimatedBytes = &b, &cells, &bytes
		}
		if m.Pairwise {
			mp.Missing = vectors.MissingPairwise
		}
		if m.ExplicitLabels {
			mp.Labels = append([]string(nil), m.Members.Labels...)
		}
		out = append(out, mp)
	}
	result.Matrices = out
}
