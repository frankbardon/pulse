package processing

import (
	"fmt"
	"math"
	"strconv"

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
//
// Prebuilt state (params.fit). The fit is a fittedState — the listwise
// co-moment plus its weight basis — and every model decomposes ONLY
// that state, never the rows. params.fit names a bound vector fit
// (model.VectorFit in the request's model.Set) instead of folding one:
// bindFitOn resolves it (PULSE_MODEL_UNKNOWN), aligns its labels to the
// slot's by label, never by position (PULSE_VECTOR_REFERENCE_MISMATCH
// reason labels_differ), and rebuilds the co-moment in the slot's member
// order (linalg.NewCoMomentFromListwise — a permutation of stored
// figures, no arithmetic), so PrePass folds nothing and Finalize hands
// the models the prebuilt state; the fit's own basis governs N* and the
// covariance, and the slot's weight never refits it. A state captured
// from a fold (AttributeVectorFit) and bound back scores every row to
// the bit of the fold that produced it.
//
// Row-local when bound (U30 E2-S5). bindFitOn CLOSES a bound fit at
// once (close: decompose, warnings, the capture offer), so the slot
// needs no pass over the rows: every routing site reads
// vectors.FitBound — attrRequiresTwoPassIn, predict's attrTwoPass, the
// fused-crosstab facts, mergegate (the three FittedAttributeTypes; a
// bound ATTR_VEC_DISTANCE stays off it like its row-local metrics) —
// and the two-pass planner treats it as row-local (prebuiltAttribute).
// It therefore streams one pass, beside groupers, features, matrices
// and tier-1 tests, fuses into a crosstab and merges across shards /
// decode workers / chain stages; the scores are the bits of the
// two-pass drive (same state, same per-row kernel). PrePass and
// Finalize stay no-ops, so the buffered arm's Compute is unchanged.

// prebuiltAttribute is implemented by a fitted attribute that may be
// bound to a prebuilt fit: prebuiltFit is true once bindFitOn bound
// and closed one, and the two-pass drive then schedules the slot as
// row-local (its PrePass / Finalize are no-ops) — the computer-side
// twin of the routing predicate vectors.FitBound.
type prebuiltAttribute interface {
	prebuiltFit() bool
}

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

// attributeStrictRefuser is implemented by an attribute whose
// data-dependent warning becomes the run's error under Strict
// (ATTR_PREVIOUS_STATE's ambiguous tie): strictRefusal, read after
// Compute, is nil when there is nothing to refuse.
type attributeStrictRefuser interface {
	strictRefusal() *errors.CodedError
}

// AttributeWarnings returns computer's Response-level warnings (nil for
// an attribute that raises none).
func AttributeWarnings(computer any) []*types.ResponseWarning {
	if w, ok := computer.(attributeWarner); ok {
		return w.attributeWarnings()
	}
	return nil
}

// fittedState is a finalized fit, handed to the operator's model: the
// one boundary every fittedModel reads — a fold's co-moment or a bound
// vector fit's rebuilt one (fittedStateFromVector), never rows.
type fittedState struct {
	co    *linalg.CoMoment
	basis weighting.Basis
	// p is the member count; nStar the fit's N*.
	p     int
	nStar float64
}

// newFittedState wraps a listwise co-moment under basis.
func newFittedState(co *linalg.CoMoment, basis weighting.Basis) fittedState {
	return fittedState{co: co, basis: basis, p: co.P(), nStar: levelNStar(basis, co)}
}

// vectorBasisOf maps a weighting basis to its model.VectorFit name.
func vectorBasisOf(b weighting.Basis) model.VectorBasis {
	switch b {
	case weighting.Frequency:
		return model.VectorBasisFrequency
	case weighting.Probability:
		return model.VectorBasisProbability
	}
	return model.VectorBasisUnweighted
}

// weightingBasisOf is vectorBasisOf's inverse; ok is false for an
// unknown name.
func weightingBasisOf(b model.VectorBasis) (weighting.Basis, bool) {
	switch b {
	case model.VectorBasisUnweighted:
		return weighting.Unweighted, true
	case model.VectorBasisFrequency:
		return weighting.Frequency, true
	case model.VectorBasisProbability:
		return weighting.Probability, true
	}
	return 0, false
}

// vectorFit captures st as a model.VectorFit named name over labels /
// members (axis order) — the stored figures exactly, so binding it back
// (fittedStateFromVector) rebuilds st to the bit.
func (st fittedState) vectorFit(name string, labels, members []string) (*model.VectorFit, error) {
	ls, err := st.co.ListwiseState()
	if err != nil {
		return nil, err
	}
	return &model.VectorFit{
		Name: name, Labels: append([]string(nil), labels...), Members: append([]string(nil), members...),
		Basis: vectorBasisOf(st.basis),
		N:     ls.N, NMass: ls.NMass, NWeightInvalid: ls.NWeightInvalid,
		SumWeights: ls.W, SumWeightsSq: ls.W2,
		Mean: ls.Mean, M2: ls.M2.ToRows(),
	}, nil
}

// fittedStateFromVector rebuilds vf's fit in the slot's member order:
// perm[i] is the index, in vf, of the slot's member i (a permutation —
// labelPermutation's). Every figure is copied, none recomputed.
func fittedStateFromVector(vf *model.VectorFit, perm []int) (fittedState, error) {
	p := len(perm)
	basis, ok := weightingBasisOf(vf.Basis)
	if !ok || len(vf.Mean) != p || len(vf.M2) != p {
		return fittedState{}, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
			fmt.Sprintf("vector fit %q is malformed (basis %q, %d means and %d co-moment rows for %d members)", vf.Name, vf.Basis, len(vf.Mean), len(vf.M2), p),
			map[string]any{"fit": vf.Name})
	}
	mean := make([]float64, p)
	rows := make([][]float64, p)
	for i, oi := range perm {
		mean[i] = vf.Mean[oi]
		rows[i] = make([]float64, p)
		for j, oj := range perm {
			if len(vf.M2[oi]) != p {
				return fittedState{}, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
					fmt.Sprintf("vector fit %q co-moment row %d has %d entries for %d members", vf.Name, oi, len(vf.M2[oi]), p),
					map[string]any{"fit": vf.Name})
			}
			// The stored matrix is symmetric; read the upper triangle so
			// the rebuilt packed entry is the captured one.
			a, b := oi, oj
			if a > b {
				a, b = b, a
			}
			rows[i][j] = vf.M2[a][b]
		}
	}
	m2, err := linalg.NewSymFromRows(rows)
	if err != nil {
		return fittedState{}, err
	}
	co, err := linalg.NewCoMomentFromListwise(linalg.ListwiseState{
		N: vf.N, NMass: vf.NMass, NWeightInvalid: vf.NWeightInvalid,
		W: vf.SumWeights, W2: vf.SumWeightsSq, Mean: mean, M2: m2,
	})
	if err != nil {
		return fittedState{}, err
	}
	return newFittedState(co, basis), nil
}

// labelPermutation aligns a bound vector fit's labels to the slot's:
// perm[i] is the index, in vf.Labels, of labels[i]. The two label sets
// must be equal — the reference Match rule — else
// PULSE_VECTOR_REFERENCE_MISMATCH reason labels_differ naming the fit.
func labelPermutation(slot string, vf *model.VectorFit, labels []string) ([]int, error) {
	return labelPermutationAt("attribute", slot, vf, labels)
}

// labelPermutationAt is labelPermutation for a slot of kind key
// ("attribute", "matrix"), named under details[key].
// The rule is vectors.AlignFit, shared with the field-reference walk
// (predict judges a loaded population's labels the same way).
func labelPermutationAt(key, slot string, vf *model.VectorFit, labels []string) ([]int, error) {
	perm, ce := vectors.AlignFit(key, slot, vf.Name, vf.Labels, labels)
	if ce != nil {
		return nil, ce
	}
	return perm, nil
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

// rotationCapNote is a fitted score attribute's caveat for a rotation
// (method) stopped at its iteration cap — nil when it converged. The
// details name the solver (rotationResult.Solver: varimax /
// gpa_<criterion>) with its run and caps, as MAT_PCA's and MAT_FACTOR's
// PULSE_MATRIX_NOT_CONVERGED do.
func rotationCapNote(method string, rot rotationResult) *fitRefusal {
	if rot.Converged {
		return nil
	}
	return &fitRefusal{code: errors.PULSE_MATRIX_NOT_CONVERGED,
		msg: "the " + method + " rotation (" + rot.Solver + ") stopped at its " + strconv.Itoa(rot.MaxIterations) +
			"-iteration cap before its criterion settled; the scores use the last iterate",
		details: map[string]any{"solver": rot.Solver, "iterations": rot.Iterations, "max_iterations": rot.MaxIterations, "tolerance": rot.Tolerance}}
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
	fill    memberFill // reads members (the shared member fill)
	schema  *encoding.Schema

	fitOnSpec  []*types.Filterer
	fitOn      []FilterFunc
	fitOnBound bool

	// fitName is params.fit ("" folds this request's fit); labels are
	// the slot's member labels it aligns to; prebuilt is the bound
	// fit's state, set by bindFitOn.
	fitName  string
	labels   []string
	prebuilt *fittedState
	// fitFailed: params.fit names a Compose vector fit that failed on
	// its data (model.Set.FailedOfKind) — no state to bind, so the slot
	// closes at bind unusable (every score null) with one
	// PULSE_FIT_UNAVAILABLE warning; row-local like a bound fit.
	fitFailed bool

	weight *types.WeightSpec
	basis  weighting.Basis

	co    *linalg.CoMoment
	x     []float64
	model fittedModel

	finalized bool
	usable    bool
	warnings  []*types.ResponseWarning

	// final is the closed fit (Finalize), captured by
	// AttributeVectorFit.
	final *fittedState

	// capture receives the closed fit when this attribute is a Compose
	// vector fit request's battery (the bound set's model.VectorCapture,
	// set by bindFitOn; nil otherwise).
	capture *model.VectorCapture
}

// fittedSource is a fitted attribute's fit source: the decoded
// params.fit_on (folded over this request's rows), or params.fit (a
// bound vector fit) aligned by labels (nil: the members).
type fittedSource struct {
	fitOn  []*types.Filterer
	fit    string
	labels []string
}

var (
	_ TwoPassAttribute = (*fittedVectorAttribute)(nil)
	_ NullEmitter      = (*fittedVectorAttribute)(nil)
	_ fitOnBinder      = (*fittedVectorAttribute)(nil)
	_ attributeWarner  = (*fittedVectorAttribute)(nil)
)

// newFittedVectorAttribute builds the shared base over attr's members
// (the resolved literal list Processor.prepare stashed) with src the
// decoded fit source and m the operator's model.
func newFittedVectorAttribute(attr *types.Attribute, schema *encoding.Schema, src fittedSource, m fittedModel) (*fittedVectorAttribute, error) {
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
	labels := src.labels
	if labels == nil {
		labels = attr.Fields
	}
	if len(labels) != len(attr.Fields) {
		return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("%s params.labels must name each of the %d members once", label, len(attr.Fields)),
			map[string]any{"attribute": label, "reason": "bad_params", "param": "labels", "labels": len(labels), "members": len(attr.Fields)})
	}
	return &fittedVectorAttribute{
		typ:       attr.Type,
		label:     label,
		members:   append([]string(nil), attr.Fields...),
		fill:      memberFillOf(attr.Fields, schema),
		schema:    schema,
		fitOnSpec: inheritExprZone(src.fitOn, attr.TimeZone),
		fitName:   src.fit,
		labels:    append([]string(nil), labels...),
		weight:    spec,
		basis:     weighting.BasisOf(spec),
		co:        co,
		x:         make([]float64, len(attr.Fields)),
		model:     m,
	}, nil
}

// bindFitOn compiles params.fit_on, or resolves and aligns params.fit
// against the request's bound models (fitOnBinder).
func (a *fittedVectorAttribute) bindFitOn(exts *ExtensionRegistry, models *model.Set, vecs []vectors.Resolved) error {
	if model.IsVectorFitAttribute(a.typ) {
		a.capture = models.Capture()
	}
	if a.fitName != "" {
		if se, failed := models.FailedOfKind(a.fitName, types.FitKindVector); failed {
			a.fitFailed, a.finalized = true, true
			a.warnings = append(a.warnings, model.FitUnavailable(a.fitName, a.label, se))
			return nil
		}
		vf, err := models.LookupVector(a.fitName)
		if err != nil {
			if ce, ok := err.(*errors.CodedError); ok {
				ce.Details["attribute"] = a.label
				ce.Message = a.label + ": " + ce.Message
			}
			return err
		}
		perm, err := labelPermutation(a.label, vf, a.labels)
		if err != nil {
			return err
		}
		st, err := fittedStateFromVector(vf, perm)
		if err != nil {
			return err
		}
		a.prebuilt = &st
		// The bound state is the whole fit: close it now, before any
		// row, so the slot is ROW-LOCAL (vectors.FitBound) — Row works
		// on every arm without a PrePass, and the fit's warnings are
		// raised whether or not a row is scored.
		return a.close()
	}
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
	return a.fill.complete(rec, x)
}

// PrePass folds one filtered record into the fit when every member is
// present, every fit_on filterer admits it and its weight is valid and
// positive.
func (a *fittedVectorAttribute) PrePass(rec *Record, _ string) error {
	if a.fitName != "" {
		// A bound fit is already fitted (and closed at bind), a failed
		// one has nothing to fit: nothing to fold.
		if a.prebuilt == nil && !a.fitFailed {
			return errors.NewCodedError(errors.PROCESSING_INTERNAL,
				string(a.typ)+" params.fit was not bound before PrePass")
		}
		return nil
	}
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
// PULSE_MATRIX_INSUFFICIENT_N; otherwise the model fits. A bound fit
// (params.fit) closed at bind, so Finalize is then a no-op.
func (a *fittedVectorAttribute) Finalize() error {
	if (a.prebuilt != nil || a.fitFailed) && a.finalized {
		return nil
	}
	if a.finalized {
		return errors.NewCodedError(errors.PROCESSING_INTERNAL,
			string(a.typ)+" Finalize called twice")
	}
	return a.close()
}

// prebuiltFit reports whether the slot is bound to a prebuilt fit
// (params.fit) — closed at bind, so row-local (prebuiltAttribute); a
// failed fit's slot closed at bind too (unusable).
func (a *fittedVectorAttribute) prebuiltFit() bool { return a.prebuilt != nil || a.fitFailed }

// close is Finalize's body: decompose the fit (folded, or the bound
// prebuilt state) and decide usability, once.
func (a *fittedVectorAttribute) close() error {
	a.finalized = true
	p := len(a.members)
	var st fittedState
	switch {
	case a.prebuilt != nil:
		st = *a.prebuilt
	case a.fitName != "":
		return errors.NewCodedError(errors.PROCESSING_INTERNAL,
			string(a.typ)+" params.fit was not bound before Finalize")
	default:
		st = newFittedState(a.co, a.basis)
	}
	a.final = &st
	if a.capture != nil {
		// A Compose vector fit request's battery: hand the closed state
		// to the fit pre-pass (bound under the fit's name there).
		vf, err := st.vectorFit("", a.labels, a.members)
		if err != nil {
			return err
		}
		vf.Operator = string(a.typ)
		a.capture.Offer(vf)
	}
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
	if st.basis.Weighted() {
		details["sum_weights"] = st.co.W()
	}
	if a.fitName != "" {
		details["fit"] = a.fitName
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

// VectorFitResult is vf's ComposedResponse.Fits summary: its stored
// counts, the mean and the fit's own covariance M2*/(N* − 1) on its
// basis — the figures fittedState derives for every consumer, by the
// same arithmetic (no figure is computed a second way).
func VectorFitResult(vf *model.VectorFit) (*types.VectorFitResult, error) {
	perm := make([]int, vf.P())
	for i := range perm {
		perm[i] = i
	}
	st, err := fittedStateFromVector(vf, perm)
	if err != nil {
		return nil, err
	}
	labels := append([]string(nil), vf.Labels...)
	return &types.VectorFitResult{
		Operator:       vf.Operator,
		Labels:         labels,
		Members:        append([]string(nil), vf.Members...),
		Basis:          string(vf.Basis),
		N:              vf.N,
		NWeightInvalid: vf.NWeightInvalid,
		SumWeights:     vf.SumWeights,
		NStar:          st.nStar,
		Mean:           st.mean(),
		Covariance: &types.MatrixValues{
			Kind:       types.MatrixKindSquareSymmetric,
			Encoding:   types.MatrixEncodingFull,
			RowKeys:    labels,
			ColumnKeys: append([]string(nil), labels...),
			Values:     st.cov(),
		},
	}, nil
}

// AttributeVectorFit returns computer's closed fit as a model.VectorFit
// named name — the state a fitted vector attribute folded (or bound),
// captured after Finalize, in its member order and labels; false for
// any other computer or before Finalize. Binding it back through
// params.fit scores every row to the bit of the fold it came from.
func AttributeVectorFit(computer AttributeComputer, name string) (*model.VectorFit, bool) {
	var a *fittedVectorAttribute
	switch c := computer.(type) {
	case *fittedVectorAttribute:
		a = c
	case *fittedComparisonAttribute:
		a = c.fittedVectorAttribute
	}
	if a == nil || a.final == nil {
		return nil, false
	}
	vf, err := a.final.vectorFit(name, a.labels, a.members)
	if err != nil {
		return nil, false
	}
	return vf, true
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
	return ResolveAttributeInputs(req, p.schema, p.exts)
}

// ResolveAttributeInputs is resolveAttributeInputs without a Processor:
// Processor.prepare applies it, and so must every arm that builds
// attributes off a request without one (the parallel decode and shard
// reducers, whose row-local attributes include a fitted vector
// attribute bound to a prebuilt fit). The caller's request is never
// mutated.
func ResolveAttributeInputs(req *types.Request, schema *encoding.Schema, exts *ExtensionRegistry) *types.Request {
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
	inputs, err := vectors.ResolveAttributes(req, schema, func(t types.AttributeType) types.AttributeType {
		if exts.isHidden(string(t)) {
			return ""
		}
		return t
	}, func(name string) bool {
		_, ok := exts.LookupAttribute(types.AttributeType(name))
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
		if cp.Type == types.ATTR_SCALE_SCORE && !exts.isHidden(string(cp.Type)) {
			// The literal members lose the vector's labels and kind
			// bounds: hand the factory canonical params (reverse by
			// member name, the declared range explicit).
			if params, ok := vectors.ScaleScoreSlotParams(a, inputs[i].Members); ok {
				cp.Params = params
			}
		}
		if vectors.IsFittedAttribute(cp.Type) && !exts.isHidden(string(cp.Type)) {
			// A bound fit aligns to the named vector's labels, which the
			// literal-member rewrite would lose: write them out.
			if params, ok := vectors.FittedSlotParams(a, inputs[i].Members); ok {
				cp.Params = params
			}
		}
		if vectors.IsComparisonAttribute(cp.Type) && !exts.isHidden(string(cp.Type)) {
			// The literal members lose the vector's labels and kind:
			// hand the factory canonical params (the metric written out,
			// the reference in member order, the labels it aligns to).
			if params, ok := vectors.ComparisonSlotParams(a, inputs[i].Members, schema); ok {
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
	return ResolveAggregationInputs(p.resolveGroupInputs(p.resolveAttributeInputs(p.stampWeights(req))), p.schema, p.exts)
}

// ResolveAggregationInputs returns req with every vector aggregator
// slot — aggregations, the crosstab cell and its margin aggregations —
// rewritten for its factory by vectors.AggregationSlotRewrite: Fields =
// the resolved literal members, Vector cleared, Label = the default
// label when empty, params.labels = the resolved display labels (the
// literal-member rewrite would otherwise drop a named vector's labels).
// Processor.prepare applies it, and so must every arm that builds
// aggregators off a request without a Processor (the parallel decode
// and shard reducers). req itself is returned when no slot reads a
// vector or resolution fails (the field-reference pass has refused
// that request; the factory refuses an unresolved Vector). The
// caller's request is never mutated.
func ResolveAggregationInputs(req *types.Request, schema *encoding.Schema, exts *ExtensionRegistry) *types.Request {
	if req == nil {
		return req
	}
	if !vectors.AnyAggregationVectorInput(req) {
		return req
	}
	inputs, err := vectors.ResolveAggregations(req, schema, exts.AggregationKinds())
	if err != nil || len(inputs) == 0 {
		return req
	}
	rewrite := func(a *types.Aggregation) *types.Aggregation {
		if in, ok := inputs[a]; ok && in.Has() {
			return vectors.AggregationSlotRewrite(a, in)
		}
		return a
	}
	c := *req
	if len(req.Aggregations) > 0 {
		c.Aggregations = make([]*types.Aggregation, len(req.Aggregations))
		for i, a := range req.Aggregations {
			c.Aggregations[i] = rewrite(a)
		}
	}
	if ct := req.Crosstab; ct != nil {
		cc := *ct
		if ct.Cell != nil {
			cc.Cell = rewrite(ct.Cell)
		}
		if ct.Cells != nil {
			cc.Cells = make([]*types.Aggregation, len(ct.Cells))
			for i, a := range ct.Cells {
				cc.Cells[i] = a
				if a != nil {
					cc.Cells[i] = rewrite(a)
				}
			}
		}
		if len(ct.MarginAggregations) > 0 {
			cc.MarginAggregations = make([]*types.Aggregation, len(ct.MarginAggregations))
			for i, a := range ct.MarginAggregations {
				cc.MarginAggregations[i] = a
				if a != nil {
					cc.MarginAggregations[i] = rewrite(a)
				}
			}
		}
		c.Crosstab = &cc
	}
	return &c
}
