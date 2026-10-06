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
