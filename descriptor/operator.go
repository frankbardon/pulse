package descriptor

// Operator describes a single registered processing component (aggregator,
// attribute, filterer, grouper, window operator, or feature operator). The
// manifest exposes one Operator entry per registered component. LLM clients
// use this metadata at session start to author valid requests without
// further discovery round-trips.
type Operator struct {
	// Name is the operator identifier (e.g. "AGG_PERCENTILE").
	Name string `json:"name"`

	// Category is the family this operator belongs to. One of:
	// "aggregator", "attribute", "filterer", "grouper", "window",
	// "feature".
	Category string `json:"category"`

	// Description is a one-sentence prose summary for LLM-side selection.
	Description string `json:"description"`

	// Params lists every parameter the operator reads from its Params
	// blob in a request. Required and optional are both listed.
	Params []Param `json:"params"`

	// AcceptsTypes lists the cohort field types this operator can be
	// applied to. Values are field type name strings (e.g. "f64",
	// "categorical_u16", "date"). Empty means "no field input".
	AcceptsTypes []string `json:"accepts_types"`

	// EmitsType is the field type produced for single-output operators.
	// Empty when the operator's emit type is conditional on input or
	// when it does not emit a typed column (e.g. an aggregator emits a
	// scalar).
	EmitsType string `json:"emits_type,omitempty"`

	// EmitsTypeNote provides context when EmitsType is empty or
	// conditional (e.g. "matches input field type", "scalar float64").
	EmitsTypeNote string `json:"emits_type_note,omitempty"`

	// Streamable mirrors types.X.Streamable() for the operator's type.
	// Source of truth for the runtime gate.
	Streamable bool `json:"streamable"`

	// StreamableHint suggests the closest streaming-capable alternative
	// when Streamable is false (e.g. AGG_MEDIAN -> "Use AGG_AVERAGE for
	// streaming, or accept the buffered path."). Empty when Streamable
	// is true or no near-equivalent exists.
	StreamableHint string `json:"streamable_hint,omitempty"`
}

// Param describes a single parameter accepted by an operator, test, or
// synth distribution.
type Param struct {
	// Name is the JSON key inside the operator's Params blob.
	Name string `json:"name"`

	// Type is the parameter's value type. One of:
	//   "float", "int", "string", "bool", "field", "enum", "list", "object".
	// "field" means the value names a cohort field; "enum" means the
	// value is one of EnumValues; "list" means a JSON array.
	Type string `json:"type"`

	// Required is true when the parameter must be supplied. Optional
	// parameters with a default carry Required=false and Default set.
	Required bool `json:"required"`

	// Default is the operator's default value when Required is false.
	// Omitted from JSON when nil.
	Default any `json:"default,omitempty"`

	// Description is a one-sentence prose explanation.
	Description string `json:"description"`

	// EnumValues lists the allowed values when Type=="enum".
	EnumValues []string `json:"enum_values,omitempty"`

	// FieldFilter constrains acceptable field types when Type=="field".
	// One of: "numeric", "categorical", "date", "geo", "any".
	FieldFilter string `json:"field_filter,omitempty"`
}

// TestMeta describes a statistical test entry in the manifest. Tier-1 and
// tier-2 tests share this shape; they live in separate top-level slices
// (Manifest.Tests for tier-1, Manifest.PostTests for tier-2). The Family
// field ties variants of the same underlying test together across tiers
// so clients can filter by family.
type TestMeta struct {
	// Name is the canonical entry identifier. For tier-1 this is the
	// TestType string (e.g. "TEST_ANOVA_WELCH"). For tier-2 this is the
	// TestType plus the variant suffix
	// (e.g. "TEST_ANOVA_WELCH/welch_one_way_post").
	Name string `json:"name"`

	// Family is the canonical TestType the variant belongs to
	// (e.g. "TEST_PEARSON_R"). Tier-1 entries always have
	// Family == Name. Tier-2 entries set Family to the underlying
	// TestType so clients can pair siblings.
	Family string `json:"family"`

	// Tier is 1 for row tests in Request.Tests, 2 for post tests in
	// Request.PostTests.
	Tier int `json:"tier"`

	// Variant is the algorithm flavour for tier-2 entries
	// (e.g. "welch_one_way_post"). Empty for tier-1 entries.
	Variant string `json:"variant,omitempty"`

	// Description is a one-sentence prose summary.
	Description string `json:"description"`

	// Streamable mirrors types.TestType.Streamable() for tier-1 entries.
	// Always false for tier-2 entries.
	Streamable bool `json:"streamable"`

	// Params lists the operator-specific parameters (alpha,
	// success_value, etc.).
	Params []Param `json:"params"`

	// Requires lists the top-level Test fields that must be set for the
	// test to run (e.g. "Field", "Field2", "SplitBy", "Rows", "Cols").
	// Drives request authoring directly.
	Requires []string `json:"requires,omitempty"`
}

// DistributionMeta describes a synth distribution entry. One entry per
// synth.AllDistributions() value.
type DistributionMeta struct {
	// Name is the distribution kind identifier (e.g. "lognormal").
	Name string `json:"name"`

	// Description is a one-sentence prose summary.
	Description string `json:"description"`

	// AppliesTo lists the FieldSpec types the distribution can drive.
	// Values are coarse families: "numeric", "categorical", "date",
	// "bool", "any".
	AppliesTo []string `json:"applies_to"`

	// Params lists the distribution-specific parameters.
	Params []Param `json:"params"`
}

// ErrorMeta describes a single error code entry. One entry per
// errors.AllCodes() value.
type ErrorMeta struct {
	// Code is the error code identifier (e.g.
	// "PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL").
	Code string `json:"code"`

	// Domain is the prefix segment of the code (PULSE, SERVICE,
	// PROCESSING, ENCODING, DATA, CLI).
	Domain string `json:"domain"`

	// Severity is "error" for failure codes and "warning" for
	// diagnostic-only codes. Today every code is reported as both
	// (depending on caller context), so severity is the soft default
	// recorded here.
	Severity string `json:"severity"`

	// Description is a one-sentence prose summary of when this code
	// surfaces and what triggered it.
	Description string `json:"description"`

	// Fixups holds remediation hint templates. Populated once the
	// error-fixup-hints work lands; empty for now and omitempty in
	// JSON. The slot is here so the contract stays stable.
	Fixups []string `json:"fixups,omitempty"`
}

// MCPTool describes a single registered MCP tool. One entry per
// internal/mcp.RegisteredTools() value.
type MCPTool struct {
	// Name is the tool identifier (e.g. "pulse_predict").
	Name string `json:"name"`

	// Description mirrors the description string registered with the
	// MCP server.
	Description string `json:"description"`
}
