package pulse

import (
	"fmt"
	"slices"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/overlayhost"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/types"
)

// extensions_overlay.go lifts an OverlayRegistration onto the engine
// (processing.ExtensionOverlay), the no-execute snapshot
// (descx.ExtensionsSnapshot.Overlays / OverlayDetails) and pulse.New's
// probe. Both sides judge a spec of the kind by the one declaration
// overlayDeclaration builds (overlayhost.ExtensionRequestRefusal), so
// predict and runtime refuse it identically.

// OverlayRegistration installs a custom OVERLAY_* kind for
// Request.Overlays, authored against extend.OverlayHandler: a read-only
// fold over an already-materialised host that returns one layer —
// exactly like a built-in kind, it never re-reads a record and never
// mutates the host result. The name follows the extension policy
// (OVERLAY_<NAMESPACE>_<NAME>; a built-in such as OVERLAY_SHARE_OF_ROW
// collides).
//
// Today a kind runs on the crosstab host (Handler.Matrix): a
// Request.Crosstab request's Request.Overlays, and a Compose slot's own.
// On a Compose, chain or facet overlay slot it is an unknown kind.
//
// Every spec of the kind is judged declaratively, at predict and at
// runtime alike, before the handler runs: its params against Params
// (the shared params allowlist), the host shape against the handlers
// set, then Ref against Refs, Scope against Scopes, Level / Within
// (both must be zero), and last the optional Validate hook. A handler
// output is held to Shapes on every run (and at the probe):
// PULSE_EXTENSION_OVERLAY_OUTPUT_MISMATCH.
//
// pulse.New probes the kind once over a synthetic two-by-two crosstab
// host with components: a Validate refusal or panic on the probe spec,
// or a handler panic, is PULSE_EXTENSION_FACTORY_PANIC (details.stage
// validate / handler); an output its Shapes do not declare is
// PULSE_EXTENSION_OVERLAY_OUTPUT_MISMATCH. A handler error on the
// synthetic host is tolerated (it may be data-dependent).
type OverlayRegistration struct {
	Name        types.OverlayKind
	Description string
	// Purpose is the optional plain-language guidance for the kind,
	// validated at pulse.New with the built-in tier's rules
	// (PULSE_EXTENSION_PURPOSE_INVALID); see
	// MatrixOpRegistration.Purpose.
	Purpose *descriptor.Purpose
	// Interpretation optionally says how to read the layer's outputs,
	// structure-checked at pulse.New (PULSE_EXTENSION_PURPOSE_INVALID).
	Interpretation []descriptor.Interpretation
	// Handler runs the kind, one function per host. Handler.Matrix is
	// required today (PULSE_EXTENSION_PARAM_INVALID, reason
	// missing_handler).
	Handler extend.OverlayHandler
	// Shapes are the payload shapes the handler may return
	// (types.OverlayShapeScalar / Series / Matrix), each listed once;
	// at least one (PULSE_EXTENSION_PARAM_INVALID, reason
	// invalid_shape).
	Shapes []types.OverlayShape
	// Scopes are the scopes a spec may author (cell, row, column,
	// matrix, total), each listed once; at least one
	// (PULSE_EXTENSION_PARAM_INVALID, reason invalid_scope). A spec with
	// another scope is PULSE_OVERLAY_SCOPE_UNSUPPORTED.
	Scopes []types.OverlayScope
	// Refs are the Ref arms a spec may set: extend.OverlayRefNone (every
	// arm empty) and extend.OverlayRefMargin (Ref.Margin), each listed
	// once (PULSE_EXTENSION_PARAM_INVALID, reason invalid_ref). Empty
	// reads as [OverlayRefNone]. A spec setting another arm, more than
	// one, or none when OverlayRefNone is not listed is
	// PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE.
	Refs []extend.OverlayRefKind
	// Params declares the keys of the spec's params object. Declared, a
	// key outside the list is PULSE_REQUEST_UNKNOWN_FIELD and a wrong
	// type / missing Required / ExactlyOneOf breach
	// PULSE_REQUEST_PARAM_INVALID at overlays[i].params, at predict and
	// at runtime alike; undeclared (nil), params pass through unchecked.
	Params []ParamMeta
	// WeightAware declares that the handler reads the host's weighted
	// figures (MatrixOverlayHost.WeightKind, CellSumWeights, CellNEff).
	WeightAware bool
	// Inferential declares that the layer carries a test statistic /
	// p-value (OverlaySummary.Statistic / PValue), so its Interpretation
	// should read them.
	Inferential bool
	// EmitsPValues declares that the layer carries p-values.
	EmitsPValues bool
	// Validate is the optional spec-only refusal hook
	// (extend.SpecValidator): it judges a spec the declarative rules
	// accepted, at predict and at runtime alike, before the handler;
	// pulse.New's probe calls it on the probe spec (a refusal or panic
	// there is PULSE_EXTENSION_FACTORY_PANIC, details.stage validate). A
	// returned *errors.CodedError keeps its code; any other error, or a
	// panic, is PULSE_EXTENSION_SPEC_REFUSED {category: "overlay", type,
	// slot: "overlays"[, name][, panic]}. Nil accepts every spec.
	Validate extend.SpecValidator[types.OverlaySpec]
}

// knownExtensionOverlayScopes are the scopes a registration may declare
// (OverlayScopeGroup is reserved).
var knownExtensionOverlayScopes = []types.OverlayScope{
	types.OverlayScopeCell, types.OverlayScopeRow, types.OverlayScopeColumn,
	types.OverlayScopeMatrix, types.OverlayScopeTotal,
}

// knownExtensionOverlayShapes are the payload shapes a registration may
// declare.
var knownExtensionOverlayShapes = []types.OverlayShape{
	types.OverlayShapeScalar, types.OverlayShapeSeries, types.OverlayShapeMatrix,
}

// knownExtensionOverlayRefs are the Ref arms a registration may declare.
var knownExtensionOverlayRefs = []extend.OverlayRefKind{extend.OverlayRefNone, extend.OverlayRefMargin}

// builtinOverlayNames is every built-in overlay kind.
func builtinOverlayNames() map[string]struct{} {
	out := make(map[string]struct{}, 64)
	for _, k := range types.AllOverlayKinds() {
		out[string(k)] = struct{}{}
	}
	return out
}

// validateOverlayRegistrations enforces the OverlayRegistration shape:
// the shared naming / duplicate / params pass, then a crosstab handler
// (reason missing_handler) and well-formed Shapes / Scopes / Refs
// (reasons invalid_shape / invalid_scope / invalid_ref), each
// PULSE_EXTENSION_PARAM_INVALID {category: overlay, name, index,
// reason[, value]}.
func validateOverlayRegistrations(regs []OverlayRegistration) error {
	if err := validateOperatorSlice(regs, categoryOverlay, builtinOverlayNames(), func(r OverlayRegistration) (string, []ParamMeta) {
		return string(r.Name), r.Params
	}, nil); err != nil {
		return err
	}
	for i, r := range regs {
		invalid := func(reason, what, value string) error {
			details := map[string]any{"category": string(categoryOverlay), "name": string(r.Name), "index": i, "reason": reason}
			if value != "" {
				details["value"] = value
			}
			return errors.NewCodedErrorWithDetails(errors.PULSE_EXTENSION_PARAM_INVALID,
				fmt.Sprintf("extension overlay %q %s", r.Name, what), details)
		}
		if r.Handler.Matrix == nil {
			return invalid("missing_handler", "sets no handler (Handler.Matrix is required)", "")
		}
		if v, ok := badEnumList(r.Shapes, knownExtensionOverlayShapes); !ok {
			return invalid("invalid_shape", "Shapes must list scalar, series or matrix, each once, at least one", v)
		}
		if v, ok := badEnumList(r.Scopes, knownExtensionOverlayScopes); !ok {
			return invalid("invalid_scope", "Scopes must list cell, row, column, matrix or total, each once, at least one", v)
		}
		if len(r.Refs) > 0 {
			if v, ok := badEnumList(r.Refs, knownExtensionOverlayRefs); !ok {
				return invalid("invalid_ref", "Refs must list none or margin, each once", v)
			}
		}
	}
	return nil
}

// badEnumList reports whether list is non-empty with every entry in
// known and listed once; on failure it returns the offending value (""
// for an empty list).
func badEnumList[T ~string](list, known []T) (string, bool) {
	if len(list) == 0 {
		return "", false
	}
	seen := map[T]bool{}
	for _, v := range list {
		if !slices.Contains(known, v) || seen[v] {
			return string(v), false
		}
		seen[v] = true
	}
	return "", true
}

// overlayDeclaration is THE declared contract of r, the one value the
// runtime registry and the snapshot both carry.
func overlayDeclaration(r OverlayRegistration) overlayhost.ExtensionDecl {
	d := overlayhost.ExtensionDecl{
		Kind:   r.Name,
		Shapes: slices.Clone(r.Shapes),
		Scopes: slices.Clone(r.Scopes),
		Refs:   []string{overlayhost.RefNone},
	}
	if r.Handler.Matrix != nil {
		d.HostShapes = append(d.HostShapes, types.OverlayShapeMatrix)
	}
	if len(r.Refs) > 0 {
		d.Refs = make([]string, len(r.Refs))
		for i, ref := range r.Refs {
			d.Refs[i] = string(ref)
		}
	}
	if r.Validate != nil {
		d.Validate = r.Validate
	}
	return d
}

// addRuntimeOverlays records ext.Overlays on the runtime registry.
func addRuntimeOverlays(r *processing.ExtensionRegistry, regs []OverlayRegistration) {
	if len(regs) == 0 {
		return
	}
	r.Overlays = make(map[types.OverlayKind]*processing.ExtensionOverlay, len(regs))
	for _, reg := range regs {
		x := &processing.ExtensionOverlay{Decl: overlayDeclaration(reg)}
		if reg.Handler.Matrix != nil {
			x.Matrix = adaptMatrixOverlay(reg.Handler.Matrix)
		}
		r.Overlays[reg.Name] = x
	}
}

// addSnapshotOverlays projects ext.Overlays onto the snapshot: the
// generic OperatorMeta and the declared contract.
func addSnapshotOverlays(snap *descx.ExtensionsSnapshot, regs []OverlayRegistration) {
	if len(regs) == 0 {
		return
	}
	snap.OverlayDetails = make(map[string]overlayhost.ExtensionDecl, len(regs))
	for _, r := range regs {
		name := string(r.Name)
		snap.Overlays = append(snap.Overlays, descriptor.OperatorMeta{
			Name:        name,
			Namespace:   parseNamespace(name),
			Description: r.Description,
			WeightAware: r.WeightAware,
			Params:      paramMetaSnapshot(r.Params),
		})
		snap.OverlayDetails[name] = overlayDeclaration(r)
	}
}

// adaptMatrixOverlay lifts the public crosstab handler onto the engine:
// the handler reads the host through matrixOverlayHost's copying
// accessors.
func adaptMatrixOverlay(fn extend.MatrixOverlayFunc) processing.ExtensionMatrixOverlayFunc {
	return func(spec *types.OverlaySpec, host *processing.CrosstabHostView) (processing.ExtensionOverlayOutput, error) {
		out, err := fn(spec, matrixOverlayHost{h: host})
		if err != nil {
			return processing.ExtensionOverlayOutput{}, err
		}
		return processing.ExtensionOverlayOutput{Payload: out.Payload, Summary: out.Summary, Warnings: out.Warnings}, nil
	}
}

// matrixOverlayHost is the extend.MatrixOverlayHost over the engine's
// read-only crosstab view. Every accessor returns a copy or a scalar.
type matrixOverlayHost struct{ h *processing.CrosstabHostView }

func (m matrixOverlayHost) Rows() int    { return m.h.RowCount() }
func (m matrixOverlayHost) Columns() int { return m.h.ColumnCount() }

func (m matrixOverlayHost) RowHeader() types.AxisHeader {
	return copyAxisHeader(m.h.Payload().RowHeader)
}

func (m matrixOverlayHost) ColumnHeader() types.AxisHeader {
	return copyAxisHeader(m.h.Payload().ColumnHeader)
}

func copyAxisHeader(h types.AxisHeader) types.AxisHeader {
	return types.AxisHeader{Fields: slices.Clone(h.Fields), Types: slices.Clone(h.Types)}
}

func (m matrixOverlayHost) RowKey(r int) types.AxisKey {
	keys := m.h.Payload().RowKeys
	if r < 0 || r >= len(keys) {
		return nil
	}
	return slices.Clone(keys[r])
}

func (m matrixOverlayHost) ColumnKey(c int) types.AxisKey {
	keys := m.h.Payload().ColumnKeys
	if c < 0 || c >= len(keys) {
		return nil
	}
	return slices.Clone(keys[c])
}

func (m matrixOverlayHost) CellLabel() string { return m.h.Payload().CellLabel }

func (m matrixOverlayHost) Cell(r, c int) (float64, bool) { return m.h.CellAt(r, c) }

func (m matrixOverlayHost) RowMargin(r int) (float64, bool) {
	return m.h.MarginFor(types.MarginAxisRow, r, 0)
}

func (m matrixOverlayHost) ColumnMargin(c int) (float64, bool) {
	return m.h.MarginFor(types.MarginAxisColumn, 0, c)
}

func (m matrixOverlayHost) GrandTotal() (float64, bool) {
	return m.h.MarginFor(types.MarginAxisGrand, 0, 0)
}

func (m matrixOverlayHost) CellN(r, c int) (int, bool)      { return m.h.CellN(r, c) }
func (m matrixOverlayHost) RowMarginN(r int) (int, bool)    { return m.h.RowMarginN(r) }
func (m matrixOverlayHost) ColumnMarginN(c int) (int, bool) { return m.h.ColumnMarginN(c) }

func (m matrixOverlayHost) WeightKind() types.WeightKind {
	switch m.h.WeightBasis() {
	case weighting.Frequency:
		return types.WeightKindFrequency
	case weighting.Probability:
		return types.WeightKindProbability
	}
	return ""
}

func (m matrixOverlayHost) CellSumWeights(r, c int) (float64, bool) {
	return m.h.CellComponentFloat(r, c, "sum_weights")
}

func (m matrixOverlayHost) CellNEff(r, c int) (float64, bool) {
	return m.h.CellComponentFloat(r, c, "n_eff")
}

// probeOverlays runs every overlay kind once over the synthetic probe
// host: its Validate hook on the probe spec, then its crosstab handler,
// then the output rule.
func probeOverlays(regs []OverlayRegistration) error {
	for _, reg := range regs {
		spec := probeOverlaySpec(reg)
		decl := overlayDeclaration(reg)
		if err := safeValidateOverlay(reg, &decl, spec); err != nil {
			return err
		}
		out, ok, err := safeRunMatrixOverlay(reg, spec)
		if err != nil {
			return err
		}
		if !ok {
			continue // a handler error on the synthetic host is tolerated
		}
		if cerr := overlayhost.ExtensionOutputRefusal(&decl, &out.Payload, out.Summary, out.Warnings, -1); cerr != nil {
			return cerr
		}
	}
	return nil
}

// probeOverlaySpec is the probe spec: the kind, its first declared
// scope, its first declared Ref arm (a row margin for margin) and the
// probe params (probeParams).
func probeOverlaySpec(reg OverlayRegistration) *types.OverlaySpec {
	spec := &types.OverlaySpec{Kind: reg.Name, Scope: reg.Scopes[0], Params: probeParams(reg.Params, nil)}
	if len(reg.Refs) > 0 && reg.Refs[0] == extend.OverlayRefMargin {
		spec.Ref.Margin = &types.OverlayMarginRef{Axis: types.MarginAxisRow}
	}
	return spec
}

// safeValidateOverlay runs the kind's request rule — its Validate hook
// last — on the probe spec. A refusal or panic is the shared
// probeValidateRefusal shape (PULSE_EXTENSION_FACTORY_PANIC,
// details.stage validate), the bytes every other category's probe emits.
func safeValidateOverlay(reg OverlayRegistration, decl *overlayhost.ExtensionDecl, spec *types.OverlaySpec) error {
	cerr := overlayhost.ExtensionRequestRefusal(decl, types.OverlayShapeMatrix, false, spec, 0)
	return probeValidateRefusal(string(categoryOverlay), string(reg.Name), cerr, spec.Params, false)
}

// safeRunMatrixOverlay runs the crosstab handler over the probe host
// under recover(): a panic is PULSE_EXTENSION_FACTORY_PANIC, details
// stage handler; an error is tolerated (ok false).
func safeRunMatrixOverlay(reg OverlayRegistration, spec *types.OverlaySpec) (out extend.OverlayOutput, ok bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			out, ok = extend.OverlayOutput{}, false
			err = errors.NewCodedErrorWithDetails(errors.PULSE_EXTENSION_FACTORY_PANIC,
				fmt.Sprintf("overlay %q handler panicked during probe-validation: %v", reg.Name, r),
				withProbeParams(map[string]any{"category": string(categoryOverlay), "name": string(reg.Name),
					"stage": "handler", "panic": fmt.Sprintf("%v", r)}, spec.Params))
		}
	}()
	payload, comps := probeOverlayHost()
	host := matrixOverlayHost{h: processing.NewCrosstabHostViewWithComponents(payload, comps)}
	o, herr := reg.Handler.Matrix(spec, host)
	if herr != nil {
		return extend.OverlayOutput{}, false, nil
	}
	return o, true, nil
}

// probeOverlayHost is the synthetic probe crosstab: two rows (a, b) by
// two columns (x, y) of counts 1, 2 / 3, 4 with every margin, and the
// components floor (n per cell and margin).
func probeOverlayHost() (*types.MatrixPayload, *types.CrosstabComponents) {
	cell := func(v float64) types.MatrixCell { return types.MatrixCell{Value: v, Present: true} }
	payload := &types.MatrixPayload{
		RowHeader:     types.AxisHeader{Fields: []string{"row"}, Types: []string{string(types.GROUP_CATEGORY)}},
		ColumnHeader:  types.AxisHeader{Fields: []string{"column"}, Types: []string{string(types.GROUP_CATEGORY)}},
		RowKeys:       []types.AxisKey{{"a"}, {"b"}},
		ColumnKeys:    []types.AxisKey{{"x"}, {"y"}},
		Cells:         [][]types.MatrixCell{{cell(1), cell(2)}, {cell(3), cell(4)}},
		RowMargins:    []types.MatrixCell{cell(3), cell(7)},
		ColumnMargins: []types.MatrixCell{cell(4), cell(6)},
		GrandTotal:    cell(10),
		CellLabel:     "n",
	}
	n := func(v int) map[string]any { return map[string]any{"n": v, "n_null": 0} }
	comps := &types.CrosstabComponents{
		CellCounts:         [][]int{{1, 2}, {3, 4}},
		CellComponents:     [][]map[string]any{{n(1), n(2)}, {n(3), n(4)}},
		RowMarginCounts:    []int{3, 7},
		ColumnMarginCounts: []int{4, 6},
		GrandTotalCount:    10,
	}
	return payload, comps
}
