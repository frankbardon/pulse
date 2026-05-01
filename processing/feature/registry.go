package feature

import (
	"fmt"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// featureRegistry maps feature operator types to their factory functions.
// Operators register from their per-operator file via init().
var featureRegistry = map[types.FeatureType]Factory{}

// register installs a factory in the registry. Called from per-operator init().
// Panics on duplicate registration to surface programmer errors at boot.
func register(t types.FeatureType, f Factory) {
	if _, exists := featureRegistry[t]; exists {
		panic("feature: duplicate registration for " + string(t))
	}
	featureRegistry[t] = f
}

// Lookup returns the factory for a feature type, if registered.
func Lookup(t types.FeatureType) (Factory, bool) {
	f, ok := featureRegistry[t]
	return f, ok
}

// RegisteredTypes returns all registered feature types. Order is map
// iteration order; callers that need determinism must sort the result.
func RegisteredTypes() []types.FeatureType {
	out := make([]types.FeatureType, 0, len(featureRegistry))
	for k := range featureRegistry {
		out = append(out, k)
	}
	return out
}

// Apply runs every feature in features against the record set, mutating
// records in place to add derived columns. Failures return coded errors:
// PROCESSING_CONFIG for unknown types or factory errors, PROCESSING_RUNTIME
// for compute failures.
//
// Apply trusts that descriptor.Predict has validated the request shape
// upstream; it does not re-check field existence, only operator dispatch
// and per-operator runtime errors.
func Apply(records []Record, features []*types.Feature, schema *encoding.Schema) error {
	if len(features) == 0 || len(records) == 0 {
		return nil
	}
	for _, feat := range features {
		factory, ok := Lookup(feat.Type)
		if !ok {
			return errors.NewCodedError(errors.PROCESSING_CONFIG,
				fmt.Sprintf("unknown feature type: %s", feat.Type))
		}
		computer, err := factory(feat, schema)
		if err != nil {
			return err
		}
		outputs, err := computer.Compute(records, feat.Field)
		if err != nil {
			return err
		}
		for label, values := range outputs {
			if len(values) != len(records) {
				return errors.NewCodedError(errors.PROCESSING_INTERNAL,
					fmt.Sprintf("feature %s produced %d values for %d records (label %s)",
						feat.Type, len(values), len(records), label))
			}
			for i, r := range records {
				r.Set(label, values[i])
			}
		}
	}
	return nil
}
