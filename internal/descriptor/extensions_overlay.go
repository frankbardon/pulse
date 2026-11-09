package descriptor

import (
	"slices"
	"sort"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/overlayhost"
	"github.com/frankbardon/pulse/types"
)

// extensionOverlay returns the declared contract of the extension
// overlay kind the spec authors, nil for a built-in, unknown or hidden
// kind (a hidden kind's route is "", which no registration carries) —
// the descriptor twin of processing.ExtensionRegistry.extensionOverlay.
// Nil-receiver-safe.
func (o *PredictOptions) extensionOverlay(kind types.OverlayKind) *overlayhost.ExtensionDecl {
	route := o.overlayRoute(kind)
	snap := o.instance().Extensions()
	if route == "" || snap == nil {
		return nil
	}
	d, ok := snap.OverlayDetails[string(route)]
	if !ok {
		return nil
	}
	return &d
}

// extensionSlotRefusal is the wrong-slot refusal an extension overlay
// kind authored on host gets (overlayhost.ExtensionSlotRefusal), nil
// for a built-in, unknown or hidden kind or a slot the kind runs on —
// the descriptor twin of processing.ExtensionRegistry.extensionSlotRefusal,
// raised at the point each validator raises overlayhost.Refusal.
func (o *PredictOptions) extensionSlotRefusal(host overlayhost.Host, kind types.OverlayKind, index int) *errors.CodedError {
	if d := o.extensionOverlay(kind); d != nil {
		return overlayhost.ExtensionSlotRefusal(d, host, index)
	}
	return nil
}

// extensionOverlayDecl is the declared contract of the extension
// overlay kind on inst, nil for a built-in, unknown or hidden kind —
// extensionOverlay over a bare instance snapshot (the multiplicity
// resolver and the p-value count hold no PredictOptions).
func extensionOverlayDecl(inst *InstanceSnapshot, kind types.OverlayKind) *overlayhost.ExtensionDecl {
	if inst.Hidden(string(kind)) {
		return nil
	}
	snap := inst.Extensions()
	if snap == nil {
		return nil
	}
	d, ok := snap.OverlayDetails[string(kind)]
	if !ok {
		return nil
	}
	return &d
}

// readsCellValue is the instance's ReadsCellValue: whether kind, on a
// MATRIX Process host, reads the crosstab cell VALUES — an extension
// kind by its declaration (overlayhost.ExtensionReadsCellValue, U66
// #447), a built-in by overlayhost.ReadsCellValue; a hidden kind never
// (it is left to the kind rule).
func readsCellValue(inst *InstanceSnapshot, kind types.OverlayKind) bool {
	if inst.Hidden(string(kind)) {
		return false
	}
	if d := extensionOverlayDecl(inst, kind); d != nil {
		return overlayhost.ExtensionReadsCellValue(d, overlayhost.Request)
	}
	return overlayhost.ReadsCellValue(kind)
}

// composeReadsCellValue is the instance's ComposeReadsCellValue: an
// extension kind by its declaration, a built-in by
// overlayhost.ComposeReadsCellValue (cellMoments: the cell's components
// carry mean and variance).
func composeReadsCellValue(inst *InstanceSnapshot, kind types.OverlayKind, cellMoments bool) bool {
	if d := extensionOverlayDecl(inst, kind); d != nil {
		return overlayhost.ExtensionReadsCellValue(d, overlayhost.Compose)
	}
	return overlayhost.ComposeReadsCellValue(kind, cellMoments)
}

// ExtensionOverlayHostCapabilities returns the host capabilities the
// extension overlay kind d declares runs under, sorted:
// capability:crosstab for a Matrix handler, capability:compose for a
// Series or Compose handler (the SERIES host rides compose, as every
// built-in SERIES kind's does — overlayHostKinds). It is the any-of
// group the kind depends on as a feature, and the hosts whose overlay
// slots it keeps visible (overlayGate) — the extension twin of
// OverlayHostCapabilities.
func ExtensionOverlayHostCapabilities(d overlayhost.ExtensionDecl) []string {
	var out []string
	for _, s := range d.HostShapes {
		switch s {
		case types.OverlayShapeMatrix:
			out = append(out, featCrosstab)
		case types.OverlayShapeSeries:
			out = append(out, featCompose)
		}
	}
	if d.Compose {
		out = append(out, featCompose)
	}
	sort.Strings(out)
	return slices.Compact(out)
}

// extensionOverlayRunsOn reports whether inst carries an enabled
// extension overlay kind running under host (a host capability).
func extensionOverlayRunsOn(inst *InstanceSnapshot, host string) bool {
	snap := inst.Extensions()
	if snap == nil {
		return false
	}
	for name, d := range snap.OverlayDetails {
		if inst.Enabled(name) && slices.Contains(ExtensionOverlayHostCapabilities(d), host) {
			return true
		}
	}
	return false
}

// extensionOverlayCapabilities projects the snapshot's extension
// overlay kinds onto the built-in Manifest.Overlays entry shape, sorted
// by kind: shapes, scopes and Ref arms from the declared contract
// (OverlayDetails; a margin Ref is the OverlayRef field "Margin", none
// lists no field), Buffered always (an extension kind runs buffered
// only — overlayhost.StreamReasons), the declared params and
// Inferential, the Purpose's intents, and — where weighting is offered
// — both weight kinds for a WeightAware kind. Never nil.
func extensionOverlayCapabilities(s *ExtensionsSnapshot, weighting bool) []descriptor.OverlayCapability {
	if s == nil || len(s.Overlays) == 0 {
		return []descriptor.OverlayCapability{}
	}
	out := make([]descriptor.OverlayCapability, 0, len(s.Overlays))
	for _, m := range sortOperatorMeta(s.Overlays) {
		d := s.OverlayDetails[m.Name]
		c := descriptor.OverlayCapability{
			Kind:        types.OverlayKind(m.Name),
			Shapes:      slices.Clone(d.Shapes),
			Scopes:      slices.Clone(d.Scopes),
			RefKinds:    []string{},
			Buffered:    true,
			Description: m.Description,
			Inferential: d.Inferential,
			Params:      slices.Clone(s.OverlayParams[m.Name]),
		}
		for _, r := range d.Refs {
			if r == overlayhost.RefMargin {
				c.RefKinds = append(c.RefKinds, "Margin")
			}
		}
		slices.Sort(c.Shapes)
		slices.Sort(c.Scopes)
		if p, ok := s.PurposeOf(m.Name); ok && len(p.Intents) > 0 {
			c.Intents = append([]string(nil), p.Intents...)
			sort.Strings(c.Intents)
		}
		if weighting && d.WeightAware {
			c.WeightKinds = []types.WeightKind{types.WeightKindFrequency, types.WeightKindProbability}
		}
		out = append(out, c)
	}
	return out
}
