package processing

import (
	"fmt"
	"math"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
)

// Fitted vector attributes — the two-pass co-moment infrastructure the
// vector-input attributes (vectors.VectorAttributeTypes: ATTR_MAHALANOBIS,
// ATTR_PC_SCORE, and the factor scores built on it) share.
//
// Shape (the regressionAttribute template, folding a co-moment instead
// of buffering rows):
//
//   - The members are the slot's Vector / Fields, resolved by
//     vectors.ResolveAttributes and stashed on the spec copy the factory
//     receives (Processor.prepare: Fields = the literal member list,
//     Vector cleared, Label set to the default when empty).
//   - PrePass folds each FILTERED row whose members are all present
//     (listwise) and which every params.fit_on filterer admits into one
//     weighted linalg.CoMoment. fit_on compiles through the fitOnBinder
//     seam (BindAttribute) with the request's filter build
//     (buildFilterFuncsWith), so it reads the record the attribute sees —
//     earlier attributes' labels included.
//   - Finalize hands the fit (fittedState) to the operator's fittedModel:
//     N* ≤ p nulls every score with PULSE_MATRIX_INSUFFICIENT_N; the model
//     decides singularity (PULSE_MATRIX_SINGULAR) and decomposes; any
//     fit, refused or usable, may still owe warnings (fitNoter).
//   - Row scores EVERY filtered row — fit_on restricts only the fit — and
//     a row with a null member scores NaN, written as a null
//     (NullEmitter).
//
// Weights follow the one rule (weighting.Basis, as TEST_HOTELLING_T2):
// N* = Σw (unweighted / frequency) or Kish n_eff (probability), M2* =
// c·M2 with c = N*/Σw, and the covariance is M2*/(N* − 1). A row whose
// weight is invalid or zero adds nothing to the fit but is still scored.
// The fit is cohort-wide over the filtered rows; a grouped request runs
// buffered (canStream) and never refits per group.
//
// Warnings ride the attributeWarner hook: every orchestrator arm
// appends them to Response.Warnings after the attribute finalizes.

// fitOnBinder is implemented by an attribute whose params.fit_on
// filterers compile against the request's registry and bound models
// before any record is read.
type fitOnBinder interface {
	bindFitOn(exts *ExtensionRegistry, models *model.Set, vecs []vectors.Resolved) error
}

// attributeWarner is implemented by an attribute whose Finalize can
// produce Response-level warnings (a fitted vector attribute's singular
// or insufficient fit).
type attributeWarner interface {
	attributeWarnings() []*types.ResponseWarning
}

// AttributeWarnings returns computer's Response-level warnings (nil for
// an attribute that raises none).
func AttributeWarnings(computer any) []*types.ResponseWarning {
	if w, ok := computer.(attributeWarner); ok {
		return w.attributeWarnings()
	}
	return nil
}

// fittedState is a finalized fit, handed to the operator's model.
type fittedState struct {
	co    *linalg.CoMoment
	basis weighting.Basis
	// p is the member count; nStar the fit's N*.
	p     int
	nStar float64
}

// mean is the (weighted) fit mean, axis order.
func (s fittedState) mean() []float64 { return s.co.Mean().Slice() }

// cov is the ddof = 1 covariance on w*: M2*/(N* − 1).
func (s fittedState) cov() [][]float64 {
	m := levelScaledM2(s.basis, s.co)
	den := s.nStar - 1
	for i := range m {
		for j := range m[i] {
			m[i][j] = m[i][j] / den
		}
	}
	return m
}

// fitRefusal is a model's reason to null every score (or, from
// fitNoter, a fit's caveat): the warning code, the sentence after
// the slot label and any code-specific details (added beside the fit's
// own figures, never replacing them).
type fitRefusal struct {
	code    errors.Code
	msg     string
	details map[string]any
}

// fittedModel is the operator half of a fitted vector attribute.
type fittedModel interface {
	// fit decomposes st; a non-nil refusal nulls every score with its
	// warning.
	fit(st fittedState) *fitRefusal
	// score returns the row's score for its complete member values x.
	score(x []float64) float64
}

// fitNoter is implemented by a fittedModel whose fit can owe
// Response-level caveats (ATTR_PC_SCORE's rotation stopped at its sweep
// cap, ATTR_FACTOR_SCORE's extraction at its iteration cap): read after
// every fit, each becomes a fit-scoped warning — before the refusal's
// own warning when the fit then refuses, beside the scores otherwise.
type fitNoter interface {
	fitNotes() []*fitRefusal
}

// fittedVectorAttribute is the shared TwoPassAttribute of every fitted
// vector attribute (see the section comment).
type fittedVectorAttribute struct {
	typ     types.AttributeType
	label   string
	members []string
	schema  *encoding.Schema

	fitOnSpec  []*types.Filterer
	fitOn      []FilterFunc
	fitOnBound bool

	weight *types.WeightSpec
	basis  weighting.Basis

	co    *linalg.CoMoment
	x     []float64
	model fittedModel

	finalized bool
	usable    bool
	warnings  []*types.ResponseWarning
}

var (
	_ TwoPassAttribute = (*fittedVectorAttribute)(nil)
	_ NullEmitter      = (*fittedVectorAttribute)(nil)
	_ fitOnBinder      = (*fittedVectorAttribute)(nil)
	_ attributeWarner  = (*fittedVectorAttribute)(nil)
)

// newFittedVectorAttribute builds the shared base over attr's members
// (the resolved literal list Processor.prepare stashed) with fitOn the
// decoded params.fit_on and m the operator's model.
func newFittedVectorAttribute(attr *types.Attribute, schema *encoding.Schema, fitOn []*types.Filterer, m fittedModel) (*fittedVectorAttribute, error) {
	if attr.Vector != "" {
		return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
			fmt.Sprintf("%s vector %q was not resolved before construction", attr.Type, attr.Vector),
			map[string]any{"attribute": string(attr.Type), "vector": attr.Vector})
	}
	if len(attr.Fields) == 0 {
		return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
			fmt.Sprintf("%s requires vector or fields (its members)", attr.Type),
			map[string]any{"attribute": string(attr.Type)})
	}
	co, err := linalg.NewCoMoment(len(attr.Fields), linalg.Listwise)
	if err != nil {
		return nil, err
	}
	label := attr.Label
	if label == "" {
		label = defaultAttributeLabel(attr)
	}
	spec := attr.Weight.Spec()
	return &fittedVectorAttribute{
		typ:       attr.Type,
		label:     label,
		members:   append([]string(nil), attr.Fields...),
		schema:    schema,
		fitOnSpec: fitOn,
		weight:    spec,
		basis:     weighting.BasisOf(spec),
		co:        co,
		x:         make([]float64, len(attr.Fields)),
		model:     m,
	}, nil
}

// bindFitOn compiles params.fit_on (fitOnBinder).
func (a *fittedVectorAttribute) bindFitOn(exts *ExtensionRegistry, models *model.Set, vecs []vectors.Resolved) error {
	fns, err := buildFilterFuncsWith(a.fitOnSpec, a.schema, exts, models, vecs)
	if err != nil {
		return err
	}
	a.fitOn, a.fitOnBound = fns, true
	return nil
}

// EmitsNull satisfies NullEmitter: a NaN score is a null.
func (a *fittedVectorAttribute) EmitsNull() bool { return true }

// attributeWarnings satisfies attributeWarner.
func (a *fittedVectorAttribute) attributeWarnings() []*types.ResponseWarning {
	return a.warnings
}

// readMembers fills x with rec's member values; false when any is null
// or NaN (listwise).
func (a *fittedVectorAttribute) readMembers(rec *Record, x []float64) bool {
	for i, f := range a.members {
		v, ok := rec.NumericValue(f)
		if !ok || math.IsNaN(v) {
			return false
		}
		x[i] = v
	}
	return true
}

// PrePass folds one filtered record into the fit when every member is
// present, every fit_on filterer admits it and its weight is valid and
// positive.
func (a *fittedVectorAttribute) PrePass(rec *Record, _ string) error {
	if a.finalized {
		return errors.NewCodedError(errors.PROCESSING_INTERNAL,
			string(a.typ)+" PrePass after Finalize")
	}
	if len(a.fitOnSpec) > 0 && !a.fitOnBound {
		return errors.NewCodedError(errors.PROCESSING_INTERNAL,
			string(a.typ)+" params.fit_on was not bound before PrePass")
	}
	if !a.readMembers(rec, a.x) {
		return nil
	}
	for _, fn := range a.fitOn {
		ok, err := fn(rec)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
	}
	w := 1.0
	if a.weight != nil {
		var reason weighting.Reason
		if w, reason = readWeight(rec, a.weight); reason != weighting.Valid || w == 0 {
			return nil
		}
	}
	a.co.Add(a.x, w)
	return nil
}

// Finalize closes the fit: N* ≤ p nulls every score with
// PULSE_MATRIX_INSUFFICIENT_N; otherwise the model fits.
func (a *fittedVectorAttribute) Finalize() error {
	if a.finalized {
		return errors.NewCodedError(errors.PROCESSING_INTERNAL,
			string(a.typ)+" Finalize called twice")
	}
	a.finalized = true
	p := len(a.members)
	st := fittedState{co: a.co, basis: a.basis, p: p, nStar: levelNStar(a.basis, a.co)}
	if !(st.nStar > float64(p)) {
		a.warnings = append(a.warnings, a.fitWarning(errors.PULSE_MATRIX_INSUFFICIENT_N, st,
			fmt.Sprintf("%s: the fit has N* = %.4g complete rows for %d members (needs N* > p); every score is null", a.label, st.nStar, p)))
		return nil
	}
	ref := a.model.fit(st)
	// A fit's notes are owed whether or not it then refuses: a
	// refusal reached after an extraction stopped at its cap still
	// reports the cap, ahead of the refusal (the order they arose).
	if n, ok := a.model.(fitNoter); ok {
		for _, note := range n.fitNotes() {
			a.warnings = append(a.warnings, a.refusalWarning(note, st))
		}
	}
	if ref != nil {
		a.warnings = append(a.warnings, a.refusalWarning(ref, st))
		return nil
	}
	a.usable = true
	return nil
}

// refusalWarning is ref's fit-scoped warning: the fit's figures plus
// ref's own details (a key the fit already sets is kept).
func (a *fittedVectorAttribute) refusalWarning(ref *fitRefusal, st fittedState) *types.ResponseWarning {
	w := a.fitWarning(ref.code, st, a.label+": "+ref.msg)
	for k, v := range ref.details {
		if _, taken := w.Details[k]; !taken {
			w.Details[k] = v
		}
	}
	return w
}

// fitWarning builds a fit-scoped warning carrying the fit's figures.
func (a *fittedVectorAttribute) fitWarning(code errors.Code, st fittedState, msg string) *types.ResponseWarning {
	details := map[string]any{
		"attribute": a.label,
		"type":      string(a.typ),
		"scope":     "fit",
		"members":   append([]string(nil), a.members...),
		"p":         st.p,
		"n":         st.co.N(),
		"n_star":    st.nStar,
	}
	if a.basis.Weighted() {
		details["sum_weights"] = st.co.W()
	}
	return &types.ResponseWarning{Code: string(code), Message: msg, Details: details}
}

// Row scores rec: NaN (null) before a usable fit or when a member is
// null.
func (a *fittedVectorAttribute) Row(rec *Record, _ string) (float64, error) {
	if !a.finalized {
		return 0, errors.NewCodedError(errors.PROCESSING_INTERNAL,
			string(a.typ)+" Row called before Finalize")
	}
	if !a.usable {
		return math.NaN(), nil
	}
	x := make([]float64, len(a.members))
	if !a.readMembers(rec, x) {
		return math.NaN(), nil
	}
	return a.model.score(x), nil
}

// Compute is the buffered drive: PrePass(every) → Finalize → Row(every).
func (a *fittedVectorAttribute) Compute(records []*Record, field string) ([]float64, error) {
	if len(records) == 0 {
		// No rows: the fit is empty, which still owes its warning.
		if err := a.Finalize(); err != nil {
			return nil, err
		}
		return []float64{}, nil
	}
	return computeTwoPass(a, records, field)
}

// attributeSlotLabel is the slot name a factory's refusals carry.
func attributeSlotLabel(attr *types.Attribute) string {
	if attr.Label != "" {
		return attr.Label
	}
	return defaultAttributeLabel(attr)
}

// resolveAttributeInputs returns req with every vector-input attribute
// — a registered extension attribute's included — rewritten for its
// factory (vectors.ResolveAttributes): Fields = the
// resolved literal members, Vector cleared, Label = the default label
// when empty (computed from the slot as written). req itself is
// returned when no attribute reads a vector or resolution fails (the
// field-reference pass has refused that request; the factory refuses an
// unresolved Vector). The caller's request is never mutated.
func (p *Processor) resolveAttributeInputs(req *types.Request) *types.Request {
	if req == nil {
		return req
	}
	has := false
	for _, a := range req.Attributes {
		if vectors.HasAttributeVectorInput(a) {
			has = true
			break
		}
	}
	if !has {
		return req
	}
	inputs, err := vectors.ResolveAttributes(req, p.schema, func(t types.AttributeType) types.AttributeType {
		if p.exts.isHidden(string(t)) {
			return ""
		}
		return t
	}, func(name string) bool {
		_, ok := p.exts.LookupAttribute(types.AttributeType(name))
		return ok
	})
	if err != nil {
		return req
	}
	c := *req
	c.Attributes = make([]*types.Attribute, len(req.Attributes))
	for i, a := range req.Attributes {
		c.Attributes[i] = a
		if a == nil || i >= len(inputs) || !inputs[i].Has() {
			continue
		}
		cp := *a
		if cp.Label == "" {
			cp.Label = defaultAttributeLabel(a)
		}
		if cp.Type == types.ATTR_SCALE_SCORE && !p.exts.isHidden(string(cp.Type)) {
			// The literal members lose the vector's labels and kind
			// bounds: hand the factory canonical params (reverse by
			// member name, the declared range explicit).
			if params, ok := vectors.ScaleScoreSlotParams(a, inputs[i].Members); ok {
				cp.Params = params
			}
		}
		cp.Vector = ""
		cp.Fields = append([]string(nil), inputs[i].Members.Members...)
		c.Attributes[i] = &cp
	}
	return &c
}

// prepare is the per-run request rewrite every engine entry applies:
// the stamped weights (StampWeightsWith), then the resolved attribute
// and grouper vector inputs. It also resolves the run's named vectors
// for the expression operators (Processor.exprVectors).
func (p *Processor) prepare(req *types.Request) *types.Request {
	p.exprVectors = ExprVectors(req, p.schema)
	return p.resolveGroupInputs(p.resolveAttributeInputs(p.stampWeights(req)))
}
