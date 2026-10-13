package synth

import (
	"bytes"
	"fmt"
	"math"
	"math/rand/v2"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// Vector fields (vec_f32 / vec_f64) are synthesised as ONE joint draw per
// row, never as dim independent scalars: a vector's elements are jointly
// distributed, and drawing each from its own marginal would write a
// cohort that parses and is quietly wrong. The construction is the
// value-scale Gaussian copula the scalar `correlations` arm already uses
// (copula.go), applied WITHIN the field — one Cholesky over the element
// correlation matrix (factorCorrelations, the package's one completion
// policy), each element pushed through its own quantile function
// (fieldMoments + quantileFor, the scalar marginal machinery reused
// verbatim) — followed by the kind's bounds. A vector never takes part
// in any cross-field structure (pairs, models, value-scale
// correlations): validateSpec refuses that, because none of those arms
// has a vector-shaped row value.

// VectorSpec declares a vec_f32 / vec_f64 field's shape and joint
// distribution. It rides FieldSpec.Vector (`vector`), additive and
// omitempty, so a spec without vector fields is byte-identical.
//
// FieldSpec.Distribution / Params are the DEFAULT element family; an
// Elements entry overrides it for one element. Supported element
// families are the ones with exact moments and a quantile function —
// normal, uniform, lognormal, exponential, bernoulli, discrete and
// mixture (vectorElementDistributions).
type VectorSpec struct {
	// Dim is the element count, 2..1024 (encoding.MinVectorDim ..
	// MaxVectorDim). Required.
	Dim int `json:"dim"`
	// Kind is the vector's meaning: measure, scale, composition or
	// binary (encoding.ParseVectorKind). Optional; it is written to the
	// output schema and bounds every element (see applyVectorKind).
	Kind string `json:"kind,omitempty"`
	// ScaleMin / ScaleMax are the declared value range, both or neither,
	// finite, ScaleMin < ScaleMax. Written to the output schema; every
	// element is clamped into it.
	ScaleMin *float64 `json:"scale_min,omitempty"`
	ScaleMax *float64 `json:"scale_max,omitempty"`
	// Labels names the elements: absent or exactly Dim entries.
	Labels []string `json:"labels,omitempty"`
	// NaNRate is the default per-element NaN rate in [0, 1] — a NaN
	// element is a stored value (a missing member), distinct from a null
	// vector, which FieldSpec.NullRate draws.
	NaNRate float64 `json:"nan_rate,omitempty"`
	// Elements overrides the element family per element: absent, or
	// exactly Dim entries in element order.
	Elements []VectorElementSpec `json:"elements,omitempty"`
	// Correlations is the inter-element correlation, as a list of
	// 1-based element pairs (the `emb[k]` numbering requests use). An
	// unlisted pair is drawn independent and counted in a warning,
	// exactly as the scalar `correlations` matrix is completed.
	Correlations []VectorCorrelationSpec `json:"correlations,omitempty"`
}

// VectorElementSpec overrides one element's marginal. Distribution
// empty inherits FieldSpec.Distribution with Params laid over
// FieldSpec.Params key by key; a non-empty Distribution replaces both.
type VectorElementSpec struct {
	Distribution string         `json:"distribution,omitempty"`
	Params       map[string]any `json:"params,omitempty"`
	// NaNRate overrides VectorSpec.NaNRate for this element when set.
	NaNRate *float64 `json:"nan_rate,omitempty"`
}

// VectorCorrelationSpec is one inter-element correlation: elements A and
// B (1-based, distinct) at target Pearson correlation in [-1, 1] on the
// latent (copula) scale.
type VectorCorrelationSpec struct {
	A           int     `json:"a"`
	B           int     `json:"b"`
	Correlation float64 `json:"correlation"`
}

// vectorElementDistributions is the element-family allowlist: exactly
// the distributions fieldMoments and quantileFor both support, so every
// element can be driven through the copula. Anything else is refused at
// build time naming the element, rather than surfacing quantileFor's
// "does not support pairwise correlation" text for a field nobody
// correlated.
var vectorElementDistributions = map[string]bool{
	DistNormal: true, DistUniform: true, DistLogNormal: true, DistExponential: true,
	DistBernoulli: true, DistDiscrete: true, DistMixture: true,
}

// isVectorFieldType reports whether a spec-string type is vec_f32 /
// vec_f64.
func isVectorFieldType(typeName string) bool {
	ft, ok := encoding.ParseFieldType(typeName)
	return ok && ft.IsVector()
}

// vectorSpecErr is the one spec-validation shape for a vector field.
func vectorSpecErr(field, msg string, details map[string]any) error {
	if details == nil {
		details = map[string]any{}
	}
	details["field"] = field
	return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
		fmt.Sprintf("vector field %q: %s", field, msg), details)
}

// validateVectorField checks a FieldSpec's vector shape in both
// directions: a vec_* field must carry a well-formed `vector` block, and
// no other field may carry one. Element families are checked again (with
// their params) when the sampler is built.
func validateVectorField(f FieldSpec) error {
	isVec := isVectorFieldType(f.Type)
	if !isVec {
		if f.Vector != nil {
			return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				fmt.Sprintf("field %q: a `vector` block is only valid on a vec_f32 / vec_f64 field", f.Name),
				map[string]any{"field": f.Name, "type": f.Type})
		}
		return nil
	}
	v := f.Vector
	if v == nil {
		return vectorSpecErr(f.Name, "requires a `vector` block declaring at least `dim`", nil)
	}
	if v.Dim < encoding.MinVectorDim || v.Dim > encoding.MaxVectorDim {
		return vectorSpecErr(f.Name, fmt.Sprintf("dim must be %d..%d", encoding.MinVectorDim, encoding.MaxVectorDim),
			map[string]any{"dim": v.Dim})
	}
	if v.Kind != "" {
		if _, ok := encoding.ParseVectorKind(v.Kind); !ok {
			return vectorSpecErr(f.Name, "kind must be measure, scale, composition or binary",
				map[string]any{"kind": v.Kind})
		}
	}
	if (v.ScaleMin == nil) != (v.ScaleMax == nil) {
		return vectorSpecErr(f.Name, "scale_min and scale_max are declared together or not at all", nil)
	}
	if v.ScaleMin != nil {
		lo, hi := *v.ScaleMin, *v.ScaleMax
		if math.IsNaN(lo) || math.IsInf(lo, 0) || math.IsNaN(hi) || math.IsInf(hi, 0) || lo >= hi {
			return vectorSpecErr(f.Name, "scale range must be finite with scale_min < scale_max",
				map[string]any{"scale_min": lo, "scale_max": hi})
		}
	}
	if len(v.Labels) != 0 && len(v.Labels) != v.Dim {
		return vectorSpecErr(f.Name, "labels must be absent or exactly dim entries",
			map[string]any{"dim": v.Dim, "labels": len(v.Labels)})
	}
	if len(v.Elements) != 0 && len(v.Elements) != v.Dim {
		return vectorSpecErr(f.Name, "elements must be absent or exactly dim entries",
			map[string]any{"dim": v.Dim, "elements": len(v.Elements)})
	}
	if v.NaNRate < 0 || v.NaNRate > 1 {
		return vectorSpecErr(f.Name, "nan_rate must be in [0, 1]", map[string]any{"nan_rate": v.NaNRate})
	}
	for k, e := range v.Elements {
		if f.Distribution == "" && e.Distribution == "" {
			return vectorSpecErr(f.Name, fmt.Sprintf("element %d has no distribution and the field declares no default", k+1),
				map[string]any{"element": k + 1})
		}
		if e.NaNRate != nil && (*e.NaNRate < 0 || *e.NaNRate > 1) {
			return vectorSpecErr(f.Name, fmt.Sprintf("element %d nan_rate must be in [0, 1]", k+1),
				map[string]any{"element": k + 1, "nan_rate": *e.NaNRate})
		}
	}
	if f.Distribution == "" && len(v.Elements) == 0 {
		return vectorSpecErr(f.Name, "declares no distribution: set the field's default element distribution or one per element", nil)
	}
	seen := make(map[[2]int]bool, len(v.Correlations))
	for _, c := range v.Correlations {
		if c.A < 1 || c.A > v.Dim || c.B < 1 || c.B > v.Dim || c.A == c.B {
			return vectorSpecErr(f.Name, "correlation elements must be distinct 1-based indices within dim",
				map[string]any{"a": c.A, "b": c.B, "dim": v.Dim})
		}
		if math.IsNaN(c.Correlation) || c.Correlation < -1 || c.Correlation > 1 {
			return vectorSpecErr(f.Name, "correlation must be in [-1, 1]",
				map[string]any{"a": c.A, "b": c.B, "value": c.Correlation})
		}
		key := [2]int{min(c.A, c.B), max(c.A, c.B)}
		if seen[key] {
			return vectorSpecErr(f.Name, "correlation pair declared twice",
				map[string]any{"a": c.A, "b": c.B})
		}
		seen[key] = true
	}
	return nil
}

// refuseVectorCrossFieldUse refuses a vector field named by any
// cross-field structure — a value-scale or residual correlation, a
// conditional pair, a model target or predictor. Every one of those arms
// reads and writes a SCALAR row value; a vector's joint structure lives
// inside its own `vector` block (Correlations), never between fields.
// Without this the failure is a mid-run ENCODING_TYPE_MISMATCH or, worse,
// a pair silently overwriting the vector with a scalar draw.
func refuseVectorCrossFieldUse(s *Spec, vectorFields map[string]bool) error {
	if len(vectorFields) == 0 {
		return nil
	}
	check := func(kind string, names ...string) error {
		for _, n := range names {
			if vectorFields[n] {
				return vectorSpecErr(n, fmt.Sprintf("a vector field cannot take part in a %s; declare inter-element structure in its `vector.correlations`", kind),
					map[string]any{"relationship": kind})
			}
		}
		return nil
	}
	for _, c := range s.Correlations {
		if err := check("correlation", c.A, c.B); err != nil {
			return err
		}
	}
	for _, c := range s.ResidualCorrelations {
		if err := check("residual correlation", c.A, c.B); err != nil {
			return err
		}
	}
	for _, p := range s.CategoricalPairs {
		if err := check("categorical pair", p.A, p.B); err != nil {
			return err
		}
	}
	for _, p := range s.CategoricalNumericPairs {
		if err := check("categorical-numeric pair", p.A, p.B); err != nil {
			return err
		}
	}
	for _, p := range s.SetCategoricalPairs {
		if err := check("set-categorical pair", p.Set, p.Categorical); err != nil {
			return err
		}
	}
	for _, p := range s.SetNumericPairs {
		if err := check("set-numeric pair", p.Set, p.Numeric); err != nil {
			return err
		}
	}
	for _, p := range s.SetSetPairs {
		if err := check("set-set pair", p.SetA, p.SetB); err != nil {
			return err
		}
	}
	for _, m := range s.Models {
		if err := check("linear model", m.Field); err != nil {
			return err
		}
		for _, pr := range m.Predictors {
			if err := check("linear model", pr.Field); err != nil {
				return err
			}
		}
	}
	return nil
}

// vectorFieldFor builds the schema's VECTORS entry for a validated
// vector FieldSpec, so the output cohort's vector metadata (dim, kind,
// scale range, labels) is exactly the spec's.
func vectorFieldFor(f FieldSpec, index int) encoding.VectorField {
	v := f.Vector
	vf := encoding.VectorField{FieldIndex: index, Dim: v.Dim}
	if v.Kind != "" {
		vf.Kind, _ = encoding.ParseVectorKind(v.Kind)
	}
	if v.ScaleMin != nil {
		vf.HasScale, vf.ScaleMin, vf.ScaleMax = true, *v.ScaleMin, *v.ScaleMax
	}
	if len(v.Labels) > 0 {
		vf.Labels = append([]string(nil), v.Labels...)
	}
	return vf
}

// vectorElementName is the element's name in messages and in the copula's
// participant list: the 1-based `field[k]` spelling requests use.
func vectorElementName(field string, k int) string {
	return fmt.Sprintf("%s[%d]", field, k+1)
}

// vectorElementFieldSpec resolves element k's effective scalar FieldSpec:
// its family, its params (with the kind's natural bounds filled in for a
// param the author left out) and its NaN rate.
func vectorElementFieldSpec(f FieldSpec, k int) (FieldSpec, float64) {
	v := f.Vector
	dist, params := f.Distribution, f.Params
	nanRate := v.NaNRate
	if len(v.Elements) > 0 {
		e := v.Elements[k]
		if e.Distribution != "" {
			dist, params = e.Distribution, e.Params
		} else if len(e.Params) > 0 {
			merged := make(map[string]any, len(params)+len(e.Params))
			for key, val := range params {
				merged[key] = val
			}
			for key, val := range e.Params {
				merged[key] = val
			}
			params = merged
		}
		if e.NaNRate != nil {
			nanRate = *e.NaNRate
		}
	}
	return FieldSpec{
		Name:         vectorElementName(f.Name, k),
		Type:         "f64",
		Distribution: dist,
		Params:       vectorKindDefaults(v, dist, params),
	}, nanRate
}

// vectorKindDefaults fills the params an element family needs from the
// vector's own bounds when the author left them out, so a from-schema
// spec can say only `{"distribution": "discrete", "vector": {"kind":
// "scale", "scale_min": 1, "scale_max": 7, ...}}`:
//
//   - discrete without values: the integers inside the scale range
//     (equal weights), or {0, 1} for a binary vector;
//   - uniform without min / max: the scale range.
//
// An explicit param always wins. The returned map is a copy; the spec's
// own params are never mutated.
func vectorKindDefaults(v *VectorSpec, dist string, params map[string]any) map[string]any {
	out := make(map[string]any, len(params)+2)
	for key, val := range params {
		out[key] = val
	}
	kind, _ := encoding.ParseVectorKind(v.Kind)
	switch dist {
	case DistDiscrete:
		if _, ok := out["values"]; ok {
			break
		}
		switch {
		case v.ScaleMin != nil:
			lo, hi := math.Ceil(*v.ScaleMin), math.Floor(*v.ScaleMax)
			if hi-lo >= maxVectorDefaultLevels {
				// Left without values so parseDiscreteLevels refuses with
				// its own message rather than this building a support
				// nobody asked for.
				break
			}
			var vals []any
			for x := lo; x <= hi; x++ {
				vals = append(vals, x)
			}
			out["values"] = vals
		case kind == encoding.VectorKindBinary:
			out["values"] = []any{0.0, 1.0}
		}
	case DistUniform:
		if v.ScaleMin != nil {
			if _, ok := out["min"]; !ok {
				out["min"] = *v.ScaleMin
			}
			if _, ok := out["max"]; !ok {
				out["max"] = *v.ScaleMax
			}
		}
	}
	return out
}

// maxVectorDefaultLevels caps the integer support vectorKindDefaults
// derives from a scale range: a range wider than this is a continuous
// scale, not a coded one, and the author should name the family.
const maxVectorDefaultLevels = 1024

// vectorSampler draws one vector per row: dim correlated standard
// normals (L·z, or z itself when no correlation is declared), each
// element through its own quantile, then the element's own clamp, the
// kind's bounds, the NaN draw and the vec_f32 rounding.
//
// RNG consumption is a function of the SPEC alone: exactly dim
// NormFloat64 per row, then — only when some element declares a NaN rate
// — exactly dim Float64, one per element, whether or not it comes up
// NaN. The null-vector draw is the ordinary nullableSampler wrapper's.
type vectorSampler struct {
	dim       int
	f32       bool
	kind      encoding.VectorKind
	hasScale  bool
	scaleMin  float64
	scaleMax  float64
	quantiles []quantileFunc
	hasClamp  []bool
	clampMin  []float64
	clampMax  []float64
	nanRates  []float64
	anyNaN    bool
	chol      [][]float64 // nil = independent elements
	warnings  []string
}

// newVectorSampler compiles a vector FieldSpec. validateVectorField has
// already accepted its shape.
func newVectorSampler(f FieldSpec) (*vectorSampler, error) {
	ft, _ := encoding.ParseFieldType(f.Type)
	v := f.Vector
	if v == nil {
		return nil, vectorSpecErr(f.Name, "requires a `vector` block declaring at least `dim`", nil)
	}
	s := &vectorSampler{
		dim:       v.Dim,
		f32:       ft == encoding.FieldTypeVecF32,
		quantiles: make([]quantileFunc, v.Dim),
		hasClamp:  make([]bool, v.Dim),
		clampMin:  make([]float64, v.Dim),
		clampMax:  make([]float64, v.Dim),
		nanRates:  make([]float64, v.Dim),
	}
	s.kind, _ = encoding.ParseVectorKind(v.Kind)
	if v.ScaleMin != nil {
		s.hasScale, s.scaleMin, s.scaleMax = true, *v.ScaleMin, *v.ScaleMax
	}
	for k := 0; k < v.Dim; k++ {
		efs, nanRate := vectorElementFieldSpec(f, k)
		if !vectorElementDistributions[efs.Distribution] {
			return nil, vectorSpecErr(f.Name, fmt.Sprintf(
				"element %d: distribution %q cannot drive a vector element (supported: bernoulli, discrete, exponential, lognormal, mixture, normal, uniform)",
				k+1, efs.Distribution),
				map[string]any{"element": k + 1, "distribution": efs.Distribution})
		}
		mean, std, cMin, cMax, clamped, err := fieldMoments(efs)
		if err != nil {
			return nil, err
		}
		q, err := quantileFor(efs, mean, std)
		if err != nil {
			return nil, err
		}
		s.quantiles[k] = q
		s.hasClamp[k], s.clampMin[k], s.clampMax[k] = clamped, cMin, cMax
		s.nanRates[k] = nanRate
		if nanRate > 0 {
			s.anyNaN = true
		}
	}
	if len(v.Correlations) > 0 {
		names := make([]string, v.Dim)
		idx := make(map[string]int, v.Dim)
		for k := range names {
			names[k] = vectorElementName(f.Name, k)
			idx[names[k]] = k
		}
		pairs := make([]CorrelationSpec, len(v.Correlations))
		for i, c := range v.Correlations {
			pairs[i] = CorrelationSpec{A: names[c.A-1], B: names[c.B-1], Correlation: c.Correlation}
		}
		chol, warnings, err := factorCorrelations(
			fmt.Sprintf("vector %q element correlation", f.Name), "element", names, idx, pairs)
		if err != nil {
			return nil, err
		}
		s.chol, s.warnings = chol, warnings
	}
	return s, nil
}

func (s *vectorSampler) next(rng *rand.Rand) (any, bool) {
	z := make([]float64, s.dim)
	u := make([]float64, s.dim)
	if s.chol != nil {
		correlatedNormals(rng, s.chol, z, u)
	} else {
		for k := range u {
			u[k] = rng.NormFloat64()
		}
	}
	// z's storage carries the result: L·z has already been read into u,
	// and the row map keeps this slice, so each row allocates afresh.
	out := z
	for k := 0; k < s.dim; k++ {
		x := s.quantiles[k](u[k], phi(u[k]))
		if s.hasClamp[k] {
			x = math.Min(math.Max(x, s.clampMin[k]), s.clampMax[k])
		}
		out[k] = x
	}
	if s.anyNaN {
		for k := 0; k < s.dim; k++ {
			if rng.Float64() < s.nanRates[k] {
				out[k] = math.NaN()
			}
		}
	}
	applyVectorKind(out, s.kind, s.hasScale, s.scaleMin, s.scaleMax)
	if s.f32 {
		for k, x := range out {
			out[k] = float64(float32(x))
		}
	}
	return out, false
}

// applyVectorKind bounds a drawn vector in place by its declared meaning.
// NaN elements are left as they are (a missing member has no value to
// bound) and are excluded from a composition's closure.
//
//   - scale range (any kind): every element clamped into [min, max];
//   - binary: every element rounded to 0 or 1 at 0.5 (the toBool rule);
//   - composition: negatives floored at 0 and the present elements
//     rescaled to sum to 1 (all-zero → equal shares) — the simplex
//     closure, which a per-element marginal alone cannot guarantee.
func applyVectorKind(v []float64, kind encoding.VectorKind, hasScale bool, lo, hi float64) {
	if hasScale {
		for k, x := range v {
			if !math.IsNaN(x) {
				v[k] = math.Min(math.Max(x, lo), hi)
			}
		}
	}
	switch kind {
	case encoding.VectorKindBinary:
		for k, x := range v {
			if math.IsNaN(x) {
				continue
			}
			if x >= 0.5 {
				v[k] = 1
			} else {
				v[k] = 0
			}
		}
	case encoding.VectorKindComposition:
		sum, present := 0.0, 0
		for k, x := range v {
			if math.IsNaN(x) {
				continue
			}
			if x < 0 {
				x = 0
				v[k] = 0
			}
			sum += x
			present++
		}
		for k, x := range v {
			if math.IsNaN(x) {
				continue
			}
			if sum > 0 {
				v[k] = x / sum
			} else {
				v[k] = 1 / float64(present)
			}
		}
	}
}

// writeVectorFieldValue encodes one vector cell at the field's dim. A
// null writes the zeroed full-stride placeholder (null rides the bitmap
// only); a non-null value must be exactly dim elements.
func writeVectorFieldValue(buf *bytes.Buffer, field *encoding.Field, dim int, val any, isNull bool) error {
	if isNull {
		return encoding.WriteVector(buf, field.Type, dim, nil)
	}
	v, ok := val.([]float64)
	if !ok || len(v) != dim {
		return errors.NewCodedErrorWithDetails(errors.ENCODING_TYPE_MISMATCH,
			fmt.Sprintf("field %q: vector value must be %d float64 elements, got %T", field.Name, dim, val),
			map[string]any{"field": field.Name, "dim": dim})
	}
	return encoding.WriteVector(buf, field.Type, dim, v)
}

// writeFieldValueDim is the dim-aware entry the encode paths use: a
// vector needs its dim (a schema property, not a field one) and every
// other type goes through writeFieldValueForField unchanged.
func writeFieldValueDim(buf *bytes.Buffer, field *encoding.Field, dim int, val any, isNull bool) error {
	if field.Type.IsVector() {
		return writeVectorFieldValue(buf, field, dim, val, isNull)
	}
	return writeFieldValueForField(buf, field, val, isNull)
}
