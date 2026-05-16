package descriptor

import (
	"strconv"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// DefaultApplied describes a single defaulted operator slot. Returned by
// ResolveDefaults so callers (predict, service, MCP transcripts) can echo
// exactly which slots had their Type inferred from the schema. The shape
// is JSON-serializable and lands on PredictResult.DefaultsApplied.
//
// Path uses JSON-style segments e.g. ["Aggregations", "0", "Type"] so the
// caller can address the slot in the original request. Field names the
// schema column that drove inference; Type is the operator string that
// was filled in; Category is "aggregation" or "grouper"; Reason carries a
// short human-readable rule trace, e.g. "f64 → AGG_SUM (rule: numeric
// default)".
type DefaultApplied struct {
	Path     []string `json:"path"`
	Field    string   `json:"field"`
	Type     string   `json:"type"`
	Category string   `json:"category"`
	Reason   string   `json:"reason"`
}

// defaultRule captures the operator defaults for a single field type.
// Empty Agg or Group means "no default applies for that category".
type defaultRule struct {
	// Agg is the aggregation type to default to. Empty means no default.
	Agg types.AggregationType
	// Group is the grouper type to default to. Empty means no default.
	Group types.GroupType
	// FamilyTag is a short string used in the Reason field (e.g.
	// "numeric default", "categorical default") so the trace reads
	// naturally without referencing rule numbers.
	FamilyTag string
}

// defaultRules is the authoritative field-type → default operator table.
// Source of truth for predict.DefaultsApplied and service-side resolution.
// Keep in sync with the Smart Defaults subsection of CLAUDE.md and the
// "Defaults" section of skills/getting-started.md.
var defaultRules = map[encoding.FieldType]defaultRule{
	// Numeric integers and floats: SUM aggregation, RANGE bucketing.
	encoding.FieldTypeU8:                 {Agg: types.AGG_SUM, Group: types.GROUP_RANGE, FamilyTag: "numeric default"},
	encoding.FieldTypeU16:                {Agg: types.AGG_SUM, Group: types.GROUP_RANGE, FamilyTag: "numeric default"},
	encoding.FieldTypeU32:                {Agg: types.AGG_SUM, Group: types.GROUP_RANGE, FamilyTag: "numeric default"},
	encoding.FieldTypeU64:                {Agg: types.AGG_SUM, Group: types.GROUP_RANGE, FamilyTag: "numeric default"},
	encoding.FieldTypeF32:                {Agg: types.AGG_SUM, Group: types.GROUP_RANGE, FamilyTag: "numeric default"},
	encoding.FieldTypeF64:                {Agg: types.AGG_SUM, Group: types.GROUP_RANGE, FamilyTag: "numeric default"},
	encoding.FieldTypeNullableU4:         {Agg: types.AGG_SUM, Group: types.GROUP_RANGE, FamilyTag: "numeric default"},
	encoding.FieldTypeNullableU8:         {Agg: types.AGG_SUM, Group: types.GROUP_RANGE, FamilyTag: "numeric default"},
	encoding.FieldTypeNullableU16:        {Agg: types.AGG_SUM, Group: types.GROUP_RANGE, FamilyTag: "numeric default"},
	encoding.FieldTypeDecimal128:         {Agg: types.AGG_SUM, Group: types.GROUP_RANGE, FamilyTag: "numeric default"},
	encoding.FieldTypeNullableDecimal128: {Agg: types.AGG_SUM, Group: types.GROUP_RANGE, FamilyTag: "numeric default"},

	// Categorical fields: FREQUENCY tallies values, CATEGORY partitions by them.
	encoding.FieldTypeCategoricalU8:  {Agg: types.AGG_FREQUENCY, Group: types.GROUP_CATEGORY, FamilyTag: "categorical default"},
	encoding.FieldTypeCategoricalU16: {Agg: types.AGG_FREQUENCY, Group: types.GROUP_CATEGORY, FamilyTag: "categorical default"},
	encoding.FieldTypeCategoricalU32: {Agg: types.AGG_FREQUENCY, Group: types.GROUP_CATEGORY, FamilyTag: "categorical default"},

	// Date: no aggregation default (must be explicit); GROUP_DATE (day bucket).
	encoding.FieldTypeDate: {Agg: "", Group: types.GROUP_DATE, FamilyTag: "date default"},

	// Booleans (single-bit or tri-state): treated as categorical for defaulting.
	encoding.FieldTypeNullableBool: {Agg: types.AGG_FREQUENCY, Group: types.GROUP_CATEGORY, FamilyTag: "boolean default"},
	encoding.FieldTypePackedBool:   {Agg: types.AGG_FREQUENCY, Group: types.GROUP_CATEGORY, FamilyTag: "boolean default"},
}

// defaultRangeInterval is the bucket width applied to a newly-defaulted
// GROUP_RANGE slot when the caller did not set one. The plan calls for
// "10 buckets"; the grouper is interval-based rather than count-based,
// so a width of 10.0 is the analogue. Callers that want column-aware
// bucketing can override by setting Interval explicitly.
const defaultRangeInterval = 10.0

// defaultDateComponent is the calendar bucket applied to a newly-
// defaulted GROUP_DATE slot when the caller did not set Params.component.
// "day" matches the plan; processing's dateGrouper already falls back to
// "month" if Params is absent, but we want the trace to be explicit.
const defaultDateComponent = "day"

// ResolveDefaults inspects every aggregation and grouper slot in req,
// fills in the Type for slots that name a field but omit the Type, and
// returns one DefaultApplied entry per change made. Mutates req in place.
//
// Rules:
//   - Defaults never override an explicit Type.
//   - Defaults never cross categories (a missing aggregator does not
//     insert a grouper, and vice versa).
//   - Tests (req.Tests, req.PostTests) are not defaulted; hypothesis tests
//     are intent-bearing.
//   - Field types absent from defaultRules (or with no rule for the slot's
//     category) are left untouched and contribute no entry.
//
// Returns a nil slice when nothing changed.
func ResolveDefaults(req *types.Request, schema *encoding.Schema) []DefaultApplied {
	if req == nil || schema == nil {
		return nil
	}

	var applied []DefaultApplied

	for i, agg := range req.Aggregations {
		if agg == nil || agg.Type != "" || agg.Field == "" {
			continue
		}
		f := schema.Field(agg.Field)
		if f == nil {
			continue
		}
		rule, ok := defaultRules[f.Type]
		if !ok || rule.Agg == "" {
			continue
		}
		agg.Type = rule.Agg
		applied = append(applied, DefaultApplied{
			Path:     []string{"Aggregations", strconv.Itoa(i), "Type"},
			Field:    agg.Field,
			Type:     string(rule.Agg),
			Category: "aggregation",
			Reason:   f.Type.String() + " → " + string(rule.Agg) + " (rule: " + rule.FamilyTag + ")",
		})
	}

	for i, grp := range req.Groups {
		if grp == nil || grp.Type != "" || grp.Field == "" {
			continue
		}
		f := schema.Field(grp.Field)
		if f == nil {
			continue
		}
		rule, ok := defaultRules[f.Type]
		if !ok || rule.Group == "" {
			continue
		}
		grp.Type = rule.Group

		// Apply category-specific parameter defaults so the inferred
		// grouper is runnable without further configuration.
		applyDefaultGroupParams(grp)

		applied = append(applied, DefaultApplied{
			Path:     []string{"Groups", strconv.Itoa(i), "Type"},
			Field:    grp.Field,
			Type:     string(rule.Group),
			Category: "grouper",
			Reason:   f.Type.String() + " → " + string(rule.Group) + " (rule: " + rule.FamilyTag + ")",
		})
	}

	return applied
}

// applyDefaultGroupParams fills in operator-specific configuration for a
// freshly-defaulted grouper slot. Only set values that the caller left
// unset — explicit configuration always wins.
//
// GROUP_RANGE: defaults Interval = defaultRangeInterval when Interval is
//
//	zero (the processing layer further falls back to 1 if still zero, so
//	this is purely about producing a sane default trace).
//
// GROUP_DATE: defaults Params.component = defaultDateComponent when
//
//	Params is empty.
func applyDefaultGroupParams(grp *types.Group) {
	switch grp.Type {
	case types.GROUP_RANGE:
		if grp.Interval == 0 {
			grp.Interval = defaultRangeInterval
		}
	case types.GROUP_DATE:
		if len(grp.Params) == 0 {
			grp.Params = []byte(`{"component":"` + defaultDateComponent + `"}`)
		}
	}
}

// cloneRequestForDefaults returns a shallow-deep hybrid copy of req
// sufficient for ResolveDefaults to mutate without touching the caller's
// request. Only the slots ResolveDefaults inspects (Aggregations, Groups)
// are deeply cloned; the rest is left shared because predict treats it as
// read-only.
//
// Used by predict to compute DefaultsApplied without altering the request
// echoed back to the caller.
func cloneRequestForDefaults(req *types.Request) *types.Request {
	if req == nil {
		return nil
	}
	clone := *req
	if len(req.Aggregations) > 0 {
		clone.Aggregations = make([]*types.Aggregation, len(req.Aggregations))
		for i, a := range req.Aggregations {
			if a == nil {
				continue
			}
			ac := *a
			clone.Aggregations[i] = &ac
		}
	}
	if len(req.Groups) > 0 {
		clone.Groups = make([]*types.Group, len(req.Groups))
		for i, g := range req.Groups {
			if g == nil {
				continue
			}
			gc := *g
			clone.Groups[i] = &gc
		}
	}
	return &clone
}
