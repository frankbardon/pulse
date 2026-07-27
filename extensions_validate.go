package pulse

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/synth"
	"github.com/frankbardon/pulse/types"
)

// extensionNameRegex captures the embedder-registration naming policy:
//
//	^(AGG|ATTR|FILTER|GROUP|WIN|FEAT|TEST|SYNTH)_<NAMESPACE>_<NAME>$
//
// with NAMESPACE and NAME drawn from uppercase ASCII (digits and
// underscores allowed after the first letter). The Pulse-shipped
// built-ins use the legacy two-segment form (AGG_COUNT, ATTR_ZSCORE)
// and are exempt from this regex; they are registered statically by
// the engine, not by embedders.
var extensionNameRegex = regexp.MustCompile(
	`^(AGG|ATTR|FILTER|GROUP|WIN|FEAT|TEST|SYNTH)_([A-Z][A-Z0-9]+)_([A-Z](?:[A-Z0-9_]*[A-Z0-9])?)$`,
)

// extensionCategory tags every registration slot for validation
// messages. The string value is part of the public error details
// payload, so changes are user-facing.
type extensionCategory string

const (
	categoryAggregator   extensionCategory = "aggregator"
	categoryAttribute    extensionCategory = "attribute"
	categoryFilterer     extensionCategory = "filterer"
	categoryGrouper      extensionCategory = "grouper"
	categoryWindow       extensionCategory = "window"
	categoryFeature      extensionCategory = "feature"
	categoryTest         extensionCategory = "test"
	categoryDistribution extensionCategory = "synth_distribution"
)

// expectedPrefix is the category-token half of the registration name
// (i.e. the segment before the first underscore).
func (c extensionCategory) expectedPrefix() string {
	switch c {
	case categoryAggregator:
		return "AGG"
	case categoryAttribute:
		return "ATTR"
	case categoryFilterer:
		return "FILTER"
	case categoryGrouper:
		return "GROUP"
	case categoryWindow:
		return "WIN"
	case categoryFeature:
		return "FEAT"
	case categoryTest:
		return "TEST"
	case categoryDistribution:
		return "SYNTH"
	}
	return ""
}

// validJSONTypes is the closed set of ParamMeta.JSONType values.
var validJSONTypes = map[string]struct{}{
	"string":  {},
	"number":  {},
	"boolean": {},
	"array":   {},
	"object":  {},
}

// validateExtensions walks the Extensions struct and returns the first
// validation failure as a CodedError. Validation order is per-category
// then by slice index; ExprFunctions / LookupTables come after the
// operator slices.
//
// Built-in collision checks use the live type registries
// (types.All*Types() + synth.AllDistributions()) so adding a new
// built-in automatically reserves its name against embedder
// collisions.
func validateExtensions(ext Extensions) error {
	if err := validateOperatorSlice(ext.Aggregators, categoryAggregator, builtinAggregatorNames(), func(r AggregatorRegistration) (string, []ParamMeta) {
		return string(r.Name), r.Params
	}); err != nil {
		return err
	}
	if err := validateOperatorSlice(ext.Attributes, categoryAttribute, builtinAttributeNames(), func(r AttributeRegistration) (string, []ParamMeta) {
		return string(r.Name), r.Params
	}); err != nil {
		return err
	}
	if err := validateAttributeModes(ext.Attributes); err != nil {
		return err
	}
	if err := validateOperatorSlice(ext.Filterers, categoryFilterer, builtinFiltererNames(), func(r FiltererRegistration) (string, []ParamMeta) {
		return string(r.Name), r.Params
	}); err != nil {
		return err
	}
	if err := validateOperatorSlice(ext.Groupers, categoryGrouper, builtinGrouperNames(), func(r GrouperRegistration) (string, []ParamMeta) {
		return string(r.Name), r.Params
	}); err != nil {
		return err
	}
	if err := validateOperatorSlice(ext.Windows, categoryWindow, builtinWindowNames(), func(r WindowRegistration) (string, []ParamMeta) {
		return string(r.Name), r.Params
	}); err != nil {
		return err
	}
	if err := validateOperatorSlice(ext.Features, categoryFeature, builtinFeatureNames(), func(r FeatureRegistration) (string, []ParamMeta) {
		return string(r.Name), r.Params
	}); err != nil {
		return err
	}
	if err := validateOperatorSlice(ext.Tests, categoryTest, builtinTestNames(), func(r TestRegistration) (string, []ParamMeta) {
		return string(r.Name), r.Params
	}); err != nil {
		return err
	}
	if err := validateTestTiers(ext.Tests); err != nil {
		return err
	}
	if err := validateOperatorSlice(ext.SynthDistributions, categoryDistribution, builtinDistributionNames(), func(r DistributionRegistration) (string, []ParamMeta) {
		return r.Name, r.Params
	}); err != nil {
		return err
	}
	if err := validateExprFunctions(ext.ExprFunctions); err != nil {
		return err
	}
	if err := validateLookupTables(ext.LookupTables); err != nil {
		return err
	}
	if err := validateLabelTables(ext.LabelTables); err != nil {
		return err
	}
	if err := validateRangeTables(ext.RangeTables); err != nil {
		return err
	}
	if err := validateComponentSchemas(ext); err != nil {
		return err
	}
	return nil
}

// validateComponentSchemas asserts every aggregator / grouper / filterer
// registration's ComponentSchema and ComponentsFunc declarations are
// consistent.
//
// Three valid registration shapes:
//   - Floor-only: empty Keys, empty Mergeability, nil ComponentsFunc.
//     The orchestrator emits the universal floor only.
//   - Floor + emitter: empty Keys, empty Mergeability, ComponentsFunc
//     supplied. Rejected — declaring an emitter without a schema is a
//     contract bug (the orchestrator would have no way to validate
//     emitted keys). Raises PULSE_EXTENSION_MISSING_COMPONENT_SCHEMA.
//   - Schema + emitter: non-empty Keys, declared Mergeability,
//     ComponentsFunc supplied. The canonical extension path.
//
// Schema-without-emitter (non-empty Keys, nil ComponentsFunc) is also
// rejected — the declared schema would never be satisfied at runtime.
// Raises PULSE_EXTENSION_PARAM_INVALID (the schema exists, the emitter
// is missing — a parameter contradiction, not a missing schema).
//
// Schema-without-mergeability (non-empty Keys, empty Mergeability) is
// rejected — every schema must declare a mergeability classification.
// Raises PULSE_EXTENSION_PARAM_INVALID.
//
// Mergeability-without-Keys (empty Keys, non-empty Mergeability) is
// rejected — mergeability has nothing to classify without keys. Raises
// PULSE_EXTENSION_MISSING_COMPONENT_SCHEMA (this is the same incoherent
// shape the probe catches: a schema declaration that is half-present).
//
// PULSE_EXTENSION_MISSING_COMPONENT_SCHEMA is enforced here alongside
// the probe-time PULSE_EXTENSION_COMPONENT_SCHEMA_MISMATCH check.
func validateComponentSchemas(ext Extensions) error {
	for i, r := range ext.Aggregators {
		if err := validateComponentSchemaDeclaration(
			"aggregator", string(r.Name), i,
			r.ComponentSchema, r.ComponentsFunc != nil,
		); err != nil {
			return err
		}
	}
	for i, r := range ext.Groupers {
		if err := validateComponentSchemaDeclaration(
			"grouper", string(r.Name), i,
			r.ComponentSchema, r.ComponentsFunc != nil,
		); err != nil {
			return err
		}
	}
	for i, r := range ext.Filterers {
		if err := validateComponentSchemaDeclaration(
			"filterer", string(r.Name), i,
			r.ComponentSchema, r.ComponentsFunc != nil,
		); err != nil {
			return err
		}
	}
	return nil
}

// validateComponentSchemaDeclaration enforces the per-registration
// consistency rules between ComponentSchema and ComponentsFunc. See
// validateComponentSchemas for the matrix.
func validateComponentSchemaDeclaration(
	category, name string,
	idx int,
	schema descriptor.ComponentSchema,
	hasEmitter bool,
) error {
	hasKeys := len(schema.Keys) > 0
	hasMergeability := schema.Mergeability != ""

	// Floor-only: neither keys nor emitter — OK.
	if !hasKeys && !hasMergeability && !hasEmitter {
		return nil
	}
	// Emitter without schema — incoherent: the orchestrator would have
	// no schema to validate emitted keys against.
	if hasEmitter && !hasKeys {
		return errors.NewCodedErrorWithDetails(
			errors.PULSE_EXTENSION_MISSING_COMPONENT_SCHEMA,
			fmt.Sprintf("extension %s %q has ComponentsFunc but empty ComponentSchema.Keys (declare schema or remove emitter)", category, name),
			map[string]any{
				"category": category,
				"name":     name,
				"index":    idx,
				"reason":   "emitter_without_schema",
			},
		)
	}
	// Mergeability set without keys — incoherent: mergeability has
	// nothing to classify without keys.
	if !hasKeys && hasMergeability {
		return errors.NewCodedErrorWithDetails(
			errors.PULSE_EXTENSION_MISSING_COMPONENT_SCHEMA,
			fmt.Sprintf("extension %s %q declares ComponentSchema.Mergeability but no Keys (mergeability has nothing to classify)", category, name),
			map[string]any{
				"category":     category,
				"name":         name,
				"index":        idx,
				"mergeability": string(schema.Mergeability),
				"reason":       "mergeability_without_keys",
			},
		)
	}
	// Schema without emitter — declared schema can never be satisfied.
	if hasKeys && !hasEmitter {
		return errors.NewCodedErrorWithDetails(
			errors.PULSE_EXTENSION_PARAM_INVALID,
			fmt.Sprintf("extension %s %q declares ComponentSchema.Keys but supplies no ComponentsFunc (no emitter would satisfy the schema)", category, name),
			map[string]any{
				"category": category,
				"name":     name,
				"index":    idx,
				"reason":   "schema_without_emitter",
			},
		)
	}
	// Schema without mergeability — every schema must declare intent.
	if hasKeys && !hasMergeability {
		return errors.NewCodedErrorWithDetails(
			errors.PULSE_EXTENSION_PARAM_INVALID,
			fmt.Sprintf("extension %s %q declares ComponentSchema.Keys but no Mergeability (declare mergeable/partial/none, or use floor-only)", category, name),
			map[string]any{
				"category": category,
				"name":     name,
				"index":    idx,
				"reason":   "missing_mergeability",
			},
		)
	}
	return nil
}

// validateOperatorSlice runs the common naming + duplicate + ParamMeta
// validation pass against any registration slice. The extract callback
// pulls the registration's name + params, which differ by struct.
func validateOperatorSlice[R any](
	regs []R,
	cat extensionCategory,
	builtins map[string]struct{},
	extract func(R) (string, []ParamMeta),
) error {
	seen := make(map[string]struct{}, len(regs))
	for i, r := range regs {
		name, params := extract(r)
		// Built-in collision check runs first: a registration that
		// shadows a Pulse-shipped name is more informative as
		// PULSE_EXTENSION_NAME_COLLISION than as a regex failure,
		// even when the built-in itself uses the legacy two-segment
		// form (AGG_COUNT, ATTR_ZSCORE).
		if _, dup := builtins[name]; dup {
			return errors.NewCodedErrorWithDetails(
				errors.PULSE_EXTENSION_NAME_COLLISION,
				fmt.Sprintf("extension %s %q collides with a built-in name", cat, name),
				map[string]any{"category": string(cat), "name": name, "index": i},
			)
		}
		if err := validateExtensionName(name, cat); err != nil {
			return wrapExtensionError(err, cat, i, name)
		}
		if _, dup := seen[name]; dup {
			return errors.NewCodedErrorWithDetails(
				errors.PULSE_EXTENSION_DUPLICATE,
				fmt.Sprintf("extension %s %q registered more than once", cat, name),
				map[string]any{"category": string(cat), "name": name, "index": i},
			)
		}
		seen[name] = struct{}{}
		if err := validateParams(params, cat, name); err != nil {
			return err
		}
	}
	return nil
}

// validateExtensionName enforces the naming policy: regex match,
// reserved-namespace check, and a category-prefix sanity check
// (an AggregatorRegistration cannot smuggle an ATTR_*-prefixed name).
func validateExtensionName(name string, cat extensionCategory) error {
	if name == "" {
		return errors.NewCodedErrorWithDetails(
			errors.PULSE_EXTENSION_NAME_INVALID,
			fmt.Sprintf("extension %s registration has empty Name", cat),
			map[string]any{"category": string(cat), "name": name},
		)
	}
	groups := extensionNameRegex.FindStringSubmatch(name)
	if groups == nil {
		return errors.NewCodedErrorWithDetails(
			errors.PULSE_EXTENSION_NAME_INVALID,
			fmt.Sprintf("extension %s name %q does not match required pattern <CATEGORY>_<NAMESPACE>_<NAME>", cat, name),
			map[string]any{
				"category": string(cat),
				"name":     name,
				"pattern":  extensionNameRegex.String(),
			},
		)
	}
	prefix, namespace := groups[1], groups[2]
	if prefix != cat.expectedPrefix() {
		return errors.NewCodedErrorWithDetails(
			errors.PULSE_EXTENSION_NAME_INVALID,
			fmt.Sprintf("extension %s name %q has wrong category prefix (expected %s_)", cat, name, cat.expectedPrefix()),
			map[string]any{
				"category":        string(cat),
				"name":            name,
				"expected_prefix": cat.expectedPrefix(),
				"actual_prefix":   prefix,
			},
		)
	}
	if _, reserved := reservedExtensionNamespaces[namespace]; reserved {
		return errors.NewCodedErrorWithDetails(
			errors.PULSE_EXTENSION_NAME_RESERVED,
			fmt.Sprintf("extension %s name %q uses reserved namespace %q", cat, name, namespace),
			map[string]any{
				"category":  string(cat),
				"name":      name,
				"namespace": namespace,
				"reserved":  sortedReservedNamespaces(),
			},
		)
	}
	return nil
}

// validateParams asserts every ParamMeta has the required fields and
// no contradictory combinations.
func validateParams(params []ParamMeta, cat extensionCategory, regName string) error {
	for i, p := range params {
		if strings.TrimSpace(p.Name) == "" {
			return errors.NewCodedErrorWithDetails(
				errors.PULSE_EXTENSION_PARAM_INVALID,
				fmt.Sprintf("extension %s %q param[%d] has empty Name", cat, regName, i),
				map[string]any{"category": string(cat), "name": regName, "param_index": i},
			)
		}
		if _, ok := validJSONTypes[p.JSONType]; !ok {
			return errors.NewCodedErrorWithDetails(
				errors.PULSE_EXTENSION_PARAM_INVALID,
				fmt.Sprintf("extension %s %q param[%d] (%q) has unknown JSONType %q", cat, regName, i, p.Name, p.JSONType),
				map[string]any{
					"category":    string(cat),
					"name":        regName,
					"param_index": i,
					"param_name":  p.Name,
					"json_type":   p.JSONType,
				},
			)
		}
		if p.Required && p.Default != nil {
			return errors.NewCodedErrorWithDetails(
				errors.PULSE_EXTENSION_PARAM_INVALID,
				fmt.Sprintf("extension %s %q param[%d] (%q) is Required=true but carries a Default", cat, regName, i, p.Name),
				map[string]any{
					"category":    string(cat),
					"name":        regName,
					"param_index": i,
					"param_name":  p.Name,
				},
			)
		}
	}
	return nil
}

// validateAttributeModes asserts every AttributeRegistration declares
// a known Mode.
func validateAttributeModes(regs []AttributeRegistration) error {
	for i, r := range regs {
		switch r.Mode {
		case AttributeModeRowLocal, AttributeModeTwoPass, AttributeModeBuffered:
			// ok
		case "":
			return errors.NewCodedErrorWithDetails(
				errors.PULSE_EXTENSION_PARAM_INVALID,
				fmt.Sprintf("extension attribute %q has empty Mode", r.Name),
				map[string]any{"category": "attribute", "name": string(r.Name), "index": i},
			)
		default:
			return errors.NewCodedErrorWithDetails(
				errors.PULSE_EXTENSION_PARAM_INVALID,
				fmt.Sprintf("extension attribute %q has unknown Mode %q", r.Name, r.Mode),
				map[string]any{
					"category": "attribute",
					"name":     string(r.Name),
					"index":    i,
					"mode":     string(r.Mode),
					"valid":    []string{string(AttributeModeRowLocal), string(AttributeModeTwoPass), string(AttributeModeBuffered)},
				},
			)
		}
	}
	return nil
}

// validateTestTiers asserts every TestRegistration declares a valid
// Tier and supplies exactly the matching factory.
func validateTestTiers(regs []TestRegistration) error {
	for i, r := range regs {
		switch r.Tier {
		case TestTierRow:
			if r.RowFactory == nil {
				return errors.NewCodedErrorWithDetails(
					errors.PULSE_EXTENSION_PARAM_INVALID,
					fmt.Sprintf("extension test %q tier=tier1 requires RowFactory", r.Name),
					map[string]any{"category": "test", "name": string(r.Name), "index": i, "tier": "tier1"},
				)
			}
			if r.PostFactory != nil {
				return errors.NewCodedErrorWithDetails(
					errors.PULSE_EXTENSION_PARAM_INVALID,
					fmt.Sprintf("extension test %q tier=tier1 must not supply PostFactory", r.Name),
					map[string]any{"category": "test", "name": string(r.Name), "index": i, "tier": "tier1"},
				)
			}
		case TestTierPost:
			if r.PostFactory == nil {
				return errors.NewCodedErrorWithDetails(
					errors.PULSE_EXTENSION_PARAM_INVALID,
					fmt.Sprintf("extension test %q tier=tier2 requires PostFactory", r.Name),
					map[string]any{"category": "test", "name": string(r.Name), "index": i, "tier": "tier2"},
				)
			}
			if r.RowFactory != nil {
				return errors.NewCodedErrorWithDetails(
					errors.PULSE_EXTENSION_PARAM_INVALID,
					fmt.Sprintf("extension test %q tier=tier2 must not supply RowFactory", r.Name),
					map[string]any{"category": "test", "name": string(r.Name), "index": i, "tier": "tier2"},
				)
			}
		case "":
			return errors.NewCodedErrorWithDetails(
				errors.PULSE_EXTENSION_PARAM_INVALID,
				fmt.Sprintf("extension test %q has empty Tier", r.Name),
				map[string]any{"category": "test", "name": string(r.Name), "index": i},
			)
		default:
			return errors.NewCodedErrorWithDetails(
				errors.PULSE_EXTENSION_PARAM_INVALID,
				fmt.Sprintf("extension test %q has unknown Tier %q", r.Name, r.Tier),
				map[string]any{
					"category": "test",
					"name":     string(r.Name),
					"index":    i,
					"tier":     string(r.Tier),
					"valid":    []string{string(TestTierRow), string(TestTierPost)},
				},
			)
		}
	}
	return nil
}

// validateExprFunctions asserts every ExprFunction has Name + Fn and
// no duplicate names.
func validateExprFunctions(fns []ExprFunction) error {
	seen := make(map[string]struct{}, len(fns))
	for i, fn := range fns {
		if strings.TrimSpace(fn.Name) == "" {
			return errors.NewCodedErrorWithDetails(
				errors.PULSE_EXTENSION_PARAM_INVALID,
				fmt.Sprintf("extension expr function index %d has empty Name", i),
				map[string]any{"category": "expr_function", "index": i},
			)
		}
		if fn.Fn == nil {
			return errors.NewCodedErrorWithDetails(
				errors.PULSE_EXTENSION_PARAM_INVALID,
				fmt.Sprintf("extension expr function %q has nil Fn", fn.Name),
				map[string]any{"category": "expr_function", "name": fn.Name, "index": i},
			)
		}
		if _, dup := seen[fn.Name]; dup {
			return errors.NewCodedErrorWithDetails(
				errors.PULSE_EXTENSION_DUPLICATE,
				fmt.Sprintf("extension expr function %q registered more than once", fn.Name),
				map[string]any{"category": "expr_function", "name": fn.Name, "index": i},
			)
		}
		seen[fn.Name] = struct{}{}
	}
	return nil
}

// validateLookupTables asserts exactly one of Rows / Lookup is set per
// table and that the table name is non-empty.
func validateLookupTables(tables map[string]LookupTable) error {
	for name, t := range tables {
		if strings.TrimSpace(name) == "" {
			return errors.NewCodedErrorWithDetails(
				errors.PULSE_EXTENSION_PARAM_INVALID,
				"lookup table has empty name",
				map[string]any{"category": "lookup_table"},
			)
		}
		hasRows := t.Rows != nil
		hasFn := t.Lookup != nil
		if hasRows == hasFn {
			return errors.NewCodedErrorWithDetails(
				errors.PULSE_EXTENSION_PARAM_INVALID,
				fmt.Sprintf("lookup table %q must set exactly one of Rows or Lookup (got rows=%v lookup=%v)", name, hasRows, hasFn),
				map[string]any{"category": "lookup_table", "name": name, "has_rows": hasRows, "has_lookup": hasFn},
			)
		}
	}
	return nil
}

// validateLabelTables asserts exactly one of Rows / Lookup is set per
// table and that the table name is non-empty. Label tables are
// referenced by name from per-request LabelBinding entries.
func validateLabelTables(tables map[string]LabelTable) error {
	for name, t := range tables {
		if strings.TrimSpace(name) == "" {
			return errors.NewCodedErrorWithDetails(
				errors.PULSE_EXTENSION_PARAM_INVALID,
				"label table has empty name",
				map[string]any{"category": "label_table"},
			)
		}
		hasRows := t.Rows != nil
		hasFn := t.Lookup != nil
		if hasRows == hasFn {
			return errors.NewCodedErrorWithDetails(
				errors.PULSE_EXTENSION_PARAM_INVALID,
				fmt.Sprintf("label table %q must set exactly one of Rows or Lookup (got rows=%v lookup=%v)", name, hasRows, hasFn),
				map[string]any{"category": "label_table", "name": name, "has_rows": hasRows, "has_lookup": hasFn},
			)
		}
	}
	return nil
}

// validateRangeTables asserts every registered range table has a
// non-empty name and that its ordered {label, start, end} entries pass
// the shared range-compilation validation (empty set, overlapping
// ranges, duplicate labels, and unparseable / inverted boundaries each
// surface the matching PULSE_RANGE_* code). A table that nothing
// references is still validated here — registration is the contract, not
// usage. The compiled set is discarded: this pass validates only, and
// the runtime recompiles from the specs when a consumer resolves the
// table by name.
func validateRangeTables(tables map[string]RangeTable) error {
	for name, t := range tables {
		if strings.TrimSpace(name) == "" {
			return errors.NewCodedErrorWithDetails(
				errors.PULSE_EXTENSION_PARAM_INVALID,
				"range table has empty name",
				map[string]any{"category": "range_table"},
			)
		}
		if _, err := processing.CompileDateRanges(t.Ranges); err != nil {
			// CompileDateRanges emits the PULSE_RANGE_* coded error; wrap
			// it with the offending table name for actionable diagnostics.
			if ce, ok := err.(*errors.CodedError); ok {
				if ce.Details == nil {
					ce.Details = map[string]any{}
				}
				ce.Details["range_table"] = name
				return ce
			}
			return err
		}
	}
	return nil
}

// wrapExtensionError augments a name-validation error with the
// position in the registration slice. Other validation paths build
// their own CodedError directly with the same details shape.
func wrapExtensionError(err error, cat extensionCategory, idx int, name string) error {
	ce, ok := err.(*errors.CodedError)
	if !ok {
		return err
	}
	if ce.Details == nil {
		ce.Details = map[string]any{}
	}
	ce.Details["index"] = idx
	ce.Details["registration_name"] = name
	ce.Details["category"] = string(cat)
	return ce
}

// builtinAggregatorNames returns the set of Pulse-shipped aggregator
// names as a lookup-friendly map.
func builtinAggregatorNames() map[string]struct{} {
	out := make(map[string]struct{}, 32)
	for _, t := range types.AllAggregationTypes() {
		out[string(t)] = struct{}{}
	}
	return out
}

func builtinAttributeNames() map[string]struct{} {
	out := make(map[string]struct{}, 16)
	for _, t := range types.AllAttributeTypes() {
		out[string(t)] = struct{}{}
	}
	return out
}

func builtinFiltererNames() map[string]struct{} {
	out := make(map[string]struct{}, 16)
	for _, t := range types.AllFiltererTypes() {
		out[string(t)] = struct{}{}
	}
	return out
}

func builtinGrouperNames() map[string]struct{} {
	out := make(map[string]struct{}, 16)
	for _, t := range types.AllGroupTypes() {
		out[string(t)] = struct{}{}
	}
	return out
}

func builtinWindowNames() map[string]struct{} {
	out := make(map[string]struct{}, 16)
	for _, t := range types.AllWindowTypes() {
		out[string(t)] = struct{}{}
	}
	return out
}

func builtinFeatureNames() map[string]struct{} {
	out := make(map[string]struct{}, 16)
	for _, t := range types.AllFeatureTypes() {
		out[string(t)] = struct{}{}
	}
	return out
}

func builtinTestNames() map[string]struct{} {
	out := make(map[string]struct{}, 32)
	for _, t := range types.AllTestTypes() {
		out[string(t)] = struct{}{}
	}
	return out
}

func builtinDistributionNames() map[string]struct{} {
	dists := synth.AllDistributions()
	out := make(map[string]struct{}, len(dists))
	for _, name := range dists {
		out[name] = struct{}{}
	}
	return out
}

// sortedReservedNamespaces returns the reserved set as a stable
// slice for error details payloads.
func sortedReservedNamespaces() []string {
	out := make([]string, 0, len(reservedExtensionNamespaces))
	for k := range reservedExtensionNamespaces {
		out = append(out, k)
	}
	// Manual sort to avoid pulling sort just for four entries; the
	// reserved set is intentionally small.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}
