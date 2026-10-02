package feature

import (
	"fmt"
	"time"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func init() {
	register(types.FEAT_DATE_FEATURES, newDateFeatures)
}

// dateFeatures expands a date field into five derived columns: year, month,
// day, day-of-week (0=Sunday..6=Saturday), and quarter (1..4). Default
// column names are "<field>_year", "<field>_month", etc. A Label override
// substitutes the prefix.
//
// Date is stored as days-since-Unix-epoch (the encoding.FieldTypeDate
// convention); decoding mirrors ATTR_DATE_PART so the two operators agree
// on calendar semantics.
type dateFeatures struct{ prefix string }

func newDateFeatures(feat *types.Feature, schema *encoding.Schema) (Computer, error) {
	if feat.Field == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"FEAT_DATE_FEATURES requires a field")
	}
	if schema != nil {
		if f := schema.Field(feat.Field); f != nil && f.Type != encoding.FieldTypeDate {
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
				fmt.Sprintf("FEAT_DATE_FEATURES: field %q must be of type date, got %s",
					feat.Field, f.Type))
		}
	}
	prefix := feat.Label
	if prefix == "" {
		prefix = feat.Field
	}
	return &dateFeatures{prefix: prefix}, nil
}

var dateFeatureParts = []string{"year", "month", "day", "dow", "quarter"}

func (c *dateFeatures) Compute(records []Record, field string) (map[string]Output, error) {
	out := make(map[string]Output, len(dateFeatureParts))
	for _, p := range dateFeatureParts {
		out[c.columnName(p)] = Output{
			Values: make([]float64, len(records)),
			Nulls:  make([]bool, len(records)),
		}
	}

	for i, r := range records {
		v, ok := r.NumericValue(field)
		if !ok {
			for _, p := range dateFeatureParts {
				out[c.columnName(p)].Nulls[i] = true
			}
			continue
		}
		y, m, d, dow, q := decodeDateParts(v)
		out[c.columnName("year")].Values[i] = y
		out[c.columnName("month")].Values[i] = m
		out[c.columnName("day")].Values[i] = d
		out[c.columnName("dow")].Values[i] = dow
		out[c.columnName("quarter")].Values[i] = q
	}
	return out, nil
}

func (c *dateFeatures) PrePass(_ Record, _ string) error { return nil }

func (c *dateFeatures) Finalize() error { return nil }

func (c *dateFeatures) EmitRow(r Record, field string) (map[string]Output, error) {
	out := make(map[string]Output, len(dateFeatureParts))
	v, ok := r.NumericValue(field)
	if !ok {
		for _, p := range dateFeatureParts {
			out[c.columnName(p)] = Output{Values: []float64{0}, Nulls: []bool{true}}
		}
		return out, nil
	}
	y, m, d, dow, q := decodeDateParts(v)
	out[c.columnName("year")] = Output{Values: []float64{y}}
	out[c.columnName("month")] = Output{Values: []float64{m}}
	out[c.columnName("day")] = Output{Values: []float64{d}}
	out[c.columnName("dow")] = Output{Values: []float64{dow}}
	out[c.columnName("quarter")] = Output{Values: []float64{q}}
	return out, nil
}

// decodeDateParts converts a days-since-Unix-epoch value into the five
// derived parts (year, month, day, day-of-week, quarter) that
// FEAT_DATE_FEATURES emits. Shared between Compute and EmitRow.
func decodeDateParts(v float64) (year, month, day, dow, quarter float64) {
	t := time.Unix(int64(v)*86400, 0).UTC()
	yy, mm, dd := t.Date()
	return float64(yy), float64(mm), float64(dd), float64(t.Weekday()), float64((int(mm)-1)/3 + 1)
}

func (c *dateFeatures) columnName(part string) string {
	return fmt.Sprintf("%s_%s", c.prefix, part)
}
