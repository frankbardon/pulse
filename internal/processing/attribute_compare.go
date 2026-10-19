package processing

import (
	stderrors "errors"
	"fmt"
	"math"
	"slices"
	"strconv"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
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
// Centroid and population references (U30 E3-S1). {centroid: {filter?,
// by?}} folds a pre-pass (centroidFold): every FILTERED row whose
// centroid.filter admits it (compiled like fit_on, against the
// attribute-step record) and whose weight is valid and > 0 adds Σw·x_i
// and Σw per PRESENT member — AGG_VEC_MEAN's per-element rule and
// arithmetic (vecBlock.add, float64(w·x)) — and Finalize writes each
// element's mean Σw·x_i / Σw (NaN when no row answered it). With by,
// one fold per by value (a categorical label or an unsigned integer's
// base-10 form — the key_field rule); each row then reads its own key's
// centroid, and a null key or a key no centroid row reached is the
// existing miss: null, counted in n_reference_missing, one
// PULSE_VECTOR_REFERENCE_MISSING warning — never a refusal. A centroid
// no row reached nulls every row the same way. The pre-pass makes the
// slot two-pass for either operator and any metric
// (vectors.ComparisonTwoPass); the fold is cohort-wide over the filtered
// rows (a grouped request runs buffered and never refolds per group).
// The weight is the slot's resolved weight (both types are ClassAware:
// it shapes the centroid, and a fitted metric's fit). {population: name}
// binds at build from the request's bound vector fits (model.Set —
// request / registered populations and in-batch Compose vector fits):
// the fit's stored mean aligned by label (vectors.PopulationAlign), an
// unbound name PULSE_VECTOR_REFERENCE_UNKNOWN (vectors.PopulationRefusal)
// — static thereafter, so row-local. {record: {cohort?, keys}} (U30
// E3-S2) binds at build from the stored record the Service resolved
// before the processor built (a point lookup over the cohort's sidecar
// index — the request's own cohort when cohort is omitted — carried on
// the request's model.Set): read by member column name (bindRecord), a
// null member a missing element; the lookup's own refusal (no index,
// stale, not found, ambiguous, a shard archive) refuses the run, never a
// fallback. Static, so row-local.
//
// Row-local metrics: single pass, streamable, fusable; the weight is
// irrelevant (unless a centroid reads it). ATTR_VEC_DISTANCE's fitted metrics (std_euclidean: the
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

	exts   *ExtensionRegistry
	models *model.Set
	bound  bool
	// unavailable is the PULSE_FIT_UNAVAILABLE warning of a
	// {population} reference naming a Compose vector fit that failed on
	// its data (bindPopulation; nil otherwise): every row is null,
	// counted in no tally.
	unavailable *types.ResponseWarning
	// centroid folds a centroid reference (nil for any other form).
	centroid *centroidFold
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
	if kc := cfg.Reference.KeyColumn(); kc != "" && schema != nil {
		if f := schema.Field(kc); f != nil {
			c.keyInt = vectors.ComparisonKeyFieldInteger(f.Type)
		}
	}
	if cfg.Reference.Form == vectors.ReferenceCentroid {
		c.centroid = &centroidFold{spec: inheritExprZone(cfg.Reference.Filter, attr.TimeZone), by: cfg.Reference.By, keyInt: c.keyInt, schema: schema,
			x: make([]float64, len(c.members)), pres: make([]bool, len(c.members))}
		if w := attr.Weight.Spec(); w != nil {
			ew := effectiveWeight(w)
			c.centroid.weight = &ew
		}
	}
	return c, true, nil
}

// bindInputs stores the request's bound fits (a population reference
// reads one) and compiles a centroid's filter against the request's
// filter build (buildFilterFuncsWith, as fit_on).
func (c *comparisonCore) bindInputs(exts *ExtensionRegistry, models *model.Set, vecs []vectors.Resolved, keys *encoding.Schema) error {
	c.models = models
	if c.centroid == nil {
		return nil
	}
	fns, err := buildFilterFuncsWith(c.centroid.spec, c.centroid.schema, exts, models, vecs, keys)
	if err != nil {
		return err
	}
	c.centroid.filter, c.centroid.bound = fns, true
	return nil
}

// bindPopulation resolves a {population} reference: the bound fit's
// stored mean, aligned to the slot's labels by label.
func (c *comparisonCore) bindPopulation() error {
	name := c.cfg.Reference.Population
	if se, failed := c.models.FailedOfKind(name, types.FitKindVector); failed {
		c.unavailable = model.FitUnavailable(name, c.label, se)
		c.bound = true
		return nil
	}
	vf, err := c.models.LookupVector(name)
	if err != nil {
		var ce *errors.CodedError
		if stderrors.As(err, &ce) {
			return vectors.PopulationRefusal(c.label, name, ce)
		}
		return err
	}
	perm, ce := vectors.PopulationAlign(c.label, name, vf.Labels, c.cfg.Labels)
	if ce != nil {
		return ce
	}
	if len(vf.Mean) != len(vf.Labels) {
		return errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
			fmt.Sprintf("%s: population %q holds %d means for %d labels", c.label, name, len(vf.Mean), len(vf.Labels)),
			map[string]any{"attribute": c.label, "population": name})
	}
	c.static = make([]float64, len(perm))
	for i, j := range perm {
		c.static[i] = vf.Mean[j]
	}
	c.bound = true
	return nil
}

// bindRecord binds a {record} reference: the stored record the Service
// resolved before the build (model.Set.LookupRecord), read member by
// member column name. Its lookup's refusal (PULSE_INDEX_* /
// PULSE_LOOKUP_*) is returned located on the slot; a member the
// record's cohort holds no numeric column for is
// PULSE_VECTOR_REFERENCE_MISMATCH record_members_absent; a null member
// is a missing element (NaN: its pair drops, min_valid applies).
func (c *comparisonCore) bindRecord() error {
	ref := c.cfg.Reference
	rec, err := c.models.LookupRecord(ref.RecordKey())
	if err != nil {
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) {
			return err
		}
		// The binding is shared by every processor of the run: locate a
		// copy, never the stored error.
		details := make(map[string]any, len(ce.Details)+2)
		for k, v := range ce.Details {
			details[k] = v
		}
		details["attribute"] = c.label
		details["slot"] = c.label + ".params.reference"
		return &errors.CodedError{Code: ce.Code, Message: c.label + " params.reference.record: " + ce.Message, Details: details, Cause: ce.Cause}
	}
	static := make([]float64, len(c.members))
	var missing []string
	for i, m := range c.members {
		v, _, known := rec.Member(m)
		if !known {
			missing = append(missing, m)
			continue
		}
		static[i] = v
	}
	if len(missing) > 0 {
		return vectors.RecordMembersAbsent(c.label, rec.Cohort(), missing)
	}
	c.static = static
	c.bound = true
	return nil
}

// bind resolves the reference against the instance's tables, the
// request's bound fits (population) or its resolved stored records
// (record). A centroid binds at Finalize.
func (c *comparisonCore) bind() error {
	if len(c.members) == 0 {
		return nil
	}
	switch c.cfg.Reference.Form {
	case vectors.ReferenceCentroid:
		c.bound = true
		return nil
	case vectors.ReferencePopulation:
		return c.bindPopulation()
	case vectors.ReferenceRecord:
		return c.bindRecord()
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
	if c.centroid != nil && !c.centroid.finalized {
		return nil, false, errors.NewCodedError(errors.PROCESSING_INTERNAL,
			string(c.typ)+" centroid reference read before Finalize")
	}
	if c.cfg.Reference.Static() {
		if c.static == nil {
			// A centroid no row reached: every row misses it.
			c.nRefMissing++
			return nil, false, nil
		}
		return c.static, true, nil
	}
	field := c.cfg.Reference.KeyColumn()
	c.nRefMissing++
	if c.keyInt {
		// Exact: a u64 key above 2^53 must not share its float64 echo
		// with a neighbour (#427).
		n, ok := rec.Uint64Value(field)
		if !ok {
			c.nKeyNull++
			return nil, false, nil
		}
		c.keyBuf = strconv.AppendUint(c.keyBuf[:0], n, 10)
		if v, has := c.profiles[string(c.keyBuf)]; has {
			c.nRefMissing--
			return v, true, nil
		}
		c.noteUnmatched(string(c.keyBuf))
		return nil, false, nil
	}
	key, ok := rec.LevelValue(field)
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
	if !c.bound {
		if err := c.bind(); err != nil {
			return math.NaN(), err
		}
	}
	if c.unavailable != nil {
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

// warnings are the slot's Response warnings: its PULSE_FIT_UNAVAILABLE
// (a {population} over a failed fit), then one
// PULSE_VECTOR_REFERENCE_MISSING when any row had no profile, one
// PULSE_VECTOR_METRIC_DOMAIN when any row fell outside the metric's
// domain; none on a clean run.
func (c *comparisonCore) warnings() []*types.ResponseWarning {
	var out []*types.ResponseWarning
	if c.unavailable != nil {
		out = append(out, c.unavailable)
	}
	if c.nRefMissing > 0 {
		keys := c.unmatched
		if keys == nil {
			keys = []string{}
		}
		out = append(out, c.missingWarning(keys))
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

// missingWarning is the slot's PULSE_VECTOR_REFERENCE_MISSING warning:
// a {table, key_field} names the table and key_field; a centroid names
// its by (reference "centroid"); a by-less centroid no row reached
// says so.
func (c *comparisonCore) missingWarning(keys []string) *types.ResponseWarning {
	ref := c.cfg.Reference
	n := strconv.FormatInt(c.nRefMissing, 10)
	split := func(what, field string) string {
		return " (" + strconv.FormatInt(c.nKeyNull, 10) + " with a null " + field + ", " + strconv.FormatInt(c.nRefMissing-c.nKeyNull, 10) +
			" with a value " + what + ") and score null"
	}
	details := map[string]any{
		"attribute":           c.label,
		"type":                string(c.typ),
		"n_reference_missing": int(c.nRefMissing),
	}
	var msg string
	switch {
	case ref.Form == vectors.ReferenceCentroid && ref.By == "":
		details["reference"] = vectors.ReferenceCentroid
		msg = c.label + ": " + n + " row(s) have no centroid — no filtered row the centroid filter admits answered a member (with a valid weight) — and score null"
	case ref.Form == vectors.ReferenceCentroid:
		details["reference"] = vectors.ReferenceCentroid
		details["by"] = ref.By
		msg = c.label + ": " + n + " row(s) have no centroid for their " + ref.By + split("no centroid row reached", ref.By)
	default:
		details["table"] = ref.Table
		details["key_field"] = ref.KeyField
		msg = c.label + ": " + n + " row(s) have no profile in reference table " + strconv.Quote(ref.Table) + split("the table has no key for", ref.KeyField)
	}
	if ref.KeyColumn() != "" {
		details["n_key_null"] = int(c.nKeyNull)
		details["n_key_unmatched"] = int(c.nRefMissing - c.nKeyNull)
		details["keys"] = slices.Clone(keys)
	}
	return &types.ResponseWarning{Code: string(errors.PULSE_VECTOR_REFERENCE_MISSING), Message: msg, Details: details}
}

// centroidFold is a centroid reference's pre-pass: per present member
// Σw·x and Σw (vecBlock, AGG_VEC_MEAN's fold) over the filtered rows
// the centroid filter admits — one block, or one per by value.
type centroidFold struct {
	spec   []*types.Filterer
	filter []FilterFunc
	bound  bool
	schema *encoding.Schema

	by     string
	keyInt bool
	keyBuf []byte

	weight *types.WeightSpec

	x    []float64
	pres []bool

	all    *vecBlock
	groups map[string]*vecBlock

	finalized bool
}

// prePass folds one filtered row (fill reads its members).
func (f *centroidFold) prePass(rec *Record, fill memberFill, typ types.AttributeType) error {
	if f.finalized {
		return errors.NewCodedError(errors.PROCESSING_INTERNAL, string(typ)+" centroid PrePass after Finalize")
	}
	if len(f.spec) > 0 && !f.bound {
		return errors.NewCodedError(errors.PROCESSING_INTERNAL, string(typ)+" centroid filter was not bound before PrePass")
	}
	if fill.fill(rec, f.x, f.pres) == 0 {
		return nil
	}
	for _, fn := range f.filter {
		ok, err := fn(rec)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
	}
	w := 1.0
	if f.weight != nil {
		var ok bool
		if w, ok = validRowWeight(rec, f.weight); !ok {
			return nil
		}
	}
	var b *vecBlock
	if f.by == "" {
		if f.all == nil {
			f.all = newVecBlock(len(f.x))
		}
		b = f.all
	} else {
		key, ok := f.key(rec)
		if !ok {
			return nil
		}
		if f.groups == nil {
			f.groups = map[string]*vecBlock{}
		}
		if b = f.groups[key]; b == nil {
			b = newVecBlock(len(f.x))
			f.groups[key] = b
		}
	}
	for i, x := range f.x {
		if f.pres[i] {
			b.add(i, x, w, true)
		}
	}
	return nil
}

// key reads rec's by value (false for a null).
func (f *centroidFold) key(rec *Record) (string, bool) {
	if f.keyInt {
		n, ok := rec.Uint64Value(f.by)
		if !ok {
			return "", false
		}
		f.keyBuf = strconv.AppendUint(f.keyBuf[:0], n, 10)
		return string(f.keyBuf), true
	}
	key, ok := rec.LevelValue(f.by)
	if !ok {
		return "", false
	}
	return key, true
}

// centroidMean is b's per-element mean Σw·x_i / Σw (NaN for an element
// no row answered) — AGG_VEC_MEAN's finish.
func centroidMean(b *vecBlock) []float64 {
	out := make([]float64, len(b.sum))
	for i := range out {
		if b.w[i] == 0 {
			out[i] = math.NaN()
		} else {
			out[i] = b.sum[i] / b.w[i]
		}
	}
	return out
}

// finishCentroid closes the fold into the core's reference: static (no
// by; nil when no row reached it) or one profile per by value.
func (c *comparisonCore) finishCentroid() error {
	f := c.centroid
	if f == nil {
		return nil
	}
	if f.finalized {
		return errors.NewCodedError(errors.PROCESSING_INTERNAL, string(c.typ)+" centroid Finalize called twice")
	}
	f.finalized = true
	if f.by == "" {
		if f.all != nil {
			c.static = centroidMean(f.all)
		}
		return nil
	}
	c.profiles = make(map[string][]float64, len(f.groups))
	for k, b := range f.groups {
		c.profiles[k] = centroidMean(b)
	}
	return nil
}

// comparisonAttribute is the row-local computer (every metric but
// ATTR_VEC_DISTANCE's fitted ones, over any reference but a centroid).
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
	_ modelBinder            = (*comparisonAttribute)(nil)

	_ TwoPassAttribute       = (*centroidComparisonAttribute)(nil)
	_ NullEmitter            = (*centroidComparisonAttribute)(nil)
	_ MetaAttribute          = (*centroidComparisonAttribute)(nil)
	_ ExtensionAware         = (*centroidComparisonAttribute)(nil)
	_ attributePreparer      = (*centroidComparisonAttribute)(nil)
	_ fitOnBinder            = (*centroidComparisonAttribute)(nil)
	_ attributeTallyResetter = (*centroidComparisonAttribute)(nil)
	_ attributeWarner        = (*centroidComparisonAttribute)(nil)

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
// computer for ATTR_VEC_DISTANCE over std_euclidean / mahalanobis (its
// pre-pass folding a centroid reference too), the centroid computer for
// a row-local metric over a centroid, the row-local one otherwise.
func newComparisonAttribute(attr *types.Attribute, schema *encoding.Schema) (AttributeComputer, error) {
	core, ok, err := newComparisonCore(attr, schema)
	if err != nil {
		return nil, err
	}
	if ok && !core.cfg.Fitted() && core.centroid != nil {
		return &centroidComparisonAttribute{comparisonCore: core}, nil
	}
	if !ok || !core.cfg.Fitted() {
		return &comparisonAttribute{comparisonCore: core}, nil
	}
	f := &fittedComparisonAttribute{core: core}
	base, ferr := newFittedVectorAttribute(attr, schema, fittedSource{fitOn: core.cfg.FitOn, fit: core.cfg.Fit, labels: core.cfg.Labels}, &comparisonFit{owner: f})
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

// bindModels satisfies modelBinder: the bound fits a population
// reference reads.
func (a *comparisonAttribute) bindModels(models *model.Set) error {
	a.models = models
	return nil
}

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

// centroidComparisonAttribute is a row-local metric over a centroid
// reference: the centroid pre-pass, then every filtered row scored
// against its centroid.
type centroidComparisonAttribute struct {
	comparisonCore
}

// SetExtensions satisfies ExtensionAware.
func (a *centroidComparisonAttribute) SetExtensions(r *ExtensionRegistry) { a.exts = r }

// bindFitOn satisfies fitOnBinder: compiles the centroid filter.
func (a *centroidComparisonAttribute) bindFitOn(exts *ExtensionRegistry, models *model.Set, vecs []vectors.Resolved, keys *encoding.Schema) error {
	return a.bindInputs(exts, models, vecs, keys)
}

// prepare satisfies attributePreparer.
func (a *centroidComparisonAttribute) prepare() error { return a.bind() }

// EmitsNull satisfies NullEmitter.
func (a *centroidComparisonAttribute) EmitsNull() bool { return true }

// PrePass folds one filtered row into the centroid.
func (a *centroidComparisonAttribute) PrePass(rec *Record, _ string) error {
	return a.centroid.prePass(rec, a.fill, a.typ)
}

// Finalize closes the centroid.
func (a *centroidComparisonAttribute) Finalize() error { return a.finishCentroid() }

// Row scores rec against its centroid.
func (a *centroidComparisonAttribute) Row(rec *Record, _ string) (float64, error) {
	return a.row(rec, nil)
}

// Compute is the buffered drive: PrePass(every) → Finalize → Row(every).
func (a *centroidComparisonAttribute) Compute(records []*Record, field string) ([]float64, error) {
	if len(records) == 0 {
		if err := a.Finalize(); err != nil {
			return nil, err
		}
		return []float64{}, nil
	}
	return computeTwoPass(a, records, field)
}

// Components satisfies MetaAttribute.
func (a *centroidComparisonAttribute) Components() (map[string]any, error) {
	return a.components(), nil
}

// attributeWarnings satisfies attributeWarner.
func (a *centroidComparisonAttribute) attributeWarnings() []*types.ResponseWarning {
	return a.warnings()
}

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

// bindFitOn satisfies fitOnBinder: the centroid filter and bound fits
// (a population reference), then the fit's own fit_on / params.fit.
func (a *fittedComparisonAttribute) bindFitOn(exts *ExtensionRegistry, models *model.Set, vecs []vectors.Resolved, keys *encoding.Schema) error {
	if err := a.core.bindInputs(exts, models, vecs, keys); err != nil {
		return err
	}
	return a.fittedVectorAttribute.bindFitOn(exts, models, vecs, keys)
}

// prebuiltFit satisfies prebuiltAttribute: a bound fit is row-local
// only when no centroid reference still needs the pre-pass
// (vectors.FitBound).
func (a *fittedComparisonAttribute) prebuiltFit() bool {
	return a.core.centroid == nil && a.fittedVectorAttribute.prebuiltFit()
}

// PrePass folds one filtered row into the fit and the centroid.
func (a *fittedComparisonAttribute) PrePass(rec *Record, field string) error {
	if err := a.fittedVectorAttribute.PrePass(rec, field); err != nil {
		return err
	}
	if a.core.centroid != nil {
		return a.core.centroid.prePass(rec, a.core.fill, a.core.typ)
	}
	return nil
}

// Finalize closes the fit, then the centroid.
func (a *fittedComparisonAttribute) Finalize() error {
	if err := a.fittedVectorAttribute.Finalize(); err != nil {
		return err
	}
	return a.core.finishCentroid()
}

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
