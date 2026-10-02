package pulse

import (
	"encoding/json"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	descx "github.com/frankbardon/pulse/internal/descriptor"
)

// buildExtensionsSnapshot translates the public Extensions struct
// into the read-only descriptor projection consumed by manifest +
// predict. Returns nil when ext carries no registrations of any
// kind, so the snapshot path stays a single nil-check at the call
// site.
func buildExtensionsSnapshot(ext Extensions) *descx.ExtensionsSnapshot {
	if !hasAnyRegistrations(ext) {
		return nil
	}
	snap := &descx.ExtensionsSnapshot{}
	// FieldInputs: the same hooks extensions_runtime.go hands the
	// projection extractor, keyed identically, so the shared
	// field-reference rule judges what each extension declares it reads.
	snap.FieldInputs = map[string]func(json.RawMessage) []string{}
	addInputs := func(category, name string, fn FieldInputsFunc) {
		if fn != nil {
			snap.FieldInputs[category+"|"+name] = fn
		}
	}
	for _, r := range ext.Aggregators {
		addInputs("aggregator", string(r.Name), r.FieldInputs)
	}
	for _, r := range ext.Attributes {
		addInputs("attribute", string(r.Name), r.FieldInputs)
	}
	for _, r := range ext.Filterers {
		addInputs("filterer", string(r.Name), r.FieldInputs)
	}
	for _, r := range ext.Groupers {
		addInputs("grouper", string(r.Name), r.FieldInputs)
	}
	for _, r := range ext.Windows {
		addInputs("window", string(r.Name), r.FieldInputs)
	}
	for _, r := range ext.Features {
		addInputs("feature", string(r.Name), r.FieldInputs)
	}
	for _, r := range ext.Tests {
		if r.Tier == TestTierRow { // the runtime records row tests' hooks only
			addInputs("test", string(r.Name), r.FieldInputs)
		}
	}
	// ComponentSchemas projects per-extension ComponentSchema declarations
	// so manifest + predict can surface extension operators on the same
	// shape as built-ins. Empty / floor-only registrations stay absent
	// from the map; the descriptor-side lookups treat a missing entry as
	// "floor-only" identically to a missing built-in capability.
	snap.ComponentSchemas = map[string]descriptor.ComponentSchema{}
	for _, r := range ext.Aggregators {
		snap.Aggregators = append(snap.Aggregators, descriptor.OperatorMeta{
			Name:        string(r.Name),
			Namespace:   parseNamespace(string(r.Name)),
			Description: r.Description,
			Streamable:  r.Streamable,
			Mergeable:   r.Mergeable,
			// The declared crosstab margin class; empty stays omitted.
			MarginReducibility: string(r.MarginReducibility),
			Accepts:            fieldTypeStrings(r.Accepts),
			Params:             paramMetaSnapshot(r.Params),
		})
		if len(r.ComponentSchema.Keys) > 0 || r.ComponentSchema.Mergeability != "" {
			snap.ComponentSchemas[string(r.Name)] = r.ComponentSchema
		}
	}
	for _, r := range ext.Attributes {
		snap.Attributes = append(snap.Attributes, descriptor.OperatorMeta{
			Name:        string(r.Name),
			Namespace:   parseNamespace(string(r.Name)),
			Description: r.Description,
			Streamable:  r.Mode != AttributeModeBuffered,
			Accepts:     fieldTypeStrings(r.Accepts),
			Emits:       string(r.Emits),
			Mode:        string(r.Mode),
			Params:      paramMetaSnapshot(r.Params),
		})
	}
	for _, r := range ext.Filterers {
		snap.Filterers = append(snap.Filterers, descriptor.OperatorMeta{
			Name:        string(r.Name),
			Namespace:   parseNamespace(string(r.Name)),
			Description: r.Description,
			Streamable:  true, // every registered filterer is row-local today
			Accepts:     fieldTypeStrings(r.Accepts),
			Params:      paramMetaSnapshot(r.Params),
		})
		if len(r.ComponentSchema.Keys) > 0 || r.ComponentSchema.Mergeability != "" {
			snap.ComponentSchemas[string(r.Name)] = r.ComponentSchema
		}
	}
	for _, r := range ext.Groupers {
		// FansOut is grouper-only and rides the snapshot because
		// descriptor/ cannot assert processing.MultiKeyStreamingGrouper
		// itself (TestPredictNoExecutionImports). Probe-validated at
		// pulse.New, so the declaration here is already known to match
		// the factory.
		snap.Groupers = append(snap.Groupers, descriptor.OperatorMeta{
			Name:        string(r.Name),
			Namespace:   parseNamespace(string(r.Name)),
			Description: r.Description,
			Streamable:  r.Streamable,
			FansOut:     r.FansOut,
			Mergeable:   r.Mergeable,
			Accepts:     fieldTypeStrings(r.Accepts),
			Params:      paramMetaSnapshot(r.Params),
		})
		if len(r.ComponentSchema.Keys) > 0 || r.ComponentSchema.Mergeability != "" {
			snap.ComponentSchemas[string(r.Name)] = r.ComponentSchema
		}
	}
	for _, r := range ext.Windows {
		snap.Windows = append(snap.Windows, descriptor.OperatorMeta{
			Name:        string(r.Name),
			Namespace:   parseNamespace(string(r.Name)),
			Description: r.Description,
			Streamable:  false, // window operators run buffered today
			Accepts:     fieldTypeStrings(r.Accepts),
			Params:      paramMetaSnapshot(r.Params),
		})
	}
	for _, r := range ext.Features {
		snap.Features = append(snap.Features, descriptor.OperatorMeta{
			Name:        string(r.Name),
			Namespace:   parseNamespace(string(r.Name)),
			Description: r.Description,
			Streamable:  r.Streamable,
			Accepts:     fieldTypeStrings(r.Accepts),
			Params:      paramMetaSnapshot(r.Params),
		})
	}
	for _, r := range ext.Tests {
		tier := string(r.Tier)
		streamable := r.Streamable
		if r.Tier == TestTierPost {
			streamable = false
		}
		snap.Tests = append(snap.Tests, descriptor.OperatorMeta{
			Name:        string(r.Name),
			Namespace:   parseNamespace(string(r.Name)),
			Description: r.Description,
			Streamable:  streamable,
			Accepts:     fieldTypeStrings(r.Accepts),
			Tier:        tier,
			Params:      paramMetaSnapshot(r.Params),
		})
	}
	for _, r := range ext.SynthDistributions {
		snap.SynthDistributions = append(snap.SynthDistributions, descriptor.OperatorMeta{
			Name:        r.Name,
			Namespace:   parseNamespace(r.Name),
			Description: r.Description,
			Params:      paramMetaSnapshot(r.Params),
		})
	}
	for _, fn := range ext.ExprFunctions {
		snap.ExprFunctions = append(snap.ExprFunctions, descriptor.ExprFunctionMeta{
			Name:        fn.Name,
			Description: fn.Description,
			Signature:   fn.Signature,
			Pure:        fn.Pure,
		})
	}
	for name, t := range ext.LookupTables {
		snap.LookupTables = append(snap.LookupTables, descriptor.LookupTableMeta{
			Name:        name,
			Description: t.Description,
			HasRowsData: t.Rows != nil,
		})
	}
	for name, t := range ext.LabelTables {
		snap.LabelTables = append(snap.LabelTables, descriptor.LabelTableMeta{
			Name:        name,
			Description: t.Description,
			HasRowsData: t.Rows != nil,
		})
	}
	for name, t := range ext.RangeTables {
		snap.RangeTables = append(snap.RangeTables, descriptor.RangeTableMeta{
			Name:        name,
			Description: t.Description,
			RangeCount:  len(t.Ranges),
		})
	}
	return snap
}

// parseNamespace extracts the namespace token from a registration
// name. Embedder-registered names match the
// <CATEGORY>_<NAMESPACE>_<NAME> pattern; the second segment is the
// namespace. For built-in names that omit a namespace or for synth
// distribution names that may not follow the regex, returns "".
func parseNamespace(name string) string {
	groups := extensionNameRegex.FindStringSubmatch(name)
	if len(groups) >= 3 {
		return groups[2]
	}
	return ""
}

// fieldTypeStrings converts a slice of encoding.FieldType into the
// manifest's string-typed Accepts slice. Nil input returns nil so
// the JSON shape stays "omitempty"-friendly.
func fieldTypeStrings(in []encoding.FieldType) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	for i, ft := range in {
		out[i] = ft.String()
	}
	return out
}

// paramMetaSnapshot converts ParamMeta entries into the manifest
// projection.
func paramMetaSnapshot(in []ParamMeta) []descriptor.OperatorParamMeta {
	if len(in) == 0 {
		return nil
	}
	out := make([]descriptor.OperatorParamMeta, len(in))
	for i, p := range in {
		out[i] = descriptor.OperatorParamMeta{
			Name:        p.Name,
			Description: p.Description,
			JSONType:    p.JSONType,
			Required:    p.Required,
			Default:     p.Default,
		}
	}
	return out
}
