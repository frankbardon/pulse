package descriptor

import (
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// request_slots.go is the request-slot gate: a capability-gated slot
// the instance hides is refused exactly as an unrecognised top-level
// JSON key, with PULSE_REQUEST_UNKNOWN_FIELD in the strict-decode shape
// ({unknown_keys, suggestions, valid_keys}). A Go caller cannot misspell
// a slot, so "unknown" for a library caller means "set, but not offered
// by this instance"; the JSON caller and the Go caller see one error.
//
// The gated slots and their owning features:
//
//   - Request.Crosstab  — capability:crosstab
//   - Request.Joins     — capability:joins
//   - Request.Overlays  — an overlay host a plain Request reaches:
//     capability:crosstab (the MATRIX host) or capability:compose (the
//     SERIES host a grouped Process runs)
//   - ComposedRequest.Overlays — capability:compose
//   - ChainRequest.Overlays    — capability:process_chain
//   - FacetRequest.Overlays    — capability:facet
//   - Request.Weight and every nested per-slot `weight` (Aggregation —
//     so also the crosstab cell and margin_aggregations —, Test,
//     RegressionSpec, Attribute, OverlaySpec, Group — so also the
//     crosstab axes) — capability:weighting
//   - every `multiplicity` block (Request, Test, OverlaySpec — so also
//     a FacetRequest's overlays —, ComposeOverlaySpec, ComposedRequest)
//     — capability:multiplicity
//
// An overlay slot is visible iff at least one of its hosts is enabled
// AND that host lists at least one enabled overlay kind
// (overlayHostKinds). A host with no runnable kind offers nothing to put
// in the slot, so the slot is hidden with it. Nil / unscoped instances
// hide nothing.

// gatedSlot is one capability-gated top-level JSON slot.
type gatedSlot struct {
	key     string
	visible func(inst *InstanceSnapshot) bool
}

// capabilityGate makes a slot visible iff the capability is enabled.
func capabilityGate(capability string) func(*InstanceSnapshot) bool {
	return func(inst *InstanceSnapshot) bool { return inst.Enabled(capability) }
}

// overlayGate makes an overlay slot visible iff some host is enabled and
// lists at least one enabled overlay kind.
func overlayGate(hosts ...string) func(*InstanceSnapshot) bool {
	return func(inst *InstanceSnapshot) bool {
		for _, h := range hosts {
			if !inst.Enabled(h) {
				continue
			}
			for _, k := range overlayHostKinds[h] {
				if inst.Enabled(k) {
					return true
				}
			}
		}
		return false
	}
}

// weightKey is the JSON key of the request-root weight and of every
// per-slot weight; all of them ride capability:weighting.
const weightKey = "weight"

// weightSlotGate is the one gate every `weight` key shares.
var weightSlotGate = gatedSlot{key: weightKey, visible: capabilityGate(featWeighting)}

// multiplicityKey is the JSON key of every multiple-comparison block;
// all of them ride capability:multiplicity.
const multiplicityKey = "multiplicity"

// multiplicitySlotGate is the one gate every `multiplicity` key shares.
var multiplicitySlotGate = gatedSlot{key: multiplicityKey, visible: capabilityGate(featMultiplicity)}

// gatedSlots maps each request root (by its Go type) to its gated
// slots, plus the nested slot objects that carry a gated key of their
// own (the per-slot `weight`). The nested entries are what the payload
// schema (structSchema reads HiddenSlotKeys per struct) and the nested
// refusal (nestedWeightRefusal) consult.
var gatedSlots = map[reflect.Type][]gatedSlot{
	reflect.TypeOf(types.Request{}): {
		{key: "crosstab", visible: capabilityGate(featCrosstab)},
		{key: "joins", visible: capabilityGate(featJoins)},
		{key: "overlays", visible: overlayGate(featCrosstab, featCompose)},
		{key: "vectors", visible: capabilityGate(featMatrices)},
		{key: "matrices", visible: capabilityGate(featMatrices)},
		weightSlotGate,
		multiplicitySlotGate,
	},
	reflect.TypeOf(types.Aggregation{}):        {weightSlotGate},
	reflect.TypeOf(types.Test{}):               {weightSlotGate, multiplicitySlotGate},
	reflect.TypeOf(types.RegressionSpec{}):     {weightSlotGate},
	reflect.TypeOf(types.MatrixSpec{}):         {weightSlotGate},
	reflect.TypeOf(types.Attribute{}):          {weightSlotGate},
	reflect.TypeOf(types.OverlaySpec{}):        {weightSlotGate, multiplicitySlotGate},
	reflect.TypeOf(types.Group{}):              {weightSlotGate},
	reflect.TypeOf(types.ComposeOverlaySpec{}): {multiplicitySlotGate},
	// The adjusted outputs a correction writes beside a test's raw p
	// ride the same capability, so a hidden instance's payload schema
	// names no multiplicity surface at all (no request ever reaches
	// them there).
	reflect.TypeOf(types.TestResult{}): {
		multiplicitySlotGate,
		{key: "p_adjusted", visible: capabilityGate(featMultiplicity)},
		{key: "significant_adjusted", visible: capabilityGate(featMultiplicity)},
	},
	// The overlay twins: the per-summary adjusted figures, the
	// parallel adjusted MATRIX payloads and the per-layer echo.
	reflect.TypeOf(types.OverlaySummary{}): {
		{key: "p_adjusted", visible: capabilityGate(featMultiplicity)},
		{key: "significant_adjusted", visible: capabilityGate(featMultiplicity)},
	},
	reflect.TypeOf(types.OverlayPayload{}): {
		{key: "p_adjusted", visible: capabilityGate(featMultiplicity)},
		{key: "significant_adjusted", visible: capabilityGate(featMultiplicity)},
	},
	reflect.TypeOf(types.OverlayLayer{}): {multiplicitySlotGate},
	// The matrix results ride capability:matrices with the request
	// slots that produce them.
	reflect.TypeOf(types.Response{}): {
		{key: "matrices", visible: capabilityGate(featMatrices)},
	},
	reflect.TypeOf(types.ResponseComponents{}): {
		{key: "matrices", visible: capabilityGate(featMatrices)},
	},
	reflect.TypeOf(types.ComposedRequest{}): {
		{key: "overlays", visible: overlayGate(featCompose)},
		multiplicitySlotGate,
	},
	reflect.TypeOf(types.ChainRequest{}): {
		{key: "overlays", visible: overlayGate(featProcessChain)},
	},
	reflect.TypeOf(types.FacetRequest{}): {
		{key: "overlays", visible: overlayGate(featFacet)},
	},
	// Explain carries every other root as a slot: a root whose
	// capability is hidden is not a slot of it, request and result alike.
	reflect.TypeOf(descriptor.ExplainRequest{}): {
		{key: "composed", visible: capabilityGate(featCompose)},
		{key: "composed_response", visible: capabilityGate(featCompose)},
		{key: "chain", visible: capabilityGate(featProcessChain)},
		{key: "chain_response", visible: capabilityGate(featProcessChain)},
		{key: "facet", visible: capabilityGate(featFacet)},
		{key: "facet_result", visible: capabilityGate(featFacet)},
		{key: "sample", visible: capabilityGate(featSample)},
	},
}

// structType resolves sample (a struct or a pointer to one) to its
// struct type, or nil.
func structType(sample any) reflect.Type {
	t := reflect.TypeOf(sample)
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil || t.Kind() != reflect.Struct {
		return nil
	}
	return t
}

// JSONObjectKeys returns the top-level JSON object keys declared by the
// struct (or pointer to struct) sample, read from each exported field's
// json tag in declaration order. Fields tagged "-" are skipped; an
// untagged field contributes its Go name.
func JSONObjectKeys(sample any) []string {
	t := structType(sample)
	if t == nil {
		return nil
	}
	keys := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		if name == "" {
			name = f.Name
		}
		keys = append(keys, name)
	}
	return keys
}

// HiddenSlotKeys returns the top-level JSON keys of the request root
// sample (types.Request, ComposedRequest, ChainRequest, FacetRequest —
// struct or pointer) that inst hides, sorted. Nil when none is hidden,
// on a nil / unscoped instance, or for a root with no gated slot. The
// payload-schema and manifest filters drop exactly these properties.
func HiddenSlotKeys(sample any, inst *InstanceSnapshot) []string {
	var out []string
	for _, g := range gatedSlots[structType(sample)] {
		if !g.visible(inst) {
			out = append(out, g.key)
		}
	}
	sort.Strings(out)
	return out
}

// VisibleSlotKeys returns JSONObjectKeys(sample) minus HiddenSlotKeys,
// in declaration order — the instance's valid top-level keys.
func VisibleSlotKeys(sample any, inst *InstanceSnapshot) []string {
	hidden := HiddenSlotKeys(sample, inst)
	keys := JSONObjectKeys(sample)
	if len(hidden) == 0 {
		return keys
	}
	out := keys[:0]
	for _, k := range keys {
		if !slices.Contains(hidden, k) {
			out = append(out, k)
		}
	}
	return out
}

// UnknownFieldError builds the PULSE_REQUEST_UNKNOWN_FIELD refusal for
// the unrecognised top-level keys unknown against the valid key set
// valid: details {unknown_keys (sorted), suggestions (unknown key →
// nearest valid key, when one is close), valid_keys (sorted)} and the
// message naming both lists. Nil when unknown is empty. It is THE shape
// of the error — the MCP strict decoder and the slot gate both build it
// here, so a hidden slot and an unknown key cannot drift apart.
func UnknownFieldError(unknown, valid []string) *errors.CodedError {
	return unknownFieldErrorAt("", unknown, valid)
}

// unknownFieldErrorAt is UnknownFieldError for the keys of the object at
// path inside the request ("aggregations[0]", "crosstab.cell"); "" is
// the request root (UnknownFieldError's message, byte-identical). A
// nested refusal names the object in its message and carries
// details.path; valid_keys / suggestions range over THAT object's keys.
func unknownFieldErrorAt(path string, unknown, valid []string) *errors.CodedError {
	if len(unknown) == 0 {
		return nil
	}
	unknown = append([]string(nil), unknown...)
	sort.Strings(unknown)
	validList := append([]string(nil), valid...)
	sort.Strings(validList)

	suggestions := make(map[string]any, len(unknown))
	parts := make([]string, 0, len(unknown))
	for _, k := range unknown {
		if near := NearestKey(k, valid); near != "" {
			suggestions[k] = near
			parts = append(parts, `"`+k+`" (did you mean "`+near+`"?)`)
		} else {
			parts = append(parts, `"`+k+`"`)
		}
	}

	where, validWhere := "top-level key(s): ", "Valid request keys: "
	if path != "" {
		where, validWhere = "key(s) in "+path+": ", "Valid keys there: "
	}
	msg := "request contains unrecognized " + where + strings.Join(parts, ", ") +
		". Unknown keys are ignored during decode, so the intended operation is silently dropped. " + validWhere +
		strings.Join(validList, ", ") + "."

	details := map[string]any{
		"unknown_keys": unknown,
		"suggestions":  suggestions,
		"valid_keys":   validList,
	}
	if path != "" {
		details["path"] = path
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_REQUEST_UNKNOWN_FIELD, msg, details)
}

// NearestKey returns the closest candidate to k by Levenshtein distance,
// or "" when no candidate is close enough. The acceptance threshold is
// edit distance < 4, further capped at the key length, so short or
// unrelated keys do not yield a misleading suggestion. Ties keep the
// earliest candidate.
func NearestKey(k string, candidates []string) string {
	limit := min(4, len(k))
	best := ""
	bestDist := limit
	for _, c := range candidates {
		if d := levenshtein(k, c); d < bestDist {
			bestDist = d
			best = c
		}
	}
	return best
}

// setSlots returns the gated keys a root actually sets (non-nil /
// non-empty). Only the gated slots are probed.
func setSlots(v any) []string {
	var out []string
	switch r := v.(type) {
	case *types.Request:
		if r == nil {
			return nil
		}
		if r.Crosstab != nil {
			out = append(out, "crosstab")
		}
		if len(r.Joins) > 0 {
			out = append(out, "joins")
		}
		if len(r.Overlays) > 0 {
			out = append(out, "overlays")
		}
		if len(r.Vectors) > 0 {
			out = append(out, "vectors")
		}
		if len(r.Matrices) > 0 {
			out = append(out, "matrices")
		}
		if r.Weight != nil {
			out = append(out, weightKey)
		}
		if r.Multiplicity != nil {
			out = append(out, multiplicityKey)
		}
	case *types.ComposedRequest:
		if r != nil && len(r.Overlays) > 0 {
			out = append(out, "overlays")
		}
		if r != nil && r.Multiplicity != nil {
			out = append(out, multiplicityKey)
		}
	case *types.ChainRequest:
		if r != nil && len(r.Overlays) > 0 {
			out = append(out, "overlays")
		}
	case *types.FacetRequest:
		if r != nil && len(r.Overlays) > 0 {
			out = append(out, "overlays")
		}
	}
	return out
}

// rootSlotRefusal gates ONE root's own top-level slots (no nesting).
func rootSlotRefusal(v any, inst *InstanceSnapshot) *errors.CodedError {
	if !inst.Scoped() {
		return nil
	}
	hidden := HiddenSlotKeys(v, inst)
	if len(hidden) == 0 {
		return nil
	}
	var unknown []string
	for _, k := range setSlots(v) {
		if slices.Contains(hidden, k) {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	return UnknownFieldError(unknown, VisibleSlotKeys(v, inst))
}

// nestedWeightRefusal refuses the first per-slot `weight` set inside
// req when inst hides capability:weighting, in the resolver's slot
// order (weightSlots): aggregations, crosstab cell, crosstab margin
// aggregations, tests, post-tests, regressions, attributes, overlays,
// groups, crosstab rows, crosstab columns. A set weight is any non-zero
// SlotWeight — an explicit `null` included, as strict decode refuses
// any set key. The refusal is PULSE_REQUEST_UNKNOWN_FIELD at that
// object (details.path), valid_keys over its visible keys.
func nestedWeightRefusal(req *types.Request, inst *InstanceSnapshot) *errors.CodedError {
	if req == nil || inst.Enabled(featWeighting) {
		return nil
	}
	at := func(path string, sample any) *errors.CodedError {
		return unknownFieldErrorAt(path, []string{weightKey}, VisibleSlotKeys(sample, inst))
	}
	for i, a := range req.Aggregations {
		if a != nil && !a.Weight.IsZero() {
			return at(fmt.Sprintf("aggregations[%d]", i), a)
		}
	}
	if ct := req.Crosstab; ct != nil {
		if ct.Cell != nil && !ct.Cell.Weight.IsZero() {
			return at("crosstab.cell", ct.Cell)
		}
		for i, a := range ct.MarginAggregations {
			if a != nil && !a.Weight.IsZero() {
				return at(fmt.Sprintf("crosstab.margin_aggregations[%d]", i), a)
			}
		}
	}
	for _, tier := range []struct {
		prefix string
		tests  []*types.Test
	}{{"tests", req.Tests}, {"post_tests", req.PostTests}} {
		for i, t := range tier.tests {
			if t != nil && !t.Weight.IsZero() {
				return at(fmt.Sprintf("%s[%d]", tier.prefix, i), t)
			}
		}
	}
	for i, r := range req.Regressions {
		if r != nil && !r.Weight.IsZero() {
			return at(fmt.Sprintf("regressions[%d]", i), r)
		}
	}
	for i := range req.Matrices {
		if !req.Matrices[i].Weight.IsZero() {
			return at(fmt.Sprintf("matrices[%d]", i), &req.Matrices[i])
		}
	}
	for i, a := range req.Attributes {
		if a != nil && !a.Weight.IsZero() {
			return at(fmt.Sprintf("attributes[%d]", i), a)
		}
	}
	if ce := overlayWeightRefusal(req.Overlays, inst); ce != nil {
		return ce
	}
	groups := func(prefix string, gs []*types.Group) *errors.CodedError {
		for i, g := range gs {
			if g != nil && !g.Weight.IsZero() {
				return at(fmt.Sprintf("%s[%d]", prefix, i), g)
			}
		}
		return nil
	}
	if ce := groups("groups", req.Groups); ce != nil {
		return ce
	}
	if ct := req.Crosstab; ct != nil {
		if ce := groups("crosstab.rows", ct.Rows); ce != nil {
			return ce
		}
		if ce := groups("crosstab.columns", ct.Columns); ce != nil {
			return ce
		}
	}
	return nil
}

// overlayWeightRefusal is nestedWeightRefusal over one overlays list
// (a Request's or a FacetRequest's).
func overlayWeightRefusal(overlays []types.OverlaySpec, inst *InstanceSnapshot) *errors.CodedError {
	if inst.Enabled(featWeighting) {
		return nil
	}
	for i := range overlays {
		if !overlays[i].Weight.IsZero() {
			return unknownFieldErrorAt(fmt.Sprintf("overlays[%d]", i), []string{weightKey},
				VisibleSlotKeys(&overlays[i], inst))
		}
	}
	return nil
}

// nestedMultiplicityRefusal refuses the first `multiplicity` block set
// inside req when inst hides capability:multiplicity, in the
// resolver's slot order: tests, post-tests, overlays. The refusal is
// PULSE_REQUEST_UNKNOWN_FIELD at that object (details.path), valid_keys
// over its visible keys.
func nestedMultiplicityRefusal(req *types.Request, inst *InstanceSnapshot) *errors.CodedError {
	if req == nil || inst.Enabled(featMultiplicity) {
		return nil
	}
	for _, tier := range []struct {
		prefix string
		tests  []*types.Test
	}{{"tests", req.Tests}, {"post_tests", req.PostTests}} {
		for i, t := range tier.tests {
			if t != nil && t.Multiplicity != nil {
				return unknownFieldErrorAt(fmt.Sprintf("%s[%d]", tier.prefix, i), []string{multiplicityKey},
					VisibleSlotKeys(t, inst))
			}
		}
	}
	return overlayMultiplicityRefusal(req.Overlays, inst)
}

// overlayMultiplicityRefusal is nestedMultiplicityRefusal over one
// overlays list (a Request's or a FacetRequest's).
func overlayMultiplicityRefusal(overlays []types.OverlaySpec, inst *InstanceSnapshot) *errors.CodedError {
	if inst.Enabled(featMultiplicity) {
		return nil
	}
	for i := range overlays {
		if overlays[i].Multiplicity != nil {
			return unknownFieldErrorAt(fmt.Sprintf("overlays[%d]", i), []string{multiplicityKey},
				VisibleSlotKeys(&overlays[i], inst))
		}
	}
	return nil
}

// composeOverlayMultiplicityRefusal is overlayMultiplicityRefusal over
// a ComposedRequest's Compose-host overlays.
func composeOverlayMultiplicityRefusal(overlays []types.ComposeOverlaySpec, inst *InstanceSnapshot) *errors.CodedError {
	if inst.Enabled(featMultiplicity) {
		return nil
	}
	for i := range overlays {
		if overlays[i].Multiplicity != nil {
			return unknownFieldErrorAt(fmt.Sprintf("overlays[%d]", i), []string{multiplicityKey},
				VisibleSlotKeys(&overlays[i], inst))
		}
	}
	return nil
}

// requestSlotRefusal gates one Request: its own top-level slots, then
// every nested slot `weight`, then every nested `multiplicity`.
func requestSlotRefusal(req *types.Request, inst *InstanceSnapshot) *errors.CodedError {
	if ce := rootSlotRefusal(req, inst); ce != nil {
		return ce
	}
	if ce := nestedWeightRefusal(req, inst); ce != nil {
		return ce
	}
	return nestedMultiplicityRefusal(req, inst)
}

// SlotRefusal is the request-slot gate. v is a *types.Request,
// *types.ComposedRequest, *types.ChainRequest or *types.FacetRequest
// (any other value — a *types.SampleRequest, nil — passes). It returns
// PULSE_REQUEST_UNKNOWN_FIELD when v sets a slot inst hides, reporting
// every hidden slot set on the first offending root:
//
//   - the root's own slots first;
//   - then the nested per-slot `weight` keys (nestedWeightRefusal) of a
//     Request, or of a FacetRequest's overlays, then the nested
//     `multiplicity` blocks (nestedMultiplicityRefusal) — for a
//     ComposedRequest, its Compose-host overlays' first;
//   - then, for a ComposedRequest, each Requests[i] in order with
//     details.request = i; for a ChainRequest each Stages[i].Request
//     with details.stage = i (the keys the service's located refusals
//     use, descx.RefusalAt).
//
// valid_keys and suggestions range over the offending root's VISIBLE
// keys only. Nil on a nil / unscoped instance. Every service funnel and
// Predict run it before any other request check, so a hidden slot is
// refused at the point an unknown JSON key would have been.
func SlotRefusal(v any, inst *InstanceSnapshot) error {
	if !inst.Scoped() {
		return nil
	}
	if ce := rootSlotRefusal(v, inst); ce != nil {
		return ce
	}
	switch r := v.(type) {
	case *types.Request:
		if ce := nestedWeightRefusal(r, inst); ce != nil {
			return ce
		}
		if ce := nestedMultiplicityRefusal(r, inst); ce != nil {
			return ce
		}
	case *types.FacetRequest:
		if r == nil {
			return nil
		}
		if ce := overlayWeightRefusal(r.Overlays, inst); ce != nil {
			return ce
		}
		if ce := overlayMultiplicityRefusal(r.Overlays, inst); ce != nil {
			return ce
		}
	case *types.ComposedRequest:
		if r == nil {
			return nil
		}
		if ce := composeOverlayMultiplicityRefusal(r.Overlays, inst); ce != nil {
			return ce
		}
		for i, req := range r.Requests {
			if ce := requestSlotRefusal(req, inst); ce != nil {
				return RefusalAt(ce, "request", i)
			}
		}
	case *types.ChainRequest:
		if r == nil {
			return nil
		}
		for i, st := range r.Stages {
			if st == nil {
				continue
			}
			if ce := requestSlotRefusal(st.Request, inst); ce != nil {
				return RefusalAt(ce, "stage", i)
			}
		}
	}
	return nil
}
