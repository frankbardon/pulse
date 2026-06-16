package descriptor

import (
	"encoding/json"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// FACET-host overlay validator — no-execute, header-and-schema-only
// validation for FacetRequest.Overlays specs. Sibling to ValidateOverlays
// (the Request-host validator); the two surfaces share the kind-catalog
// + Scope / Ref-family enum source of truth but enforce different host-
// shape contracts.
//
// Per-kind host-arm acceptance:
//
//   - OVERLAY_INDEX_VS_POP / OVERLAY_ZSCORE_VS_POP: discrete OR numeric
//     host arm accepted — the runtime handler dispatches internally.
//   - OVERLAY_CHISQ_VS_POP: discrete arm only (categorical / boolean
//     host); numeric host fires PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE.
//   - OVERLAY_KS_VS_POP: numeric arm only; categorical / boolean host
//     fires PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE.
//
// Shared shape contract for all four kinds:
//
//   - Ref.Population MUST be populated (the cohort lives on
//     Ref.Population.Cohort). Any other ref-family pointer (Margin /
//     Sibling / BaselineIndex / Prior / RollingMean / YoY / Stage /
//     Slot) fires PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE — single-arm
//     enforcement matches the Request-host implicit-margin family.
//   - Scope MUST be GROUP. CELL / ROW / COLUMN / MATRIX / TOTAL scopes
//     are not meaningful for the per-value population-comparison
//     statistic the kinds emit.
//   - Level / Within MUST both be 0. Population comparison is a single-
//     value lookup, not an axis prefix; non-zero values fire
//     PULSE_OVERLAY_LEVEL_OUT_OF_RANGE mirroring the Request-host
//     implicit-ref family.
//   - Host-field selection: each spec MAY name the FacetRequest.Fields
//     entry it decorates via Params["field"]. When omitted AND the
//     FacetRequest declares exactly one Field, that single field is
//     selected. When omitted AND the FacetRequest declares multiple
//     Fields the spec fires PULSE_OVERLAY_PARAM_MISSING so the renderer
//     receives an unambiguous host-field assignment. Unknown field
//     names fire PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE carrying the
//     {field, available_fields} detail map (mirrors the runtime
//     PULSE_OVERLAY_REF_UNKNOWN shape the resolver emits when a
//     population FacetResult is missing the same field).
//
// Structural invariants (mirrors descriptor/overlay.go):
//
//   - This file MUST NOT import github.com/frankbardon/pulse/service or
//     github.com/frankbardon/pulse/processing. Predict is no-execute;
//     overlay catalog data lives in types/, capability lookups go
//     through types/ constants.
//   - No fmt.Sprintf in any JSON-bearing path. Error messages are built
//     with string concatenation so descriptor envelope output stays
//     grep-clean against the structural defense ban.

// facetOverlaySupportedScopes is the supported scope set for the FACET-
// host overlay kinds (OVERLAY_INDEX_VS_POP / OVERLAY_ZSCORE_VS_POP /
// OVERLAY_CHISQ_VS_POP / OVERLAY_KS_VS_POP). GROUP is the only sensible
// footprint for the per-value population-comparison statistic these
// kinds emit; CELL / ROW / COLUMN / MATRIX / TOTAL fires
// PULSE_OVERLAY_SCOPE_UNSUPPORTED.
var facetOverlaySupportedScopes = map[types.OverlayScope]bool{
	types.OverlayScopeGroup: true,
}

// ValidateFacetOverlays walks every spec in req.Overlays and appends
// structural errors to the envelope. Sibling to ValidateOverlays — the
// Request-host validator — but enforces the FACET-host contract: the
// host is a FacetSchema FacetResult, the population reference is
// resolved against an alternate cohort, and the host-field the spec
// decorates is selected from FacetRequest.Fields.
//
// No-op when req is nil or req.Overlays is empty. The schema is used
// for the per-kind host-arm gates (CHISQ rejects numeric; KS rejects
// categorical); when schema is nil those gates short-circuit.
func ValidateFacetOverlays(env *Envelope, req *types.FacetRequest, schema *encoding.Schema) {
	if req == nil || len(req.Overlays) == 0 {
		return
	}
	for i := range req.Overlays {
		spec := &req.Overlays[i]
		validateFacetOverlaySpec(env, req, schema, spec, i)
	}
}

// validateFacetOverlaySpec applies the FACET-host ruleset to one
// OverlaySpec. Errors are emitted with deterministic Details so MCP /
// CLI envelopes can render the index, kind, and offending value
// without re-parsing the message string.
func validateFacetOverlaySpec(env *Envelope, req *types.FacetRequest, schema *encoding.Schema, spec *types.OverlaySpec, index int) {
	if spec == nil {
		return
	}
	// Unknown-kind probe first — every other rule is keyed by Kind so we
	// cannot reasonably validate Scope / Ref against an unknown catalog
	// entry. Mirrors the Request-host validator's policy.
	if _, known := types.OverlayStreamable(spec.Kind); !known {
		env.AddError(string(errors.PULSE_OVERLAY_KIND_UNKNOWN),
			"overlay kind is not in the catalog: "+string(spec.Kind),
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
			})
		return
	}
	// FACET host accepts only the four FACET-host kinds. Any other
	// kind belongs on a Request.Overlays slot, not FacetRequest.Overlays.
	if !isFacetOverlayKind(spec.Kind) {
		env.AddError(string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
			"overlay "+string(spec.Kind)+" is not a FACET-host kind; FacetRequest.Overlays accepts only OVERLAY_INDEX_VS_POP / OVERLAY_ZSCORE_VS_POP / OVERLAY_CHISQ_VS_POP / OVERLAY_KS_VS_POP",
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
				"host":  "facet",
			})
		return
	}
	// Shared shape contract — Ref.Population only, GROUP scope,
	// Level / Within == 0. Apply before the per-kind host-arm gate so
	// callers see the structural failure first.
	if !validateFacetOverlayRef(env, spec, index) {
		return
	}
	if !validateFacetOverlayScope(env, spec, index) {
		return
	}
	if !validateFacetOverlayLevelWithin(env, spec, index) {
		return
	}
	// Host-field selection — read Params["field"], fall back to the
	// single-field default, reject unknown / ambiguous slots.
	hostField, ok := resolveFacetOverlayHostField(env, req, spec, index)
	if !ok {
		return
	}
	// Per-kind host-arm gate (CHISQ_VS_POP rejects numeric;
	// KS_VS_POP rejects categorical). INDEX_VS_POP / ZSCORE_VS_POP
	// accept both arms (the runtime handler dispatches internally).
	validateFacetOverlayHostArm(env, schema, spec, hostField, index)
}

// isFacetOverlayKind reports whether kind is one of the four FACET-host
// overlay kinds. Used as the gate for FacetRequest.Overlays — non-FACET
// kinds belong on Request.Overlays instead.
func isFacetOverlayKind(kind types.OverlayKind) bool {
	switch kind {
	case types.OverlayKindIndexVsPop,
		types.OverlayKindZScoreVsPop,
		types.OverlayKindChiSqVsPop,
		types.OverlayKindKSVsPop:
		return true
	}
	return false
}

// validateFacetOverlayRef enforces the Ref.Population-only contract for
// FACET-host overlay kinds. Ref.Population MUST be populated; every
// other ref-family pointer is a shape mismatch
// (PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE). Returns true when the Ref
// passes; false when an error was emitted.
func validateFacetOverlayRef(env *Envelope, spec *types.OverlaySpec, index int) bool {
	if spec.Ref.Margin != nil ||
		spec.Ref.Sibling != nil ||
		spec.Ref.BaselineIndex != nil ||
		spec.Ref.Prior != nil ||
		spec.Ref.RollingMean != nil ||
		spec.Ref.YoY != nil ||
		spec.Ref.Stage != nil ||
		spec.Ref.Slot != nil {
		env.AddError(string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
			"overlay "+string(spec.Kind)+" requires Ref.Population only (no Margin / Sibling / BaselineIndex / Prior / RollingMean / YoY / Stage / Slot)",
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
			})
		return false
	}
	if spec.Ref.Population == nil {
		env.AddError(string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
			"overlay "+string(spec.Kind)+" requires Ref.Population (population-comparison reference; the comparison cohort lives on Ref.Population.Cohort)",
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
			})
		return false
	}
	if spec.Ref.Population.Cohort == "" {
		env.AddError(string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
			"overlay "+string(spec.Kind)+" Ref.Population.Cohort is empty (population reference requires a comparison-cohort name)",
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
			})
		return false
	}
	return true
}

// validateFacetOverlayScope enforces the GROUP-scope contract for
// FACET-host overlay kinds. Returns true when the scope passes; false
// when an error was emitted.
func validateFacetOverlayScope(env *Envelope, spec *types.OverlaySpec, index int) bool {
	if !facetOverlaySupportedScopes[spec.Scope] {
		env.AddError(string(errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED),
			"overlay "+string(spec.Kind)+" does not support scope "+string(spec.Scope)+" (supports: group)",
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
				"scope": string(spec.Scope),
			})
		return false
	}
	return true
}

// validateFacetOverlayLevelWithin enforces the Level / Within == 0
// contract for FACET-host overlay kinds. Population comparison is a
// single-value lookup, not an axis prefix; non-zero values fire
// PULSE_OVERLAY_LEVEL_OUT_OF_RANGE mirroring the Request-host implicit-
// ref family.
func validateFacetOverlayLevelWithin(env *Envelope, spec *types.OverlaySpec, index int) bool {
	if spec.Level != 0 || spec.Within != 0 {
		env.AddError(string(errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE),
			"overlay "+string(spec.Kind)+" does not support Level / Within (population-comparison kind: the reference is a single-value lookup, not an axis prefix)",
			map[string]any{
				"index":  index,
				"kind":   string(spec.Kind),
				"level":  spec.Level,
				"within": spec.Within,
			})
		return false
	}
	return true
}

// resolveFacetOverlayHostField returns the FacetRequest.Fields entry
// the spec decorates. The host field is read from Params["field"]
// first; when omitted AND req.Fields carries exactly one entry, that
// single field is selected. When omitted AND req.Fields carries
// multiple entries the helper fires PULSE_OVERLAY_PARAM_MISSING so the
// caller's spec is unambiguous. Unknown field names fire
// PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE carrying the
// {field, available_fields} detail map.
//
// Returns (fieldName, true) on success; ("", false) when an error was
// emitted. The returned field name is guaranteed to appear in
// req.Fields.
func resolveFacetOverlayHostField(env *Envelope, req *types.FacetRequest, spec *types.OverlaySpec, index int) (string, bool) {
	requested, hasParam := readFacetOverlayFieldParam(spec.Params)
	if !hasParam {
		// No Params["field"] — single-field default applies when the
		// FacetRequest declares exactly one Field; otherwise the spec
		// is ambiguous.
		if len(req.Fields) == 1 {
			return req.Fields[0], true
		}
		env.AddError(string(errors.PULSE_OVERLAY_PARAM_MISSING),
			"overlay "+string(spec.Kind)+" requires Params[\"field\"] when FacetRequest.Fields declares multiple fields",
			map[string]any{
				"index":            index,
				"kind":             string(spec.Kind),
				"param":            "field",
				"available_fields": append([]string(nil), req.Fields...),
			})
		return "", false
	}
	// Params["field"] populated — must reference one of req.Fields.
	for _, name := range req.Fields {
		if name == requested {
			return requested, true
		}
	}
	env.AddError(string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
		"overlay "+string(spec.Kind)+" Params[\"field\"] does not reference any FacetRequest.Fields entry: "+requested,
		map[string]any{
			"index":            index,
			"kind":             string(spec.Kind),
			"field":            requested,
			"available_fields": append([]string(nil), req.Fields...),
		})
	return "", false
}

// validateFacetOverlayHostArm enforces the per-kind host-arm gate.
// CHISQ_VS_POP requires a discrete (categorical / boolean) host;
// KS_VS_POP requires a numeric host. INDEX_VS_POP / ZSCORE_VS_POP
// accept both arms — the runtime handler dispatches internally.
//
// When schema is nil the gate short-circuits — the runtime handler
// remains as the last line of defense via the FacetResult.Fields
// payload it actually produced. When the host field is not in the
// schema (defensive — the FacetSchema validator already gates this) the
// gate short-circuits as well; the missing-field error has already
// surfaced from validateFacetSchemaRequest.
func validateFacetOverlayHostArm(env *Envelope, schema *encoding.Schema, spec *types.OverlaySpec, hostField string, index int) {
	if schema == nil {
		return
	}
	f := schema.Field(hostField)
	if f == nil {
		return
	}
	switch spec.Kind {
	case types.OverlayKindChiSqVsPop:
		// Discrete arm only — χ² goodness-of-fit requires categorical
		// buckets to form the observed × expected contingency.
		if facetIsNumeric(f.Type) && !f.Type.IsCategorical() {
			env.AddError(string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
				"overlay "+string(spec.Kind)+" requires a discrete host field; got numeric field "+hostField+" ("+f.Type.String()+")",
				map[string]any{
					"index": index,
					"kind":  string(spec.Kind),
					"field": hostField,
					"type":  f.Type.String(),
				})
		}
	case types.OverlayKindKSVsPop:
		// Numeric arm only — KS distance requires an ordered numeric
		// distribution (the CDF is undefined on unordered categorical
		// buckets).
		if !facetIsNumeric(f.Type) || f.Type.IsCategorical() {
			env.AddError(string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
				"overlay "+string(spec.Kind)+" requires a numeric host field; got non-numeric field "+hostField+" ("+f.Type.String()+")",
				map[string]any{
					"index": index,
					"kind":  string(spec.Kind),
					"field": hostField,
					"type":  f.Type.String(),
				})
		}
	}
}

// readFacetOverlayFieldParam pulls the "field" key out of a raw JSON
// Params blob. Returns (value, true) when the blob is a valid object
// carrying a string-typed "field" entry; ("", false) when the blob is
// absent / empty / non-object / missing the key / the value is non-
// string / the value is empty. Mirrors the per-kind Params readers
// already on the Request-host validator (readYoYFrequencyJSON, etc.).
func readFacetOverlayFieldParam(params json.RawMessage) (string, bool) {
	if len(params) == 0 {
		return "", false
	}
	var m map[string]any
	if err := json.Unmarshal(params, &m); err != nil {
		return "", false
	}
	raw, present := m["field"]
	if !present {
		return "", false
	}
	s, ok := raw.(string)
	if !ok {
		return "", false
	}
	if s == "" {
		return "", false
	}
	return s, true
}
