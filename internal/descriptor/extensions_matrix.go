package descriptor

import (
	"sort"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// extensions_matrix.go is the no-execute half of an embedder-registered
// MAT_* operator (pulse.MatrixOpRegistration): the snapshot facts
// resolution, predict, the weight resolver, the merge gate, the
// multiplicity resolver and the manifest read. The runtime twin is
// internal/processing/matrix_extension.go.

// ExtensionMatrixOp is one extension MAT_* operator's facts beyond its
// OperatorMeta projection (ExtensionsSnapshot.MatrixOps): the typed
// member types it accepts (empty: the built-in member types), its
// declared OutputKeys and the auxiliary key holding its p-values ("":
// it opts out of multiplicity correction).
type ExtensionMatrixOp struct {
	Accepts    []encoding.FieldType
	OutputKeys descriptor.MatrixOutputKeys
	PValues    string
	// Params are the operator's own params in the manifest's Param
	// shape; the engine-owned missing / max_drop_share are prepended by
	// the manifest projection (MatrixMemberParams).
	Params []descriptor.Param
}

// MatrixMemberTypes are the member column types every built-in matrix
// operator accepts — integer and float fields, plus packed_bool under
// the vector's coerce "binary" (internal/vectors.MemberTypeAllowed). An
// extension registration with no Accepts lists them in the manifest.
func MatrixMemberTypes() []string {
	return []string{"f32", "f64", "packed_bool", "u16", "u32", "u4", "u64", "u8"}
}

// MatrixMemberParams are the engine-owned params every matrix
// operator takes (internal/vectors.EngineMatrixParams): the
// missing-data mode and the listwise heavy-drop threshold.
func MatrixMemberParams() []descriptor.Param {
	return []descriptor.Param{
		{Name: "missing", Type: "enum", Required: false, Default: vectors.MissingListwise, EnumValues: vectors.MissingModes(), Description: "listwise drops a row with any member null; pairwise computes each pair over the rows where both are present and adds auxiliary.n (pairwise N)."},
		{Name: "max_drop_share", Type: "float", Required: false, Description: "Listwise only, no default: warn PULSE_MATRIX_LISTWISE_HEAVY_DROP when the share of rows dropped exceeds it (0 to 1)."},
	}
}

// matrixOp returns the extension MAT_* operator name's projection and
// facts. Nil-safe.
func (s *ExtensionsSnapshot) matrixOp(name string) (descriptor.OperatorMeta, ExtensionMatrixOp, bool) {
	if s == nil {
		return descriptor.OperatorMeta{}, ExtensionMatrixOp{}, false
	}
	m, ok := findMeta(s.MatrixOps, name)
	if !ok {
		return descriptor.OperatorMeta{}, ExtensionMatrixOp{}, false
	}
	return m, s.MatrixDetails[name], true
}

// matrixExtensionFor is the resolver's view of the instance's extension
// matrix operators (vectors.MatrixResolve.Extension): snap, else the
// instance's own projection; a name inst hides is no operator. The
// twin of processing.ExtensionRegistry.matrixExtension.
func matrixExtensionFor(inst *InstanceSnapshot, snap *ExtensionsSnapshot) func(types.MatrixType) (vectors.MatrixExtension, bool) {
	if snap == nil {
		snap = inst.Extensions()
	}
	if snap == nil || len(snap.MatrixOps) == 0 {
		return nil
	}
	return func(t types.MatrixType) (vectors.MatrixExtension, bool) {
		if inst.Hidden(string(t)) {
			return vectors.MatrixExtension{}, false
		}
		m, d, ok := snap.matrixOp(string(t))
		if !ok {
			return vectors.MatrixExtension{}, false
		}
		return vectors.MatrixExtension{Streamable: m.Streamable, Accepts: d.Accepts, Auxiliary: d.OutputKeys.Auxiliary}, true
	}
}

// isExtensionMatrixType reports whether t is an extension MAT_*
// operator the instance offers.
func isExtensionMatrixType(inst *InstanceSnapshot, snap *ExtensionsSnapshot, t types.MatrixType) bool {
	ext := matrixExtensionFor(inst, snap)
	if ext == nil {
		return false
	}
	_, ok := ext(t)
	return ok
}

// matrixSpecStreamable is predict's spec-level streamability of a
// matrix spec: an offered extension operator's declared tier, else the
// built-in types.MatrixSpec.Streamable of the routed type (a hidden
// type routes to "" and is not streamable). The twin of
// processing.ExtensionRegistry.MatrixSpecStreamable.
func matrixSpecStreamable(inst *InstanceSnapshot, snap *ExtensionsSnapshot, spec types.MatrixSpec) bool {
	if ext := matrixExtensionFor(inst, snap); ext != nil {
		if e, ok := ext(spec.Type); ok {
			return e.Streamable
		}
	}
	routed := spec
	routed.Type = opRoute(inst, spec.Type)
	return routed.Streamable()
}

// matrixPValueKey is the auxiliary key holding the p-values of matrix
// type t on the instance: "p" for a built-in inferential operator
// (vectors.InferenceTypes), an extension's declared PValues, else "".
func matrixPValueKey(inst *InstanceSnapshot, t types.MatrixType) string {
	routed := opRoute(inst, t)
	for _, k := range vectors.InferenceTypes() {
		if k == routed {
			return MatrixAuxP
		}
	}
	if routed == "" {
		return ""
	}
	if _, d, ok := inst.Extensions().matrixOp(string(t)); ok {
		return d.PValues
	}
	return ""
}

// MatrixAuxP is the built-in inferential matrix operators' p-value
// auxiliary key.
const MatrixAuxP = "p"

// MatrixAdjustedKeys are the auxiliary twins the multiplicity fold
// writes beside a p-value matrix stored under key: key+"_adjusted" (so
// "p" gives the built-ins' p_adjusted) and significant_adjusted.
func MatrixAdjustedKeys(key string) (adjusted, significant string) {
	return key + "_adjusted", "significant_adjusted"
}

func (f snapshotMergeFacts) Matrix(name string) (bool, bool) {
	if f.inst.Hidden(name) {
		return false, true
	}
	m, _, ok := f.s.matrixOp(name)
	return m.Streamable, ok
}

// extensionMatrixMetas projects the snapshot's extension MAT_*
// operators onto the manifest's MatrixMeta shape — the built-ins' —
// sorted by name: the declared description, member types (the built-in
// set when Accepts is empty), the engine-owned params then the
// operator's own, OutputKeys, the declared tier (Mergeable follows
// Streamable: the blocked merge carries a co-moment operator), the
// matrix floor plus the operator's ComponentSchema keys (Mergeable, as
// every matrix entry) and the intents its Purpose declares. weighting
// reports an instance offering capability:weighting: a WeightAware
// operator then carries the weighted floor keys and both weight kinds.
func extensionMatrixMetas(s *ExtensionsSnapshot, weighting bool) []descriptor.MatrixMeta {
	if s == nil || len(s.MatrixOps) == 0 {
		return []descriptor.MatrixMeta{}
	}
	out := make([]descriptor.MatrixMeta, 0, len(s.MatrixOps))
	for _, m := range sortOperatorMeta(s.MatrixOps) {
		d := s.MatrixDetails[m.Name]
		accepts := m.Accepts
		if len(accepts) == 0 {
			accepts = MatrixMemberTypes()
		}
		keys := matrixFloorKeys()
		if weighting && m.WeightAware {
			keys = append(keys, weightFloorKeys()...)
		}
		if own, ok := s.ComponentSchemas[m.Name]; ok {
			keys = append(keys, own.Keys...)
		}
		meta := descriptor.MatrixMeta{
			Name:            m.Name,
			Description:     m.Description,
			AcceptsTypes:    append([]string(nil), accepts...),
			Params:          append(MatrixMemberParams(), d.Params...),
			OutputKeys:      d.OutputKeys,
			Streamable:      m.Streamable,
			Mergeable:       m.Streamable,
			ComponentSchema: descriptor.ComponentSchema{Keys: keys, Mergeability: descriptor.Mergeable},
		}
		if p, ok := s.PurposeOf(m.Name); ok && len(p.Intents) > 0 {
			meta.Intents = append([]string(nil), p.Intents...)
			sort.Strings(meta.Intents)
		}
		if weighting && m.WeightAware {
			meta.WeightKinds = []types.WeightKind{types.WeightKindFrequency, types.WeightKindProbability}
		}
		out = append(out, meta)
	}
	return out
}

// extensionPValueMatrixNames lists the offered extension MAT_*
// operators that declare a p-value key, sorted.
func extensionPValueMatrixNames(inst *InstanceSnapshot) []string {
	snap := inst.Extensions()
	if snap == nil {
		return nil
	}
	var out []string
	for _, m := range snap.MatrixOps {
		if d := snap.MatrixDetails[m.Name]; d.PValues != "" && !inst.Hidden(m.Name) {
			out = append(out, m.Name)
		}
	}
	sort.Strings(out)
	return out
}
