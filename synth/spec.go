package synth

import (
	"encoding/json"
	"fmt"

	"github.com/frankbardon/pulse/errors"
)

// Spec is the parsed top-level synthesis request. It is the in-memory
// shape of from-schema JSON. A from-profile call builds a Spec internally
// from the Profile and shares the rest of the writer pipeline.
type Spec struct {
	// RowCount is the number of rows to generate. Required (>0).
	RowCount int `json:"row_count"`

	// Fields declares each column with its type and distribution.
	Fields []FieldSpec `json:"fields"`

	// Constraints, if non-empty, are evaluated per row by the expression
	// evaluator. Rows that fail any constraint are rejected and re-drawn.
	Constraints []ConstraintSpec `json:"constraints,omitempty"`

	// MaxRejectionRate caps the fraction of rejected rows during
	// constraint-driven rejection sampling. Defaults to 0.5 if zero.
	MaxRejectionRate float64 `json:"max_rejection_rate,omitempty"`

	// Correlations lists optional pairwise correlations to induce via
	// Gaussian copula post-processing. Each entry references two numeric
	// fields by name with a target Pearson correlation in [-1, 1]. Only
	// numeric fields can participate.
	Correlations []CorrelationSpec `json:"correlations,omitempty"`
}

// FieldSpec is a single column declaration.
type FieldSpec struct {
	Name         string         `json:"name"`
	Type         string         `json:"type"`
	Description  string         `json:"description,omitempty"`
	Distribution string         `json:"distribution"`
	Params       map[string]any `json:"params,omitempty"`

	// Precision and Scale apply to decimal128 / nullable_decimal128.
	Precision uint8 `json:"precision,omitempty"`
	Scale     uint8 `json:"scale,omitempty"`

	// NullRate is the per-row probability that the field will be null.
	// Only meaningful for nullable types and decimal128's nullable form.
	NullRate float64 `json:"null_rate,omitempty"`
}

// ConstraintSpec wraps an expression evaluated against the in-memory row.
type ConstraintSpec struct {
	Expr string `json:"expr"`
}

// CorrelationSpec declares a target Pearson correlation between two
// numeric fields.
type CorrelationSpec struct {
	A           string  `json:"a"`
	B           string  `json:"b"`
	Correlation float64 `json:"correlation"`
}

// Options modulates how the spec is realized.
type Options struct {
	// Seed makes the output deterministic. Same spec + same seed must
	// produce a byte-identical .pulse file.
	Seed int64
}

// Result is what the writer reports after a successful Synth call.
type Result struct {
	RowsGenerated int      `json:"rows_generated"`
	RowsRejected  int      `json:"rows_rejected"`
	OutputPath    string   `json:"output_path"`
	Warnings      []string `json:"warnings,omitempty"`
}

// ParseSpec parses Spec JSON. Returns SERVICE_VALIDATION if shape is wrong.
func ParseSpec(raw []byte) (*Spec, error) {
	var s Spec
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, errors.WrapCodedError(err, errors.SERVICE_VALIDATION, "parsing synth spec")
	}
	if err := validateSpec(&s); err != nil {
		return nil, err
	}
	return &s, nil
}

func validateSpec(s *Spec) error {
	if s.RowCount <= 0 {
		return errors.NewCodedError(errors.SERVICE_VALIDATION, "row_count must be > 0")
	}
	if len(s.Fields) == 0 {
		return errors.NewCodedError(errors.SERVICE_VALIDATION, "spec must declare at least one field")
	}
	seen := make(map[string]bool, len(s.Fields))
	for i, f := range s.Fields {
		if f.Name == "" {
			return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				"field has empty name", map[string]any{"index": i})
		}
		if seen[f.Name] {
			return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				"duplicate field name", map[string]any{"name": f.Name})
		}
		seen[f.Name] = true
		if f.Type == "" {
			return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				"field has empty type", map[string]any{"name": f.Name})
		}
		if f.Distribution == "" {
			return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				"field has empty distribution", map[string]any{"name": f.Name})
		}
		if f.NullRate < 0 || f.NullRate > 1 {
			return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				"null_rate must be in [0, 1]", map[string]any{"name": f.Name, "null_rate": f.NullRate})
		}
	}
	for _, c := range s.Correlations {
		if c.A == c.B {
			return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				"correlation a and b must differ", map[string]any{"a": c.A})
		}
		if c.Correlation < -1 || c.Correlation > 1 {
			return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				"correlation must be in [-1, 1]",
				map[string]any{"a": c.A, "b": c.B, "value": c.Correlation})
		}
		if !seen[c.A] || !seen[c.B] {
			return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				"correlation references unknown field",
				map[string]any{"a": c.A, "b": c.B})
		}
	}
	if s.MaxRejectionRate < 0 || s.MaxRejectionRate >= 1 {
		return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			"max_rejection_rate must be in [0, 1)", map[string]any{"value": s.MaxRejectionRate})
	}
	return nil
}

// paramFloat extracts a float64 parameter from FieldSpec.Params.
func paramFloat(name string, params map[string]any, key string, def float64) (float64, bool, error) {
	v, ok := params[key]
	if !ok {
		return def, false, nil
	}
	switch x := v.(type) {
	case float64:
		return x, true, nil
	case int:
		return float64(x), true, nil
	case int64:
		return float64(x), true, nil
	case json.Number:
		f, err := x.Float64()
		if err != nil {
			return 0, false, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				fmt.Sprintf("field %q: param %q must be a number", name, key),
				map[string]any{"value": fmt.Sprint(v)})
		}
		return f, true, nil
	default:
		return 0, false, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("field %q: param %q must be a number", name, key),
			map[string]any{"value": fmt.Sprint(v)})
	}
}

// paramInt extracts an integer parameter.
func paramInt(name string, params map[string]any, key string, def int64) (int64, bool, error) {
	f, ok, err := paramFloat(name, params, key, float64(def))
	if err != nil {
		return 0, false, err
	}
	return int64(f), ok, nil
}

// paramString extracts a string parameter.
func paramString(name string, params map[string]any, key, def string) (string, bool, error) {
	v, ok := params[key]
	if !ok {
		return def, false, nil
	}
	s, isStr := v.(string)
	if !isStr {
		return "", false, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("field %q: param %q must be a string", name, key),
			map[string]any{"value": fmt.Sprint(v)})
	}
	return s, true, nil
}

// paramStringSlice extracts a []string from params.
func paramStringSlice(name string, params map[string]any, key string) ([]string, bool, error) {
	v, ok := params[key]
	if !ok {
		return nil, false, nil
	}
	arr, isArr := v.([]any)
	if !isArr {
		return nil, false, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("field %q: param %q must be an array", name, key), nil)
	}
	out := make([]string, 0, len(arr))
	for i, e := range arr {
		s, ok := e.(string)
		if !ok {
			return nil, false, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				fmt.Sprintf("field %q: param %q[%d] must be a string", name, key, i), nil)
		}
		out = append(out, s)
	}
	return out, true, nil
}

// paramFloatSlice extracts a []float64 from params.
func paramFloatSlice(name string, params map[string]any, key string) ([]float64, bool, error) {
	v, ok := params[key]
	if !ok {
		return nil, false, nil
	}
	arr, isArr := v.([]any)
	if !isArr {
		return nil, false, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("field %q: param %q must be an array", name, key), nil)
	}
	out := make([]float64, 0, len(arr))
	for i, e := range arr {
		switch x := e.(type) {
		case float64:
			out = append(out, x)
		case int:
			out = append(out, float64(x))
		case int64:
			out = append(out, float64(x))
		default:
			return nil, false, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				fmt.Sprintf("field %q: param %q[%d] must be a number", name, key, i), nil)
		}
	}
	return out, true, nil
}
