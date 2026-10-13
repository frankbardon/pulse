package synth

import (
	"fmt"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// Synth has no model for a vector field (vec_f32 / vec_f64): its
// elements are jointly distributed, and every arm here is per-scalar —
// drawing each element from an independent marginal would write a
// cohort that parses and is quietly wrong. So every surface refuses a
// vector up front with PULSE_SYNTH_VECTOR_UNSUPPORTED, never skipping
// the field (a silently missing column) or flattening it. Vector
// synthesis is planned work (roadmap U52, synth realism).

// vectorUnsupportedErr is the one refusal every surface raises.
func vectorUnsupportedErr(surface, field string, ft encoding.FieldType) error {
	return errors.NewCodedErrorWithDetails(errors.PULSE_SYNTH_VECTOR_UNSUPPORTED,
		fmt.Sprintf("%s: field %q is a %s vector field, and synthetic data cannot model vector fields yet (planned: roadmap U52, synth realism)",
			surface, field, ft),
		map[string]any{"field": field, "type": ft.String(), "surface": surface})
}

// refuseVectorSpecType refuses a spec field declaring a vector type. It
// runs in validateSpec, ahead of the "unknown field type" fallback, so a
// vec_* spec field gets the code that says why rather than reading as a
// typo.
func refuseVectorSpecType(f FieldSpec) error {
	if ft, ok := encoding.ParseFieldType(f.Type); ok && ft.IsVector() {
		return vectorUnsupportedErr("synth spec", f.Name, ft)
	}
	return nil
}

// refuseVectorSchema refuses a cohort schema carrying any vector field —
// the profile-capture source (`synth from-profile`, `pulse profile
// create`) and the tagged top-up source.
func refuseVectorSchema(s *encoding.Schema, surface string) error {
	if s == nil {
		return nil
	}
	for i := range s.Fields {
		if s.Fields[i].Type.IsVector() {
			return vectorUnsupportedErr(surface, s.Fields[i].Name, s.Fields[i].Type)
		}
	}
	return nil
}
