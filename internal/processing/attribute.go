package processing

import (
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/datepart"
	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/temporal"
	"github.com/frankbardon/pulse/internal/totalcall"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/types"
)

// popScoreState is the pass-1 state ATTR_ZSCORE and ATTR_TSCORE share:
// the population mean and the POPULATION standard deviation √(M2/Σw)
// of the filter-passing values, folded through the shared weighted
// Welford bucket (weighting.Welford). An unweighted slot folds every
// row with w = 1, which is the unweighted Welford recurrence bit for
// bit (Σw is the exact count), so an unweighted score is unchanged. A
// weighted slot (its stamped weight, processing.StampWeightsWith) folds
// each row with its weight; a row whose weight is invalid or zero adds
// nothing (U11 validity rule) but still receives a score — an attribute
// owes every row a value. The weighted mean and √(M2_w/Σw) are
// invariant to rescaling the weights, so frequency and probability
// weights give the same scores (statsmodels DescrStatsW(ddof = 0)).
type popScoreState struct {
	weight *types.WeightSpec
	bucket weighting.Welford
	// Locked at Finalize.
	finalMean   float64
	finalStdDev float64
	finalized   bool
}

func newPopScoreState(attr *types.Attribute) popScoreState {
	return popScoreState{weight: attr.Weight.Spec()}
}

func (s *popScoreState) prePass(r *Record, field string) {
	v, ok := r.NumericValue(field)
	if !ok {
		return
	}
	w := 1.0
	if s.weight != nil {
		var reason weighting.Reason
		if w, reason = readWeight(r, s.weight); reason != weighting.Valid || w == 0 {
			return
		}
	}
	s.bucket.Add(v, w)
}

func (s *popScoreState) finalize() {
	if s.bucket.N == 0 {
		s.finalMean = 0
		s.finalStdDev = 0
	} else {
		s.finalMean = s.bucket.Mean
		s.finalStdDev = math.Sqrt(s.bucket.M2 / s.bucket.SumW)
	}
	s.finalized = true
}

// zscoreAttribute computes (x - mean) / stddev per row using the
// population mean/stddev derived in pass 1 (popScoreState). Implements
// TwoPassAttribute so the streaming path can avoid buffering the
// record set.
type zscoreAttribute struct {
	popScoreState
}

func newZScoreAttribute(attr *types.Attribute, schema *encoding.Schema) (AttributeComputer, error) {
	if err := rejectSetFieldForNumericAttribute(attr, schema); err != nil {
		return nil, err
	}
	return &zscoreAttribute{popScoreState: newPopScoreState(attr)}, nil
}

func (a *zscoreAttribute) PrePass(r *Record, field string) error {
	a.prePass(r, field)
	return nil
}

func (a *zscoreAttribute) Finalize() error {
	a.finalize()
	return nil
}

func (a *zscoreAttribute) Row(r *Record, field string) (float64, error) {
	v, ok := r.NumericValue(field)
	if !ok || a.finalStdDev == 0 {
		return 0, nil
	}
	return (v - a.finalMean) / a.finalStdDev, nil
}

func (a *zscoreAttribute) Compute(records []*Record, field string) ([]float64, error) {
	return computeTwoPass(a, records, field)
}

// tscoreAttribute emits z*10+50 per row using the population
// mean/stddev from pass 1 (popScoreState). Implements TwoPassAttribute.
type tscoreAttribute struct {
	popScoreState
}

func newTScoreAttribute(attr *types.Attribute, schema *encoding.Schema) (AttributeComputer, error) {
	if err := rejectSetFieldForNumericAttribute(attr, schema); err != nil {
		return nil, err
	}
	return &tscoreAttribute{popScoreState: newPopScoreState(attr)}, nil
}

func (a *tscoreAttribute) PrePass(r *Record, field string) error {
	a.prePass(r, field)
	return nil
}

func (a *tscoreAttribute) Finalize() error {
	a.finalize()
	return nil
}

func (a *tscoreAttribute) Row(r *Record, field string) (float64, error) {
	v, ok := r.NumericValue(field)
	if !ok || a.finalStdDev == 0 {
		return 50, nil // T-score of mean when sd=0 or null input
	}
	return ((v-a.finalMean)/a.finalStdDev)*10 + 50, nil
}

func (a *tscoreAttribute) Compute(records []*Record, field string) ([]float64, error) {
	return computeTwoPass(a, records, field)
}

// computeTwoPass is the buffered Compute of a TwoPassAttribute: PrePass
// over every record, Finalize, then Row per record.
func computeTwoPass(a TwoPassAttribute, records []*Record, field string) ([]float64, error) {
	if len(records) == 0 {
		return []float64{}, nil
	}
	for _, r := range records {
		if err := a.PrePass(r, field); err != nil {
			return nil, err
		}
	}
	if err := a.Finalize(); err != nil {
		return nil, err
	}
	result := make([]float64, len(records))
	for i, r := range records {
		val, err := a.Row(r, field)
		if err != nil {
			return nil, err
		}
		result[i] = val
	}
	return result, nil
}

// normalizedAttribute emits (x-min)/(max-min) per row using population
// min/max from a Welford-shaped pass 1. Implements TwoPassAttribute.
type normalizedAttribute struct {
	seen     bool
	min, max float64
	rng      float64
}

func newNormalizedAttribute(attr *types.Attribute, schema *encoding.Schema) (AttributeComputer, error) {
	if err := rejectSetFieldForNumericAttribute(attr, schema); err != nil {
		return nil, err
	}
	return &normalizedAttribute{}, nil
}

func (a *normalizedAttribute) PrePass(r *Record, field string) error {
	v, ok := r.NumericValue(field)
	if !ok {
		return nil
	}
	if !a.seen {
		a.min, a.max = v, v
		a.seen = true
		return nil
	}
	if v < a.min {
		a.min = v
	}
	if v > a.max {
		a.max = v
	}
	return nil
}

func (a *normalizedAttribute) Finalize() error {
	a.rng = a.max - a.min
	return nil
}

func (a *normalizedAttribute) Row(r *Record, field string) (float64, error) {
	v, ok := r.NumericValue(field)
	if !ok || a.rng == 0 {
		return 0, nil
	}
	return (v - a.min) / a.rng, nil
}

func (a *normalizedAttribute) Compute(records []*Record, field string) ([]float64, error) {
	if len(records) == 0 {
		return []float64{}, nil
	}
	for _, r := range records {
		if err := a.PrePass(r, field); err != nil {
			return nil, err
		}
	}
	if err := a.Finalize(); err != nil {
		return nil, err
	}
	result := make([]float64, len(records))
	for i, r := range records {
		val, err := a.Row(r, field)
		if err != nil {
			return nil, err
		}
		result[i] = val
	}
	return result, nil
}

type formulaAttribute struct {
	expression string
	// label is the slot's output label (its own or the default), the
	// one a PULSE_FIT_UNAVAILABLE warning names.
	label  string
	schema *encoding.Schema
	exts   *ExtensionRegistry
	// models are the request's bound models coef() reads (nil: none),
	// set by bindModels before prepare compiles.
	models *model.Set
	// vecs are the request's resolved named vectors the formula may
	// read (nil: none), set by bindVectors before prepare compiles.
	vecs []vectors.Resolved
	// totals is the cohort-total state of a formula that calls
	// total(expr) (formula_total.go); nil for a row-local formula.
	totals *formulaTotals
	// zone is the zone to_day() reads in: the slot `tz` the zone
	// transport wrote (expr_zone.go); nil for UTC.
	zone *temporal.Zone

	once    sync.Once
	prog    *exprProgram
	progErr error
	// unavailable are the PULSE_FIT_UNAVAILABLE warnings of a formula
	// whose literal coef() calls name a fit that failed on its data
	// (set by prepare; nil otherwise): the formula then has no value
	// on any row, so every row is null — as ATTR_REG_PREDICT over a
	// failed fit — one warning per failed fit named.
	unavailable []*types.ResponseWarning
}

var _ attributeWarner = (*formulaAttribute)(nil)

// newFormulaAttribute builds ATTR_FORMULA. A formula that calls
// total(expr) is built as a formulaTotalAttribute — a TwoPassAttribute
// whose PrePass folds each total over the post-filter rows — so every
// interface-asserting arm (buffered Compute, the two-pass streaming
// drive, materialize) schedules the pre-pass; any other formula stays
// the row-local formulaAttribute, byte-identical to before.
func newFormulaAttribute(attr *types.Attribute, schema *encoding.Schema) (AttributeComputer, error) {
	if attr.Expression == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG, "formula attribute requires an expression")
	}
	zone, err := exprZone(attr.TimeZone)
	if err != nil {
		return nil, err
	}
	a := &formulaAttribute{
		expression: attr.Expression,
		label:      attributeSlotLabel(attr),
		schema:     schema,
		zone:       zone,
	}
	if totalcall.Contains(attr.Expression) {
		a.totals = &formulaTotals{weight: attr.Weight.Spec()}
		return &formulaTotalAttribute{formulaAttribute: a}, nil
	}
	return a, nil
}

// SetExtensions implements ExtensionAware so the Processor can inject
// the live registry after construction. Custom expr functions and
// lookup tables registered by the embedder are then visible to the
// formula expression at compile / evaluation time. Every construction
// site injects before the first Row / Compute, which is when the
// program compiles.
func (a *formulaAttribute) SetExtensions(r *ExtensionRegistry) {
	a.exts = r
}

// prepare compiles the formula once (see expr_program.go) — against the
// schema prototype, or on the first record when the formula names a
// non-schema column. It is called by the Processor's construction sites
// right after SetExtensions, so a compile error surfaces at build; Row
// and Compute call it too, for any caller that skips that step.
func (a *formulaAttribute) prepare() error {
	a.once.Do(func() {
		if a.progErr = a.exts.coefStaticRefusal(a.expression, a.models); a.progErr != nil {
			return
		}
		for _, f := range a.exts.failedCoefFits(a.expression, a.models) {
			a.unavailable = append(a.unavailable, model.FitUnavailable(f.name, a.label, f.err))
		}
		if a.totals != nil {
			a.prog, a.progErr = a.prepareTotals()
			return
		}
		a.prog, a.progErr = newExprProgram(a.expression, exprenv.SiteFormula, a.schema, a.vecs, a.exts.referenceTables(), a.exts.exprOptionsWithModels(a.models, a.zone))
	})
	return a.progErr
}

// bindModels satisfies modelBinder: coef() reads the request's bound
// models. Its static rule runs in prepare, after the binding.
func (a *formulaAttribute) bindModels(models *model.Set) error {
	a.models = models
	return nil
}

// bindVectors satisfies vectorBinder: the formula reads the request's
// named vectors as []float64 identifiers.
func (a *formulaAttribute) bindVectors(vecs []vectors.Resolved) { a.vecs = vecs }

func (a *formulaAttribute) Compute(records []*Record, field string) ([]float64, error) {
	if err := a.prepare(); err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return []float64{}, nil
	}

	result := make([]float64, len(records))
	for i, r := range records {
		v, err := a.Row(r, field)
		if err != nil {
			return nil, err
		}
		result[i] = v
	}
	return result, nil
}

// Row evaluates the compiled formula against a single record. A null
// referenced field binds as nil (expr_program.go): `x ?? 0` and
// `x == nil ? a : b` guard it; an operator that cannot take nil raises
// PROCESSING_RUNTIME "evaluating formula expression", so the formula
// never invents a number for the row. A formula whose RESULT is nil
// (`vcount(v) >= 6 ? vmean(v) : nil`, or a bare null field) yields a
// null attribute value: Row returns AttributeNull, which every arm
// writes as a null under the label (SetAttributeOutput), so a
// downstream aggregation skips the row and counts it in n_null.
func (a *formulaAttribute) Row(r *Record, _ string) (float64, error) {
	if err := a.prepare(); err != nil {
		return 0, err
	}
	if a.unavailable != nil {
		// A literal coef() names a failed fit: no row has a value.
		return AttributeNull, nil
	}
	output, _, err := a.prog.runRecord(r)
	if err != nil {
		return 0, err
	}
	if output == nil {
		return AttributeNull, nil
	}
	return formulaNumber(output)
}

// attributeWarnings satisfies attributeWarner: the PULSE_FIT_UNAVAILABLE
// warnings of a formula over a failed fit (none when prepare refused).
func (a *formulaAttribute) attributeWarnings() []*types.ResponseWarning {
	if a.prepare() != nil {
		return nil
	}
	return a.unavailable
}

// formulaNumber is a formula's numeric reading of an expression value:
// a number as float64, a decimal as its nearest float64, a bool as 1 /
// 0; any other type is refused.
func formulaNumber(output any) (float64, error) {
	switch v := output.(type) {
	case float64:
		return v, nil
	case float32:
		return float64(v), nil
	case int:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case bool:
		if v {
			return 1.0, nil
		}
		return 0.0, nil
	case exprenv.Decimal:
		// An exact decimal result downcasts to the float64 column.
		return v.Float64(), nil
	case []float64:
		return 0, vectorResultError("formula")
	default:
		return 0, errors.NewCodedError(errors.PROCESSING_RUNTIME,
			fmt.Sprintf("formula expression returned unsupported type %T", output))
	}
}

// attributePreparer is implemented by attributes with build-time work
// that needs the injected ExtensionRegistry (ATTR_FORMULA's compile).
type attributePreparer interface {
	prepare() error
}

// modelBinder is implemented by attributes that score a bound model
// (ATTR_REG_PREDICT): bindModels resolves it in the request's bound
// models before any record is read.
type modelBinder interface {
	bindModels(models *model.Set) error
}

// NullEmitter is implemented by an attribute whose NaN output means "no
// value for this row" (ATTR_REG_PREDICT: a null input). Every
// orchestrator arm writes such a value as a NULL under the label
// (SetAttributeOutput), so a downstream aggregation skips the row and
// counts it in n_null instead of folding NaN. An attribute without it
// keeps its NaN as a value.
type NullEmitter interface {
	EmitsNull() bool
}

// EmitsNull reports whether computer writes NaN as a null.
func EmitsNull(computer AttributeComputer) bool {
	ne, ok := computer.(NullEmitter)
	return ok && ne.EmitsNull()
}

// attributeNullBits is AttributeNull's bit pattern: a quiet NaN with a
// payload no IEEE operation produces from ordinary operands.
const attributeNullBits = 0x7FF8_4E55_4C4C_0001

// AttributeNull is the value an attribute's Row / Compute returns for
// "no value on this row" WITHOUT being a NullEmitter — ATTR_FORMULA's
// nil result. Every arm writes it as a null (IsAttributeNull), whatever
// the computer's EmitsNull; any other NaN keeps its NullEmitter rule.
var AttributeNull = math.Float64frombits(attributeNullBits)

// IsAttributeNull reports whether v is AttributeNull (bit-exact: an
// ordinary NaN is not).
func IsAttributeNull(v float64) bool { return math.Float64bits(v) == attributeNullBits }

// attributeOutputNull reports whether an attribute output v is written
// as a null: AttributeNull always, any NaN when nullable (EmitsNull).
func attributeOutputNull(v float64, nullable bool) bool {
	return IsAttributeNull(v) || (nullable && math.IsNaN(v))
}

// SetAttributeOutput writes one attribute value under label on a
// row-at-a-time arm: a NULL when v is AttributeNull, or when nullable
// (EmitsNull) and v is NaN; otherwise the value with any prior null
// mark cleared (Record.Set).
func SetAttributeOutput(rec *Record, label string, v float64, nullable bool) {
	if attributeOutputNull(v, nullable) {
		rec.SetNull(label)
		return
	}
	rec.Set(label, v)
}

// vectorBinder is implemented by the expression operators
// (ATTR_FORMULA, FILTER_EXPRESSION): bindVectors hands them the
// request's resolved named vectors (ExprVectors) before they compile.
type vectorBinder interface {
	bindVectors(vecs []vectors.Resolved)
}

// ExprVectors resolves req's named vectors (Request.Vectors) against
// schema for the expression operators, followed by the schema's native
// vector fields (vectors.Resolve): nil when there are neither, or when
// one fails to resolve (the field-reference pass refused that request
// before any build).
func ExprVectors(req *types.Request, schema *encoding.Schema) []vectors.Resolved {
	if req == nil || (len(req.Vectors) == 0 && (schema == nil || !schema.HasVectors())) {
		return nil
	}
	resolved, err := vectors.Resolve(req.Vectors, schema)
	if err != nil {
		return nil
	}
	return resolved
}

// BindAttribute injects the registry and the request's bound models
// into a freshly built attribute and runs its build-time preparation,
// so a compile error or an unresolvable model surfaces before any record
// is read. models may be nil (nothing bound). It binds no named
// vectors: BindAttributeWithVectors does.
func BindAttribute(computer AttributeComputer, exts *ExtensionRegistry, models *model.Set) error {
	return BindAttributeWithVectors(computer, exts, models, nil)
}

// BindAttributeWithVectors is BindAttribute that also hands an
// expression attribute the request's resolved named vectors
// (ExprVectors; nil: none).
func BindAttributeWithVectors(computer AttributeComputer, exts *ExtensionRegistry, models *model.Set, vecs []vectors.Resolved) error {
	if aware, ok := computer.(ExtensionAware); ok {
		aware.SetExtensions(exts)
	}
	if vb, ok := computer.(vectorBinder); ok {
		vb.bindVectors(vecs)
	}
	if mb, ok := computer.(modelBinder); ok {
		if err := mb.bindModels(models); err != nil {
			return err
		}
	}
	if fb, ok := computer.(fitOnBinder); ok {
		if err := fb.bindFitOn(exts, models, vecs); err != nil {
			return err
		}
	}
	if p, ok := computer.(attributePreparer); ok {
		return p.prepare()
	}
	return nil
}

// bindAttribute is BindAttributeWithVectors with this processor's
// registry, bound models and the run's named vectors.
func (p *Processor) bindAttribute(computer AttributeComputer) error {
	return BindAttributeWithVectors(computer, p.exts, p.models, p.exprVectors)
}

type percentileAttribute struct{}

func newPercentileAttribute(attr *types.Attribute, schema *encoding.Schema) (AttributeComputer, error) {
	if err := rejectSetFieldForNumericAttribute(attr, schema); err != nil {
		return nil, err
	}
	return &percentileAttribute{}, nil
}

func (a *percentileAttribute) Compute(records []*Record, field string) ([]float64, error) {
	if len(records) == 0 {
		return []float64{}, nil
	}

	// Collect values with their original indices
	type indexedVal struct {
		idx int
		val float64
	}
	var indexed []indexedVal
	for i, r := range records {
		v, ok := r.NumericValue(field)
		if ok {
			indexed = append(indexed, indexedVal{idx: i, val: v})
		}
	}

	if len(indexed) == 0 {
		return make([]float64, len(records)), nil
	}

	// Sort by value
	sort.Slice(indexed, func(i, j int) bool {
		return indexed[i].val < indexed[j].val
	})

	// Compute percentile rank for each position
	result := make([]float64, len(records))
	n := float64(len(indexed))
	for rank, iv := range indexed {
		// Percentile = (rank + 1) / n * 100
		result[iv.idx] = float64(rank+1) / n * 100.0
	}
	return result, nil
}

// datePartAttribute is ATTR_DATE_PART. A `date` column is read as
// epoch days on the calendar (no zone ever applies); a `datetime` column
// as an instant whose parts are the wall clock in the slot's zone (zone
// nil: UTC), via temporal.LocalParts.
type datePartAttribute struct {
	part    string
	seconds bool
	zone    *temporal.Zone
}

func newDatePartAttribute(attr *types.Attribute, schema *encoding.Schema) (AttributeComputer, error) {
	part, err := datepart.Parse(attr.Params)
	if err != nil {
		return nil, err
	}
	var f *encoding.Field
	if schema != nil {
		f = schema.Field(attr.Field)
	}
	if err := datepart.CheckField(part, attr.Field, f); err != nil {
		return nil, err
	}
	df, err := resolveDateField(string(types.ATTR_DATE_PART), attr.Field, attr.TimeZone, schema, true)
	if err != nil {
		return nil, err
	}
	return &datePartAttribute{part: part, seconds: df.seconds, zone: df.zone}, nil
}

func (a *datePartAttribute) Compute(records []*Record, field string) ([]float64, error) {
	if len(records) == 0 {
		return []float64{}, nil
	}

	result := make([]float64, len(records))
	for i, r := range records {
		v, err := a.Row(r, field)
		if err != nil {
			return nil, err
		}
		result[i] = v
	}
	return result, nil
}

// Row extracts the configured date part from a single record. Null date
// values produce 0 (matches buffered Compute semantics).
func (a *datePartAttribute) Row(r *Record, field string) (float64, error) {
	v, ok := r.NumericValue(field)
	if !ok {
		return 0, nil
	}
	if a.seconds {
		return a.instantPart(int64(v)), nil
	}
	t := temporal.DayToTime(int64(v))
	year, month, day := t.Date()
	return encodeDatePart(a.part, year, month, day), nil
}

// instantPart is the `datetime` arm of Row: the part of instant sec on
// the wall clock of the slot's zone (UTC when none applies).
func (a *datePartAttribute) instantPart(sec int64) float64 {
	z := a.zone
	if z == nil {
		z = temporal.UTC
	}
	p := temporal.LocalParts(sec, z)
	if a.part == datepart.PartHour {
		return float64(p.Hour)
	}
	return encodeDatePart(a.part, p.Year, p.Month, p.Day)
}

// encodeDatePart is the encoded integer ATTR_DATE_PART emits for a
// calendar date.
func encodeDatePart(part string, year int, month time.Month, day int) float64 {
	switch part {
	case "year":
		return float64(year)
	case "month":
		return float64(month)
	case "day":
		return float64(day)
	case "year_month":
		return float64(year*100 + int(month))
	case "year_month_day":
		return float64(year*10000 + int(month)*100 + day)
	case "month_day":
		return float64(int(month)*100 + day)
	}
	return 0
}
