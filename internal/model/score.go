package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/types"
)

// Scale values of ATTR_REG_PREDICT's params.scale (REG_GLM only).
const (
	// ScaleResponse scores a GLM on the response scale: the inverse link
	// of the linear predictor (the fit's own fitted means). The default.
	ScaleResponse = "response"
	// ScaleLink scores a GLM on the link scale: the linear predictor.
	ScaleLink = "link"
)

// The PULSE_MODEL_SCORE_INVALID reasons.
const (
	ReasonParamsInvalid   = "params_invalid"
	ReasonFitMissing      = "fit_missing"
	ReasonSlotFields      = "slot_fields"
	ReasonTermsEmpty      = "terms_empty"
	ReasonTermDuplicate   = "term_duplicate"
	ReasonScaleUnknown    = "scale_unknown"
	ReasonScaleNotGLM     = "scale_not_glm"
	ReasonTypeUnsupported = "type_unsupported"
	ReasonBoundUnknown    = "bound_unknown"
	ReasonBoundTerms      = "bound_terms"
	// ReasonNoScorer refuses scoring an extension regression's fit whose
	// registration supplies no Scorer.
	ReasonNoScorer = "no_scorer"
	// ReasonTermsUnsupported / ReasonBoundUnsupported refuse params.terms
	// and a lower / upper params.bound on an extension regression's fit:
	// its scorer scores the whole model only.
	ReasonTermsUnsupported = "terms_unsupported"
	ReasonBoundUnsupported = "bound_unsupported"
)

// Bound values of ATTR_REG_PREDICT's params.bound: which coefficient
// figure a single-term score multiplies its column by.
const (
	// BoundPoint scores with the coefficient itself. The default.
	BoundPoint = "point"
	// BoundLower scores with the coefficient's lower confidence bound
	// (RegressionResult.CILower) — single-term only.
	BoundLower = "lower"
	// BoundUpper scores with the coefficient's upper confidence bound
	// (RegressionResult.CIUpper) — single-term only.
	BoundUpper = "upper"
)

// ScoreParams is ATTR_REG_PREDICT's params block, decoded by the one
// function both the runtime and predict call (DecodeScoreParams).
type ScoreParams struct {
	// Fit names the bound model to score.
	Fit string `json:"fit"`
	// Terms restricts the score to a subset of the model's terms — its
	// predictor names, plus InterceptKey to include the intercept. Nil
	// scores the whole model, intercept included.
	Terms []string `json:"terms,omitempty"`
	// Scale is ScaleResponse (default) or ScaleLink; REG_GLM only.
	Scale string `json:"scale,omitempty"`
	// Bound is BoundPoint (default), BoundLower or BoundUpper. A
	// non-point bound needs Terms to list exactly one predictor: it is
	// that term's coefficient-interval contribution (bound × column),
	// never a prediction interval.
	Bound string `json:"bound,omitempty"`
}

// Signature is the static face of a bound model: what a scoring
// reference is judged against without the fit's numbers. The runtime
// takes it from a Bound, predict from the fit request as written, so
// both judge one reference the same way.
type Signature struct {
	// Name is the bound name.
	Name string
	// Type is the regression operator that produces the fit.
	Type types.RegressionType
	// Target is a regression fit's response field: the column an
	// ATTR_REG_RESIDUAL slot's params.fit subtracts its score from.
	Target string
	// Terms are the predictor columns the fit was offered, in spec
	// order: the names `terms` may list and the columns a scoring frame
	// must hold. Under Selection it is the candidate set, a superset of
	// the predictors the fit keeps (a dropped candidate scores 0).
	Terms []string

	// Source is where the name is bound (a `fits` or `models` entry).
	Source Source

	// Kind is the bound fit's kind: types.FitKindRegression (the zero
	// value reads as it — EffectiveKind) or types.FitKindVector. One
	// namespace holds both; a reference of the other kind is
	// PULSE_MODEL_KIND_MISMATCH.
	Kind types.FitKind

	// Members are a vector fit's member labels in axis order — the
	// names a consuming slot aligns to — when known: the runtime's Set
	// always knows them; predict's static FitCatalog never resolves a
	// battery, so it leaves them nil. Nil on a regression.
	Members []string
}

// EffectiveKind is Kind, types.FitKindRegression when unset.
func (s Signature) EffectiveKind() types.FitKind {
	if s.Kind == "" {
		return types.FitKindRegression
	}
	return s.Kind
}

// Catalog resolves bound names to their Signatures: the runtime's Set,
// or predict's FitCatalog. A nil Catalog binds nothing.
type Catalog interface {
	// Signature returns name's signature and whether name is bound.
	Signature(name string) (Signature, bool)
	// Names returns the bound names, sorted.
	Names() []string
}

// Signature satisfies Catalog. Nil-receiver-safe.
func (s *Set) Signature(name string) (Signature, bool) {
	if s == nil {
		return Signature{}, false
	}
	if b, ok := s.byName[name]; ok {
		return b.Signature(), true
	}
	if v, ok := s.vectors[name]; ok {
		return v.Signature(), true
	}
	if f, ok := s.failed[name]; ok {
		return f.sig, true
	}
	return Signature{}, false
}

// Signature is v's static face: kind vector, its member labels.
func (v *VectorFit) Signature() Signature {
	return Signature{Name: v.Name, Kind: types.FitKindVector, Members: append([]string(nil), v.Labels...), Source: v.Source}
}

// Signature is b's static face.
func (b *Bound) Signature() Signature {
	return Signature{Name: b.Name, Type: b.Type, Target: b.Target, Terms: append([]string(nil), b.Terms...), Source: b.Source, Kind: types.FitKindRegression}
}

// FitCatalog is the static Catalog of a ComposedRequest `fits` list —
// predict's view of the names the runtime's pre-pass binds. Entries
// CheckFits would refuse are skipped (that refusal comes first).
type FitCatalog map[string]Signature

// CatalogFromFits builds the FitCatalog of fits: a regression fit's
// signature (type, terms), or a vector fit's (kind vector; members
// unknown statically — FitKindOf).
func CatalogFromFits(fits []types.FitSpec) FitCatalog {
	out := make(FitCatalog, len(fits))
	for i, f := range fits {
		if f.Name == "" || f.Request == nil {
			continue
		}
		switch FitKindOf(f.Request) {
		case types.FitKindRegression:
			reg := f.Request.Regressions[0]
			out[f.Name] = Signature{Name: f.Name, Type: reg.Type, Target: reg.Target, Terms: append([]string(nil), reg.Predictors...), Source: FitSource(i), Kind: types.FitKindRegression}
		case types.FitKindVector:
			out[f.Name] = Signature{Name: f.Name, Source: FitSource(i), Kind: types.FitKindVector}
		}
	}
	return out
}

// Signature satisfies Catalog.
func (c FitCatalog) Signature(name string) (Signature, bool) {
	s, ok := c[name]
	return s, ok
}

// Names satisfies Catalog.
func (c FitCatalog) Names() []string {
	out := make([]string, 0, len(c))
	for n := range c {
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}

// scorableBuiltins are the built-in regression types with a RowScorer
// (scorer.go): REG_OLS — penalties and selection included — REG_GLM,
// REG_BAYES_LINEAR at its posterior mean. It is the static face of the
// engine's built-in ScorerSource (regression.BuiltinScorer), which
// predict cannot import; TestBuiltinScorer_MatchesScorableType keeps
// the two one set.
var scorableBuiltins = []types.RegressionType{types.REG_OLS, types.REG_GLM, types.REG_BAYES_LINEAR}

// ScorableType reports whether a fit of type t can be scored: a type
// with a RowScorer (scorableBuiltins). It is the built-in view; an
// instance's view (ScorableFunc) adds its extension regressions that
// supply a scorer.
func ScorableType(t types.RegressionType) bool {
	return slices.Contains(scorableBuiltins, t)
}

// ScorableFunc is an instance's view of the scorable regression types:
// ScorableType plus every registered, visible extension regression
// whose registration supplies a Scorer. The runtime reads it off the
// engine's extension registry, predict off the extensions snapshot, so
// both judge one reference the same way.
type ScorableFunc func(t types.RegressionType) bool

// BuiltinRegression reports whether t is a built-in regression type
// (types.AllRegressionTypes) — any other bound type is an extension
// regression's.
func BuiltinRegression(t types.RegressionType) bool {
	return slices.Contains(types.AllRegressionTypes(), t)
}

// CheckScore is the one rule over a scoring reference (an
// ATTR_REG_PREDICT slot), run by the runtime's field-reference pass, the
// attribute's own bind and predict alike: params decode, the slot
// carries none of the refit attributes' fields, the fit name resolves in
// cat, the fit type is scorable, `terms` lists distinct names of the
// fit's terms (InterceptKey included), `scale` is a GLM-only
// response|link and `bound` is point|lower|upper, a non-point one with
// exactly one predictor term. The first fault wins. It does not judge the scoring
// frame's columns — the caller's field-reference walk does, with
// PredictorMissing / PredictorNotNumeric over Signature.Terms.
//
// CheckScore judges scorability by the built-ins alone (ScorableType);
// CheckScoreFor takes the instance's view.
func CheckScore(attr *types.Attribute, cat Catalog) (ScoreParams, Signature, *errors.CodedError) {
	return CheckScoreFor(attr, cat, ScorableType)
}

// CheckScoreFor is CheckScore over the instance's scorable types (nil:
// ScorableType). An extension regression's fit is scorable only when
// its registration supplies a Scorer (else reason no_scorer), and its
// scorer scores the whole model: params.terms (terms_unsupported) and a
// lower / upper params.bound (bound_unsupported) are refused on it.
func CheckScoreFor(attr *types.Attribute, cat Catalog, scorable ScorableFunc) (ScoreParams, Signature, *errors.CodedError) {
	if scorable == nil {
		scorable = ScorableType
	}
	p, err := DecodeScoreParams(attr.Params)
	if err != nil {
		return p, Signature{}, err
	}
	if attr.Field != "" || attr.Target != "" || len(attr.Predictors) > 0 || attr.Penalty != "" {
		return p, Signature{}, scoreInvalid(p.Fit, ReasonSlotFields,
			"takes no field, target, predictors or penalty — the fitted model supplies them; name the fit under params.fit and the output under label", nil)
	}
	if p.Fit == "" {
		return p, Signature{}, scoreInvalid("", ReasonFitMissing, "requires params.fit, the name of a bound model", nil)
	}
	var sig Signature
	ok := false
	if cat != nil {
		sig, ok = cat.Signature(p.Fit)
	}
	if !ok {
		return p, Signature{}, UnknownModel(p.Fit, NamesOfKind(cat, types.FitKindRegression))
	}
	if sig.EffectiveKind() != types.FitKindRegression {
		return p, sig, KindMismatch(p.Fit, types.FitKindRegression, sig.EffectiveKind())
	}
	extension := !BuiltinRegression(sig.Type)
	if !scorable(sig.Type) {
		if extension {
			return p, sig, noScorer(p.Fit, sig.Type)
		}
		return p, sig, typeUnsupported(p.Fit, sig.Type)
	}
	if extension {
		if p.Terms != nil {
			return p, sig, scoreInvalid(p.Fit, ReasonTermsUnsupported,
				fmt.Sprintf("params.terms does not apply to %q, a %s fit: an extension regression's scorer scores the whole model; drop params.terms", p.Fit, sig.Type),
				map[string]any{"type": string(sig.Type)})
		}
		if p.Bound == BoundLower || p.Bound == BoundUpper {
			return p, sig, scoreInvalid(p.Fit, ReasonBoundUnsupported,
				fmt.Sprintf("params.bound %q does not apply to %q, a %s fit: an extension regression's scorer scores the whole model at its point estimates; drop params.bound", p.Bound, p.Fit, sig.Type),
				map[string]any{"type": string(sig.Type), "bound": p.Bound})
		}
	}
	if p.Terms != nil {
		if len(p.Terms) == 0 {
			return p, sig, scoreInvalid(p.Fit, ReasonTermsEmpty, "lists no terms; omit params.terms to score the whole model", nil)
		}
		seen := make(map[string]bool, len(p.Terms))
		for _, t := range p.Terms {
			if seen[t] {
				return p, sig, scoreInvalid(p.Fit, ReasonTermDuplicate,
					fmt.Sprintf("lists term %q twice", t), map[string]any{"term": t})
			}
			seen[t] = true
			if t != InterceptKey && !slices.Contains(sig.Terms, t) {
				return p, sig, TermUnknown(p.Fit, t, sig.Terms)
			}
		}
	}
	switch p.Scale {
	case "", ScaleResponse, ScaleLink:
	default:
		return p, sig, scoreInvalid(p.Fit, ReasonScaleUnknown,
			fmt.Sprintf("params.scale %q is not one of response, link", p.Scale),
			map[string]any{"scale": p.Scale, "valid": []string{ScaleResponse, ScaleLink}})
	}
	if p.Scale != "" && sig.Type != types.REG_GLM {
		return p, sig, scoreInvalid(p.Fit, ReasonScaleNotGLM,
			fmt.Sprintf("params.scale applies to a generalized-linear fit only; %q is %s, whose scores are on the target's scale", p.Fit, sig.Type),
			map[string]any{"scale": p.Scale, "type": string(sig.Type)})
	}
	switch p.Bound {
	case "", BoundPoint:
	case BoundLower, BoundUpper:
		if len(p.Terms) != 1 || p.Terms[0] == InterceptKey {
			terms := p.Terms
			if terms == nil {
				terms = []string{}
			}
			return p, sig, scoreInvalid(p.Fit, ReasonBoundTerms,
				fmt.Sprintf("params.bound %q needs params.terms to list exactly one predictor — a coefficient bound is not a prediction interval", p.Bound),
				map[string]any{"bound": p.Bound, "terms": terms})
		}
	default:
		return p, sig, scoreInvalid(p.Fit, ReasonBoundUnknown,
			fmt.Sprintf("params.bound %q is not one of point, lower, upper", p.Bound),
			map[string]any{"bound": p.Bound, "valid": []string{BoundPoint, BoundLower, BoundUpper}})
	}
	return p, sig, nil
}

// DecodeScoreParams decodes ATTR_REG_PREDICT's params; anything but a
// JSON object of the ScoreParams keys (with their types) is
// PULSE_MODEL_SCORE_INVALID reason params_invalid. Absent params decode
// to the zero ScoreParams (and then refuse as fit_missing).
func DecodeScoreParams(raw json.RawMessage) (ScoreParams, *errors.CodedError) {
	var p ScoreParams
	paramread.Declare(paramread.Attribute, string(types.ATTR_REG_PREDICT), &p)
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return p, nil
	}
	if err := paramread.DecodeStrict(paramread.Attribute, string(types.ATTR_REG_PREDICT), raw, &p); err != nil {
		return ScoreParams{}, scoreInvalid("", ReasonParamsInvalid,
			"params must be an object {fit, terms?, scale?, bound?}: "+err.Error(), nil)
	}
	return p, nil
}

func scoreInvalid(fit, reason, what string, extra map[string]any) *errors.CodedError {
	return scoreInvalidFor(types.ATTR_REG_PREDICT, fit, reason, what, extra)
}

// scoreInvalidFor is scoreInvalid for the scoring slot of type attr
// (ATTR_REG_PREDICT, or an ATTR_REG_FITTED / ATTR_REG_RESIDUAL slot's
// params.fit).
func scoreInvalidFor(attr types.AttributeType, fit, reason, what string, extra map[string]any) *errors.CodedError {
	details := map[string]any{"attribute": string(attr), "reason": reason}
	if fit != "" {
		details["fit"] = fit
	}
	for k, v := range extra {
		details[k] = v
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_MODEL_SCORE_INVALID,
		string(attr)+" "+what, details)
}

// TermUnknown is the PULSE_MODEL_TERM_UNKNOWN refusal: term is neither
// one of fit's terms nor InterceptKey. available lists the terms a
// reference may name, intercept first.
func TermUnknown(fit, term string, terms []string) *errors.CodedError {
	available := append([]string{InterceptKey}, terms...)
	return errors.NewCodedErrorWithDetails(errors.PULSE_MODEL_TERM_UNKNOWN,
		fmt.Sprintf("model %q has no term %q", fit, term),
		map[string]any{"fit": fit, "term": term, "available": available})
}

// PredictorMissing is the PULSE_MODEL_PREDICTOR_MISSING refusal: the
// scoring frame (schema, feature outputs and earlier attribute labels)
// has no column for one of fit's predictors.
func PredictorMissing(fit, column string) *errors.CodedError {
	return PredictorMissingFor(types.ATTR_REG_PREDICT, fit, column)
}

// PredictorMissingFor is PredictorMissing for the scoring slot of type
// attr.
func PredictorMissingFor(attr types.AttributeType, fit, column string) *errors.CodedError {
	return errors.NewCodedErrorWithDetails(errors.PULSE_MODEL_PREDICTOR_MISSING,
		fmt.Sprintf("%s: predictor %q of model %q is not a column of the scoring frame", attr, column, fit),
		map[string]any{"attribute": string(attr), "fit": fit, "field": column})
}

// PredictorNotNumeric is the PULSE_MODEL_PREDICTOR_NOT_NUMERIC refusal:
// a predictor column of fit is a schema field of a type a regression
// cannot read (fieldType is its type name).
func PredictorNotNumeric(fit, column, fieldType string) *errors.CodedError {
	return PredictorNotNumericFor(types.ATTR_REG_PREDICT, fit, column, fieldType)
}

// PredictorNotNumericFor is PredictorNotNumeric for the scoring slot of
// type attr.
func PredictorNotNumericFor(attr types.AttributeType, fit, column, fieldType string) *errors.CodedError {
	return errors.NewCodedErrorWithDetails(errors.PULSE_MODEL_PREDICTOR_NOT_NUMERIC,
		fmt.Sprintf("%s: predictor %q of model %q is %s, not a numeric column", attr, column, fit, fieldType),
		map[string]any{"attribute": string(attr), "fit": fit, "field": column, "field_type": fieldType})
}

// DefaultScoreLabel is ATTR_REG_PREDICT's output label when the slot
// sets none: "ATTR_REG_PREDICT_<fit>" (params unreadable: no suffix).
func DefaultScoreLabel(attr *types.Attribute) string {
	p, _ := DecodeScoreParams(attr.Params)
	return string(attr.Type) + "_" + p.Fit
}
