package pulse

import (
	"fmt"

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
	return nil
}

// probeAggregators validates every registered aggregator. Streamable
// aggregators must return an OnlineAggregator; non-streamable
// aggregators just need to construct without panicking.
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
