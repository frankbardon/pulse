package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/distlagparams"
	"github.com/frankbardon/pulse/types"
)

// inline.go is the inline `models` entry (ModelRef.Inline): a
// coefficient-only model declared in the request. Its body is the
// model-file document, judged by the one codec (DecodeFile — version,
// features, strict keys, the structural check), after every number
// written as a scalar expression string is resolved over the request's
// params (InlineNumberFunc, the scalar-slot evaluator the caller hands
// in: internal/scalarslot imports this package, so it cannot be called
// from here). Then checkInline judges the type and the coefficient
// names. The runtime and predict both load through LoadFiles, so they
// resolve the same numbers and refuse the same body alike.

// The PULSE_MODEL_REF_INVALID reasons an inline entry adds.
const (
	// ReasonRefPathAndInline: the entry sets both path and inline.
	ReasonRefPathAndInline = "path_and_inline"
	// ReasonInlineTypeUnsupported: the inline body's type is not one
	// InlineTypes lists (details type, valid).
	ReasonInlineTypeUnsupported = "inline_type_unsupported"
	// ReasonInlineTermUnknown: a coefficient (or std_errors / ci_lower
	// / ci_upper entry) names a term the model does not report
	// (details term, valid).
	ReasonInlineTermUnknown = "inline_term_unknown"
	// ReasonInlineTermMissing: a term the model reports has no
	// coefficient (details term).
	ReasonInlineTermMissing = "inline_term_missing"
	// InlineReasonPrefix prefixes a scalar-slot reason (invalid,
	// not_numeric, not_finite, not_integer) refusing one inline number:
	// inline_invalid, inline_not_numeric, inline_not_finite,
	// inline_not_integer (details field, plus the slot's own keys).
	InlineReasonPrefix = "inline_"
)

// The PULSE_MODEL_SCORE_INVALID reason refusing a score of an inline
// model: it carries coefficients only, so nothing scores it.
const ReasonInlineNotScorable = "inline_not_scorable"

// InlineSourceName names an inline body where the model-file codec
// names a file (DecodeFile's source).
const InlineSourceName = "(inline)"

// inlineTypes are the regression types an inline model may declare:
// the built-in least-squares-shaped fits (coefficients keyed by the
// intercept and the predictors) and the media-mix models, whose curve
// coefficients ModelRegressionTerms names.
var inlineTypes = []types.RegressionType{types.REG_OLS, types.REG_GLM, types.REG_BAYES_LINEAR, types.REG_MMM_HILL, types.REG_MMM_BAYES}

// InlineTypes returns the regression types an inline model may declare,
// in a fresh slice.
func InlineTypes() []types.RegressionType { return slices.Clone(inlineTypes) }

// InlineNumberRefusal is an InlineNumberFunc's refusal before loadInline
// locates it: a scalar-slot Reason (invalid, not_numeric, not_finite,
// not_integer) with its message clause and details, or a shared coded
// error (a params refusal) kept as it is.
type InlineNumberRefusal struct {
	Reason  string
	What    string
	Details map[string]any
	Coded   *errors.CodedError
}

// InlineNumberFunc evaluates one inline number written as an expression
// src over the request's params (raw, as written): literals,
// params.<name> and the pure built-ins only — never a request-bound
// function (coef(), sweep_axis(), lookup(), an embedder function), so
// the value is known before anything runs. integer asks for an exact
// integer (n_obs).
type InlineNumberFunc func(params types.Params, src string, integer bool) (float64, *InlineNumberRefusal)

// The inline body's number positions: the model file's numbers.
var (
	inlineRealKeys    = []string{"conf_level", "df", "dispersion", "n_eff", "sum_weights", "theta"}
	inlineIntegerKeys = []string{"n_obs"}
	inlineMapKeys     = []string{"ci_lower", "ci_upper", "coefficients", "std_errors"}
	// An adstock record's numbers, under adstock.<channel>.
	inlineAdstockRealKeys    = []string{"decay", "peak", "scale", "shape"}
	inlineAdstockIntegerKeys = []string{"max_lag"}
)

// InlineNumberPaths are the JSON key paths (array indices dropped, "*"
// any key) in an inline body where a number may be written as a
// scalar expression — the list internal/exprslots mirrors.
func InlineNumberPaths() [][]string {
	var out [][]string
	for _, k := range inlineMapKeys {
		out = append(out, []string{k, "*"})
	}
	for _, k := range slices.Sorted(slices.Values(append(slices.Clone(inlineRealKeys), inlineIntegerKeys...))) {
		out = append(out, []string{k})
	}
	for _, k := range slices.Sorted(slices.Values(append(slices.Clone(inlineAdstockRealKeys), inlineAdstockIntegerKeys...))) {
		out = append(out, []string{"adstock", "*", k})
	}
	return append(out, []string{"vcov", "values"})
}

// loadInline resolves models[j]'s inline body (resolveInline), decodes
// it with the model-file codec and judges it (checkInline). A codec
// refusal keeps its code and reason, located by details.model / name
// and details.inline (no path).
func loadInline(j int, r types.ModelRef, params types.Params, eval InlineNumberFunc) (*File, *errors.CodedError) {
	data, cerr := resolveInline(j, r, params, eval)
	if cerr != nil {
		return nil, cerr
	}
	f, derr := DecodeFile(InlineSourceName, data)
	if derr != nil {
		ce, _ := derr.(*errors.CodedError)
		if ce == nil {
			ce = errors.WrapCodedError(derr, errors.PULSE_MODEL_FILE_INVALID, "decoding an inline model")
		}
		details := maps.Clone(ce.Details)
		if details == nil {
			details = map[string]any{}
		}
		delete(details, "path")
		details["inline"], details["model"], details["name"] = true, j, r.Name
		return nil, errors.NewCodedErrorWithDetails(ce.Code, fmt.Sprintf("models[%d]: %s", j, ce.Message), details)
	}
	if cerr := checkInline(j, r, f); cerr != nil {
		return nil, cerr
	}
	return f, nil
}

// resolveInline returns models[j]'s inline body with every number
// written as a string at a number position (InlineNumberPaths)
// replaced by its value (eval over params) — except the codec's
// infinity spellings "+Inf" / "-Inf", literals kept as written. A body
// that is no JSON
// object, or holds no such string, is returned as written, for the
// codec to judge.
func resolveInline(j int, r types.ModelRef, params types.Params, eval InlineNumberFunc) ([]byte, *errors.CodedError) {
	dec := json.NewDecoder(bytes.NewReader(r.Inline))
	dec.UseNumber()
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil || doc == nil {
		return r.Inline, nil
	}
	changed := false
	// number resolves src; the codec's own infinity tokens ("+Inf",
	// "-Inf") stay as written (ok false), so a saved file's body loads
	// inline unchanged.
	number := func(field, src string, integer bool) (any, *errors.CodedError) {
		if IsInfinityToken(src) {
			return src, nil
		}
		changed = true
		if eval == nil {
			return "", inlineNumberInvalid(j, r, field, &InlineNumberRefusal{Reason: "invalid",
				What: "is an expression, and none is evaluated here", Details: map[string]any{"compile_error": "no evaluator"}})
		}
		v, ref := eval(params, src, integer)
		if ref != nil {
			return "", inlineNumberInvalid(j, r, field, ref)
		}
		if integer {
			return json.Number(strconv.FormatFloat(v, 'f', -1, 64)), nil
		}
		return json.Number(strconv.FormatFloat(v, 'g', -1, 64)), nil
	}
	for _, k := range inlineRealKeys {
		if s, ok := doc[k].(string); ok {
			n, cerr := number(k, s, false)
			if cerr != nil {
				return nil, cerr
			}
			doc[k] = n
		}
	}
	for _, k := range inlineIntegerKeys {
		if s, ok := doc[k].(string); ok {
			n, cerr := number(k, s, true)
			if cerr != nil {
				return nil, cerr
			}
			doc[k] = n
		}
	}
	for _, k := range inlineMapKeys {
		m, ok := doc[k].(map[string]any)
		if !ok {
			continue
		}
		for _, term := range slices.Sorted(maps.Keys(m)) {
			if s, ok := m[term].(string); ok {
				n, cerr := number(k+"."+term, s, false)
				if cerr != nil {
					return nil, cerr
				}
				m[term] = n
			}
		}
	}
	if ad, ok := doc["adstock"].(map[string]any); ok {
		for _, ch := range slices.Sorted(maps.Keys(ad)) {
			rec, ok := ad[ch].(map[string]any)
			if !ok {
				continue
			}
			for _, k := range append(slices.Clone(inlineAdstockRealKeys), inlineAdstockIntegerKeys...) {
				if s, ok := rec[k].(string); ok {
					n, cerr := number("adstock."+ch+"."+k, s, slices.Contains(inlineAdstockIntegerKeys, k))
					if cerr != nil {
						return nil, cerr
					}
					rec[k] = n
				}
			}
		}
	}
	if vc, ok := doc["vcov"].(map[string]any); ok {
		if rows, ok := vc["values"].([]any); ok {
			for a, row := range rows {
				cells, ok := row.([]any)
				if !ok {
					continue
				}
				for b, c := range cells {
					if s, ok := c.(string); ok {
						n, cerr := number(fmt.Sprintf("vcov.values[%d][%d]", a, b), s, false)
						if cerr != nil {
							return nil, cerr
						}
						cells[b] = n
					}
				}
			}
		}
	}
	if !changed {
		return r.Inline, nil
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.PROCESSING_INTERNAL, "re-encoding an inline model")
	}
	return out, nil
}

// IsInfinityToken reports whether s is the model file's spelling of an
// infinity ("+Inf" / "-Inf"): a literal, never an expression.
func IsInfinityToken(s string) bool { return s == posInf || s == negInf }

// inlineNumberInvalid locates an inline number's refusal at models[j]
// field: a coded refusal keeps its code, gaining details.model / name /
// field; a scalar-slot reason is PULSE_MODEL_REF_INVALID reason
// InlineReasonPrefix + reason.
func inlineNumberInvalid(j int, r types.ModelRef, field string, ref *InlineNumberRefusal) *errors.CodedError {
	if ref.Coded != nil {
		details := maps.Clone(ref.Coded.Details)
		if details == nil {
			details = map[string]any{}
		}
		details["model"], details["name"], details["field"] = j, r.Name, field
		return errors.NewCodedErrorWithDetails(ref.Coded.Code,
			fmt.Sprintf("models[%d]: inline model %q %s: %s", j, r.Name, field, ref.Coded.Message), details)
	}
	details := maps.Clone(ref.Details)
	if details == nil {
		details = map[string]any{}
	}
	details["model"], details["name"], details["field"], details["reason"] = j, r.Name, field, InlineReasonPrefix+ref.Reason
	return errors.NewCodedErrorWithDetails(errors.PULSE_MODEL_REF_INVALID,
		fmt.Sprintf("models[%d]: inline model %q %s %s; an inline number is a literal or a scalar expression over literals and params.<name>", j, r.Name, field, ref.What),
		details)
}

// inlineBayesKernel is the adstock kernel whose coefficient names an
// inline REG_MMM_BAYES model's terms follow (MMMBayesAdstockPrefixes),
// read off its coefficients: a peak:<channel> names the delayed kernel,
// a shape:<channel> or scale:<channel> a Weibull one (weibull_cdf and
// weibull_pdf share their names), anything else the geometric default.
// Every other type reads "" (its terms carry no kernel).
func inlineBayesKernel(f *File, channels []string) string {
	if f.Type != types.REG_MMM_BAYES {
		return ""
	}
	for _, ch := range channels {
		if _, ok := f.Coefficients[MMMPeakPrefix+ch]; ok {
			return distlagparams.KernelDelayed
		}
		_, shape := f.Coefficients[MMMAdstockShapePrefix+ch]
		_, scale := f.Coefficients[MMMAdstockScalePrefix+ch]
		if shape || scale {
			return distlagparams.KernelWeibullCDF
		}
	}
	return distlagparams.KernelGeometric
}

// inlineSaturation is the saturation curve whose coefficient names an
// inline REG_MMM_BAYES model's terms follow: its `saturation` section
// (feature saturation), hill when absent. Every other type reads hill.
func inlineSaturation(f *File) string {
	if f.Saturation == "" {
		return MMMSaturationHill
	}
	return f.Saturation
}

// checkInline judges a decoded inline body: its type is one InlineTypes
// lists, and its coefficients name exactly the terms the model reports
// — the intercept and every predictor, plus, for a media-mix model,
// ModelRegressionTerms' curve terms of its channels (a predictor with a
// k:, s:, decay: or lam: coefficient is a channel; a REG_MMM_BAYES body's
// curve is its `saturation`, hill when absent). std_errors, ci_lower and
// ci_upper may name only those terms.
func checkInline(j int, r types.ModelRef, f *File) *errors.CodedError {
	if !slices.Contains(inlineTypes, f.Type) {
		valid := make([]string, len(inlineTypes))
		for i, t := range inlineTypes {
			valid[i] = string(t)
		}
		return inlineRefInvalid(j, r, ReasonInlineTypeUnsupported,
			fmt.Sprintf("declares type %s; an inline model is one of %v (coefficients only)", f.Type, valid),
			map[string]any{"type": string(f.Type), "valid": valid})
	}
	expected := append([]string{InterceptKey}, f.Predictors...)
	if ModelRegression(f.Type) {
		var channels []string
		for _, p := range f.Predictors {
			for _, prefix := range []string{MMMHalfSaturationPrefix, MMMShapePrefix, MMMDecayPrefix, MMMLogisticLambdaPrefix} {
				if _, ok := f.Coefficients[prefix+p]; ok {
					channels = append(channels, p)
					break
				}
			}
		}
		if len(channels) == 0 {
			return inlineRefInvalid(j, r, ReasonInlineTermMissing,
				fmt.Sprintf("is a %s model with no channel: give at least one predictor its %s<channel> and %s<channel> coefficients", f.Type, MMMHalfSaturationPrefix, MMMShapePrefix),
				map[string]any{"term": MMMHalfSaturationPrefix + "<channel>"})
		}
		params, _ := json.Marshal(map[string]any{"channels": channels, "adstock": inlineBayesKernel(f, channels), "saturation": inlineSaturation(f)})
		terms, _ := ModelRegressionTerms(&types.RegressionSpec{Type: f.Type, Params: params})
		expected = append(expected, terms...)
	}
	slices.Sort(expected)
	for _, m := range []map[string]Float{f.Coefficients, f.StdErrors, f.CILower, f.CIUpper} {
		for _, term := range slices.Sorted(maps.Keys(m)) {
			if !slices.Contains(expected, term) {
				return inlineRefInvalid(j, r, ReasonInlineTermUnknown,
					fmt.Sprintf("names term %q, which a %s model with these predictors does not report", term, f.Type),
					map[string]any{"term": term, "valid": expected})
			}
		}
	}
	for _, term := range expected {
		if _, ok := f.Coefficients[term]; !ok {
			return inlineRefInvalid(j, r, ReasonInlineTermMissing,
				fmt.Sprintf("has no coefficient for term %q, which a %s model with these predictors reports", term, f.Type),
				map[string]any{"term": term})
		}
	}
	return nil
}

// inlineRefInvalid is PULSE_MODEL_REF_INVALID at an inline models[j].
func inlineRefInvalid(j int, r types.ModelRef, reason, what string, extra map[string]any) *errors.CodedError {
	details := map[string]any{"model": j, "name": r.Name, "inline": true, "reason": reason}
	for k, v := range extra {
		details[k] = v
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_MODEL_REF_INVALID,
		fmt.Sprintf("models[%d]: inline model %q %s", j, r.Name, what), details)
}
