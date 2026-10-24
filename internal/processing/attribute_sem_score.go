package processing

import (
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/sem"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
	"gonum.org/v1/gonum/mat"
)

// ATTR_SEM_SCORE — each row's score on one latent variable of a
// structural equation model fitted inline (vectors.SEMScoreParams):
// lavaan's lavPredict regression or Bartlett scores (sem.NewScorer) of
// the fit MAT_SEM would report on the same rows.
//
// The fit (PrePass, Finalize) folds every FILTERED row whose members
// are all present and which every params.fit_on filterer admits into a
// listwise co-moment — one per level of params.group, keyed by the
// level code (a categorical's dictionary index, else the value), when
// grouped; a null group is a listwise drop. A frequency weight is a
// replication count (MAT_SEM's: N = Σw); a row whose weight is invalid
// or zero adds nothing but is still scored. Finalize fits MAT_SEM's
// model on the moments exactly as MAT_SEM's finalizer does
// (sem.FitMoments on the population covariance Cov(0) in model order,
// the nearest positive-definite substitute under params.singular
// "nearest"; sem.FitGroups on the per-level moments), and builds each
// group's scorer from the fit's parameter matrices (semScoreFit — the
// representation a bound or saved SEM fit carries: model, options,
// levels and per-group sem.Params).
//
// Row scores EVERY filtered row: a null member or a null group scores
// null; a level without fit rows scores null and is tallied into one
// PULSE_SEM_SCORE_LEVEL_UNSEEN warning. A fit that cannot score nulls
// every score with one fit-scoped warning (the matrix codes:
// PULSE_MATRIX_INSUFFICIENT_N, PULSE_MATRIX_SINGULAR,
// PULSE_MATRIX_NOT_IDENTIFIED); a usable fit that owes a caveat scores
// and warns (PULSE_MATRIX_NOT_CONVERGED, PULSE_MATRIX_NEAREST_PD).
//
// The fit is cohort-wide over the filtered rows (two-pass, like the
// fitted vector attributes); a grouped request never refits per bucket.
//
// Bound (params.fit naming a SEM fit — a Compose `fits` entry of kind
// sem, model.SEMFit): the slot fits nothing. bindFitOn aligns the
// fit's member labels to the slot's (by label), rebuilds the fit's
// model from its description and options (semScoreFitFromBound) and
// each group's scorer from its stored matrices by the one arithmetic
// Finalize uses, and closes the slot — ROW-LOCAL (vectors.FitBound,
// prebuiltAttribute): it streams one pass, fuses into a crosstab and
// merges across shards, decode workers and chain stages, its
// unseen-level tally folding by MergeTally. The scores are the inline
// fit's bits on the same fit rows (same parameters, same scorer). A
// row's group is matched to the fit's levels by LABEL (a categorical's
// dictionary entry, else the integer), so a fit taken on one cohort
// scores any cohort holding its members and group field; a failed fit
// nulls every score with PULSE_FIT_UNAVAILABLE.

// semScoreFit is a closed SEM fit in the form a scorer reads: the
// model (one per group), the levels (the group labels, fit order, and
// their codes; a single "" level ungrouped) and each group's parameter
// matrices. A bound or saved fit carries the same figures.
type semScoreFit struct {
	models []*sem.Model
	labels []string
	codes  []uint64
	params []sem.Params
}

// semScoreFitFromBound rebuilds a bound SEM fit's scoring form: the
// model re-specified from its description and options (one model for
// every group — a group's own model differs only in the fixed values
// and labels its matrices already hold) and each group's stored
// matrices. A model that does not reproduce the fit's variable orders
// is PROCESSING_INTERNAL: the matrices would be read misaligned.
func semScoreFitFromBound(sf *model.SEMFit) (*semScoreFit, error) {
	opt := sem.ModelOptions{MeanStructure: sf.MeanStructure, GroupEqual: sf.GroupEqual, GroupPartial: sf.GroupPartial, Lavaan: sf.Lavaan}
	newModel := sem.NewModelWithOptions
	if sf.Grouped() {
		newModel = sem.NewGroupModel
	}
	m, err := newModel(sf.Model, opt)
	if err == nil && (!slices.Equal(m.Observed, sf.Observed) || !slices.Equal(m.Inner, sf.Inner)) {
		err = fmt.Errorf("its model specifies observed %v and inner %v, the fit stores %v and %v", m.Observed, m.Inner, sf.Observed, sf.Inner)
	}
	if err != nil {
		return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
			fmt.Sprintf("bound SEM fit %q cannot be rebuilt: %v", sf.Name, err),
			map[string]any{"fit": sf.Name, "error": err.Error()})
	}
	out := &semScoreFit{}
	for _, g := range sf.Groups {
		out.models = append(out.models, m)
		out.labels = append(out.labels, g.Level)
		out.params = append(out.params, sem.Params{Lambda: g.Lambda, Beta: g.Beta, Psi: g.Psi, Theta: g.Theta,
			Nu: g.Nu, Alpha: g.Alpha, SampleMean: g.SampleMean})
	}
	return out, nil
}

// semScoreAttribute is ATTR_SEM_SCORE's TwoPassAttribute.
type semScoreAttribute struct {
	label   string
	members []string
	fill    memberFill
	schema  *encoding.Schema
	cfg     vectors.SEMScoreConfig

	fitOnSpec  []*types.Filterer
	fitOn      []FilterFunc
	fitOnBound bool

	weight *types.WeightSpec

	// groupField / dict read params.group (dict: a categorical
	// field's, guarding the index).
	groupField string
	dict       *encoding.Field
	// levels are the group field's level labels by code when the
	// schema knows them (a categorical's dictionary, packed_bool's
	// "0" / "1"); nil on an integer field.
	levels []string

	// fitName is params.fit ("" fits inline); labels are the slot's
	// member labels the bound fit aligns to; bound is the fit, set by
	// bindFitOn, and prebuilt reports the slot closed at bind (bound,
	// or its fit failed on its data).
	fitName  string
	labels   []string
	bound    *model.SEMFit
	prebuilt bool

	base    *linalg.CoMoment
	buckets map[uint64]*linalg.CoMoment
	x       []float64

	finalized bool
	fit       *semScoreFit
	// scorers holds each fit level's scorer by level code (the single
	// key 0 ungrouped); nil when the fit cannot score.
	scorers map[uint64]*sem.Scorer
	// order maps model-order position k to member position (the
	// plan's Order); y is the reordered row.
	order []int

	warnings []*types.ResponseWarning
	// unseen tallies the scored rows of each level without fit rows.
	unseen map[uint64]int64
}

var (
	_ TwoPassAttribute        = (*semScoreAttribute)(nil)
	_ NullEmitter             = (*semScoreAttribute)(nil)
	_ fitOnBinder             = (*semScoreAttribute)(nil)
	_ attributeWarner         = (*semScoreAttribute)(nil)
	_ prebuiltAttribute       = (*semScoreAttribute)(nil)
	_ MergeableAttributeTally = (*semScoreAttribute)(nil)
	_ attributeTallyResetter  = (*semScoreAttribute)(nil)
)

// newSEMScoreAttribute is ATTR_SEM_SCORE's registry factory.
func newSEMScoreAttribute(attr *types.Attribute, schema *encoding.Schema) (AttributeComputer, error) {
	if attr == nil {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG, "ATTR_SEM_SCORE requires a non-nil spec")
	}
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
	at := attributeSlotLabel(attr)
	cfg, cerr := vectors.SEMScoreParams(at, attr.Params)
	if cerr != nil {
		return nil, cerr
	}
	members := vectors.Resolved{Members: append([]string(nil), attr.Fields...)}
	labels, cerr := vectors.ResolveSEMScore(at, &cfg, members, schema)
	if cerr != nil {
		return nil, cerr
	}
	base, err := linalg.NewCoMoment(len(attr.Fields), linalg.Listwise)
	if err != nil {
		return nil, err
	}
	a := &semScoreAttribute{
		label:     at,
		members:   append([]string(nil), attr.Fields...),
		fill:      memberFillOf(attr.Fields, schema),
		schema:    schema,
		cfg:       cfg,
		fitOnSpec: inheritExprZone(cfg.FitOn, attr.TimeZone),
		weight:    attr.Weight.Spec(),
		base:      base,
		x:         make([]float64, len(attr.Fields)),
		fitName:   cfg.Fit,
		labels:    labels,
	}
	if cfg.Fit != "" {
		// The bound fit supplies the model, groups and member order
		// (bindFitOn).
		return a, nil
	}
	a.order = cfg.Plan.Order
	if cfg.Plan.Grouped() {
		a.groupField = cfg.Plan.Group
		a.levels = cfg.Plan.GroupLevels
		a.buckets = map[uint64]*linalg.CoMoment{}
		if schema != nil {
			if f := schema.Field(a.groupField); f != nil && f.Type.IsCategorical() {
				a.dict = f
			}
		}
	}
	return a, nil
}

// bindFitOn compiles params.fit_on against the request's registry and
// bound models, or binds and closes params.fit (fitOnBinder).
func (a *semScoreAttribute) bindFitOn(exts *ExtensionRegistry, models *model.Set, vecs []vectors.Resolved, keys *encoding.Schema) error {
	if a.fitName != "" {
		return a.bindFit(models)
	}
	fns, err := buildFilterFuncsWith(a.fitOnSpec, a.schema, exts, models, vecs, keys)
	if err != nil {
		return err
	}
	a.fitOn, a.fitOnBound = fns, true
	return nil
}

// bindFit resolves params.fit in models and closes the slot: a failed
// SEM fit nulls every score with PULSE_FIT_UNAVAILABLE; another kind
// is PULSE_MODEL_KIND_MISMATCH, an unbound name PULSE_MODEL_UNKNOWN
// (details.attribute, the message prefixed with the slot); the fit's
// member labels must be the slot's (PULSE_VECTOR_REFERENCE_MISMATCH
// labels_differ), its group field a group field of the cohort, and
// params.latent a latent of its model (SERVICE_VALIDATION). The fit's
// own caveats and refusals are the inline fit's: too few fit rows or
// negative degrees of freedom null every score with a warning, a
// solver stop short of its tolerance or the nearest positive-definite
// substitute score with one.
func (a *semScoreAttribute) bindFit(models *model.Set) error {
	a.finalized, a.prebuilt = true, true
	if se, failed := models.FailedOfKind(a.fitName, types.FitKindSEM); failed {
		a.warnings = append(a.warnings, model.FitUnavailable(a.fitName, a.label, se))
		return nil
	}
	sf, err := models.LookupSEM(a.fitName)
	if err != nil {
		if ce, ok := err.(*errors.CodedError); ok {
			ce.Details["attribute"] = a.label
			ce.Message = a.label + ": " + ce.Message
		}
		return err
	}
	if _, cerr := vectors.AlignFit("attribute", a.label, a.fitName, sf.Labels, a.labels); cerr != nil {
		return cerr
	}
	pos := make(map[string]int, len(a.labels))
	for i, l := range a.labels {
		pos[l] = i
	}
	a.order = make([]int, len(sf.Observed))
	for k, v := range sf.Observed {
		i, ok := pos[v]
		if !ok {
			return errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
				fmt.Sprintf("%s: bound SEM fit %q observes %q, which is none of its member labels", a.label, a.fitName, v),
				map[string]any{"attribute": a.label, "fit": a.fitName, "observed": v})
		}
		a.order[k] = i
	}
	fit, err := semScoreFitFromBound(sf)
	if err != nil {
		return err
	}
	lats := fit.models[0].Latents()
	switch {
	case a.cfg.Latent == "" && len(lats) == 1:
		a.cfg.Latent = lats[0]
	case a.cfg.Latent == "":
		return attributeFitInvalid(a.label, a.fitName, "bad_params",
			fmt.Sprintf("params.latent is required: bound SEM fit %q's model has %d latent variables (%s)", a.fitName, len(lats), strings.Join(lats, ", ")),
			map[string]any{"param": "latent", "latents": lats})
	case !slices.Contains(lats, a.cfg.Latent):
		return attributeFitInvalid(a.label, a.fitName, "latent_unknown",
			fmt.Sprintf("params.latent %q is not a latent variable of bound SEM fit %q (latents: %s)", a.cfg.Latent, a.fitName, strings.Join(lats, ", ")),
			map[string]any{"param": "latent", "value": a.cfg.Latent, "latents": lats})
	}
	a.bound = sf
	codes, err := a.bindFitGroups(sf)
	if err != nil {
		return err
	}
	fit.codes = codes
	k := len(sf.Observed)
	if !sf.Grouped() && (sf.N <= int64(k) || !(sf.SumWeights > float64(k))) {
		a.warnings = append(a.warnings, a.fitWarning(&fitRefusal{code: errors.PULSE_MATRIX_INSUFFICIENT_N,
			msg:     fmt.Sprintf("the fit has %d complete rows for the model's %d observed variables (needs more rows, and weight, than variables); every score is null", sf.N, k),
			details: map[string]any{"n": sf.N, "sum_weights": sf.SumWeights, "observed": k}}))
		return nil
	}
	if sf.NearestPD {
		a.warnings = append(a.warnings, a.fitWarning(&fitRefusal{code: errors.PULSE_MATRIX_NEAREST_PD,
			msg:     "the fit rows' covariance is not positive definite; the model is fitted to its nearest positive-definite substitute (MAT_SEM's params.singular \"nearest\") and scored from that fit",
			details: map[string]any{"min_eigenvalue": sf.MinEigenvalue}}))
	}
	if note := semConvergenceNote(sf.Converged, sf.Status, sem.Solver(sf.Solver), sf.Iterations); note != nil {
		a.warnings = append(a.warnings, a.fitWarning(note))
	}
	if ref := semIdentifiedRefusal(sem.Stats{DoF: sf.DoF, NParams: sf.NParams}); ref != nil {
		a.warnings = append(a.warnings, a.fitWarning(ref))
		return nil
	}
	a.closeFit(fit)
	return nil
}

// bindFitGroups points the slot at the bound fit's group field on this
// cohort and returns each fit group's level code here (nil ungrouped):
// a categorical's dictionary index of the level's label, packed_bool's
// 0 / 1, an integer's value. A level the cohort cannot hold takes no
// code (no row scores with it). The field must be a group field of the
// cohort (SERVICE_VALIDATION reason fit_group_field).
func (a *semScoreAttribute) bindFitGroups(sf *model.SEMFit) ([]uint64, error) {
	if !sf.Grouped() {
		return []uint64{0}, nil
	}
	var f *encoding.Field
	if a.schema != nil {
		f = a.schema.Field(sf.Group)
	}
	if f == nil || !slices.Contains(vectors.SEMGroupFieldTypes(), f.Type) {
		what := "which the cohort does not have"
		if f != nil {
			what = "which is " + f.Type.String() + " here, not a group field (categorical, packed_bool, u4 or u8)"
		}
		return nil, attributeFitInvalid(a.label, a.fitName, "fit_group_field",
			fmt.Sprintf("bound SEM fit %q is a multi-group fit by field %q, %s", a.fitName, sf.Group, what),
			map[string]any{"group": sf.Group})
	}
	a.groupField = sf.Group
	switch {
	case f.Type.IsCategorical():
		a.dict = f
		if f.Dictionary != nil {
			for i, l := range f.Dictionary.Values() {
				if l == "" {
					l = strconv.Itoa(i)
				}
				a.levels = append(a.levels, l)
			}
		}
	case f.Type == encoding.FieldTypePackedBool:
		a.levels = []string{"0", "1"}
	}
	codes := make([]uint64, len(sf.Groups))
	for g, gr := range sf.Groups {
		codes[g] = math.MaxUint64 // no code: the cohort holds no such level
		if a.levels != nil {
			if i := slices.Index(a.levels, gr.Level); i >= 0 {
				codes[g] = uint64(i)
			}
		} else if v, err := strconv.ParseUint(gr.Level, 10, 64); err == nil {
			codes[g] = v
		}
	}
	return codes, nil
}

// attributeFitInvalid is a bound slot's SERVICE_VALIDATION refusal.
func attributeFitInvalid(label, fit, reason, msg string, extra map[string]any) *errors.CodedError {
	d := map[string]any{"attribute": label, "reason": reason, "fit": fit}
	for k, v := range extra {
		d[k] = v
	}
	return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION, label+": "+msg, d)
}

// prebuiltFit satisfies prebuiltAttribute: a bound (or failed) fit
// closed at bind, so the slot is row-local (vectors.FitBound).
func (a *semScoreAttribute) prebuiltFit() bool { return a.prebuilt }

// MergeTally satisfies MergeableAttributeTally: the bound form's
// unseen-level tally folds across partitions (integers, exact). The
// inline form is two-pass and never reaches the reducers.
func (a *semScoreAttribute) MergeTally(other RowLocalAttribute) error {
	o, ok := other.(*semScoreAttribute)
	if !ok {
		return tallyPartnerMismatch(types.ATTR_SEM_SCORE, other)
	}
	for c, n := range o.unseen {
		if a.unseen == nil {
			a.unseen = map[uint64]int64{}
		}
		a.unseen[c] += n
	}
	return nil
}

// resetTally satisfies attributeTallyResetter: a stage a prepass layer
// scored once already counts its unseen rows again on the emission
// scan.
func (a *semScoreAttribute) resetTally() { a.unseen = nil }

// EmitsNull satisfies NullEmitter: a NaN score is a null.
func (a *semScoreAttribute) EmitsNull() bool { return true }

// attributeWarnings satisfies attributeWarner: the fit's warnings, then
// the unseen-level tally of the rows scored so far.
func (a *semScoreAttribute) attributeWarnings() []*types.ResponseWarning {
	out := append([]*types.ResponseWarning(nil), a.warnings...)
	if len(a.unseen) == 0 {
		return out
	}
	codes := slices.Sorted(maps.Keys(a.unseen))
	levels := make([]string, len(codes))
	rows := make([]int64, len(codes))
	var total int64
	for i, c := range codes {
		levels[i], rows[i] = a.levelLabel(c), a.unseen[c]
		total += a.unseen[c]
	}
	return append(out, &types.ResponseWarning{
		Code: string(errors.PULSE_SEM_SCORE_LEVEL_UNSEEN),
		Message: a.label + ": " + strconv.FormatInt(total, 10) + " row(s) belong to a level of params.group " + strconv.Quote(a.groupField) +
			" the fit has no rows for, so the model has no parameters to score them with; their scores are null",
		Details: map[string]any{"attribute": a.label, "type": string(types.ATTR_SEM_SCORE), "group": a.groupField,
			"levels": levels, "rows": rows, "fit_levels": append([]string(nil), a.fit.labels...)},
	})
}

// levelLabel is a level code's label: the dictionary entry (the
// field's levels), else the integer.
func (a *semScoreAttribute) levelLabel(code uint64) string {
	if lv := a.levels; a.dict != nil && int(code) < len(lv) {
		return lv[code]
	}
	return strconv.FormatUint(code, 10)
}

// groupCode reads rec's group level code; false on a null group.
func (a *semScoreAttribute) groupCode(rec *Record) (uint64, bool, error) {
	v, ok := rec.NumericValue(a.groupField)
	if !ok || math.IsNaN(v) {
		return 0, false, nil
	}
	if a.dict != nil {
		if err := encx.CategoricalIndexFault(a.dict, v); err != nil {
			return 0, false, err
		}
	}
	return uint64(v), true, nil
}

// PrePass folds one filtered record into the fit (complete members,
// every fit_on filterer admitting it, a valid positive weight; grouped,
// a non-null group).
func (a *semScoreAttribute) PrePass(rec *Record, _ string) error {
	if a.prebuilt {
		// A bound fit is already fitted: no row is folded.
		return nil
	}
	if a.finalized {
		return errors.NewCodedError(errors.PROCESSING_INTERNAL, "ATTR_SEM_SCORE PrePass after Finalize")
	}
	if len(a.fitOnSpec) > 0 && !a.fitOnBound {
		return errors.NewCodedError(errors.PROCESSING_INTERNAL, "ATTR_SEM_SCORE params.fit_on was not bound before PrePass")
	}
	if !a.fill.complete(rec, a.x) {
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
	if a.buckets == nil {
		a.base.Add(a.x, w)
		return nil
	}
	code, ok, err := a.groupCode(rec)
	if err != nil || !ok {
		return err
	}
	b, ok := a.buckets[code]
	if !ok {
		if b, err = linalg.NewCoMoment(len(a.members), linalg.Listwise); err != nil {
			return err
		}
		a.buckets[code] = b
	}
	b.Add(a.x, w)
	a.base.Add(a.x, w)
	return nil
}

// Finalize fits the model and builds the scorers, once.
func (a *semScoreAttribute) Finalize() error {
	if a.prebuilt {
		return nil
	}
	if a.finalized {
		return errors.NewCodedError(errors.PROCESSING_INTERNAL, "ATTR_SEM_SCORE Finalize called twice")
	}
	a.finalized = true
	fit, notes, ref := a.fitModel()
	for _, n := range notes {
		a.warnings = append(a.warnings, a.fitWarning(n))
	}
	if ref != nil {
		a.warnings = append(a.warnings, a.fitWarning(ref))
		return nil
	}
	a.closeFit(fit)
	return nil
}

// closeFit builds each fit group's scorer under its level code — the
// one scorer arithmetic an inline and a bound fit share (sem.NewScorer
// over the group's model and matrices). A scorer that cannot be built
// nulls every score with PULSE_MATRIX_SINGULAR; a group without a code
// on this cohort (a bound fit's level the cohort cannot hold) scores
// no row.
func (a *semScoreAttribute) closeFit(fit *semScoreFit) {
	scorers := make(map[uint64]*sem.Scorer, len(fit.codes))
	for g, code := range fit.codes {
		sc, err := sem.NewScorer(fit.models[g], fit.params[g], a.cfg.Latent, a.cfg.Method)
		if err != nil {
			a.warnings = append(a.warnings, a.fitWarning(&fitRefusal{code: errors.PULSE_MATRIX_SINGULAR,
				msg:     "the fitted model's implied covariance cannot be inverted" + levelSuffix(fit.labels[g]) + ", so its " + string(a.cfg.Method) + " scores are undefined; every score is null",
				details: map[string]any{"error": err.Error()}}))
			return
		}
		if code == math.MaxUint64 {
			continue
		}
		scorers[code] = sc
	}
	a.fit, a.scorers = fit, scorers
}

func levelSuffix(label string) string {
	if label == "" {
		return ""
	}
	return " in group " + strconv.Quote(label)
}

// fitModel fits MAT_SEM's model to the folded moments: the closed fit,
// the caveats it owes (scored) and a refusal (every score null).
func (a *semScoreAttribute) fitModel() (*semScoreFit, []*fitRefusal, *fitRefusal) {
	plan := a.cfg.Plan
	if plan.Grouped() {
		return a.fitGroups()
	}
	k := len(plan.Model.Observed)
	cm := a.base
	if cm.N() <= int64(k) || !(cm.W() > float64(k)) {
		return nil, nil, &fitRefusal{code: errors.PULSE_MATRIX_INSUFFICIENT_N,
			msg:     fmt.Sprintf("the fit has %d complete rows for the model's %d observed variables (needs more rows, and weight, than variables); every score is null", cm.N(), k),
			details: map[string]any{"n": cm.N(), "sum_weights": cm.W(), "observed": k}}
	}
	s, mean := a.moments(cm)
	raw := s
	var notes []*fitRefusal
	if !sem.PositiveDefinite(s) {
		if plan.Singular != vectors.SEMSingularNearest {
			return nil, nil, &fitRefusal{code: errors.PULSE_MATRIX_SINGULAR,
				msg: "the fit rows' covariance is not positive definite (a member is constant or a linear combination of others) and params.singular is \"refuse\"; every score is null"}
		}
		adj, err := sem.CovNearest(s, sem.NearestThreshold)
		if err != nil {
			return nil, nil, &fitRefusal{code: errors.PULSE_MATRIX_SINGULAR,
				msg:     "the fit rows' covariance is not positive definite and has no nearest positive-definite substitute (" + err.Error() + "); every score is null",
				details: map[string]any{"error": err.Error()}}
		}
		s = adj.Cov
		notes = append(notes, &fitRefusal{code: errors.PULSE_MATRIX_NEAREST_PD,
			msg:     "the fit rows' covariance is not positive definite; the model is fitted to its nearest positive-definite substitute (MAT_SEM's params.singular \"nearest\") and scored from that fit",
			details: map[string]any{"min_eigenvalue": adj.MinEigenvalue}})
	}
	fit, err := sem.FitMoments(plan.Model, s, raw, mean, cm.W(), plan.Options)
	if err != nil {
		return nil, notes, &fitRefusal{code: errors.PULSE_MATRIX_SINGULAR,
			msg: "the SEM fit is undefined (" + err.Error() + "); every score is null", details: map[string]any{"error": err.Error()}}
	}
	if err := sem.ComputeStats(fit, plan.Desc); err != nil {
		return nil, notes, &fitRefusal{code: errors.PULSE_MATRIX_SINGULAR,
			msg: "the SEM fit is undefined (" + err.Error() + "); every score is null", details: map[string]any{"error": err.Error()}}
	}
	if note := semConvergenceNote(fit.Converged, fit.Status, fit.Options.Solver, fit.Iterations); note != nil {
		notes = append(notes, note)
	}
	if ref := semIdentifiedRefusal(fit.Stats); ref != nil {
		return nil, notes, ref
	}
	params, _ := fit.Params()
	if params.SampleMean == nil {
		// A covariance-only fit's scores center at the fit rows.
		params.SampleMean = mean
	}
	return &semScoreFit{models: []*sem.Model{plan.Model}, labels: []string{""}, codes: []uint64{0}, params: []sem.Params{params}}, notes, nil
}

// fitGroups is fitModel for a grouped plan: MAT_SEM's joint
// multi-group fit (finalizeSEMGroups' refusals, the fit-scoped
// warnings here).
func (a *semScoreAttribute) fitGroups() (*semScoreFit, []*fitRefusal, *fitRefusal) {
	plan := a.cfg.Plan
	k := len(plan.Model.Observed)
	codes := make([]uint64, 0, len(a.buckets))
	for c := range a.buckets {
		codes = append(codes, c)
	}
	slices.Sort(codes)
	labels := make([]string, len(codes))
	for i, c := range codes {
		labels[i] = a.levelLabel(c)
	}
	if len(codes) < 2 {
		return nil, nil, &fitRefusal{code: errors.PULSE_MATRIX_INSUFFICIENT_N,
			msg:     "params.group " + strconv.Quote(plan.Group) + " has fewer than two levels with fit rows; a multi-group fit needs two groups at least, and every score is null",
			details: map[string]any{"reason": "one_level", "group": plan.Group, "levels": labels}}
	}
	ref := 0
	if plan.GroupReference != "" {
		if ref = slices.Index(labels, plan.GroupReference); ref < 0 {
			return nil, nil, &fitRefusal{code: errors.PULSE_MATRIX_INSUFFICIENT_N,
				msg:     "params.group_reference " + strconv.Quote(plan.GroupReference) + " holds no fit rows; every score is null",
				details: map[string]any{"reason": "reference_absent", "group": plan.Group, "group_reference": plan.GroupReference, "levels": labels}}
		}
	}
	moments := make([]sem.GroupMoments, len(codes))
	for g, c := range codes {
		cm := a.buckets[c]
		if cm.N() <= int64(k) || !(cm.W() > float64(k)) {
			return nil, nil, &fitRefusal{code: errors.PULSE_MATRIX_INSUFFICIENT_N,
				msg:     fmt.Sprintf("group %q has %d fit rows; each group needs more rows (and weight) than the model's %d observed variables, and every score is null", labels[g], cm.N(), k),
				details: map[string]any{"reason": "level_too_thin", "group": plan.Group, "level": labels[g], "n": cm.N(), "sum_weights": cm.W(), "observed": k}}
		}
		s, mean := a.moments(cm)
		if !sem.PositiveDefinite(s) {
			return nil, nil, &fitRefusal{code: errors.PULSE_MATRIX_SINGULAR,
				msg:     "group " + strconv.Quote(labels[g]) + "'s fit-row covariance is not positive definite; a multi-group fit has no nearest substitute, and every score is null",
				details: map[string]any{"reason": "level_singular", "group": plan.Group, "level": labels[g]}}
		}
		level := g
		if plan.GroupLevels != nil {
			level = int(c)
		}
		moments[g] = sem.GroupMoments{Label: labels[g], S: s, Raw: s, Mean: mean, N: cm.W(), Level: level, Reference: g == ref}
	}
	gf, err := sem.FitGroups(plan.Model, moments, plan.Options)
	if err != nil {
		return nil, nil, &fitRefusal{code: errors.PULSE_MATRIX_SINGULAR,
			msg: "the SEM fit is undefined (" + err.Error() + "); every score is null", details: map[string]any{"error": err.Error()}}
	}
	var notes []*fitRefusal
	if note := semConvergenceNote(gf.Converged, gf.Status, gf.Options.Solver, gf.Iterations); note != nil {
		notes = append(notes, note)
	}
	if ref := semIdentifiedRefusal(gf.Stats); ref != nil {
		return nil, notes, ref
	}
	out := &semScoreFit{labels: labels, codes: codes}
	for _, f := range gf.Groups {
		p, _ := f.Params()
		out.models = append(out.models, f.Model)
		out.params = append(out.params, p)
	}
	return out, notes, nil
}

// moments are cm's population covariance (divisor W, MAT_SEM's S) and
// mean in the model's observed order.
func (a *semScoreAttribute) moments(cm *linalg.CoMoment) (*mat.SymDense, []float64) {
	k := len(a.order)
	cov, mv := cm.Cov(0), cm.Mean()
	s := mat.NewSymDense(k, nil)
	mean := make([]float64, k)
	for i := 0; i < k; i++ {
		mean[i] = mv.At(a.order[i])
		for j := i; j < k; j++ {
			s.SetSym(i, j, cov.At(a.order[i], a.order[j]))
		}
	}
	return s, mean
}

// semConvergenceNote is the caveat of a solver stopped short of its
// tolerance (the scores use the last iterate); nil when converged.
func semConvergenceNote(converged bool, status string, solver sem.Solver, iterations int) *fitRefusal {
	if converged {
		return nil
	}
	return &fitRefusal{code: errors.PULSE_MATRIX_NOT_CONVERGED,
		msg:     status + " after the solver's iterations; the scores use its last iterate",
		details: map[string]any{"solver": "sem_" + string(solver), "status": status, "iterations": iterations}}
}

// semIdentifiedRefusal refuses a fit with negative degrees of freedom
// (st, the fit's statistics) — its estimates, so its scores, are not
// identified (MAT_SEM fits such a model and warns; a score cannot).
func semIdentifiedRefusal(st sem.Stats) *fitRefusal {
	if st.DoF >= 0 {
		return nil
	}
	return &fitRefusal{code: errors.PULSE_MATRIX_NOT_IDENTIFIED,
		msg:     "the model has more free parameters than the moments it is fitted to (negative degrees of freedom), so its estimates and scores are not identified; every score is null",
		details: map[string]any{"dof": st.DoF, "n_params": st.NParams}}
}

// fitWarning is ref's fit-scoped warning.
func (a *semScoreAttribute) fitWarning(ref *fitRefusal) *types.ResponseWarning {
	details := map[string]any{
		"attribute": a.label,
		"type":      string(types.ATTR_SEM_SCORE),
		"scope":     "fit",
		"latent":    a.cfg.Latent,
		"members":   append([]string(nil), a.members...),
		"n":         a.base.N(),
	}
	if a.weight != nil {
		details["sum_weights"] = a.base.W()
	}
	if a.fitName != "" {
		// A bound fit's figures are the fit's own.
		details["fit"] = a.fitName
		if sf := a.bound; sf != nil {
			details["n"] = sf.N
			delete(details, "sum_weights")
			if sf.Basis != model.VectorBasisUnweighted {
				details["sum_weights"] = sf.SumWeights
			}
		}
	}
	for k, v := range ref.details {
		if _, taken := details[k]; !taken {
			details[k] = v
		}
	}
	return &types.ResponseWarning{Code: string(ref.code), Message: a.label + ": " + ref.msg, Details: details}
}

// Row scores rec: null before a usable fit, on a null member or group,
// or on a level the fit has no parameters for (tallied).
func (a *semScoreAttribute) Row(rec *Record, _ string) (float64, error) {
	if !a.finalized {
		return 0, errors.NewCodedError(errors.PROCESSING_INTERNAL, "ATTR_SEM_SCORE Row called before Finalize")
	}
	if a.scorers == nil {
		return math.NaN(), nil
	}
	x := make([]float64, len(a.members))
	if !a.fill.complete(rec, x) {
		return math.NaN(), nil
	}
	code := uint64(0)
	if a.groupField != "" {
		c, ok, err := a.groupCode(rec)
		if err != nil {
			return 0, err
		}
		if !ok {
			return math.NaN(), nil
		}
		code = c
	}
	sc, ok := a.scorers[code]
	if !ok {
		if a.unseen == nil {
			a.unseen = map[uint64]int64{}
		}
		a.unseen[code]++
		return math.NaN(), nil
	}
	y := make([]float64, len(a.order))
	for k, i := range a.order {
		y[k] = x[i]
	}
	return sc.Score(y), nil
}

// Compute is the buffered drive: PrePass(every) → Finalize → Row(every).
func (a *semScoreAttribute) Compute(records []*Record, field string) ([]float64, error) {
	if len(records) == 0 {
		if err := a.Finalize(); err != nil {
			return nil, err
		}
		return []float64{}, nil
	}
	return computeTwoPass(a, records, field)
}
