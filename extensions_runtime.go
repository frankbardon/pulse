package pulse

import (
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/processing/feature"
	"github.com/frankbardon/pulse/processing/window"
	"github.com/frankbardon/pulse/types"
)

// buildRuntimeExtensions converts the public pulse.Extensions struct
// into the runtime-facing processing.ExtensionRegistry. Returns nil
// when ext has no operator registrations of any kind — the runtime
// path then skips the overlay-lookup entirely.
//
// The mapping is purely structural: every category's slice becomes a
// map keyed by the registration's typed Name. Streamability overrides
// are recorded by category for predict + IsStreamable consumption.
//
// This helper does NOT run probe-validation (factory invocation) —
// that lives in the per-category integration phases (E3 onward) and
// emits PULSE_EXTENSION_STREAMABLE_MISMATCH when the declared
// streaming tier disagrees with the factory's returned interface.
func buildRuntimeExtensions(ext Extensions) *processing.ExtensionRegistry {
	if !hasAnyRegistrations(ext) {
		return nil
	}

	r := &processing.ExtensionRegistry{
		Streamable:  make(map[string]bool),
		FieldInputs: make(map[string]processing.FieldInputsFunc),
	}

	addFieldInputs := func(category, name string, fn FieldInputsFunc) {
		if fn == nil {
			return
		}
		r.FieldInputs[processing.StreamabilityKey(category, name)] = processing.FieldInputsFunc(fn)
	}

	if len(ext.ExprFunctions) > 0 {
		r.ExprFunctions = make([]processing.ExprFunction, 0, len(ext.ExprFunctions))
		for _, fn := range ext.ExprFunctions {
			r.ExprFunctions = append(r.ExprFunctions, processing.ExprFunction{
				Name: fn.Name,
				Fn:   fn.Fn,
			})
		}
	}

	if len(ext.LookupTables) > 0 {
		r.LookupTables = make(map[string]processing.LookupTable, len(ext.LookupTables))
		for name, t := range ext.LookupTables {
			r.LookupTables[name] = processing.LookupTable{
				Rows:   t.Rows,
				Lookup: t.Lookup,
			}
		}
	}

	if len(ext.LabelTables) > 0 {
		r.LabelTables = make(map[string]processing.LabelTable, len(ext.LabelTables))
		for name, t := range ext.LabelTables {
			r.LabelTables[name] = processing.LabelTable{
				Rows:   t.Rows,
				Lookup: t.Lookup,
			}
		}
	}

	if len(ext.Aggregators) > 0 {
		r.Aggregators = make(map[types.AggregationType]processing.AggregatorFactory, len(ext.Aggregators))
		for _, reg := range ext.Aggregators {
			r.Aggregators[reg.Name] = reg.Factory
			r.Streamable[processing.StreamabilityKey("aggregator", string(reg.Name))] = reg.Streamable
			addFieldInputs("aggregator", string(reg.Name), reg.FieldInputs)
		}
	}

	if len(ext.Attributes) > 0 {
		r.Attributes = make(map[types.AttributeType]processing.AttributeFactory, len(ext.Attributes))
		for _, reg := range ext.Attributes {
			r.Attributes[reg.Name] = reg.Factory
			r.Streamable[processing.StreamabilityKey("attribute", string(reg.Name))] = reg.Mode != AttributeModeBuffered
			addFieldInputs("attribute", string(reg.Name), reg.FieldInputs)
		}
	}

	if len(ext.Filterers) > 0 {
		r.Filterers = make(map[types.FiltererType]processing.FiltererFactory, len(ext.Filterers))
		for _, reg := range ext.Filterers {
			r.Filterers[reg.Name] = reg.Factory
			// Custom filterers are always row-local streamable today.
			r.Streamable[processing.StreamabilityKey("filterer", string(reg.Name))] = true
			addFieldInputs("filterer", string(reg.Name), reg.FieldInputs)
		}
	}

	if len(ext.Groupers) > 0 {
		r.Groupers = make(map[types.GroupType]processing.GrouperFactory, len(ext.Groupers))
		for _, reg := range ext.Groupers {
			r.Groupers[reg.Name] = reg.Factory
			r.Streamable[processing.StreamabilityKey("grouper", string(reg.Name))] = reg.Streamable
			addFieldInputs("grouper", string(reg.Name), reg.FieldInputs)
		}
	}

	if len(ext.Windows) > 0 {
		r.Windows = make(map[types.WindowType]window.WindowFactory, len(ext.Windows))
		for _, reg := range ext.Windows {
			r.Windows[reg.Name] = reg.Factory
			// Custom windows run buffered today; the runtime path
			// reflects the built-in behaviour.
			r.Streamable[processing.StreamabilityKey("window", string(reg.Name))] = false
			addFieldInputs("window", string(reg.Name), reg.FieldInputs)
		}
	}

	if len(ext.Features) > 0 {
		r.Features = make(map[types.FeatureType]feature.Factory, len(ext.Features))
		for _, reg := range ext.Features {
			r.Features[reg.Name] = reg.Factory
			r.Streamable[processing.StreamabilityKey("feature", string(reg.Name))] = reg.Streamable
			addFieldInputs("feature", string(reg.Name), reg.FieldInputs)
		}
	}

	if len(ext.Tests) > 0 {
		for _, reg := range ext.Tests {
			switch reg.Tier {
			case TestTierRow:
				if r.RowTests == nil {
					r.RowTests = make(map[types.TestType]processing.RowTestFactory)
				}
				r.RowTests[reg.Name] = reg.RowFactory
				r.Streamable[processing.StreamabilityKey("test", string(reg.Name))] = reg.Streamable
				addFieldInputs("test", string(reg.Name), reg.FieldInputs)
			case TestTierPost:
				if r.PostTests == nil {
					r.PostTests = make(map[types.TestType]processing.PostTestFactory)
				}
				r.PostTests[reg.Name] = reg.PostFactory
				// Tier-2 tests are always buffered at runtime.
				r.Streamable[processing.StreamabilityKey("test", string(reg.Name))] = false
			}
		}
	}

	// Synth distributions are wired into the runtime in a later phase
	// alongside the synth-overlay work. The registry surface stays
	// stable so embedders can declare them today.
	_ = ext.SynthDistributions

	return r
}

// hasAnyRegistrations reports whether ext carries any
// extension-API entry (operator or expression-side). Used to short-
// circuit buildRuntimeExtensions when nothing is registered.
func hasAnyRegistrations(ext Extensions) bool {
	return len(ext.Aggregators) > 0 ||
		len(ext.Attributes) > 0 ||
		len(ext.Filterers) > 0 ||
		len(ext.Groupers) > 0 ||
		len(ext.Windows) > 0 ||
		len(ext.Features) > 0 ||
		len(ext.Tests) > 0 ||
		len(ext.SynthDistributions) > 0 ||
		len(ext.ExprFunctions) > 0 ||
		len(ext.LookupTables) > 0 ||
		len(ext.LabelTables) > 0
}
