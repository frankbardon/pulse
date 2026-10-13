package processing

import (
	stderrors "errors"
	"fmt"
	"math"
	"slices"
	"strconv"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/linalg/metric"
	"github.com/frankbardon/pulse/types"
)

// ATTR_PROFILE_SIMILARITY / ATTR_VEC_DISTANCE — each row's similarity
// (distance) to a reference across the slot's p members, one engine
// split by direction (internal/vectors/compare.go holds the rules both
// predict and this file read).
//
// The members and params arrive canonical from Processor.prepare
// (resolveAttributeInputs: Fields = the literal members, Vector
// cleared, params rewritten by vectors.ComparisonConfig.CanonicalParams
// — the metric written out, so a vector kind's default survives the
// rewrite, the reference in member order or as its table form, the
// vector's labels as params.labels); the factory judges them through
// the same vectors.ComparisonParams predict calls. A table reference
// binds at build (ExtensionAware + attributePreparer): an unregistered
// table or key, or labels that are not the vector's, refuse the run
// (PULSE_VECTOR_REFERENCE_UNKNOWN / _MISMATCH) before any record.
//
// Per row: the members read as a vector (a null member is a missing
// element, NaN); the reference is the static one, or — {table,
// key_field} — the profile keyed by the row's key_field value (a
// categorical label, or an unsigned integer's canonical base-10 form):
// a null key or a value with no profile is a null value, counted in
// n_reference_missing, never a refusal. Fewer than params.min_valid
// complete pairs is null; otherwise the registry metric
// (metric.Metric.Eval, pairwise-complete with the p/p_observed rescaling)
// in the operator's form. A value outside the metric's domain (a
// negative part under hellinger / aitchison, an aitchison zero, a
// non-0/1 value under a set metric) is null, counted in n_domain_invalid.
// Either tally, when non-zero, also raises one Response warning per slot
// after the run (attributeWarner: PULSE_VECTOR_REFERENCE_MISSING,
// PULSE_VECTOR_METRIC_DOMAIN), components on or off — never under
// Strict escalation, like MAT_DISTANCE's PULSE_MATRIX_METRIC_DOMAIN.
//
// Row-local metrics: single pass, streamable, fusable; the weight is
// irrelevant. ATTR_VEC_DISTANCE's fitted metrics (std_euclidean: the
// per-member SDs; mahalanobis: the inverse covariance) read
// ATTR_MAHALANOBIS's fit pass (fittedVectorAttribute: listwise rows that
// params.fit_on admits, weighted when a weight is in force, N* ≤ p or a
// singular fit nulls every value with its warning); every filtered row
// is then scored pairwise-complete (mahalanobis needs every member).

// comparisonCore is the state both computers share.
type comparisonCore struct {
	typ     types.AttributeType
	label   string
	members []string
	fill    memberFill // reads members (the shared member fill)
	cfg     vectors.ComparisonConfig
	form    metric.Form

	exts  *ExtensionRegistry
	bound bool
	// static is the reference in member order (every form but
	// {table, key_field}); profiles maps a key_field label to its
	// profile in member order.
	static   []float64
	profiles map[string][]float64
	// keyInt: the key_field is an unsigned integer, matched by its
	// base-10 form (keyBuf the reused formatting buffer).
	keyInt bool
	keyBuf []byte

	x []float64

	// nRefMissing counts rows whose key_field had no profile (or was
	// null) — nKeyNull of them a null key, the rest unmatched, the
	// smallest of whose values ride unmatched; nDomain rows whose value
	// fell outside the metric's domain, for reasons domainReasons.
	nRefMissing   int64
	nKeyNull      int64
	unmatched     []string
	nDomain       int64
	domainReasons map[string]bool
}

// comparisonUnmatchedKeys caps the unmatched key values a
// PULSE_VECTOR_REFERENCE_MISSING warning lists (the smallest, so the
// list is independent of row order).
const comparisonUnmatchedKeys = 10

// newComparisonCore decodes attr's canonical params over its literal
// members. ok is false for a registry probe (no members).
func newComparisonCore(attr *types.Attribute, schema *encoding.Schema) (comparisonCore, bool, error) {
	if attr == nil {
		return comparisonCore{}, false, errors.NewCodedError(errors.PROCESSING_CONFIG, "comparison attribute requires an attribute spec")
	}
	if attr.Vector != "" {
		return comparisonCore{}, false, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
			fmt.Sprintf("%s vector %q was not resolved before construction", attr.Type, attr.Vector),
			map[string]any{"attribute": string(attr.Type), "vector": attr.Vector})
	}
	label := attr.Label
	if label == "" {
		label = defaultAttributeLabel(attr)
	}
	_, form, _ := vectors.ComparisonConsumer(attr.Type)
	c := comparisonCore{typ: attr.Type, label: label, members: append([]string(nil), attr.Fields...), form: form,
		fill: memberFillOf(attr.Fields, schema)}
	if len(c.members) == 0 {
		return c, false, nil
	}
	cfg, err := vectors.ComparisonParams(label, attr.Type, attr.Params,
		vectors.Resolved{Members: c.members, Labels: c.members}, schema)
	if err != nil {
		return comparisonCore{}, false, err
	}
	c.cfg = cfg
	c.x = make([]float64, len(c.members))
	if cfg.Reference.Form == vectors.ReferenceTableKeyField && schema != nil {
		if f := schema.Field(cfg.Reference.KeyField); f != nil {
			c.keyInt = vectors.ComparisonKeyFieldInteger(f.Type)
		}
	}
	return c, true, nil
}

// bind resolves the reference against the instance's tables.
func (c *comparisonCore) bind() error {
	if len(c.members) == 0 {
		return nil
	}
	b, err := vectors.ComparisonTableIssue(c.label, c.cfg, c.exts.referenceTables())
	if err != nil {
		return err
	}
	c.static = b.Static
	if c.cfg.Reference.Form == vectors.ReferenceTableKeyField {
		c.profiles = make(map[string][]float64, len(b.Table.Keys))
		for _, k := range b.Table.Keys {
			v, _ := b.Table.Profile(k, b.Perm)
			c.profiles[k] = v
		}
	}
	c.bound = true
	return nil
}

// reference returns rec's reference; false when its key_field has no
// profile (counted).
func (c *comparisonCore) reference(rec *Record) ([]float64, bool, error) {
	if !c.bound {
		// A computer driven without the build's bind (a probe) binds
		// here: a static reference needs no table, and a table reference
		// without the registry refuses with the coded error.
		if err := c.bind(); err != nil {
			return nil, false, err
		}
	}
	if c.profiles == nil {
		return c.static, true, nil
	}
	field := c.cfg.Reference.KeyField
	c.nRefMissing++
	if c.keyInt {
		n, ok := rec.NumericValue(field)
		if !ok {
			c.nKeyNull++
			return nil, false, nil
		}
		c.keyBuf = strconv.AppendUint(c.keyBuf[:0], uint64(n), 10)
		if v, has := c.profiles[string(c.keyBuf)]; has {
			c.nRefMissing--
			return v, true, nil
		}
		c.noteUnmatched(string(c.keyBuf))
		return nil, false, nil
	}
	key, ok := rec.StringValue(field)
	if !ok && rec.IsNull(field) {
		c.nKeyNull++
		return nil, false, nil
	}
	if v, has := c.profiles[key]; ok && has {
		c.nRefMissing--
		return v, true, nil
	}
	c.noteUnmatched(key)
	return nil, false, nil
}

// noteUnmatched keeps key among the comparisonUnmatchedKeys smallest
// distinct unmatched values (sorted).
func (c *comparisonCore) noteUnmatched(key string) {
	i, found := slices.BinarySearch(c.unmatched, key)
	if found {
		return
	}
	if len(c.unmatched) == comparisonUnmatchedKeys {
		if i == comparisonUnmatchedKeys {
			return
		}
		c.unmatched = c.unmatched[:comparisonUnmatchedKeys-1]
	}
	c.unmatched = slices.Insert(c.unmatched, i, key)
}

// readMembers fills c.x with rec's members (a null member is NaN).
func (c *comparisonCore) readMembers(rec *Record) {
	c.fill.fill(rec, c.x, nil)
}

// eval is the metric between the row's members and ref.
func (c *comparisonCore) eval(ref []float64, stats *metric.Stats) (float64, error) {
	if obs, _ := metric.Observed(c.x, ref); obs == 0 || obs < c.cfg.MinValid {
		return math.NaN(), nil
	}
	v, err := c.cfg.Metric.Eval(c.form, c.x, ref, stats)
	if err != nil {
		var de *metric.DomainError
		if stderrors.As(err, &de) {
			c.nDomain++
			if c.domainReasons == nil {
				c.domainReasons = map[string]bool{}
			}
			c.domainReasons[de.Reason] = true
			return math.NaN(), nil
		}
		return math.NaN(), errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
			c.label+": "+err.Error(), map[string]any{"attribute": c.label, "metric": c.cfg.Metric.Name()})
	}
	return v, nil
}

// row scores rec (stats nil for a row-local metric).
func (c *comparisonCore) row(rec *Record, stats *metric.Stats) (float64, error) {
	if len(c.members) == 0 {
		return math.NaN(), nil
	}
	ref, ok, err := c.reference(rec)
	if err != nil || !ok {
		return math.NaN(), err
	}
	c.readMembers(rec)
	return c.eval(ref, stats)
}

// components is the slot's operator map (MetaAttribute).
func (c *comparisonCore) components() map[string]any {
	return map[string]any{
		"n_reference_missing": int(c.nRefMissing),
		"n_domain_invalid":    int(c.nDomain),
	}
}

func (c *comparisonCore) resetTally() {
	c.nRefMissing, c.nKeyNull, c.unmatched, c.nDomain, c.domainReasons = 0, 0, nil, 0, nil
}

// warnings are the slot's Response warnings: one
// PULSE_VECTOR_REFERENCE_MISSING when any row had no profile, one
// PULSE_VECTOR_METRIC_DOMAIN when any row fell outside the metric's
// domain; none on a clean run.
func (c *comparisonCore) warnings() []*types.ResponseWarning {
	var out []*types.ResponseWarning
	if c.nRefMissing > 0 {
		keys := c.unmatched
		if keys == nil {
			keys = []string{}
		}
		ref := c.cfg.Reference
		out = append(out, &types.ResponseWarning{
			Code: string(errors.PULSE_VECTOR_REFERENCE_MISSING),
			Message: c.label + ": " + strconv.FormatInt(c.nRefMissing, 10) + " row(s) have no profile in reference table " + strconv.Quote(ref.Table) +
				" (" + strconv.FormatInt(c.nKeyNull, 10) + " with a null " + ref.KeyField + ", " + strconv.FormatInt(c.nRefMissing-c.nKeyNull, 10) +
				" with a value the table has no key for) and score null",
			Details: map[string]any{
				"attribute":           c.label,
				"type":                string(c.typ),
				"table":               ref.Table,
				"key_field":           ref.KeyField,
				"n_reference_missing": int(c.nRefMissing),
				"n_key_null":          int(c.nKeyNull),
				"n_key_unmatched":     int(c.nRefMissing - c.nKeyNull),
				"keys":                slices.Clone(keys),
			},
		})
	}
	if c.nDomain > 0 {
		reasons := make([]string, 0, len(c.domainReasons))
		for r := range c.domainReasons {
			reasons = append(reasons, r)
		}
		slices.Sort(reasons)
		name := c.cfg.Metric.Name()
		out = append(out, &types.ResponseWarning{
			Code: string(errors.PULSE_VECTOR_METRIC_DOMAIN),
			Message: c.label + ": " + strconv.FormatInt(c.nDomain, 10) + " row(s) hold values outside the " + name +
				" metric's domain and score null",
			Details: map[string]any{
				"attribute":        c.label,
				"type":             string(c.typ),
				"metric":           name,
				"n_domain_invalid": int(c.nDomain),
				"reasons":          reasons,
			},
		})
	}
	return out
}

// comparisonAttribute is the row-local computer (every metric but
// ATTR_VEC_DISTANCE's fitted ones).
type comparisonAttribute struct {
	comparisonCore
}

var (
	_ AttributeComputer      = (*comparisonAttribute)(nil)
	_ RowLocalAttribute      = (*comparisonAttribute)(nil)
	_ NullEmitter            = (*comparisonAttribute)(nil)
	_ MetaAttribute          = (*comparisonAttribute)(nil)
	_ ExtensionAware         = (*comparisonAttribute)(nil)
	_ attributePreparer      = (*comparisonAttribute)(nil)
	_ attributeTallyResetter = (*comparisonAttribute)(nil)
	_ attributeWarner        = (*comparisonAttribute)(nil)

	_ TwoPassAttribute       = (*fittedComparisonAttribute)(nil)
	_ NullEmitter            = (*fittedComparisonAttribute)(nil)
	_ MetaAttribute          = (*fittedComparisonAttribute)(nil)
	_ ExtensionAware         = (*fittedComparisonAttribute)(nil)
	_ attributePreparer      = (*fittedComparisonAttribute)(nil)
	_ fitOnBinder            = (*fittedComparisonAttribute)(nil)
	_ attributeWarner        = (*fittedComparisonAttribute)(nil)
	_ attributeTallyResetter = (*fittedComparisonAttribute)(nil)
)

// newComparisonAttribute is both types' registry factory: the fitted
// computer for ATTR_VEC_DISTANCE over std_euclidean / mahalanobis, the
// row-local one otherwise.
func newComparisonAttribute(attr *types.Attribute, schema *encoding.Schema) (AttributeComputer, error) {
	core, ok, err := newComparisonCore(attr, schema)
	if err != nil {
		return nil, err
	}
	if !ok || !core.cfg.Fitted() {
		return &comparisonAttribute{comparisonCore: core}, nil
	}
	f := &fittedComparisonAttribute{core: core}
	base, ferr := newFittedVectorAttribute(attr, schema, core.cfg.FitOn, &comparisonFit{owner: f})
	if ferr != nil {
		return nil, ferr
	}
	f.fittedVectorAttribute = base
	return f, nil
}

// SetExtensions satisfies ExtensionAware: the tables a reference reads.
func (a *comparisonAttribute) SetExtensions(r *ExtensionRegistry) { a.exts = r }

// prepare satisfies attributePreparer: binds the reference.
func (a *comparisonAttribute) prepare() error { return a.bind() }

// EmitsNull satisfies NullEmitter.
func (a *comparisonAttribute) EmitsNull() bool { return true }

// Row scores one record.
func (a *comparisonAttribute) Row(rec *Record, _ string) (float64, error) {
	return a.row(rec, nil)
}

// Compute is Row over every record (the buffered arm).
func (a *comparisonAttribute) Compute(records []*Record, field string) ([]float64, error) {
	out := make([]float64, len(records))
	for i, r := range records {
		v, err := a.Row(r, field)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

// Components satisfies MetaAttribute.
func (a *comparisonAttribute) Components() (map[string]any, error) { return a.components(), nil }

// attributeWarnings satisfies attributeWarner.
func (a *comparisonAttribute) attributeWarnings() []*types.ResponseWarning { return a.warnings() }

// fittedComparisonAttribute is ATTR_VEC_DISTANCE over a fitted metric:
// ATTR_MAHALANOBIS's fit pass (the embedded fittedVectorAttribute's
// PrePass / Finalize / fit_on / weight / warnings), then every filtered
// row scored against the reference, pairwise-complete.
type fittedComparisonAttribute struct {
	*fittedVectorAttribute
	core  comparisonCore
	stats *metric.Stats
}

// SetExtensions satisfies ExtensionAware.
func (a *fittedComparisonAttribute) SetExtensions(r *ExtensionRegistry) { a.core.exts = r }

// prepare satisfies attributePreparer: binds the reference.
func (a *fittedComparisonAttribute) prepare() error { return a.core.bind() }

// Row scores rec: null before a usable fit.
func (a *fittedComparisonAttribute) Row(rec *Record, _ string) (float64, error) {
	if !a.finalized {
		return 0, errors.NewCodedError(errors.PROCESSING_INTERNAL, string(a.typ)+" Row called before Finalize")
	}
	if !a.usable {
		return math.NaN(), nil
	}
	return a.core.row(rec, a.stats)
}

// Compute is the buffered drive: PrePass(every) → Finalize → Row(every).
func (a *fittedComparisonAttribute) Compute(records []*Record, field string) ([]float64, error) {
	if len(records) == 0 {
		if err := a.Finalize(); err != nil {
			return nil, err
		}
		return []float64{}, nil
	}
	return computeTwoPass(a, records, field)
}

// Components satisfies MetaAttribute.
func (a *fittedComparisonAttribute) Components() (map[string]any, error) {
	return a.core.components(), nil
}

func (a *fittedComparisonAttribute) resetTally() { a.core.resetTally() }

// attributeWarnings satisfies attributeWarner: the fit's warnings (a
// singular or insufficient fit), then the scoring tallies'.
func (a *fittedComparisonAttribute) attributeWarnings() []*types.ResponseWarning {
	fit := a.fittedVectorAttribute.attributeWarnings()
	rows := a.core.warnings()
	if len(rows) == 0 {
		return fit
	}
	return append(slices.Clone(fit), rows...)
}

// comparisonFit is the fittedModel of a fitted comparison: the per-member
// SDs (std_euclidean) or the inverse covariance (mahalanobis) of the fit.
type comparisonFit struct {
	owner *fittedComparisonAttribute
}

// fit decomposes the fit covariance into the metric's statistics.
func (m *comparisonFit) fit(st fittedState) *fitRefusal {
	cov := st.cov()
	met := m.owner.core.cfg.Metric
	if met.Fit() == metric.FitSD {
		sd := make([]float64, st.p)
		for i := range sd {
			sd[i] = math.Sqrt(cov[i][i])
			if !(sd[i] > 0) || math.IsInf(sd[i], 0) {
				return &fitRefusal{code: errors.PULSE_MATRIX_SINGULAR,
					msg:     "member " + m.owner.members[i] + " has no spread over the fit rows; std_euclidean divides by each member's SD, so every value is null",
					details: map[string]any{"member": m.owner.members[i], "metric": met.Name()}}
			}
		}
		m.owner.stats = &metric.Stats{SD: sd}
		return nil
	}
	singular := &fitRefusal{code: errors.PULSE_MATRIX_SINGULAR,
		msg:     "the fit covariance is singular (a member is constant or a linear combination of others); the mahalanobis distance is undefined and every value is null",
		details: map[string]any{"metric": met.Name()}}
	if rankDeficient(cov) {
		return singular
	}
	sym, err := linalg.NewSymFromRows(cov)
	if err != nil {
		return singular
	}
	inv, err := linalg.InverseSPD(sym)
	if err != nil {
		return singular
	}
	m.owner.stats = &metric.Stats{InverseCovariance: inv}
	return nil
}

// score is unused: the owner's Row reads members pairwise-complete
// against the reference rather than the base's listwise centre.
func (m *comparisonFit) score([]float64) float64 { return math.NaN() }
