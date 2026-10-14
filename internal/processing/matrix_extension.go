package processing

import (
	"encoding/json"
	stderrors "errors"
	"strconv"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
)

// matrix_extension.go is the engine half of an embedder-registered
// MAT_* operator (pulse.MatrixOpRegistration). The operator is
// FINALIZE-ONLY: its slot is an ordinary matrixSlot — per-block
// co-moments, the fixed merge tree, grouped buckets, the engine-owned
// missing / max_drop_share, weights, the components floor and the
// co-moment data-quality warnings — and the registration's finalizer
// replaces the built-in matrixFinalizers entry. The root adapter
// (extensions_adapt.go) lifts the public extend.MatrixOperator onto
// MatrixOpFactory; this package never imports extend.

// MatrixOpRows is a buffered extension slot's admitted rows, as the
// operator reads them (extend.MatrixRows' engine twin).
type MatrixOpRows interface {
	Len() int
	Row(i int) []float64
	Weight(i int) float64
}

// MatrixOpInput is one extension Finalize call's input
// (extend.MatrixInput's engine twin). CoMoment is a CLONE of the slot's
// merged state, made per call: an operator that mutates it (against
// its contract) cannot reach the components floor, another bucket or
// another slot, so the result stays a function of the rows alone.
type MatrixOpInput struct {
	Name     string
	CoMoment *linalg.CoMoment
	// Rows is set on a buffered (Streamable: false) slot only.
	Rows    MatrixOpRows
	Members []string
	// Labels are set when the members carry explicit labels.
	Labels []string
	// Params are the spec's params without the engine-owned keys.
	Params     json.RawMessage
	WeightKind types.WeightKind
	Encoding   types.MatrixEncoding

	WantAuxiliary, WantVectors, WantScalars bool
}

// MatrixOpOutput is an extension Finalize call's output
// (extend.MatrixOutput's engine twin).
type MatrixOpOutput struct {
	Primary    *types.MatrixValues
	Auxiliary  map[string]*types.MatrixValues
	Vectors    map[string]any
	Scalars    map[string]float64
	Warnings   []*types.ResponseWarning
	Components map[string]any
}

// MatrixOpFinalizer finalizes one matrix result of an extension
// operator. It must be pure and re-entrant (grouped buckets call it once
// each).
type MatrixOpFinalizer func(in *MatrixOpInput) (MatrixOpOutput, error)

// MatrixOpFactory builds a slot's finalizer from the stamped spec
// (Params already stripped of the engine-owned keys) and the schema.
type MatrixOpFactory func(spec *types.MatrixSpec, schema *encoding.Schema) (MatrixOpFinalizer, error)

// MatrixOp is one registered extension MAT_* operator.
type MatrixOp struct {
	Factory MatrixOpFactory
	// Streamable is the declared co-moment tier; false keeps rows and
	// runs the request buffered and serially.
	Streamable bool
	// Accepts narrows the member column types (empty: the built-in
	// member types).
	Accepts []encoding.FieldType
	// Auxiliary are the declared auxiliary keys
	// (vectors.Matrix.AuxiliaryKeys).
	Auxiliary []string
	// Components reports a declared ComponentSchema with keys: on a
	// run that keeps components.matrices but skips the matrices slot,
	// the finalizer still runs (every part off) to fill them, as for
	// the built-in finalizerComponents.
	Components bool
}

// LookupMatrixOp returns the registered extension operator t; false for
// a built-in, an unregistered and a hidden name. Nil-receiver-safe.
func (r *ExtensionRegistry) LookupMatrixOp(t types.MatrixType) (MatrixOp, bool) {
	if r == nil || r.isHidden(string(t)) {
		return MatrixOp{}, false
	}
	op, ok := r.MatrixOps[t]
	return op, ok
}

// HasMatrixOp reports whether t is an offered extension MAT_* operator.
func (r *ExtensionRegistry) HasMatrixOp(t types.MatrixType) bool {
	_, ok := r.LookupMatrixOp(t)
	return ok
}

// matrixExtension is the resolver's view of the extension operators
// (vectors.MatrixResolve.Extension).
func (r *ExtensionRegistry) matrixExtension(t types.MatrixType) (vectors.MatrixExtension, bool) {
	op, ok := r.LookupMatrixOp(t)
	if !ok {
		return vectors.MatrixExtension{}, false
	}
	return vectors.MatrixExtension{Streamable: op.Streamable, Accepts: op.Accepts, Auxiliary: op.Auxiliary}, true
}

// MatrixSpecStreamable is the instance's spec-level streamability of a
// matrix spec: an extension operator's declared tier, else the built-in
// types.MatrixSpec.Streamable (type folded with params). A hidden type
// is not streamable. Nil-receiver-safe.
func (r *ExtensionRegistry) MatrixSpecStreamable(spec types.MatrixSpec) bool {
	if r.isHidden(string(spec.Type)) {
		return false
	}
	if op, ok := r.LookupMatrixOp(spec.Type); ok {
		return op.Streamable
	}
	return spec.Streamable()
}

// CustomMatrixNames returns the extension MAT_* names (map order).
func (r *ExtensionRegistry) CustomMatrixNames() []types.MatrixType {
	if r == nil {
		return nil
	}
	out := make([]types.MatrixType, 0, len(r.MatrixOps))
	for t := range r.MatrixOps {
		out = append(out, t)
	}
	return out
}

// buildMatrixOps builds one finalizer per extension plan of set (nil
// entries for built-ins), once per run: every bucket of a grouped run
// shares it (Finalize is re-entrant). The factory sees a copy of the
// stamped spec whose Params are the operator's own (the engine-owned
// keys stripped). A factory error is returned as is when coded, else as
// SERVICE_VALIDATION bad_params naming the slot.
func (s *matrixPlanSet) buildMatrixOps(req *types.Request) error {
	for i, plan := range s.plans {
		if !plan.Extension {
			continue
		}
		op, ok := s.exts.LookupMatrixOp(plan.Type)
		if !ok {
			return errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
				"extension matrix operator is not registered", map[string]any{"type": string(plan.Type)})
		}
		spec := req.Matrices[plan.Index]
		spec.Params = append(json.RawMessage(nil), plan.ExtParams...)
		fin, err := op.Factory(&spec, s.schema)
		if err != nil {
			var ce *errors.CodedError
			if stderrors.As(err, &ce) {
				return err
			}
			at := "matrices[" + strconv.Itoa(plan.Index) + "]"
			return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				at+" params are refused by "+string(plan.Type)+": "+err.Error(),
				map[string]any{"matrix": at, "reason": "bad_params", "type": string(plan.Type)})
		}
		if fin == nil {
			return errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
				"extension matrix factory returned no operator", map[string]any{"type": string(plan.Type)})
		}
		if s.extOps == nil {
			s.extOps = make([]MatrixOpFinalizer, len(s.plans))
			s.extComponents = make([]bool, len(s.plans))
		}
		s.extOps[i], s.extComponents[i] = fin, op.Components
	}
	return nil
}

// finalizeExtension runs the slot's extension finalizer over a clone of
// cm and lifts its output onto the built-in matrixOutput: the engine's
// co-moment data-quality warnings (thin rows, zero spread, the listwise
// heavy drop — the engine owns missing / max_drop_share) lead the
// operator's own, and its Components become the entry's operator keys.
// want reports whether the result parts are rendered at all (false on
// a components-only run, every Want* off).
func (m *matrixSlot) finalizeExtension(cm *linalg.CoMoment, want bool) (matrixOutput, error) {
	in := &MatrixOpInput{
		Name:          m.plan.Name,
		CoMoment:      cm.Clone(),
		Members:       append([]string(nil), m.cols...),
		Params:        append(json.RawMessage(nil), m.plan.ExtParams...),
		Encoding:      m.plan.Encoding,
		WantAuxiliary: want && m.compute.MatrixAuxiliary,
		WantVectors:   want && m.compute.MatrixVectors,
		WantScalars:   want && m.compute.MatrixScalars,
	}
	if in.Encoding == "" {
		in.Encoding = types.MatrixEncodingFull
	}
	if m.plan.ExplicitLabels {
		_, labels := m.plan.OutputMembers()
		in.Labels = labels
	}
	if m.weight != nil {
		in.WeightKind = m.weight.EffectiveKind()
	}
	if m.rows != nil {
		in.Rows = &matrixOpRows{b: m.rows}
	}
	out, err := m.extOp(in)
	if err != nil {
		return matrixOutput{}, err
	}
	return matrixOutput{
		Primary:   out.Primary,
		Auxiliary: out.Auxiliary,
		Vectors:   out.Vectors,
		Scalars:   out.Scalars,
		Warnings:  append(m.warnings(cm, nil), out.Warnings...),
		Operator:  out.Components,
	}, nil
}

// matrixOpRows is the read-only MatrixOpRows view of a buffered slot's
// rows: Row copies into a scratch slice (valid until the next call), so
// an operator writing it cannot reach the slot's store.
type matrixOpRows struct {
	b       *matrixRows
	scratch []float64
}

func (r *matrixOpRows) Len() int { return r.b.Len() }

func (r *matrixOpRows) Row(i int) []float64 {
	r.scratch = append(r.scratch[:0], r.b.Row(i)...)
	return r.scratch
}

func (r *matrixOpRows) Weight(i int) float64 { return r.b.Weight(i) }
