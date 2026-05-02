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

func (c *dateFeatures) Compute(records []Record, field string) (map[string]Output, error) {
	parts := []string{"year", "month", "day", "dow", "quarter"}
	out := make(map[string]Output, len(parts))
	for _, p := range parts {
		out[c.columnName(p)] = Output{
			Values: make([]float64, len(records)),
			Nulls:  make([]bool, len(records)),
		}
	}

	for i, r := range records {
		v, ok := r.NumericValue(field)
		if !ok {
			for _, p := range parts {
				out[c.columnName(p)].Nulls[i] = true
			}
			continue
		}
		t := time.Unix(int64(v)*86400, 0).UTC()
		year, month, day := t.Date()

		out[c.columnName("year")].Values[i] = float64(year)
		out[c.columnName("month")].Values[i] = float64(month)
		out[c.columnName("day")].Values[i] = float64(day)
		out[c.columnName("dow")].Values[i] = float64(t.Weekday())
		out[c.columnName("quarter")].Values[i] = float64((int(month)-1)/3 + 1)
	}
	return out, nil
}

func (c *dateFeatures) columnName(part string) string {
	return fmt.Sprintf("%s_%s", c.prefix, part)
}
