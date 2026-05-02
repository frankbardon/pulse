package feature

import (
	"fmt"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func init() {
	register(types.FEAT_FREQUENCY_ENCODE, newFrequencyEncode)
}

// frequencyEncode replaces a categorical value with the proportion of rows
// in the cohort that share that value. A two-pass operator: pass one tallies
// counts per category; pass two writes the per-row encoded value. Rows
// whose category does not appear in the count pass (only possible if the
// record set changes between passes — which it does not here) get null.
type frequencyEncode struct {
	label string
	// Streaming state. PrePass populates counts and total; Finalize
	// captures denom (or marks empty=true when total is zero) so EmitRow
	// can serve per-row lookups without re-scanning.
	counts map[string]int
	total  int
	denom  float64
	empty  bool
}

func newFrequencyEncode(feat *types.Feature, schema *encoding.Schema) (Computer, error) {
	if feat.Field == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"FEAT_FREQUENCY_ENCODE requires a field")
	}
	if schema != nil {
		if f := schema.Field(feat.Field); f != nil && !f.Type.IsCategorical() {
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
				fmt.Sprintf("FEAT_FREQUENCY_ENCODE: field %q must be categorical, got %s",
					feat.Field, f.Type))
		}
	}
	label := feat.Label
	if label == "" {
		label = fmt.Sprintf("FREQ_%s", feat.Field)
	}
	return &frequencyEncode{label: label}, nil
}

// PrePass tallies the category for one record into the operator's
// running counts. Records whose target field is null contribute nothing.
func (c *frequencyEncode) PrePass(r Record, field string) error {
	if c.counts == nil {
		c.counts = make(map[string]int, 16)
	}
	if s, ok := r.StringValue(field); ok {
		c.counts[s]++
		c.total++
	}
	return nil
}

// Finalize freezes the denominator so EmitRow is O(1) per row. When no
// non-null categories were observed, every emitted row is null.
func (c *frequencyEncode) Finalize() error {
	if c.total == 0 {
		c.empty = true
		return nil
	}
	c.denom = float64(c.total)
	return nil
}

// EmitRow returns the proportion (count[s]/total) for the record's
// category, or null if the category is missing on this record or the
// cohort had no non-null categories at all.
func (c *frequencyEncode) EmitRow(r Record, field string) (map[string]Output, error) {
	if c.empty {
		return map[string]Output{c.label: {Values: []float64{0}, Nulls: []bool{true}}}, nil
	}
	s, ok := r.StringValue(field)
	if !ok {
		return map[string]Output{c.label: {Values: []float64{0}, Nulls: []bool{true}}}, nil
	}
	return map[string]Output{c.label: {Values: []float64{float64(c.counts[s]) / c.denom}}}, nil
}

func (c *frequencyEncode) Compute(records []Record, field string) (map[string]Output, error) {
	if len(records) == 0 {
		return map[string]Output{c.label: {Values: []float64{}}}, nil
	}

	counts := make(map[string]int, 16)
	total := 0
	for _, r := range records {
		s, ok := r.StringValue(field)
		if !ok {
			continue
		}
		counts[s]++
		total++
	}

	values := make([]float64, len(records))
	nulls := make([]bool, len(records))
	if total == 0 {
		for i := range nulls {
			nulls[i] = true
		}
		return map[string]Output{c.label: {Values: values, Nulls: nulls}}, nil
	}

	denom := float64(total)
	for i, r := range records {
		s, ok := r.StringValue(field)
		if !ok {
			nulls[i] = true
			continue
		}
		values[i] = float64(counts[s]) / denom
	}
	return map[string]Output{c.label: {Values: values, Nulls: nulls}}, nil
}
