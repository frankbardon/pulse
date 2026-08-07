// Package template turns a stored, parameterised JSON document into a
// validated Pulse request.
//
// A template is valid JSON at rest with an explicit wrapper: a `target`
// naming the request root the rendered body decodes into, a `variables`
// block declaring what a caller may (or must) supply, and a `body` holding
// the parameterised request with its variable markers still in place. The
// body is deliberately NOT runnable as-is — it becomes a request only once
// rendered.
//
//	{
//	  "description": "Revenue by region",
//	  "target": "request",
//	  "variables": [
//	    {"name": "metric",   "type": "field",   "required": true},
//	    {"name": "bucket",   "type": "integer", "default": 10},
//	    {"name": "segments", "type": "list",    "items": "string"}
//	  ],
//	  "body": {"cohort": {"filename": "sales.pulse"}, "aggregations": []}
//	}
//
// This file carries the document model only — the shapes a template file
// deserializes into, plus their two closed enums. Declaration validation
// lives in validate.go. Variable resolution, the render walk, and strict
// decode into the target request type arrive in later files.
//
// Import ceiling: this package depends only on the standard library plus
// the pulse types and errors packages. It must never import descriptor,
// processing, or service — TestTemplatePackage_ImportBoundary enforces
// that, keeping the package dependency-light and execution-free.
package template

import (
	"encoding/json"
	"slices"
)

// Target names the public request root a rendered template decodes into.
// It is a closed enum, spelled lowercase on the wire; the value selects
// the strict-decode type at render, so it can never be inferred from the
// body shape. An absent or unrecognised target is
// PULSE_TEMPLATE_TARGET_UNKNOWN.
type Target string

const (
	// TargetRequest renders into types.Request — the single-cohort
	// process/predict envelope and the 95% case.
	TargetRequest Target = "request"

	// TargetComposed renders into types.ComposedRequest — a multi-slot
	// Compose batch with optional Compose-host overlays.
	TargetComposed Target = "composed"

	// TargetChain renders into types.ChainRequest — a source-rooted
	// linear ProcessChain.
	TargetChain Target = "chain"

	// TargetFacet renders into types.FacetRequest — the facet endpoints.
	TargetFacet Target = "facet"

	// TargetSample renders into types.SampleRequest — record sampling.
	TargetSample Target = "sample"
)

// allTargets is the authoritative ordered list of valid targets. Order is
// the declaration order above and is the order AllTargets reports.
var allTargets = []Target{
	TargetRequest,
	TargetComposed,
	TargetChain,
	TargetFacet,
	TargetSample,
}

// AllTargets returns every valid Target in declaration order. The returned
// slice is a copy — callers may sort or mutate it freely.
func AllTargets() []Target {
	out := make([]Target, len(allTargets))
	copy(out, allTargets)
	return out
}

// Valid reports whether t is one of the five recognised targets. The empty
// string is not valid: a template must state its target explicitly.
func (t Target) Valid() bool {
	return slices.Contains(allTargets, t)
}

// String returns the on-the-wire spelling of the target.
func (t Target) String() string { return string(t) }

// VarType is the declared type of a template variable. It is a closed
// enum: an unrecognised type is a declaration error, never a pass-through.
//
// The scalar members (string, number, integer, boolean, field, date) are
// the only types permitted as a list's element type — see IsScalar.
type VarType string

const (
	// VarString accepts any JSON string.
	VarString VarType = "string"

	// VarNumber accepts any JSON number, integral or fractional.
	VarNumber VarType = "number"

	// VarInteger accepts a JSON number with no fractional part; 1 and 1.0
	// qualify, 1.5 does not.
	VarInteger VarType = "integer"

	// VarBoolean accepts a JSON bool.
	VarBoolean VarType = "boolean"

	// VarField accepts a JSON string naming a cohort field. It is
	// shape-identical to VarString and exists as a distinct type so that
	// cohort-bound field-name constraining can be layered on later
	// without a wire change.
	VarField VarType = "field"

	// VarEnum accepts a JSON string that is a member of the declaration's
	// Values set. A VarEnum declaration without Values is invalid.
	VarEnum VarType = "enum"

	// VarList accepts a JSON array whose every element satisfies the
	// declaration's Items type. A VarList declaration without Items, or
	// with a non-scalar Items, is invalid — lists do not nest.
	VarList VarType = "list"

	// VarDate accepts a JSON string parsing as an ISO date.
	VarDate VarType = "date"

	// VarPeriod accepts a JSON object carrying exactly one of `ranges` or
	// `table`, mirroring the GROUP_DATE_RANGES / FILTER_DATE_RANGES
	// labeled-date-range Params shape.
	VarPeriod VarType = "period"
)

// allVarTypes is the authoritative ordered list of valid variable types.
var allVarTypes = []VarType{
	VarString,
	VarNumber,
	VarInteger,
	VarBoolean,
	VarField,
	VarEnum,
	VarList,
	VarDate,
	VarPeriod,
}

// AllVarTypes returns every valid VarType in declaration order. The
// returned slice is a copy.
func AllVarTypes() []VarType {
	out := make([]VarType, len(allVarTypes))
	copy(out, allVarTypes)
	return out
}

// Valid reports whether v is one of the nine recognised variable types.
// The empty string is not valid: every variable must state its type.
func (v VarType) Valid() bool {
	return slices.Contains(allVarTypes, v)
}

// IsScalar reports whether v denotes a single JSON scalar and is therefore
// legal as a list's element type.
//
// The three non-scalars are excluded for distinct reasons: list would nest
// (and a nested list has no element-type slot to declare); period is a
// JSON object, not a scalar; enum is scalar on the wire but draws its
// membership set from the variable-level Values slot, which a list
// declaration cannot express per-element unambiguously.
func (v VarType) IsScalar() bool {
	switch v {
	case VarString, VarNumber, VarInteger, VarBoolean, VarField, VarDate:
		return true
	default:
		return false
	}
}

// String returns the on-the-wire spelling of the variable type.
func (v VarType) String() string { return string(v) }

// Variable is one declared template parameter. The declaration is the
// contract a caller's supplied variable map is checked against: it fixes
// the accepted JSON type, whether a value is mandatory, and what stands in
// when the caller supplies nothing.
type Variable struct {
	// Name is the variable's identifier — the key a caller supplies it
	// under and the token the body's markers reference. Required,
	// non-empty, unique within a template.
	Name string `json:"name"`

	// Type is the declared type. Required; must be one of AllVarTypes.
	Type VarType `json:"type"`

	// Description is optional human-facing prose. It exists so a caller
	// can build a form (or a prompt) from the declaration alone.
	Description string `json:"description,omitempty"`

	// Required marks a variable that must resolve to a value at render.
	// Required together with a Default is legal, and the pair is not a
	// contradiction: the default resolves the variable, so a required
	// variable carrying a default can never go missing.
	Required bool `json:"required,omitempty"`

	// Default is the value used when the caller supplies none, held as
	// raw JSON so integer fidelity survives to the render walk. An
	// explicit JSON null means "no default", identical to omitting it.
	Default json.RawMessage `json:"default,omitempty"`

	// Values is the permitted member set for a VarEnum variable.
	// Membership is exact and case-sensitive. Required for VarEnum,
	// meaningless for every other type.
	Values []string `json:"values,omitempty"`

	// Items is the element type for a VarList variable. Required for
	// VarList, meaningless for every other type, and must name a scalar
	// type — lists do not nest.
	Items VarType `json:"items,omitempty"`
}

// Template is the parsed template document.
type Template struct {
	// Name is the template's lookup identifier. It is derived by the
	// store from the file's path relative to its directory root (minus
	// the .json extension, forward-slash separated); a document may also
	// carry its own name, and a Template built directly in Go may leave
	// it empty. Declaration validation does not require it — naming is
	// the store's job.
	Name string `json:"name,omitempty"`

	// Description is optional human-facing prose describing what the
	// template produces.
	Description string `json:"description,omitempty"`

	// Target names the request root the rendered body decodes into.
	// Required.
	Target Target `json:"target"`

	// Variables declares every parameter the template accepts, in
	// author order. A template with no variables is legal — it renders
	// to a constant request.
	Variables []*Variable `json:"variables,omitempty"`

	// Body is the parameterised request, held as raw JSON with its
	// variable markers intact. Required, and must be a non-empty JSON
	// object. It is not a valid request until rendered.
	Body json.RawMessage `json:"body"`
}

// VariableNames returns the declared variable names in author order.
// Nil-safe; returns nil for a template with no declarations.
func (t *Template) VariableNames() []string {
	if t == nil || len(t.Variables) == 0 {
		return nil
	}
	out := make([]string, 0, len(t.Variables))
	for _, v := range t.Variables {
		if v == nil {
			continue
		}
		out = append(out, v.Name)
	}
	return out
}

// Variable returns the declaration for name and whether it exists. Lookup
// is exact and case-sensitive. Nil-safe.
func (t *Template) Variable(name string) (*Variable, bool) {
	if t == nil {
		return nil, false
	}
	for _, v := range t.Variables {
		if v != nil && v.Name == name {
			return v, true
		}
	}
	return nil, false
}

// Summary is the lightweight projection of a template used for listing —
// everything a caller needs to choose a template and build a form for it,
// without the body. It is what ListTemplates reports.
type Summary struct {
	// Name is the template's lookup identifier.
	Name string `json:"name"`

	// Description mirrors Template.Description.
	Description string `json:"description,omitempty"`

	// Target mirrors Template.Target.
	Target Target `json:"target"`

	// Variables lists the declared variable names in author order. The
	// full declarations are reachable by fetching the template itself.
	Variables []string `json:"variables,omitempty"`

	// Path is the source file the winning entry was loaded from. Empty
	// for a template registered programmatically.
	Path string `json:"path,omitempty"`

	// Shadows lists the source paths of same-named templates this entry
	// takes precedence over. Template directories are an ordered
	// precedence list and the first directory wins; the losing entries
	// are recorded here rather than discarded, so a layered setup is
	// visible instead of mysterious. Empty when nothing is shadowed.
	Shadows []string `json:"shadows,omitempty"`
}

// Summarize projects the template into a Summary, attaching the source
// path it was loaded from and the source paths of any same-named entries
// it shadows. Both may be empty. Nil-safe; returns the zero Summary for a
// nil template.
func (t *Template) Summarize(path string, shadows []string) Summary {
	if t == nil {
		return Summary{}
	}
	s := Summary{
		Name:        t.Name,
		Description: t.Description,
		Target:      t.Target,
		Variables:   t.VariableNames(),
		Path:        path,
	}
	if len(shadows) > 0 {
		s.Shadows = make([]string, len(shadows))
		copy(s.Shadows, shadows)
	}
	return s
}
