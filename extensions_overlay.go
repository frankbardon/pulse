package pulse

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/overlayhost"
	"github.com/frankbardon/pulse/internal/paramread"
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

// OverlayRegistration installs a custom OVERLAY_* kind, authored
// against extend.OverlayHandler: a read-only
// fold over an already-materialised host that returns one layer —
// exactly like a built-in kind, it never re-reads a record and never
// mutates the host result. The name follows the extension policy
// (OVERLAY_<NAMESPACE>_<NAME>; a built-in such as OVERLAY_SHARE_OF_ROW
// collides).
//
// A kind runs on the hosts whose handler it sets: Handler.Matrix on a
// crosstab request's Request.Overlays (and a Compose slot's own),
// Handler.Series on a grouped request's Request.Overlays (scored on its
// first aggregation), Handler.Compose on ComposedRequest.Overlays. A
// spec over a host the kind does not declare — and any spec on
// ChainRequest.Overlays or FacetRequest.Overlays — is refused at predict
// and at runtime alike (PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE, the
// wrong-shape / wrong-slot refusal a built-in gets).
//
// Every spec of the kind is judged declaratively, at predict and at
// runtime alike, before the handler runs: its params against Params
// (the shared params allowlist), the host shape against the handlers
// set, then Ref against Refs, Scope against Scopes, Level / Within
// (both must be zero), and last the optional Validate hook. A handler
// output is held to Shapes on every run (and at the probe):
// PULSE_EXTENSION_OVERLAY_OUTPUT_MISMATCH.
//
// pulse.New probes every handler once over a synthetic host — a
// two-by-two crosstab with components (Matrix), a three-group series
// (Series), a reference and a target slot of each of those shapes
// (Compose): a Validate / ValidateCompose refusal or panic on the probe
// spec, or a handler panic, is PULSE_EXTENSION_FACTORY_PANIC
// (details.stage validate / handler, details.host for a Series or
// Compose handler); an output its Shapes do not declare is
// PULSE_EXTENSION_OVERLAY_OUTPUT_MISMATCH. A handler error on a
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
	// DependsOn names the features this kind needs, each an AND edge;
	// see AggregatorRegistration.DependsOn. The kind is a feature like
	// any extension operator: its host group is the any-of set of the
	// hosts its handlers run on (Matrix → capability:crosstab; Series
	// and Compose → capability:compose), and a feature profile that
	// omits it hides it on every surface. Optional; extensions carry no
	// Since.
	DependsOn []string
	// Handler runs the kind, one function per host; at least one is
	// required (PULSE_EXTENSION_PARAM_INVALID, reason missing_handler).
	Handler extend.OverlayHandler
	// Shapes are the payload shapes the handler may return
	// (types.OverlayShapeScalar / Series / Matrix), each listed once;
	// at least one (PULSE_EXTENSION_PARAM_INVALID, reason
	// invalid_shape).
	Shapes []types.OverlayShape
	// Scopes are the scopes a spec may author (cell, row, column,
	// matrix, total, group — a built-in SERIES kind's scope), each
	// listed once; at least one
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
	// figures (MatrixOverlayHost.WeightKind, CellSumWeights, CellNEff)
	// and computes a correct layer under either weight kind. Without
	// it, a spec of the kind under ANY weight resolved on its slot
	// (slot → request → Options.DefaultWeight; on a Compose spec, a
	// request or default weight on a slot it reads) is
	// PULSE_EXTENSION_NOT_WEIGHT_AWARE at predict and at runtime alike;
	// `weight: null` on the spec opts it out.
	WeightAware bool
	// Inferential declares that the layer carries a test statistic /
	// p-value (OverlaySummary.Statistic / PValue). An Inferential
	// registration must declare an Interpretation
	// (PULSE_EXTENSION_PURPOSE_INVALID, part interpretation, rule
	// required).
	Inferential bool
	// EmitsPValues declares that the layer carries RAW p-values at its
	// payload shape's site — every present cell of a MATRIX layer (a
	// float64 p grid, NaN for an untestable cell), every SERIES entry's
	// Summary.PValue, a SCALAR layer's Summary.PValue — and requires
	// Inferential (PULSE_EXTENSION_PARAM_INVALID, reason
	// p_values_not_inferential). Under a `multiplicity` block they join
	// the request's correction family like a built-in inferential
	// kind's: the engine writes OverlayPayload.PAdjusted /
	// SignificantAdjusted (MATRIX) or the summaries' PAdjusted /
	// SignificantAdjusted beside the raw p, which never moves, and the
	// OverlayLayer.Multiplicity echo. A p-value off the site, outside
	// [0, 1], or on a registration not declaring EmitsPValues is
	// PULSE_EXTENSION_OVERLAY_OUTPUT_MISMATCH (reasons
	// misplaced_p_values, invalid_p_value, undeclared_p_values); the
	// probe also requires one p-value over its synthetic host
	// (missing_p_values).
	//
	// Every extension kind runs buffered only: a request carrying one
	// never takes the streaming path (PredictResult.Streamable is false
	// with the reason named).
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
	// ValidateCompose is Validate for a ComposedRequest.Overlays spec
	// (types.ComposeOverlaySpec), judged after the declarative rules
	// (the Compose handler, Scope, Level / Within) and before the
	// Reference / Targets gates, at predict and at runtime alike; the
	// probe calls it on the Compose probe spec. Nil accepts every spec.
	ValidateCompose extend.SpecValidator[types.ComposeOverlaySpec]
}

// knownExtensionOverlayScopes are the scopes a registration may declare.
var knownExtensionOverlayScopes = []types.OverlayScope{
	types.OverlayScopeCell, types.OverlayScopeRow, types.OverlayScopeColumn,
	types.OverlayScopeMatrix, types.OverlayScopeTotal, types.OverlayScopeGroup,
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
// the shared naming / duplicate / params pass, then at least one
// handler (reason missing_handler), well-formed Shapes / Scopes / Refs
// (reasons invalid_shape / invalid_scope / invalid_ref) and
// EmitsPValues only with Inferential (reason p_values_not_inferential),
// each
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
		if r.Handler.Matrix == nil && r.Handler.Series == nil && r.Handler.Compose == nil {
			return invalid("missing_handler", "sets no handler (set Handler.Matrix, Handler.Series or Handler.Compose)", "")
		}
		if v, ok := badEnumList(r.Shapes, knownExtensionOverlayShapes); !ok {
			return invalid("invalid_shape", "Shapes must list scalar, series or matrix, each once, at least one", v)
		}
		if v, ok := badEnumList(r.Scopes, knownExtensionOverlayScopes); !ok {
			return invalid("invalid_scope", "Scopes must list cell, row, column, matrix, total or group, each once, at least one", v)
		}
		if len(r.Refs) > 0 {
			if v, ok := badEnumList(r.Refs, knownExtensionOverlayRefs); !ok {
				return invalid("invalid_ref", "Refs must list none or margin, each once", v)
			}
		}
		if r.EmitsPValues && !r.Inferential {
			return invalid("p_values_not_inferential", "declares EmitsPValues without Inferential (a layer carrying p-values is inferential)", "")
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
		Kind:         r.Name,
		Shapes:       slices.Clone(r.Shapes),
		Scopes:       slices.Clone(r.Scopes),
		Refs:         []string{overlayhost.RefNone},
		WeightAware:  r.WeightAware,
		Inferential:  r.Inferential,
		EmitsPValues: r.EmitsPValues,
	}
	if r.Handler.Matrix != nil {
		d.HostShapes = append(d.HostShapes, types.OverlayShapeMatrix)
	}
	if r.Handler.Series != nil {
		d.HostShapes = append(d.HostShapes, types.OverlayShapeSeries)
	}
	d.Compose = r.Handler.Compose != nil
	if len(r.Refs) > 0 {
		d.Refs = make([]string, len(r.Refs))
		for i, ref := range r.Refs {
			d.Refs[i] = string(ref)
		}
	}
	if r.Validate != nil {
		d.Validate = r.Validate
	}
	if r.ValidateCompose != nil {
		d.ValidateCompose = r.ValidateCompose
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
		if reg.Handler.Series != nil {
			x.Series = adaptSeriesOverlay(reg.Handler.Series)
		}
		if reg.Handler.Compose != nil {
			x.Compose = adaptComposeOverlay(reg.Handler.Compose)
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
	snap.OverlayParams = map[string][]descriptor.Param{}
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
		if ps := matrixParamSnapshot(r.Params); ps != nil {
			snap.OverlayParams[name] = ps
		}
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

// adaptSeriesOverlay lifts the public SERIES handler onto the engine.
func adaptSeriesOverlay(fn extend.SeriesOverlayFunc) processing.ExtensionSeriesOverlayFunc {
	return func(spec *types.OverlaySpec, host *processing.SeriesHostView) (processing.ExtensionOverlayOutput, error) {
		out, err := fn(spec, seriesOverlayHost{h: host})
		if err != nil {
			return processing.ExtensionOverlayOutput{}, err
		}
		return processing.ExtensionOverlayOutput{Payload: out.Payload, Summary: out.Summary, Warnings: out.Warnings}, nil
	}
}

// adaptComposeOverlay lifts the public Compose handler onto the engine.
func adaptComposeOverlay(fn extend.ComposeOverlayFunc) processing.ExtensionComposeOverlayFunc {
	return func(spec *types.ComposeOverlaySpec, host *processing.ExtensionComposeHost) (processing.ExtensionOverlayOutput, error) {
		out, err := fn(spec, composeOverlayHost{h: host})
		if err != nil {
			return processing.ExtensionOverlayOutput{}, err
		}
		return processing.ExtensionOverlayOutput{Payload: out.Payload, Summary: out.Summary, Warnings: out.Warnings}, nil
	}
}

// seriesOverlayHost is the extend.SeriesOverlayHost over the engine's
// read-only series view. Every accessor returns a copy or a scalar.
type seriesOverlayHost struct{ h *processing.SeriesHostView }

func (s seriesOverlayHost) Groups() int                     { return s.h.GroupCount() }
func (s seriesOverlayHost) GroupFields() []string           { return slices.Clone(s.h.GroupFields()) }
func (s seriesOverlayHost) GrouperKinds() []types.GroupType { return slices.Clone(s.h.GrouperKinds()) }
func (s seriesOverlayHost) ValueLabel() string              { return s.h.ValueLabel() }
func (s seriesOverlayHost) Value(i int) (float64, bool)     { return s.h.ValueAt(i) }

func (s seriesOverlayHost) GroupKey(i int) types.AxisKey {
	k, ok := s.h.GroupKey(i)
	if !ok {
		return nil
	}
	return slices.Clone(k)
}

// composeOverlayHost is the extend.ComposeOverlayHost over the engine's
// resolved slots.
type composeOverlayHost struct {
	h *processing.ExtensionComposeHost
}

func (c composeOverlayHost) Reference() extend.ComposeOverlaySlot {
	return composeOverlaySlot{s: c.h.Reference}
}

func (c composeOverlayHost) Targets() int { return len(c.h.Targets) }

func (c composeOverlayHost) Target(i int) extend.ComposeOverlaySlot {
	if i < 0 || i >= len(c.h.Targets) {
		return nil
	}
	return composeOverlaySlot{s: c.h.Targets[i]}
}

// composeOverlaySlot is the extend.ComposeOverlaySlot over one resolved
// slot: the view its shape offers, nil for the others.
type composeOverlaySlot struct {
	s *processing.ExtensionComposeSlot
}

func (c composeOverlaySlot) Label() string             { return c.s.Label }
func (c composeOverlaySlot) Index() int                { return c.s.Index }
func (c composeOverlaySlot) Shape() types.OverlayShape { return c.s.Shape }
func (c composeOverlaySlot) Scalar() (float64, bool)   { return c.s.Scalar, c.s.ScalarOK }

func (c composeOverlaySlot) Matrix() extend.MatrixOverlayHost {
	if c.s.Matrix == nil {
		return nil
	}
	return matrixOverlayHost{h: c.s.Matrix}
}

func (c composeOverlaySlot) Series() extend.SeriesOverlayHost {
	if c.s.Series == nil {
		return nil
	}
	return seriesOverlayHost{h: c.s.Series}
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

// probeOverlays runs every overlay kind once per handler over the
// synthetic probe hosts: its Validate hook on the Request probe spec and
// its ValidateCompose hook on the Compose probe spec, then each handler,
// then the output rule.
func probeOverlays(regs []OverlayRegistration) error {
	for _, reg := range regs {
		decl := overlayDeclaration(reg)
		spec := probeOverlaySpec(reg)
		if len(decl.HostShapes) > 0 {
			cerr := overlayhost.ExtensionRequestRefusal(&decl, decl.HostShapes[0], false, spec, 0)
			if err := probeValidateRefusal(string(categoryOverlay), string(reg.Name), cerr, spec.Params, false); err != nil {
				return err
			}
		}
		var cspec *types.ComposeOverlaySpec
		if decl.Compose {
			cspec = probeComposeOverlaySpec(reg, spec)
			cerr := overlayhost.ExtensionComposeRefusal(&decl, cspec, 0)
			if err := probeValidateRefusal(string(categoryOverlay), string(reg.Name), cerr, spec.Params, false); err != nil {
				return err
			}
		}
		for _, run := range probeOverlayRuns(reg, spec, cspec) {
			out, ok, err := safeRunOverlay(reg, run.host, spec.Params, run.call)
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
	}
	return nil
}

// probeOverlaySpec is the Request probe spec: the kind, its first
// declared scope, its first declared Ref arm (a row margin for margin)
// and the probe params (probeParams).
func probeOverlaySpec(reg OverlayRegistration) *types.OverlaySpec {
	spec := &types.OverlaySpec{Kind: reg.Name, Scope: reg.Scopes[0], Params: probeParams(reg.Params, nil)}
	if len(reg.Refs) > 0 && reg.Refs[0] == extend.OverlayRefMargin {
		spec.Ref.Margin = &types.OverlayMarginRef{Axis: types.MarginAxisRow}
	}
	return spec
}

// probeComposeOverlaySpec is the Compose probe spec: the kind, its
// first declared scope, a "reference" and a "target" slot and the probe
// params as a params map.
func probeComposeOverlaySpec(reg OverlayRegistration, spec *types.OverlaySpec) *types.ComposeOverlaySpec {
	c := &types.ComposeOverlaySpec{Kind: reg.Name, Scope: reg.Scopes[0], Reference: "reference", Targets: []string{"target"}}
	if len(spec.Params) > 0 {
		_ = paramread.Probe(spec.Params, &c.Params) // probeParams marshals an object
	}
	return c
}

// probeOverlayRun is one handler call over one synthetic host; host
// names it in a panic's details ("" for the crosstab handler, whose
// details predate the others).
type probeOverlayRun struct {
	host string
	call func() (extend.OverlayOutput, error)
}

// probeOverlayRuns lists every handler call the probe makes: the
// crosstab handler over the probe crosstab, the SERIES handler over the
// probe series, the Compose handler over a reference and a target slot
// of the crosstab, then of the series.
func probeOverlayRuns(reg OverlayRegistration, spec *types.OverlaySpec, cspec *types.ComposeOverlaySpec) []probeOverlayRun {
	var runs []probeOverlayRun
	if fn := reg.Handler.Matrix; fn != nil {
		runs = append(runs, probeOverlayRun{call: func() (extend.OverlayOutput, error) {
			return fn(cloneProbeSpec(spec), matrixOverlayHost{h: probeMatrixView()})
		}})
	}
	if fn := reg.Handler.Series; fn != nil {
		runs = append(runs, probeOverlayRun{host: "series", call: func() (extend.OverlayOutput, error) {
			return fn(cloneProbeSpec(spec), seriesOverlayHost{h: probeSeriesView()})
		}})
	}
	if fn := reg.Handler.Compose; fn != nil {
		for _, shape := range []types.OverlayShape{types.OverlayShapeMatrix, types.OverlayShapeSeries} {
			runs = append(runs, probeOverlayRun{host: "compose", call: func() (extend.OverlayOutput, error) {
				return fn(cloneProbeComposeSpec(cspec), composeOverlayHost{h: probeComposeHost(shape)})
			}})
		}
	}
	return runs
}

func cloneProbeSpec(spec *types.OverlaySpec) *types.OverlaySpec {
	c := *spec
	c.Params = slices.Clone(spec.Params)
	return &c
}

func cloneProbeComposeSpec(spec *types.ComposeOverlaySpec) *types.ComposeOverlaySpec {
	c := *spec
	c.Targets = slices.Clone(spec.Targets)
	if spec.Params != nil {
		c.Params = maps.Clone(spec.Params)
	}
	return &c
}

// safeRunOverlay runs one probe handler call under recover(): a panic
// is PULSE_EXTENSION_FACTORY_PANIC, details stage handler (and host for
// a SERIES or Compose handler); an error is tolerated (ok false).
func safeRunOverlay(reg OverlayRegistration, host string, params json.RawMessage, call func() (extend.OverlayOutput, error)) (out extend.OverlayOutput, ok bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			out, ok = extend.OverlayOutput{}, false
			details := map[string]any{"category": string(categoryOverlay), "name": string(reg.Name),
				"stage": "handler", "panic": fmt.Sprintf("%v", r)}
			if host != "" {
				details["host"] = host
			}
			err = errors.NewCodedErrorWithDetails(errors.PULSE_EXTENSION_FACTORY_PANIC,
				fmt.Sprintf("overlay %q handler panicked during probe-validation: %v", reg.Name, r),
				withProbeParams(details, params))
		}
	}()
	o, herr := call()
	if herr != nil {
		return extend.OverlayOutput{}, false, nil
	}
	return o, true, nil
}

// probeMatrixView is the probe crosstab as a host view.
func probeMatrixView() *processing.CrosstabHostView {
	payload, comps := probeOverlayHost()
	return processing.NewCrosstabHostViewWithComponents(payload, comps)
}

// probeSeriesView is the synthetic probe series: three groups (a, b, c)
// of one GROUP_CATEGORY grouper on "group", scored 1, 2, 3 on "n".
func probeSeriesView() *processing.SeriesHostView {
	values := []float64{1, 2, 3}
	return processing.NewExtensionSeriesHostView([]types.AxisKey{{"a"}, {"b"}, {"c"}},
		func(i int) (float64, bool) { return values[i], true },
		[]string{"group"}, []types.GroupType{types.GROUP_CATEGORY}, "n")
}

// probeComposeHost is a reference and a target slot of the probe host
// of shape (matrix or series).
func probeComposeHost(shape types.OverlayShape) *processing.ExtensionComposeHost {
	slot := func(label string, index int) *processing.ExtensionComposeSlot {
		s := &processing.ExtensionComposeSlot{Label: label, Index: index, Shape: shape}
		if shape == types.OverlayShapeMatrix {
			s.Matrix = probeMatrixView()
		} else {
			s.Series = probeSeriesView()
		}
		return s
	}
	return &processing.ExtensionComposeHost{Reference: slot("reference", 0), Targets: []*processing.ExtensionComposeSlot{slot("target", 1)}}
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
