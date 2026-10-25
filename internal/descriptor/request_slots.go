package descriptor

import (
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/strictjson"
	"github.com/frankbardon/pulse/internal/sweep"
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
//   - CrosstabSpec.Cells (+ the MatrixPayload cell_labels / measures and
//     CrosstabComponents measures it produces) — capability:crosstab_cells
//   - Request.Joins     — capability:joins
//   - Request.Overlays  — an overlay host a plain Request reaches:
//     capability:crosstab (the MATRIX host) or capability:compose (the
//     SERIES host a grouped Process runs)
//   - ComposedRequest.Overlays — capability:compose
//   - ComposedRequest.Sweep    — capability:compose_sweep
//   - the sampled sweep's `samples` / `rng_seed` (SweepSpec), every
//     axis `range` (SweepAxis), the random / lhs modes and the
//     ComposedResponse.SweepSeed echo — capability:compose_sweep_sampling
//   - ComposedRequest.Banner   — capability:compose_banner (+ the
//     ComposedResponse.Banner it produces)
//   - ComposedRequest.Optimize — capability:compose_optimize (+ the
//     ComposedResponse.Optimizations it produces)
//   - ComposedRequest.Fits     — capability:compose_fits (each fit
//     request's own slots are gated like a slot's, details.fit)
//   - ChainRequest.Overlays    — capability:process_chain
//   - FacetRequest.Overlays    — capability:facet
//   - Request.Weight and every nested per-slot `weight` (Aggregation —
//     so also the crosstab cell and margin_aggregations —, Test,
//     RegressionSpec, Attribute, OverlaySpec, Group — so also the
//     crosstab axes) — capability:weighting
//   - every `multiplicity` block (Request, Test, OverlaySpec — so also
//     a FacetRequest's overlays —, ComposeOverlaySpec, ComposedRequest)
//     — capability:multiplicity
//   - every item `for_each` (Window, Feature, Attribute) —
//     capability:for_each
//   - every `save` (RegressionSpec, FitSpec) — capability:model_save
//   - every regression `holdout` (+ the RegressionResult block) —
//     capability:regression_holdout
//   - Request.Models and FacetRequest.Models — capability:models
//   - Request.Populations — capability:models
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

// forEachKey is the JSON key of the item fan-out on a window, feature
// or attribute; every one rides capability:for_each.
const forEachKey = "for_each"

// forEachSlotGate is the one gate every `for_each` key shares.
var forEachSlotGate = gatedSlot{key: forEachKey, visible: capabilityGate(featForEach)}

// saveKey is the JSON key of a model file save on a RegressionSpec and
// a ComposedRequest `fits` entry; both ride capability:model_save.
const saveKey = "save"

// modelSaveSlotGate is the one gate every `save` key shares.
var modelSaveSlotGate = gatedSlot{key: saveKey, visible: capabilityGate(featModelSave)}

// holdoutSlotGate gates the `holdout` key on a RegressionSpec and the
// RegressionResult block it produces: capability:regression_holdout.
var holdoutSlotGate = gatedSlot{key: "holdout", visible: capabilityGate(featRegHoldout)}

// modelsSlotGate gates the `models` key on a Request and a
// FacetRequest; both ride capability:models.
var modelsSlotGate = gatedSlot{key: "models", visible: capabilityGate(featModels)}

// populationsSlotGate gates the `populations` key on a Request: a
// population file loads into the same bound-model namespace, so it
// rides capability:models too.
var populationsSlotGate = gatedSlot{key: "populations", visible: capabilityGate(featModels)}

// paramsSlotGate gates the `params` key on a Request, a
// ComposedRequest and a ChainRequest (the top-level block every
// sub-request inherits): capability:params. A nested operator's own `params` (a filterer's, an
// overlay's) is a different key on a different struct and never gated.
var paramsSlotGate = gatedSlot{key: "params", visible: capabilityGate(featParams)}

// crosstabCellsSlotGate gates the `cells` key on a CrosstabSpec (the
// multi-measure crosstab): capability:crosstab_cells.
var crosstabCellsSlotGate = gatedSlot{key: "cells", visible: capabilityGate(featCrosstabCells)}

// requestOverlaysSlotGate gates the `overlays` key on a Request and on
// the Response it produces: visible iff a request-level overlay host
// (the crosstab MATRIX host, the Compose series host or the
// MATRIX_RESULT host a Request.Matrices request produces) is enabled
// with one of its kinds.
var requestOverlaysSlotGate = gatedSlot{key: "overlays", visible: overlayGate(featCrosstab, featCompose, featMatrices)}

// sweepSamplingGate gates every key only a sampled sweep (mode random /
// lhs) reads or writes: capability:compose_sweep_sampling.
var sweepSamplingGate = capabilityGate(featSweepSampling)

// gatedSlots maps each request root (by its Go type) to its gated
// slots, plus the nested slot objects that carry a gated key of their
// own (the per-slot `weight`). The nested entries are what the payload
// schema (structSchema reads HiddenSlotKeys per struct) and the nested
// refusal (nestedWeightRefusal) consult.
var gatedSlots = map[reflect.Type][]gatedSlot{
	reflect.TypeOf(types.Request{}): {
		{key: "crosstab", visible: capabilityGate(featCrosstab)},
		{key: "joins", visible: capabilityGate(featJoins)},
		requestOverlaysSlotGate,
		{key: "vectors", visible: capabilityGate(featMatrices)},
		{key: "matrices", visible: capabilityGate(featMatrices)},
		weightSlotGate,
		multiplicitySlotGate,
		modelsSlotGate,
		populationsSlotGate,
		paramsSlotGate,
	},
	reflect.TypeOf(types.Aggregation{}): {weightSlotGate},
	// The multi-measure crosstab: the `cells` request key and the
	// MEASURE-folded payload slots it alone produces.
	reflect.TypeOf(types.CrosstabSpec{}): {crosstabCellsSlotGate},
	reflect.TypeOf(types.MatrixPayload{}): {
		{key: "cell_labels", visible: capabilityGate(featCrosstabCells)},
		{key: "measures", visible: capabilityGate(featCrosstabCells)},
	},
	reflect.TypeOf(types.CrosstabComponents{}): {
		{key: "measures", visible: capabilityGate(featCrosstabCells)},
	},
	reflect.TypeOf(types.Test{}):               {weightSlotGate, multiplicitySlotGate},
	reflect.TypeOf(types.RegressionSpec{}):     {weightSlotGate, modelSaveSlotGate, multiplicitySlotGate, holdoutSlotGate},
	reflect.TypeOf(types.FitSpec{}):            {modelSaveSlotGate},
	reflect.TypeOf(types.MatrixSpec{}):         {weightSlotGate, multiplicitySlotGate},
	reflect.TypeOf(types.Attribute{}):          {weightSlotGate, forEachSlotGate},
	reflect.TypeOf(types.Window{}):             {forEachSlotGate},
	reflect.TypeOf(types.Feature{}):            {forEachSlotGate},
	reflect.TypeOf(types.OverlaySpec{}):        {weightSlotGate, multiplicitySlotGate},
	reflect.TypeOf(types.Group{}):              {weightSlotGate},
	reflect.TypeOf(types.ComposeOverlaySpec{}): {multiplicitySlotGate},
	// The sampled sweep's own keys; the random / lhs mode VALUES ride
	// the same capability (nestedSweepSamplingRefusal, the SweepMode
	// enum).
	reflect.TypeOf(types.SweepSpec{}): {
		{key: "samples", visible: sweepSamplingGate},
		{key: "rng_seed", visible: sweepSamplingGate},
	},
	reflect.TypeOf(types.SweepAxis{}): {{key: "range", visible: sweepSamplingGate}},
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
	// The matrix echo; auxiliary.p_adjusted / significant_adjusted are
	// keys of the open auxiliary map, so no request on a hidden
	// instance ever reaches them.
	reflect.TypeOf(types.MatrixResult{}): {multiplicitySlotGate},
	// The regression twins: the adjusted coefficient maps and the echo.
	reflect.TypeOf(types.RegressionResult{}): {
		multiplicitySlotGate,
		// The out-of-sample figures only a holdout produces.
		holdoutSlotGate,
		{key: "p_adjusted", visible: capabilityGate(featMultiplicity)},
		{key: "significant_adjusted", visible: capabilityGate(featMultiplicity)},
	},
	// The matrix results ride capability:matrices with the request
	// slots that produce them; the overlay layers ride the same gate
	// as the request `overlays` slot that produces them.
	reflect.TypeOf(types.Response{}): {
		{key: "matrices", visible: capabilityGate(featMatrices)},
		requestOverlaysSlotGate,
	},
	reflect.TypeOf(types.ResponseComponents{}): {
		{key: "matrices", visible: capabilityGate(featMatrices)},
	},
	reflect.TypeOf(types.ComposedRequest{}): {
		{key: "overlays", visible: overlayGate(featCompose)},
		multiplicitySlotGate,
		{key: "sweep", visible: capabilityGate(featComposeSweep)},
		{key: "fits", visible: capabilityGate(featComposeFits)},
		{key: "banner", visible: capabilityGate(featComposeBanner)},
		paramsSlotGate,
		{key: "optimize", visible: capabilityGate(featComposeOpt)},
	},
	// The sweep rank rides capability:compose_sweep with the `sweep`
	// slot that produces it; the seed echo only a sampled sweep writes
	// rides capability:compose_sweep_sampling.
	// The top-level overlay layers of each result root ride the gate
	// of the request root's `overlays` slot that produces them.
	reflect.TypeOf(types.ComposedResponse{}): {
		{key: "overlays", visible: overlayGate(featCompose)},
		{key: "ranking", visible: capabilityGate(featComposeSweep)},
		{key: "sweep_seed", visible: sweepSamplingGate},
		{key: "fits", visible: capabilityGate(featComposeFits)},
		{key: "banner", visible: capabilityGate(featComposeBanner)},
		{key: "optimizations", visible: capabilityGate(featComposeOpt)},
	},
	reflect.TypeOf(types.ChainRequest{}): {
		{key: "overlays", visible: overlayGate(featProcessChain)},
		paramsSlotGate,
	},
	reflect.TypeOf(types.ChainResponse{}): {
		{key: "overlays", visible: overlayGate(featProcessChain)},
	},
	reflect.TypeOf(types.FacetRequest{}): {
		{key: "overlays", visible: overlayGate(featFacet)},
		modelsSlotGate,
	},
	reflect.TypeOf(types.FacetResult{}): {
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
// details.path — the first offending key's location
// ("aggregations[0].weight"); valid_keys / suggestions range over THAT
// object's keys. The shape is built by strictjson.UnknownFieldError,
// which the strict request decoder raises too.
func unknownFieldErrorAt(path string, unknown, valid []string) *errors.CodedError {
	return strictjson.UnknownFieldError(path, unknown, valid)
}

// NearestKey returns the closest candidate to k by Levenshtein distance,
// or "" when no candidate is close enough (strictjson.NearestKey: edit
// distance < 4, capped at the key length; ties keep the earliest).
func NearestKey(k string, candidates []string) string {
	return strictjson.NearestKey(k, candidates)
}

// StrictHidden adapts inst's hidden request slots (gatedSlots, keyed by
// struct type) to strictjson.Options.Hidden, so the strict decoder
// refuses a hidden slot at any depth exactly as SlotRefusal does — the
// same code, keys and visible valid_keys. Nil for a nil / unscoped
// instance (nothing hidden).
func StrictHidden(inst *InstanceSnapshot) func(reflect.Type) []string {
	if inst == nil {
		return nil
	}
	return func(t reflect.Type) []string {
		var out []string
		for _, g := range gatedSlots[t] {
			if !g.visible(inst) {
				out = append(out, g.key)
			}
		}
		return out
	}
}

// StrictOptions is the strict-decode option set for a JSON request
// decoded on behalf of inst: hidden slots refused as unknown keys, a
// root `_meta` example sidecar tolerated.
func StrictOptions(inst *InstanceSnapshot) strictjson.Options {
	return strictjson.Options{Hidden: StrictHidden(inst), AllowMeta: true}
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
		if len(r.Models) > 0 {
			out = append(out, "models")
		}
		if len(r.Populations) > 0 {
			out = append(out, "populations")
		}
		if len(r.Params) > 0 {
			out = append(out, "params")
		}
	case *types.ComposedRequest:
		if r != nil && len(r.Overlays) > 0 {
			out = append(out, "overlays")
		}
		if r != nil && r.Multiplicity != nil {
			out = append(out, multiplicityKey)
		}
		if r != nil && r.Sweep != nil {
			out = append(out, "sweep")
		}
		if r != nil && len(r.Fits) > 0 {
			out = append(out, "fits")
		}
		if r != nil && r.Banner != nil {
			out = append(out, "banner")
		}
		if r != nil && len(r.Params) > 0 {
			out = append(out, "params")
		}
		if r != nil && len(r.Optimize) > 0 {
			out = append(out, "optimize")
		}
	case *types.ChainRequest:
		if r != nil && len(r.Overlays) > 0 {
			out = append(out, "overlays")
		}
		if r != nil && len(r.Params) > 0 {
			out = append(out, "params")
		}
	case *types.FacetRequest:
		if r != nil && len(r.Overlays) > 0 {
			out = append(out, "overlays")
		}
		if r != nil && len(r.Models) > 0 {
			out = append(out, "models")
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
		for i, cell := range ct.CellAggregations() {
			if cell != nil && !cell.Weight.IsZero() {
				return at(ct.CellSlotPath(i), cell)
			}
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
// resolver's slot order: tests, post-tests, overlays, matrices,
// regressions. The refusal is
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
	if err := overlayMultiplicityRefusal(req.Overlays, inst); err != nil {
		return err
	}
	for i := range req.Matrices {
		if req.Matrices[i].Multiplicity != nil {
			return unknownFieldErrorAt(fmt.Sprintf("matrices[%d]", i), []string{multiplicityKey},
				VisibleSlotKeys(&req.Matrices[i], inst))
		}
	}
	for i, r := range req.Regressions {
		if r != nil && r.Multiplicity != nil {
			return unknownFieldErrorAt(fmt.Sprintf("regressions[%d]", i), []string{multiplicityKey},
				VisibleSlotKeys(r, inst))
		}
	}
	return nil
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

// nestedModelSaveRefusal refuses the first regression `save` set inside
// req when inst hides capability:model_save. The refusal is
// PULSE_REQUEST_UNKNOWN_FIELD at that regression (details.path),
// valid_keys over its visible keys.
func nestedModelSaveRefusal(req *types.Request, inst *InstanceSnapshot) *errors.CodedError {
	if req == nil || inst.Enabled(featModelSave) {
		return nil
	}
	for i, r := range req.Regressions {
		if r != nil && r.Save != "" {
			return unknownFieldErrorAt(fmt.Sprintf("regressions[%d]", i), []string{saveKey}, VisibleSlotKeys(r, inst))
		}
	}
	return nil
}

// nestedHoldoutRefusal refuses the first regression `holdout` set inside
// req when inst hides capability:regression_holdout —
// PULSE_REQUEST_UNKNOWN_FIELD at that regression (details.path),
// valid_keys over its visible keys.
func nestedHoldoutRefusal(req *types.Request, inst *InstanceSnapshot) *errors.CodedError {
	if req == nil || inst.Enabled(featRegHoldout) {
		return nil
	}
	for i, r := range req.Regressions {
		if r != nil && r.Holdout != "" {
			return unknownFieldErrorAt(fmt.Sprintf("regressions[%d]", i), []string{holdoutSlotGate.key}, VisibleSlotKeys(r, inst))
		}
	}
	return nil
}

// nestedForEachRefusal refuses the first item `for_each` set inside req
// when inst hides capability:for_each, in expansion order: features,
// attributes, windows. The refusal is PULSE_REQUEST_UNKNOWN_FIELD at that
// item (details.path), valid_keys over its visible keys.
func nestedForEachRefusal(req *types.Request, inst *InstanceSnapshot) *errors.CodedError {
	if req == nil || inst.Enabled(featForEach) {
		return nil
	}
	at := func(path string, sample any) *errors.CodedError {
		return unknownFieldErrorAt(path, []string{forEachKey}, VisibleSlotKeys(sample, inst))
	}
	for i, f := range req.Features {
		if f != nil && f.ForEach != nil {
			return at(fmt.Sprintf("features[%d]", i), f)
		}
	}
	for i, a := range req.Attributes {
		if a != nil && a.ForEach != nil {
			return at(fmt.Sprintf("attributes[%d]", i), a)
		}
	}
	for i, w := range req.Windows {
		if w != nil && w.ForEach != nil {
			return at(fmt.Sprintf("windows[%d]", i), w)
		}
	}
	return nil
}

// nestedCrosstabCellsRefusal refuses a set crosstab `cells` key when
// inst hides capability:crosstab_cells — PULSE_REQUEST_UNKNOWN_FIELD at
// the crosstab object (details.path "crosstab"), valid_keys over its
// visible keys. A present key is refused even when empty, as strict
// decode refuses any set key; a Go caller's empty slice is not set.
func nestedCrosstabCellsRefusal(req *types.Request, inst *InstanceSnapshot) *errors.CodedError {
	if req == nil || req.Crosstab == nil || req.Crosstab.Cells == nil || inst.Enabled(featCrosstabCells) {
		return nil
	}
	return unknownFieldErrorAt("crosstab", []string{crosstabCellsSlotGate.key}, VisibleSlotKeys(req.Crosstab, inst))
}

// nestedSweepSamplingRefusal gates a ComposedRequest's sweep when inst
// hides capability:compose_sweep_sampling: a set `samples` / `rng_seed`
// is PULSE_REQUEST_UNKNOWN_FIELD at the sweep (details.path "sweep"),
// then the first axis `range` at that axis ("sweep.axes[i]") — valid_keys
// over the object's visible keys — and a mode other than grid / zip is
// the unknown-mode refusal (sweep.EnumerationModeRefusal), its `valid`
// naming grid and zip only.
func nestedSweepSamplingRefusal(spec *types.SweepSpec, inst *InstanceSnapshot) *errors.CodedError {
	if spec == nil || inst.Enabled(featSweepSampling) {
		return nil
	}
	var keys []string
	if spec.Samples != 0 {
		keys = append(keys, "samples")
	}
	if spec.RNGSeed != 0 {
		keys = append(keys, "rng_seed")
	}
	if len(keys) > 0 {
		return unknownFieldErrorAt("sweep", keys, VisibleSlotKeys(spec, inst))
	}
	for i := range spec.Axes {
		if spec.Axes[i].Range != nil {
			return unknownFieldErrorAt(fmt.Sprintf("sweep.axes[%d]", i), []string{"range"}, VisibleSlotKeys(&spec.Axes[i], inst))
		}
	}
	return sweep.EnumerationModeRefusal(spec.Mode)
}

// requestSlotRefusal gates one Request: its own top-level slots, then
// the crosstab `cells` key, then every nested slot `weight`, then every
// nested `multiplicity`, then every item `for_each`, then every
// regression `save`, then every regression `holdout`.
func requestSlotRefusal(req *types.Request, inst *InstanceSnapshot) *errors.CodedError {
	if ce := rootSlotRefusal(req, inst); ce != nil {
		return ce
	}
	if ce := nestedCrosstabCellsRefusal(req, inst); ce != nil {
		return ce
	}
	if ce := nestedWeightRefusal(req, inst); ce != nil {
		return ce
	}
	if ce := nestedMultiplicityRefusal(req, inst); ce != nil {
		return ce
	}
	if ce := nestedForEachRefusal(req, inst); ce != nil {
		return ce
	}
	if ce := nestedModelSaveRefusal(req, inst); ce != nil {
		return ce
	}
	return nestedHoldoutRefusal(req, inst)
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
// keys only. Then — on every instance, scoped or not — every slot's
// params object is held to its operator's manifest allowlist
// (ParamsRefusal). Every service funnel and Predict run it before any
// other request check, so a hidden slot or an unknown params key is
// refused at the point an unknown JSON key would have been.
func SlotRefusal(v any, inst *InstanceSnapshot) error {
	if !inst.Scoped() {
		return ParamsRefusal(v, inst)
	}
	if ce := rootSlotRefusal(v, inst); ce != nil {
		return ce
	}
	switch r := v.(type) {
	case *types.Request:
		if ce := nestedCrosstabCellsRefusal(r, inst); ce != nil {
			return ce
		}
		if ce := nestedWeightRefusal(r, inst); ce != nil {
			return ce
		}
		if ce := nestedMultiplicityRefusal(r, inst); ce != nil {
			return ce
		}
		if ce := nestedForEachRefusal(r, inst); ce != nil {
			return ce
		}
		if ce := nestedModelSaveRefusal(r, inst); ce != nil {
			return ce
		}
		if ce := nestedHoldoutRefusal(r, inst); ce != nil {
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
		if ce := nestedSweepSamplingRefusal(r.Sweep, inst); ce != nil {
			return ce
		}
		for i, req := range r.Requests {
			if ce := requestSlotRefusal(req, inst); ce != nil {
				return RefusalAt(ce, "request", i)
			}
		}
		for i, fit := range r.Fits {
			if fit.Save != "" && !inst.Enabled(featModelSave) {
				return RefusalAt(unknownFieldErrorAt(fmt.Sprintf("fits[%d]", i), []string{saveKey},
					VisibleSlotKeys(&r.Fits[i], inst)), "fit", i)
			}
			if ce := requestSlotRefusal(fit.Request, inst); ce != nil {
				return RefusalAt(ce, "fit", i)
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
	return ParamsRefusal(v, inst)
}
