package encoding

import (
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// JoinedSchema synthesises the schema a single inner hash join
// produces: left fields unchanged, then every right field renamed with
// the `as` prefix (when non-empty). A right field whose final name
// collides with an earlier field is PULSE_JOIN_FIELD_COLLISION with
// {field, as} details.
//
// It is the ONE join-schema rule: the runtime (processing.JoinedSchema,
// which the hash-join iterator builds its records over) and predict
// (internal/descriptor, which may not import the engine) both call it,
// so zone resolution, smart defaults and field validation see the same
// field set and the same field types on both sides. The result has no
// on-wire byte layout; it exists for field lookups over in-memory
// joined records.
func JoinedSchema(left, right *encoding.Schema, as string) (*encoding.Schema, error) {
	if left == nil || right == nil {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"joined schema requires non-nil left and right schemas")
	}
	fields := make([]encoding.Field, 0, len(left.Fields)+len(right.Fields))
	seen := make(map[string]struct{}, len(left.Fields)+len(right.Fields))
	for _, f := range left.Fields {
		// Copied by value; every Field keeps its original Dictionary
		// pointer, so categorical lookups still resolve.
		seen[f.Name] = struct{}{}
		fields = append(fields, f)
	}
	for _, f := range right.Fields {
		name := f.Name
		if as != "" {
			name = as + f.Name
		}
		if _, dup := seen[name]; dup {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_JOIN_FIELD_COLLISION,
				"joined schema field name collides between left and right",
				map[string]any{"field": name, "as": as})
		}
		seen[name] = struct{}{}
		copied := f
		copied.Name = name
		fields = append(fields, copied)
	}
	return &encoding.Schema{Fields: fields}, nil
}
