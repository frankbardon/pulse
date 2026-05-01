package feature

import (
	"fmt"
	"sort"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func init() {
	register(types.FEAT_ONE_HOT, newOneHot)
}

// oneHot expands a categorical field into N boolean columns. Output column
// names default to "<field>_<category>"; setting Label overrides the prefix
// to "<label>_<category>". Categories come from the schema dictionary so
// the output column set is deterministic — predict can materialize the
// post-feature schema without scanning records.
type oneHot struct {
	prefix     string
	categories []string
}

func newOneHot(feat *types.Feature, schema *encoding.Schema) (Computer, error) {
	if feat.Field == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"FEAT_ONE_HOT requires a field")
	}
	if schema == nil {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"FEAT_ONE_HOT requires a schema")
	}
	f := schema.Field(feat.Field)
	if f == nil {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			fmt.Sprintf("FEAT_ONE_HOT: field %q not in schema", feat.Field))
	}
	if !f.Type.IsCategorical() || f.Dictionary == nil {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			fmt.Sprintf("FEAT_ONE_HOT: field %q must be categorical with a dictionary", feat.Field))
	}

	prefix := feat.Label
	if prefix == "" {
		prefix = feat.Field
	}

	cats := append([]string(nil), f.Dictionary.Values()...)
	sort.Strings(cats)
	return &oneHot{prefix: prefix, categories: cats}, nil
}

func (c *oneHot) Compute(records []Record, field string) (map[string]Output, error) {
	out := make(map[string]Output, len(c.categories))
	for _, cat := range c.categories {
		out[c.columnName(cat)] = Output{Values: make([]float64, len(records))}
	}

	for i, r := range records {
		s, ok := r.StringValue(field)
		if !ok {
			continue
		}
		col := c.columnName(s)
		o, exists := out[col]
		if !exists {
			continue
		}
		o.Values[i] = 1
	}
	return out, nil
}

func (c *oneHot) columnName(category string) string {
	// Sanitize whitespace and reserved separators so labels stay
	// addressable as field references downstream. Replace spaces with
	// underscores; leave other punctuation to the caller's discretion.
	clean := strings.ReplaceAll(category, " ", "_")
	return fmt.Sprintf("%s_%s", c.prefix, clean)
}
