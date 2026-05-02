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

// StreamingHandle pairs a feature spec with its constructed
// StreamingComputer. processStreaming holds a slice of these for the
// duration of a request; PrePass / Finalize / EmitRow drive each handle.
type StreamingHandle struct {
	Feature  *types.Feature
	Computer StreamingComputer
}

// IsStreamable reports whether every feature's Computer implements
// StreamingComputer. Returns false on any unknown type or factory error
// so the buffered path can surface the canonical error.
func IsStreamable(features []*types.Feature, schema *encoding.Schema) bool {
	for _, feat := range features {
		factory, ok := Lookup(feat.Type)
		if !ok {
			return false
		}
		c, err := factory(feat, schema)
		if err != nil {
			return false
		}
		if _, ok := c.(StreamingComputer); !ok {
			return false
		}
	}
	return true
}

// BuildStreaming constructs StreamingComputer instances for each feature
// in order. Caller should verify streamability via IsStreamable first;
// PROCESSING_INTERNAL is returned when an operator lacks streaming
// support, PROCESSING_CONFIG for unknown types or factory failures.
func BuildStreaming(features []*types.Feature, schema *encoding.Schema) ([]StreamingHandle, error) {
	if len(features) == 0 {
		return nil, nil
	}
	out := make([]StreamingHandle, 0, len(features))
	for _, feat := range features {
		factory, ok := Lookup(feat.Type)
		if !ok {
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
				fmt.Sprintf("unknown feature type: %s", feat.Type))
		}
		c, err := factory(feat, schema)
		if err != nil {
			return nil, err
		}
		sc, ok := c.(StreamingComputer)
		if !ok {
			return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL,
				fmt.Sprintf("feature %s does not implement StreamingComputer", feat.Type))
		}
		out = append(out, StreamingHandle{Feature: feat, Computer: sc})
	}
	return out, nil
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
		for label, out := range outputs {
			if len(out.Values) != len(records) {
				return errors.NewCodedError(errors.PROCESSING_INTERNAL,
					fmt.Sprintf("feature %s produced %d values for %d records (label %s)",
						feat.Type, len(out.Values), len(records), label))
			}
			if out.Nulls != nil && len(out.Nulls) != len(records) {
				return errors.NewCodedError(errors.PROCESSING_INTERNAL,
					fmt.Sprintf("feature %s null mask length %d != %d records (label %s)",
						feat.Type, len(out.Nulls), len(records), label))
			}
			for i, r := range records {
				if out.Nulls != nil && out.Nulls[i] {
					r.SetNull(label)
				} else {
					r.Set(label, out.Values[i])
				}
			}
		}
	}
	return nil
}
