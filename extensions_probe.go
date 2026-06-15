package pulse

import (
	"fmt"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
)

// probeExtensions invokes each registered factory once against a
// minimal synthetic spec + empty schema and verifies the returned
// instance satisfies the streaming interface declared on the
// registration. Panics during the probe surface as
// PULSE_EXTENSION_FACTORY_PANIC; type-mismatch surfaces as
// PULSE_EXTENSION_STREAMABLE_MISMATCH.
//
// Embedder factories MUST tolerate a nil/empty Schema and a spec
// carrying only the operator Name; documented in
// skills/extension-points.md (E10).
func probeExtensions(ext Extensions) error {
	if err := probeAggregators(ext.Aggregators); err != nil {
		return err
	}
	if err := probeAttributes(ext.Attributes); err != nil {
		return err
	}
	if err := probeGroupers(ext.Groupers); err != nil {
		return err
	}
	if err := probeFilterers(ext.Filterers); err != nil {
		return err
	}
	return nil
}

// probeAggregators validates every registered aggregator. Streamable
// aggregators must return an OnlineAggregator; non-streamable
// aggregators just need to construct without panicking.
//
// When the registration supplies a ComponentsFunc, the probe invokes it
// once against the constructed instance and confirms the returned map
// keys are a subset of ComponentSchema.Keys (universal-floor keys are
// allowed but not required). Divergence raises
// PULSE_EXTENSION_COMPONENT_SCHEMA_MISMATCH; the
// emitter-without-schema and Mergeability-without-Keys variants raise
// PULSE_EXTENSION_MISSING_COMPONENT_SCHEMA (wired via the parity check
// below). Factory panics still surface as
// PULSE_EXTENSION_FACTORY_PANIC.
func probeAggregators(regs []AggregatorRegistration) error {
	probeSchema := &encoding.Schema{}
	for _, reg := range regs {
		instance, err := safeBuildAggregator(reg, probeSchema)
		if err != nil {
			return err
		}
		if reg.Streamable {
			if _, ok := instance.(processing.OnlineAggregator); !ok {
				return errors.NewCodedErrorWithDetails(
					errors.PULSE_EXTENSION_STREAMABLE_MISMATCH,
					fmt.Sprintf("aggregator %q declares Streamable=true but factory does not return processing.OnlineAggregator", reg.Name),
					map[string]any{
						"category":   "aggregator",
						"name":       string(reg.Name),
						"streamable": true,
					},
				)
			}
		}
		if reg.ComponentsFunc != nil {
			if err := verifyComponentSchemaPresence(
				"aggregator", string(reg.Name), reg.ComponentSchema, true,
			); err != nil {
				return err
			}
			emitted, err := safeInvokeAggregatorComponents(reg, instance)
			if err != nil {
				return err
			}
			if err := verifyComponentKeysSubset(
				"aggregator", string(reg.Name),
				reg.ComponentSchema, emitted,
			); err != nil {
				return err
			}
		} else {
			if err := verifyComponentSchemaPresence(
				"aggregator", string(reg.Name), reg.ComponentSchema, false,
			); err != nil {
				return err
			}
		}
	}
	return nil
}

// probeGroupers validates every registered grouper. When ComponentsFunc
// is supplied the probe invokes it against a constructed instance and
// confirms the emitted key set matches the declared schema. Mirrors
// probeAggregators in shape.
func probeGroupers(regs []GrouperRegistration) error {
	probeSchema := &encoding.Schema{}
	for _, reg := range regs {
		instance, err := safeBuildGrouper(reg, probeSchema)
		if err != nil {
			return err
		}
		if reg.ComponentsFunc != nil {
			if err := verifyComponentSchemaPresence(
				"grouper", string(reg.Name), reg.ComponentSchema, true,
			); err != nil {
				return err
			}
			emitted, err := safeInvokeGrouperComponents(reg, instance)
			if err != nil {
				return err
			}
			if err := verifyComponentKeysSubset(
				"grouper", string(reg.Name),
				reg.ComponentSchema, emitted,
			); err != nil {
				return err
			}
		} else {
			if err := verifyComponentSchemaPresence(
				"grouper", string(reg.Name), reg.ComponentSchema, false,
			); err != nil {
				return err
			}
		}
	}
	return nil
}

// probeFilterers validates every registered filterer. When
// ComponentsFunc is supplied the probe invokes it against a constructed
// FiltererBuilder and confirms the emitted key set matches the declared
// schema. Mirrors probeAggregators in shape.
func probeFilterers(regs []FiltererRegistration) error {
	for _, reg := range regs {
		builder, err := safeBuildFilterer(reg)
		if err != nil {
			return err
		}
		if reg.ComponentsFunc != nil {
			if err := verifyComponentSchemaPresence(
				"filterer", string(reg.Name), reg.ComponentSchema, true,
			); err != nil {
				return err
			}
			emitted, err := safeInvokeFiltererComponents(reg, builder)
			if err != nil {
				return err
			}
			if err := verifyComponentKeysSubset(
				"filterer", string(reg.Name),
				reg.ComponentSchema, emitted,
			); err != nil {
				return err
			}
		} else {
			if err := verifyComponentSchemaPresence(
				"filterer", string(reg.Name), reg.ComponentSchema, false,
			); err != nil {
				return err
			}
		}
	}
	return nil
}

// probeAttributes validates every registered attribute factory
// matches its declared Mode.
func probeAttributes(regs []AttributeRegistration) error {
	probeSchema := &encoding.Schema{}
	for _, reg := range regs {
		instance, err := safeBuildAttribute(reg, probeSchema)
		if err != nil {
			return err
		}
		switch reg.Mode {
		case AttributeModeRowLocal:
			if _, ok := instance.(processing.RowLocalAttribute); !ok {
				return attributeModeMismatch(reg, "RowLocalAttribute")
			}
		case AttributeModeTwoPass:
			if _, ok := instance.(processing.TwoPassAttribute); !ok {
				return attributeModeMismatch(reg, "TwoPassAttribute")
			}
		case AttributeModeBuffered:
			// AttributeComputer is the minimum interface; the factory
			// signature already enforces this at compile time.
		}
	}
	return nil
}

func attributeModeMismatch(reg AttributeRegistration, want string) error {
	return errors.NewCodedErrorWithDetails(
		errors.PULSE_EXTENSION_STREAMABLE_MISMATCH,
		fmt.Sprintf("attribute %q declares Mode=%s but factory does not return processing.%s", reg.Name, reg.Mode, want),
		map[string]any{
			"category": "attribute",
			"name":     string(reg.Name),
			"mode":     string(reg.Mode),
			"required": "processing." + want,
		},
	)
}

// safeBuildAggregator invokes the factory under a deferred recover so
// embedder panics become a coded error instead of crashing pulse.New.
func safeBuildAggregator(reg AggregatorRegistration, schema *encoding.Schema) (instance processing.Aggregator, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errors.NewCodedErrorWithDetails(
				errors.PULSE_EXTENSION_FACTORY_PANIC,
				fmt.Sprintf("aggregator factory %q panicked during probe-validation: %v", reg.Name, r),
				map[string]any{"category": "aggregator", "name": string(reg.Name), "panic": fmt.Sprintf("%v", r)},
			)
		}
	}()
	spec := &types.Aggregation{Type: reg.Name}
	instance, err = reg.Factory(spec, schema)
	if err != nil {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PULSE_EXTENSION_FACTORY_PANIC,
			fmt.Sprintf("aggregator factory %q returned error during probe-validation: %v", reg.Name, err),
			map[string]any{"category": "aggregator", "name": string(reg.Name), "factory_error": err.Error()},
		)
	}
	if instance == nil {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PULSE_EXTENSION_FACTORY_PANIC,
			fmt.Sprintf("aggregator factory %q returned a nil instance during probe-validation", reg.Name),
			map[string]any{"category": "aggregator", "name": string(reg.Name)},
		)
	}
	return instance, nil
}

// safeBuildAttribute invokes an attribute factory under a deferred
// recover with the same contract as safeBuildAggregator.
func safeBuildAttribute(reg AttributeRegistration, schema *encoding.Schema) (instance processing.AttributeComputer, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errors.NewCodedErrorWithDetails(
				errors.PULSE_EXTENSION_FACTORY_PANIC,
				fmt.Sprintf("attribute factory %q panicked during probe-validation: %v", reg.Name, r),
				map[string]any{"category": "attribute", "name": string(reg.Name), "panic": fmt.Sprintf("%v", r)},
			)
		}
	}()
	spec := &types.Attribute{Type: reg.Name}
	instance, err = reg.Factory(spec, schema)
	if err != nil {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PULSE_EXTENSION_FACTORY_PANIC,
			fmt.Sprintf("attribute factory %q returned error during probe-validation: %v", reg.Name, err),
			map[string]any{"category": "attribute", "name": string(reg.Name), "factory_error": err.Error()},
		)
	}
	if instance == nil {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PULSE_EXTENSION_FACTORY_PANIC,
			fmt.Sprintf("attribute factory %q returned a nil instance during probe-validation", reg.Name),
			map[string]any{"category": "attribute", "name": string(reg.Name)},
		)
	}
	return instance, nil
}

// safeBuildGrouper invokes a grouper factory under a deferred recover
// with the same contract as safeBuildAggregator.
func safeBuildGrouper(reg GrouperRegistration, schema *encoding.Schema) (instance processing.Grouper, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errors.NewCodedErrorWithDetails(
				errors.PULSE_EXTENSION_FACTORY_PANIC,
				fmt.Sprintf("grouper factory %q panicked during probe-validation: %v", reg.Name, r),
				map[string]any{"category": "grouper", "name": string(reg.Name), "panic": fmt.Sprintf("%v", r)},
			)
		}
	}()
	spec := &types.Group{Type: reg.Name}
	instance, err = reg.Factory(spec, schema)
	if err != nil {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PULSE_EXTENSION_FACTORY_PANIC,
			fmt.Sprintf("grouper factory %q returned error during probe-validation: %v", reg.Name, err),
			map[string]any{"category": "grouper", "name": string(reg.Name), "factory_error": err.Error()},
		)
	}
	if instance == nil {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PULSE_EXTENSION_FACTORY_PANIC,
			fmt.Sprintf("grouper factory %q returned a nil instance during probe-validation", reg.Name),
			map[string]any{"category": "grouper", "name": string(reg.Name)},
		)
	}
	return instance, nil
}

// safeBuildFilterer invokes a filterer factory under a deferred recover
// with the same contract as safeBuildAggregator. Filterer factories take
// no arguments — they always return a fresh FiltererBuilder.
func safeBuildFilterer(reg FiltererRegistration) (builder processing.FiltererBuilder, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errors.NewCodedErrorWithDetails(
				errors.PULSE_EXTENSION_FACTORY_PANIC,
				fmt.Sprintf("filterer factory %q panicked during probe-validation: %v", reg.Name, r),
				map[string]any{"category": "filterer", "name": string(reg.Name), "panic": fmt.Sprintf("%v", r)},
			)
		}
	}()
	builder = reg.Factory()
	if builder == nil {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PULSE_EXTENSION_FACTORY_PANIC,
			fmt.Sprintf("filterer factory %q returned a nil builder during probe-validation", reg.Name),
			map[string]any{"category": "filterer", "name": string(reg.Name)},
		)
	}
	return builder, nil
}

// safeInvokeAggregatorComponents calls the registration's
// ComponentsFunc under a deferred recover so a panic in the emitter
// becomes a coded error instead of crashing pulse.New.
func safeInvokeAggregatorComponents(reg AggregatorRegistration, instance processing.Aggregator) (out map[string]any, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errors.NewCodedErrorWithDetails(
				errors.PULSE_EXTENSION_FACTORY_PANIC,
				fmt.Sprintf("aggregator %q ComponentsFunc panicked during probe-validation: %v", reg.Name, r),
				map[string]any{"category": "aggregator", "name": string(reg.Name), "panic": fmt.Sprintf("%v", r)},
			)
		}
	}()
	out, err = reg.ComponentsFunc(instance)
	if err != nil {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PULSE_EXTENSION_FACTORY_PANIC,
			fmt.Sprintf("aggregator %q ComponentsFunc returned error during probe-validation: %v", reg.Name, err),
			map[string]any{"category": "aggregator", "name": string(reg.Name), "components_error": err.Error()},
		)
	}
	return out, nil
}

// safeInvokeGrouperComponents mirrors safeInvokeAggregatorComponents
// for grouper registrations.
func safeInvokeGrouperComponents(reg GrouperRegistration, instance processing.Grouper) (out map[string]any, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errors.NewCodedErrorWithDetails(
				errors.PULSE_EXTENSION_FACTORY_PANIC,
				fmt.Sprintf("grouper %q ComponentsFunc panicked during probe-validation: %v", reg.Name, r),
				map[string]any{"category": "grouper", "name": string(reg.Name), "panic": fmt.Sprintf("%v", r)},
			)
		}
	}()
	out, err = reg.ComponentsFunc(instance)
	if err != nil {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PULSE_EXTENSION_FACTORY_PANIC,
			fmt.Sprintf("grouper %q ComponentsFunc returned error during probe-validation: %v", reg.Name, err),
			map[string]any{"category": "grouper", "name": string(reg.Name), "components_error": err.Error()},
		)
	}
	return out, nil
}

// safeInvokeFiltererComponents mirrors safeInvokeAggregatorComponents
// for filterer registrations.
func safeInvokeFiltererComponents(reg FiltererRegistration, builder processing.FiltererBuilder) (out map[string]any, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errors.NewCodedErrorWithDetails(
				errors.PULSE_EXTENSION_FACTORY_PANIC,
				fmt.Sprintf("filterer %q ComponentsFunc panicked during probe-validation: %v", reg.Name, r),
				map[string]any{"category": "filterer", "name": string(reg.Name), "panic": fmt.Sprintf("%v", r)},
			)
		}
	}()
	out, err = reg.ComponentsFunc(builder)
	if err != nil {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PULSE_EXTENSION_FACTORY_PANIC,
			fmt.Sprintf("filterer %q ComponentsFunc returned error during probe-validation: %v", reg.Name, err),
			map[string]any{"category": "filterer", "name": string(reg.Name), "components_error": err.Error()},
		)
	}
	return out, nil
}

// verifyComponentKeysSubset asserts every key in emitted appears in the
// declared schema's Keys list. Universal-floor keys are allowed but not
// required — embedders may smuggle a {"n", "n_null"} pair without
// declaring them on the schema (the orchestrator's universal-floor pass
// overwrites them anyway).
//
// Per the E4-S6 universal-floor decision: the schema may also legally
// CONTAIN the floor keys (E1-S3 puts them on every aggregator schema
// for self-contained manifest output). The probe compares the EMITTED
// set to declared-MINUS-floor and asserts the emitted set is a subset
// of declared-PLUS-floor — i.e. the orchestrator-owned floor keys are
// neither required nor penalised in either direction.
//
// Divergence raises PULSE_EXTENSION_COMPONENT_SCHEMA_MISMATCH (wired in
// E4-S6); the details payload carries the offending extras and the
// declared key list so callers can either widen the schema or trim the
// emission.
func verifyComponentKeysSubset(category, name string, schema descriptor.ComponentSchema, emitted map[string]any) error {
	if len(emitted) == 0 {
		return nil
	}
	allowed := make(map[string]struct{}, len(schema.Keys)+6)
	for _, k := range schema.Keys {
		allowed[k.Name] = struct{}{}
	}
	// Universal-floor keys are always allowed (the orchestrator owns
	// them; embedders may smuggle them in without penalty).
	allowed["n"] = struct{}{}
	allowed["n_null"] = struct{}{}
	allowed["total_n"] = struct{}{}
	allowed["n_in"] = struct{}{}
	allowed["n_out"] = struct{}{}
	allowed["n_null_input"] = struct{}{}

	var extras []string
	for k := range emitted {
		if _, ok := allowed[k]; !ok {
			extras = append(extras, k)
		}
	}
	if len(extras) == 0 {
		return nil
	}
	declared := make([]string, 0, len(schema.Keys))
	for _, k := range schema.Keys {
		declared = append(declared, k.Name)
	}
	return errors.NewCodedErrorWithDetails(
		errors.PULSE_EXTENSION_COMPONENT_SCHEMA_MISMATCH,
		fmt.Sprintf("extension %s %q ComponentsFunc emitted keys not declared in ComponentSchema: %v (declared: %v)", category, name, extras, declared),
		map[string]any{
			"category":      category,
			"name":          name,
			"undeclared":    extras,
			"declared_keys": declared,
			"reason":        "components_schema_mismatch",
		},
	)
}

// verifyComponentSchemaPresence asserts the ComponentSchema declaration
// is coherent given the presence (or absence) of a ComponentsFunc
// emitter. Two incoherent shapes raise
// PULSE_EXTENSION_MISSING_COMPONENT_SCHEMA at pulse.New() time:
//
//   - hasEmitter == true AND schema.Keys is empty — the emitter has no
//     schema to validate against; the probe cannot verify the emitted
//     key set against an empty contract.
//   - schema.Mergeability != "" AND schema.Keys is empty — declaring
//     mergeability intent without keys is incoherent; mergeability only
//     describes per-key fold semantics for an existing key list.
//
// The floor-only path (empty Keys + empty Mergeability + nil emitter)
// remains a valid registration shape and is NOT subject to this code.
// The schema-without-emitter and schema-without-mergeability shapes are
// caught earlier by validateComponentSchemas with the legacy
// PULSE_EXTENSION_PARAM_INVALID code — those remain unchanged.
func verifyComponentSchemaPresence(category, name string, schema descriptor.ComponentSchema, hasEmitter bool) error {
	hasKeys := len(schema.Keys) > 0
	hasMergeability := schema.Mergeability != ""

	if hasEmitter && !hasKeys {
		return errors.NewCodedErrorWithDetails(
			errors.PULSE_EXTENSION_MISSING_COMPONENT_SCHEMA,
			fmt.Sprintf("extension %s %q has ComponentsFunc but ComponentSchema.Keys is empty (declare schema to validate emitted keys)", category, name),
			map[string]any{
				"category": category,
				"name":     name,
				"reason":   "emitter_without_schema_keys",
			},
		)
	}
	if !hasKeys && hasMergeability {
		return errors.NewCodedErrorWithDetails(
			errors.PULSE_EXTENSION_MISSING_COMPONENT_SCHEMA,
			fmt.Sprintf("extension %s %q declares ComponentSchema.Mergeability but no Keys (mergeability has nothing to classify)", category, name),
			map[string]any{
				"category":     category,
				"name":         name,
				"mergeability": string(schema.Mergeability),
				"reason":       "mergeability_without_keys",
			},
		)
	}
	return nil
}
