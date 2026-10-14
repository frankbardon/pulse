package pulse

import (
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/extend"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
)

// extensions_matrix_runtime.go lifts a MatrixOpRegistration onto the
// engine (processing.MatrixOp) and the no-execute snapshot
// (descx.ExtensionsSnapshot.MatrixOps / MatrixDetails), so an extension
// MAT_* operator runs, predicts, streams, merges, weights, corrects and
// self-describes exactly like a built-in.

// adaptMatrixFactory wraps the registration's extend.MatrixFactory as
// the engine's processing.MatrixOpFactory. The finalizer it returns
// maps the engine input onto extend.MatrixInput (CoMoment is the
// engine's per-call clone; Rows the engine's read-only scratch view),
// runs Finalize, and holds the output to the registration's contract
// on EVERY run — the same checks the pulse.New run-probe applies:
// an undeclared auxiliary / vectors / scalars key or a missing primary
// is PULSE_EXTENSION_MATRIX_OUTPUT_MISMATCH, an undeclared component
// key PULSE_EXTENSION_COMPONENT_SCHEMA_MISMATCH. A Finalize error
// refuses the matrix as is.
func adaptMatrixFactory(reg MatrixOpRegistration) processing.MatrixOpFactory {
	return func(spec *types.MatrixSpec, schema *encoding.Schema) (processing.MatrixOpFinalizer, error) {
		op, err := reg.Factory(spec, schema)
		if err != nil || op == nil {
			return nil, err
		}
		return func(in *processing.MatrixOpInput) (processing.MatrixOpOutput, error) {
			ein := &extend.MatrixInput{
				Name:          in.Name,
				CoMoment:      in.CoMoment,
				Members:       in.Members,
				Labels:        in.Labels,
				Params:        in.Params,
				WeightKind:    in.WeightKind,
				Encoding:      in.Encoding,
				WantAuxiliary: in.WantAuxiliary,
				WantVectors:   in.WantVectors,
				WantScalars:   in.WantScalars,
			}
			if in.Rows != nil {
				ein.Rows = in.Rows
			}
			out, err := op.Finalize(ein)
			if err != nil {
				return processing.MatrixOpOutput{}, err
			}
			if err := verifyMatrixOutputKeys(reg, out, false); err != nil {
				return processing.MatrixOpOutput{}, err
			}
			if err := verifyMatrixComponentKeys(reg, out.Components, false); err != nil {
				return processing.MatrixOpOutput{}, err
			}
			return processing.MatrixOpOutput{
				Primary:    out.Primary,
				Auxiliary:  out.Auxiliary,
				Vectors:    out.Vectors,
				Scalars:    out.Scalars,
				Warnings:   out.Warnings,
				Components: out.Components,
			}, nil
		}, nil
	}
}

// addRuntimeMatrixOps records ext.MatrixOps on the runtime registry:
// the operator, its declared tier as both the streamable and the
// mergeable fact (the engine's blocked merge carries a co-moment
// operator; a buffered one keeps rows), its WeightAware declaration
// and its FieldInputs hook, all keyed "matrix|<name>".
func addRuntimeMatrixOps(r *processing.ExtensionRegistry, regs []MatrixOpRegistration) {
	if len(regs) == 0 {
		return
	}
	r.MatrixOps = make(map[types.MatrixType]processing.MatrixOp, len(regs))
	for _, reg := range regs {
		key := processing.StreamabilityKey("matrix", string(reg.Name))
		r.MatrixOps[reg.Name] = processing.MatrixOp{
			Factory:    adaptMatrixFactory(reg),
			Streamable: reg.Streamable,
			Accepts:    append([]encoding.FieldType(nil), reg.Accepts...),
			Auxiliary:  append([]string(nil), reg.OutputKeys.Auxiliary...),
			Components: len(reg.ComponentSchema.Keys) > 0,
		}
		r.Streamable[key] = reg.Streamable
		r.Mergeable[key] = reg.Streamable
		r.WeightAware[key] = reg.WeightAware
		if reg.FieldInputs != nil {
			r.FieldInputs[key] = processing.FieldInputsFunc(reg.FieldInputs)
		}
	}
}

// addSnapshotMatrixOps projects ext.MatrixOps onto the snapshot: the
// generic OperatorMeta (every per-category walk), the matrix-only
// details, the operator's own ComponentSchema and its FieldInputs hook.
func addSnapshotMatrixOps(snap *descx.ExtensionsSnapshot, regs []MatrixOpRegistration) {
	if len(regs) == 0 {
		return
	}
	snap.MatrixDetails = make(map[string]descx.ExtensionMatrixOp, len(regs))
	for _, r := range regs {
		name := string(r.Name)
		snap.MatrixOps = append(snap.MatrixOps, descriptor.OperatorMeta{
			Name:        name,
			Namespace:   parseNamespace(name),
			Description: r.Description,
			Streamable:  r.Streamable,
			Mergeable:   r.Streamable,
			WeightAware: r.WeightAware,
			Accepts:     fieldTypeStrings(r.Accepts),
			Params:      paramMetaSnapshot(r.Params),
		})
		snap.MatrixDetails[name] = descx.ExtensionMatrixOp{
			Accepts: append([]encoding.FieldType(nil), r.Accepts...),
			OutputKeys: descriptor.MatrixOutputKeys{
				Primary:   r.OutputKeys.Primary,
				Auxiliary: append([]string(nil), r.OutputKeys.Auxiliary...),
				Vectors:   append([]string(nil), r.OutputKeys.Vectors...),
				Scalars:   append([]string(nil), r.OutputKeys.Scalars...),
			},
			PValues: r.PValues,
			Params:  matrixParamSnapshot(r.Params),
		}
		if len(r.ComponentSchema.Keys) > 0 {
			snap.ComponentSchemas[name] = r.ComponentSchema
		}
		if r.FieldInputs != nil {
			snap.FieldInputs["matrix|"+name] = r.FieldInputs
		}
	}
}

// matrixParamSnapshot converts a matrix registration's ParamMeta into
// the manifest's Param shape (the built-in matrix operators' params):
// JSON Schema types map onto the Param type vocabulary.
func matrixParamSnapshot(in []ParamMeta) []descriptor.Param {
	if len(in) == 0 {
		return nil
	}
	kind := map[string]string{"number": "float", "boolean": "bool", "array": "list", "string": "string", "object": "object"}
	out := make([]descriptor.Param, len(in))
	for i, p := range in {
		out[i] = descriptor.Param{
			Name:         p.Name,
			Type:         kind[p.JSONType],
			Required:     p.Required,
			Default:      p.Default,
			Description:  p.Description,
			ExactlyOneOf: p.ExactlyOneOf,
		}
	}
	return out
}
