package processing

import (
	"fmt"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// CanChainRequest reports whether a request is eligible to participate
// in a ProcessChain stage. A request qualifies iff:
//
//   - It passes the existing mergeable gate (CanMergeRequest), i.e. its
//     online state can be merged across partitions of the input.
//   - Every aggregator emits a single scalar value per output row
//     (FREQUENCY emits a map; MODE emits a string — both are excluded
//     from chain output until a richer downstream-schema synth lands).
//
// The chain executor calls this before each stage. A failing stage
// surfaces PULSE_CHAIN_NOT_MERGEABLE with the stage index in details.
func CanChainRequest(req *types.Request, schema *encoding.Schema) bool {
	if !CanMergeRequest(req, schema) {
		return false
	}
	for _, agg := range req.Aggregations {
		if agg == nil {
			return false
		}
		if !aggregatorEmitsScalar(agg.Type) {
			return false
		}
	}
	return true
}

// aggregatorEmitsScalar reports whether the aggregator's Finalize
// produces a single float64 cell suitable for inclusion in a
// downstream record. Excludes the map-emitting (AGG_FREQUENCY) and
// string-emitting (AGG_MODE) aggregators.
func aggregatorEmitsScalar(t types.AggregationType) bool {
	switch t {
	case types.AGG_FREQUENCY, types.AGG_MODE:
		return false
	}
	// Every other mergeable aggregator emits a single float64.
	return true
}

// AggregationLabel returns the output column name for an aggregation,
// matching the labelling used by the streaming and buffered paths in
// Processor.Process. Empty label falls back to "<TYPE>_<Field>".
func AggregationLabel(agg *types.Aggregation) string {
	if agg.Label != "" {
		return agg.Label
	}
	return fmt.Sprintf("%s_%s", agg.Type, agg.Field)
}

// ChainOutputSchema synthesises the schema produced by a mergeable
// chain stage. The returned schema is purely in-memory — no byte
// layout — and is intended for downstream stages constructed via
// SliceIterator over RecordsFromChainRows.
//
// Layout:
//   - One categorical_u32 field per Group entry (named grp.Field),
//     with an empty Dictionary that the caller populates as rows are
//     materialized.
//   - One f64 field per Aggregation entry (named via
//     AggregationLabel).
//
// Row-local attributes (FORMULA, DATE_PART) feed aggregators but do
// not surface in the streaming/grouped output today, so they do not
// appear in the chain schema. ByteOffset / BitPosition are zero
// across the board because the schema is never serialised.
func ChainOutputSchema(req *types.Request) (*encoding.Schema, error) {
	if req == nil {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG, "chain output schema requires non-nil request")
	}
	fields := make([]encoding.Field, 0, len(req.Groups)+len(req.Aggregations))
	seen := make(map[string]struct{}, len(req.Groups)+len(req.Aggregations))

	for _, g := range req.Groups {
		if g == nil {
			continue
		}
		if _, dup := seen[g.Field]; dup {
			continue
		}
		seen[g.Field] = struct{}{}
		fields = append(fields, encoding.Field{
			Name:       g.Field,
			Type:       encoding.FieldTypeCategoricalU32,
			Dictionary: encoding.NewDictionary(),
		})
	}
	for _, agg := range req.Aggregations {
		if agg == nil {
			continue
		}
		label := AggregationLabel(agg)
		if _, dup := seen[label]; dup {
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
				fmt.Sprintf("chain output label %q collides with another column", label))
		}
		seen[label] = struct{}{}
		fields = append(fields, encoding.Field{
			Name: label,
			Type: encoding.FieldTypeF64,
		})
	}
	return &encoding.Schema{Fields: fields}, nil
}

// RecordsFromChainRows materializes records for the next chain stage
// from a Response.Data slice + the synthesised schema. Categorical
// fields land in the dictionary lazily as new keys arrive. Numeric
// cells are converted to float64; missing / nil cells become nulls.
//
// The returned records share the schema reference; the schema's
// dictionaries are mutated as new keys are observed.
func RecordsFromChainRows(rows []map[string]any, schema *encoding.Schema) ([]*Record, error) {
	if schema == nil {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG, "chain materialization requires schema")
	}
	out := make([]*Record, 0, len(rows))
	for ri, row := range rows {
		values := make(map[string]float64, len(schema.Fields))
		nulls := make(map[string]bool)
		for i := range schema.Fields {
			f := &schema.Fields[i]
			raw, present := row[f.Name]
			if !present || raw == nil {
				nulls[f.Name] = true
				continue
			}
			switch f.Type {
			case encoding.FieldTypeCategoricalU32:
				s, ok := stringifyChainKey(raw)
				if !ok {
					nulls[f.Name] = true
					continue
				}
				id, err := f.Dictionary.Add(s)
				if err != nil {
					return nil, errors.NewCodedError(errors.PROCESSING_RUNTIME,
						fmt.Sprintf("row %d field %q: %s", ri, f.Name, err.Error()))
				}
				values[f.Name] = float64(id)
			case encoding.FieldTypeF64:
				v, ok := coerceFloat(raw)
				if !ok {
					nulls[f.Name] = true
					continue
				}
				values[f.Name] = v
			default:
				return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL,
					fmt.Sprintf("unsupported chain field type %s for %q", f.Type.String(), f.Name))
			}
		}
		out = append(out, NewRecordWithNulls(schema, values, nulls))
	}
	return out, nil
}

// stringifyChainKey converts any JSON-shaped value to a string for
// categorical lookup. Mirrors the streaming-path category grouper
// which stores keys as strings in its bucket map.
func stringifyChainKey(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, true
	case fmt.Stringer:
		return x.String(), true
	case float64:
		return fmt.Sprintf("%g", x), true
	case float32:
		return fmt.Sprintf("%g", float64(x)), true
	case int:
		return fmt.Sprintf("%d", x), true
	case int64:
		return fmt.Sprintf("%d", x), true
	case int32:
		return fmt.Sprintf("%d", x), true
	case uint64:
		return fmt.Sprintf("%d", x), true
	case uint32:
		return fmt.Sprintf("%d", x), true
	case bool:
		if x {
			return "true", true
		}
		return "false", true
	}
	return "", false
}

// coerceFloat reads any JSON-shaped numeric into float64. Returns
// (0, false) for nil / unrecognised / non-numeric values so the caller
// can mark the cell null.
func coerceFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case int32:
		return float64(x), true
	case uint64:
		return float64(x), true
	case uint32:
		return float64(x), true
	}
	return 0, false
}
