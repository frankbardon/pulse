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
type frequencyEncode struct{ label string }

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
