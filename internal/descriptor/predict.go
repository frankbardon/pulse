package descriptor

import (
	"bytes"
	"io"
	"maps"
	"slices"
	"strconv"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/featstream"
	"github.com/frankbardon/pulse/internal/fieldaccept"
	"github.com/frankbardon/pulse/internal/limits"
	"github.com/frankbardon/pulse/internal/mergegate"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/prevstate"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// decimalAggregationRefused reports whether agg on a decimal128 field
// draws PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL: a built-in with no decimal
// arm in the accepted-types table (fieldaccept.HasDecimalArm). A
// registered extension aggregator is never refused: it owns its decimal
// semantics (extend.Record.DecimalValue) and the runtime dispatches it
// to its own factory.
func decimalAggregationRefused(agg types.AggregationType, snap *ExtensionsSnapshot) bool {
	return !fieldaccept.HasDecimalArm(string(agg)) && !snap.HasAggregator(string(agg))
}

// PredictOptions controls predict behavior.
type PredictOptions struct {
	// Strict upgrades warnings to errors.
	Strict bool

	// Extensions is the read-only snapshot of embedder-registered
	// operators + expression-side state. Nil takes the built-in-only
	// path. The snapshot adds every custom operator name to the
	// validator's known-types set so predict does not flag
	// embedder-registered ops as unknown, and feeds streamability
	// overrides into computeStreamable.
	Extensions *ExtensionsSnapshot

	// EchoRequest causes Predict to populate envelope.Request with the
	// normalized (post-defaults) request. PredictResult.Request stays
	// the raw input request — the envelope field is the new uniform
	// surface across all envelope-producing endpoints. Off by default.
	EchoRequest bool

	// DefaultTimeZone is pulse.Options.DefaultTimeZone — the zone a
	// zone-capable slot inherits when neither its own `tz` nor the
	// request's `time_zone` names one. Empty means UTC.
	DefaultTimeZone string

	// DefaultWeight is pulse.Options.DefaultWeight — the row weight a
	// weight-bearing slot inherits when neither its own `weight` nor
	// the request's `weight` names one. Nil means none.
	DefaultWeight *types.WeightSpec

	// DefaultMultiplicity is pulse.Options.DefaultMultiplicity — the
	// multiple-comparison block every test, post-test and overlay
	// inherits field by field after its own block and its request's.
	// Nil means none.
	DefaultMultiplicity *types.Multiplicity

	// ZoneLoader resolves zone names (nil: temporal.LoadZone). The
	// facade passes its per-instance cache so predict and the runtime
	// resolve through the same loader.
	ZoneLoader ZoneLoader

	// DisableComponents is pulse.Options.DisableComponents — the engine
	// default a request's disable_components overrides. Predict reads it
	// where a components-disabled host changes the answer (an overlay
	// that needs the host's weighted floor is refused).
	DisableComponents bool

	// DisableDefaults is pulse.Options.DisableDefaults. When set,
	// predict validates the request WITHOUT smart defaults — exactly
	// what the runtime executes — so a slot left with an empty Type is
	// reported the way the runtime reports it. DefaultsApplied is still
	// computed: it then lists the defaults that WOULD apply.
	DisableDefaults bool

	// SchemaLoader reads the header + schema of the cohort at a path
	// (no record data). The facade passes the runtime's own cohort
	// opener. Predict uses it for Request.Joins: the request is
	// validated against the joined schema (internal/encoding.
	// JoinedSchema, the rule the runtime builds its join over), so a
	// right-side field is resolved by its real type. Nil leaves a join
	// unresolved and validation runs against the left schema alone.
	SchemaLoader func(path string) (*encoding.Schema, error)

	// RecordCounter returns the record count of the cohort at a path
	// from its header + schema alone (no record data) — the facade
	// passes the runtime's own header-only counter (Service.
	// CountRecords). Predict uses it for the build (right) side of a
	// Request.Joins entry, whose count is exactly the number of records
	// the runtime's join build decodes, so a MaxJoinBuildRows breach is
	// a certain finding. Nil (or a count error) yields no join-build
	// finding.
	RecordCounter func(path string) (int64, error)

	// CohortCounter returns the header facts the limit rules read for
	// the cohort at a path (CohortCounts: the record count, an
	// archive's per-shard counts and byte length) — the facade passes
	// the runtime pre-flight's own reader (Service.CohortCounts). The
	// Compose root reads it per slot and fit, so a slot's limit
	// findings carry the figures its Process refuses with. Nil falls
	// back to RecordCounter's count (no archive terms).
	CohortCounter func(path string) (CohortCounts, error)

	// ModelLoader reads a model file's bytes at a path relative to the
	// instance filesystem root — the facade passes the runtime's own
	// read (Service.ReadModelFile). Predict uses it for every `models`
	// entry (Request, FacetRequest, Compose slots and fits, chain
	// stages): the file is decoded and version-checked by the runtime's
	// rule and its terms judged against the scoring frame. Nil refuses
	// every entry as PULSE_MODEL_FILE_UNREADABLE (reason unreadable).
	ModelLoader ModelFileReader

	// Instance is the instance feature set. The request-time overlay
	// kind gates (Request, Compose and Facet hosts) report a kind it
	// hides exactly as a kind not in the catalog. Nil hides nothing.
	Instance *InstanceSnapshot

	// SuggestedWeightVariable is the weighting variable the cohort's SPSS
	// metadata sidecar records; the facade reads the sidecar (Predict
	// itself never opens a file) and leaves it "" when there is none.
	// Predict echoes it as PredictResult.SuggestedWeight when no slot
	// resolves a weight — it is never applied.
	SuggestedWeightVariable string

	// SidecarMeasureLevels maps a field name to the SPSS measure level
	// ("nominal", "ordinal", "scale", "unset") the cohort's SPSS metadata
	// sidecar records; the facade reads it (nil when there is no usable
	// sidecar). It feeds the measure-level advisories and nothing else.
	SidecarMeasureLevels map[string]string

	// SidecarLoader reads the SPSS metadata sidecar facts beside the
	// cohort at a path — the weighting variable ("" when there is none or
	// capability:weighting is hidden) and the measure levels (nil when
	// there is no usable sidecar). Compose predict calls it once per slot
	// cohort, because each slot names its own cohort; the single-cohort
	// roots take SuggestedWeightVariable / SidecarMeasureLevels instead.
	// Nil reads nothing: no sidecar advisory fires on a Compose slot.
	SidecarLoader func(path string) (weightVariable string, measureLevels map[string]string)

	// DisableCrosstabFusion is pulse.Options.DisableCrosstabFusion. When
	// set, PredictResult.CrosstabFusable answers false with
	// CrosstabFusionDisabledReason for every crosstab request, as the
	// engine then never dispatches the fused arm.
	DisableCrosstabFusion bool

	// ShardWorkers is pulse.Options.ShardWorkers (0 = runtime.NumCPU,
	// 1 = serial). Over a shard archive it decides whether the memory
	// estimate counts the per-shard parallel arm's partials
	// (ShardFanOut), as the engine's dispatch decides the arm.
	ShardWorkers int

	// DecodeWorkers is pulse.Options.DecodeWorkers (0 = runtime.NumCPU,
	// 1 = serial). Over a single file it decides, with DecodeMappable,
	// whether the memory estimate counts the parallel-decode reducer's
	// per-worker partials (DecodeFanOut), as the engine's dispatch
	// decides the arm.
	DecodeWorkers int

	// DecodeMappable reports whether the runtime can memory-map the
	// single-file cohort at path — the parallel-decode reducer's
	// precondition (a real on-disk path). The facade passes the
	// runtime's own probe; nil answers false (no parallel decode is
	// predicted). Called only when a memory limit is set and the other
	// fan-out conditions hold.
	DecodeMappable func(path string) bool
}

// CrosstabFusionDisabledReason is the PredictResult.CrosstabFusionReasons
// entry reported when PredictOptions.DisableCrosstabFusion is set.
const CrosstabFusionDisabledReason = "crosstab fusion disabled on this instance (Options.DisableCrosstabFusion)"

// componentsDisabled mirrors the runtime's components gate
// (Service.componentsGateClosed, which feeds the compute plan): the
// request's disable_components when set, else the engine default.
func (o *PredictOptions) componentsDisabled(req *types.Request) bool {
	return EffectiveDisableComponents(req, o != nil && o.DisableComponents)
}

// instance returns the options' instance feature set (nil — hide
// nothing — on a nil receiver). Nil-receiver-safe.
func (o *PredictOptions) instance() *InstanceSnapshot {
	if o == nil {
		return nil
	}
	return o.Instance
}

// defaultMultiplicity is the nil-safe DefaultMultiplicity read.
func (o *PredictOptions) defaultMultiplicity() *types.Multiplicity {
	if o == nil {
		return nil
	}
	return o.DefaultMultiplicity
}

// opRoute returns t, or "" — a name no operator table, switch or
// capability lookup knows — when inst hides it, so every type-keyed
// predict site takes the never-registered branch for a hidden name.
// Callers key lookups on the route and keep naming the authored t in
// messages; an emptiness check (a slot with no Type) never reads the
// route. Nil-instance-safe: nil hides nothing. The descriptor twin of
// the runtime registry's hidden-name lookups.
func opRoute[T ~string](inst *InstanceSnapshot, t T) T {
	if inst.Hidden(string(t)) {
		return ""
	}
	return t
}

// overlayRoute returns the kind the overlay validators key on: the
// authored kind, or "" — a kind no catalog, table or switch knows —
// when the instance hides it, so a hidden kind takes the
// never-registered branch while messages keep naming the authored kind.
// The descriptor twin of processing.ExtensionRegistry.overlayRoute.
// Nil-receiver-safe.
func (o *PredictOptions) overlayRoute(kind types.OverlayKind) types.OverlayKind {
	if o != nil && o.Instance.Hidden(string(kind)) {
		return ""
	}
	return kind
}

// Predict validates a request against a .pulse file without executing it.
// It reads only the header and schema, never record data.
// The returned Envelope contains the PredictResult in Data and any
// errors/warnings encountered.
//
// Detection is by the first four bytes at the reader's position, as in
// Inspect: zip magic PK\x03\x04 routes to the shard-archive path, where
// the canonical _schema.pulse entry supplies the schema the request
// validates against and per-shard headers contribute the cumulative
// RecordCount and Shards listing (streamability is computed from the
// same gates the single-file path uses — an archive inherits the
// streamability of its request against the canonical schema, not of its
// shape). Any other prefix takes the single-file path, which surfaces
// the standard ENCODING_INVALID envelope on malformed input.
func Predict(fileData io.ReadSeeker, req *types.Request, opts *PredictOptions) *descriptor.Envelope {
	// A slot the instance hides is an unknown field — refused first, as
	// Service.Process refuses it before the join-count rule or opening
	// the cohort. Nothing else is validated: the runtime never gets
	// further either.
	if serr := SlotRefusal(req, opts.instance()); serr != nil {
		result := newPredictResult(req)
		result.Valid = false
		env := descriptor.NewEnvelope(result)
		addCodedError(env, serr)
		return env
	}
	// Then every item for_each expands (ExpandRequestItems — the
	// runtime's expansion, with the MaxDerivedColumns preflight before
	// any item renders); everything below judges the expanded request,
	// while PredictResult.Request stays as written.
	written := req
	expanded, fan, finding, xerr := ExpandRequestItems(req, opts.instance().Limits())
	if xerr != nil {
		return predictForEachRefusal(req, xerr, finding)
	}
	var env *descriptor.Envelope
	if src, ok := sniffArchive(fileData); ok {
		env = predictArchive(src, expanded, opts)
	} else {
		env = predictSingle(fileData, expanded, opts, nil, nil, 0)
	}
	if len(fan) > 0 {
		if result, ok := env.Data.(*descriptor.PredictResult); ok {
			result.Request = written
			result.ForEach = forEachSummaries(fan)
		}
	}
	return env
}

// predictSingle is Predict over one header + schema. records is the
// cohort's record count when the caller knows it (an archive's
// per-shard sum); nil derives it from a single file's length. It bounds
// the response size estimates (PredictResult.Sizes), the limit findings
// and the matrix state estimate — RecordCount stays the archive-only
// figure it documents. shardRecords are an archive's per-shard counts
// (merge blocks restart per shard) and directoryBytes its
// central-directory bytes (the memory estimate's archive terms); nil
// and 0 for a single file and an anchor-extracted shard.
func predictSingle(fileData io.ReadSeeker, req *types.Request, opts *PredictOptions, records *int64, shardRecords []int64, directoryBytes int64) *descriptor.Envelope {
	if opts == nil {
		opts = &PredictOptions{}
	}

	result := newPredictResult(req)
	env := descriptor.NewEnvelope(result)

	// Read header only.
	pulseVersion, err := encoding.ReadHeader(fileData)
	if err != nil {
		env.AddError(string(headerErrorCode(err)), "invalid pulse file header: "+err.Error(), nil)
		result.Valid = false
		return env
	}

	// Read schema (still header, no record data).
	schema, err := encoding.ReadSchema(fileData, pulseVersion)
	if err != nil {
		env.AddError(string(errors.ENCODING_INVALID), "invalid pulse schema: "+err.Error(), nil)
		result.Valid = false
		return env
	}

	// The record count bounds the return size estimates; derived from
	// the file length (never a record), as Inspect derives it.
	sizeIn := returnSizeInputs{components: !opts.componentsDisabled(req)}
	if records != nil {
		sizeIn.records, sizeIn.recordsKnown = *records, true
	} else if n, _, ok := deriveSingleFileRecordCount(fileData, schema); ok {
		sizeIn.records, sizeIn.recordsKnown = n, true
	}

	result.SchemaInfo = &descriptor.PredictSchemaInfo{
		FieldCount: len(schema.Fields),
	}
	for _, f := range schema.Fields {
		result.SchemaInfo.Fields = append(result.SchemaInfo.Fields, f.Name)
	}

	// More than one JoinSpec: the runtime refuses before it opens a
	// cohort or applies a default, so predict reports it first.
	if jerr := JoinCountRefusal(req); jerr != nil {
		addCodedError(env, jerr)
	}
	// The matrix-host rule (matrices with joins or a crosstab), which the
	// runtime applies right after the join-count rule.
	if merr := mergegate.MatrixRefusal(req); merr != nil {
		addCodedError(env, merr)
	}
	// The request's `models` and `populations` — the runtime loads them right after its
	// save-path rule (validateRegressions reports that one), before any
	// record is read: read and decoded through opts.ModelLoader, the
	// loaded terms then judged by the field-reference pass below.
	models, unloadedModels, lerr := predictModels(model.RequestRefs(req), opts.populations(), opts)
	if lerr != nil {
		addCodedError(env, lerr)
	}

	// Multiplicity resolution — the same single pass the runtime runs
	// right after the join-count rule, before any dispatch
	// (ResolveMultiplicity). Schema-free.
	multPlan, merr := ResolveMultiplicity(req, opts.DefaultMultiplicity, opts.Instance)
	if merr != nil {
		addCodedError(env, merr)
	}

	// Response shaping — the same schema-free pass the runtime runs
	// before dispatch (ResolveReturn). The resolved plan is reported
	// once the data-column rule below also passes.
	returnPlan, rerr := ResolveReturn(req, opts.Instance)
	if rerr != nil {
		addCodedError(env, rerr)
	}

	// A join executes over the joined schema; validate against it.
	// SchemaInfo above stays the cohort's own schema.
	cohortSchema := schema
	schema = predictJoinedSchema(env, req, schema, opts)

	// Compute defaults on a clone so the echoed Request is untouched. The
	// rest of validation runs against the resolved clone so a slot that
	// only got its Type from the default rules table isn't flagged as
	// missing a Type by downstream validators. Under DisableDefaults the
	// runtime executes the request as written, so validation does too;
	// DefaultsApplied still reports what the rules table would infer.
	resolved := cloneRequestForDefaults(req)
	if opts.DisableDefaults {
		if applied := ResolveDefaults(cloneRequestForDefaults(req), schema, opts.Instance); len(applied) > 0 {
			result.DefaultsApplied = applied
		}
	} else if applied := ResolveDefaults(resolved, schema, opts.Instance); len(applied) > 0 {
		result.DefaultsApplied = applied
	}
	req = resolved

	// EchoRequest publishes the normalized clone on the envelope. The
	// clone is the same struct downstream validators see, so what the
	// caller gets on env.Request is exactly what an engine run would
	// execute. PredictResult.Request remains the raw input (back-compat).
	if opts.EchoRequest {
		env.Request = resolved
	}

	// Multiple-comparison trigger data: the inferential p-values the
	// request emits and how many no correction reaches. Counted on the
	// defaults-resolved request (crosstab axes may take a defaulted
	// grouper) against the schema it executes over; withheld when the
	// blocks are refused or the instance hides the capability.
	if merr == nil && opts.instance().Enabled(featMultiplicity) {
		result.PValues = countPValues(req, schema, multPlan, opts)
	}

	// Zone resolution — the same single pass the runtime runs before
	// executing (ResolveZones). A refusal is a predict error carrying
	// the runtime's own code and details.
	if zones, zerr := ResolveZones(req, schema, opts.DefaultTimeZone, opts.ZoneLoader, opts.Instance); zerr != nil {
		addCodedError(env, zerr)
	} else {
		result.TimeZones = zones
	}

	// Field references — the one rule the runtime refuses with at the
	// same point (after defaults and zones, against the schema the
	// request executes over). Every unknown name is reported.
	for _, ce := range dropUnloadedUnknowns(fieldRefRefusalsWithModels(req, schema, extensionsFromOpts(opts), opts.Instance, models), unloadedModels) {
		env.AddError(string(ce.Code), ce.Message, ce.Details)
	}

	// Virtual vectors — the field-reference pass above refuses a bad
	// one; a clean resolution is echoed (resolved_vectors) together with
	// one PULSE_VECTOR_UNREFERENCED warning per vector no operator slot
	// references, as the runtime warns.
	// `return` data columns — judged on the defaults-resolved request
	// (a defaulted aggregation's label carries its inferred type) over
	// the schema it executes over.
	if rerr == nil {
		if cerr := ReturnColumnRefusal(req, schema, opts.Instance); cerr != nil {
			addCodedError(env, cerr)
		} else {
			result.Return = returnPlanDescriptor(returnPlan)
		}
	}

	predictVectors(env, result, req, schema, opts.Strict)
	predictReferences(env, req, schema, extensionsFromOpts(opts))
	// The merge-fragment count the matrix state scales with: over the
	// per-shard counts for an archive (plus the rows non-head fragments
	// buffer), else over the record count; unknown without one.
	blocks, buffered := int64(-1), int64(0)
	if len(shardRecords) > 0 {
		blocks = limits.MergeBlocks(shardRecords...)
		buffered = limits.MergeBufferedRows(shardRecords...)
	} else if sizeIn.recordsKnown {
		blocks = limits.MergeBlocks(sizeIn.records)
	}
	matrixRecords := int64(-1)
	if len(shardRecords) > 0 {
		matrixRecords = 0
		for _, n := range shardRecords {
			matrixRecords += n
		}
	} else if sizeIn.recordsKnown {
		matrixRecords = sizeIn.records
	}
	if multPlan != nil {
		for _, rm := range multPlan.Matrices {
			sizeIn.matrixMembers = append(sizeIn.matrixMembers, rm.Member)
		}
		sizeIn.regressionMembers = multPlan.Regressions
	}
	sizeIn.matrixPlans = predictMatrices(result, req, schema, extensionsFromOpts(opts), opts.Instance, blocks, buffered, matrixRecords)
	// Instance resource limits — the rule the process pre-flight
	// refuses with (LimitRefusal), on the defaults-resolved request over
	// the schema it executes over. A certain finding is a predict error.
	limitRecords := int64(-1)
	if sizeIn.recordsKnown {
		limitRecords = sizeIn.records
	}
	predictLimits(env, result, req, schema, opts, limitRecords, shardRecords, directoryBytes)

	// Response size estimates — on every request, on the
	// defaults-resolved request over the schema it executes over. With
	// no effective `return` block (nil plan) shaped equals full; a
	// refused block withholds them. The Open includes predict cannot
	// resolve ride the reported plan.
	if rerr == nil && (returnPlan == nil || result.Return != nil) {
		sizeIn.req, sizeIn.schema, sizeIn.inst, sizeIn.matrices = req, schema, opts.Instance, result.Matrices
		result.Sizes = responseSizes(returnPlan, sizeIn)
	}
	if result.Return != nil {
		result.Return.UnresolvedIncludes = unresolvedIncludes(returnPlan, req, schema, opts.Instance)
	}

	// Weight resolution — the same single pass the runtime runs right
	// after the field-reference rule (ResolveWeights). A refusal is a
	// predict error carrying the runtime's own code and details.
	// With capability:weighting hidden the refusals still run (an
	// AGG_WEIGHTED_MEAN params.weight_field stays ungated) but the
	// per-slot report is not offered.
	weights, werr := ResolveWeights(req, schema, opts.DefaultWeight, opts.Instance)
	if werr != nil {
		addCodedError(env, werr)
	} else if opts.instance().Enabled(featWeighting) {
		result.Weights = weights
		// The cohort's own suggestion (SPSS sidecar), echoed as data
		// only while nothing resolves a weight field — never a warning,
		// never applied. Judged against the COHORT schema the sidecar
		// describes, not a joined one.
		if !anyWeightResolves(weights) {
			result.SuggestedWeight = SuggestWeight(cohortSchema, opts.SuggestedWeightVariable, opts.Instance)
		}
	}

	// The request body — every per-slot operator, param, field-type,
	// window, test, sort, crosstab, overlay and label rule (the checks
	// the runtime's operator construction makes). Shared with every
	// Compose slot and fit request (validateComposeRequest).
	validateRequestBody(env, req, schema, opts, werr)

	// Populate the predict surface from req.Overlays. One descriptor per
	// spec in matching order; OverlayCost is a per-kind multiplier keyed
	// by the spec's renderer-facing name (or a synthesised default when
	// empty) — streamable kinds carry overlayCostStreamable, buffered
	// kinds carry overlayCostBuffered. OverlaysSchemaDivergence stays
	// empty on Predict — Compose wiring exposes divergence on
	// ValidateCompose.
	populateOverlayDescriptors(result, req, opts)

	// Populate the per-aggregation predict surface: one
	// AggregationPredict descriptor per req.Aggregations slot in
	// matching order. ComponentSchema is projected from the static
	// capability table; BufferedComponents flags slots whose
	// Mergeability is None so streaming consumers know the Components
	// block arrives only on terminal flush. Predict stays no-execute
	// — the schema is looked up via aggregatorComponentSchemaIndex,
	// never via constructed operators.
	populateAggregationPredicts(result, req, schema, opts)

	// Populate the per-grouper predict surface: one GroupPredict
	// descriptor per req.Groups slot in matching order.
	// ComponentSchema is projected from the static grouper capability
	// table; BufferedComponents flags slots whose Mergeability is None
	// (GROUP_QUANTILE today). Predict stays no-execute — the schema is
	// looked up via grouperComponentSchemaIndex, never via constructed
	// operators.
	populateGroupPredicts(result, req, opts)
	// A buffered vector grouper's point store (GROUP_KMEANS), the
	// grouper term of the memory estimate, per groups[i] slot.
	if buf := GroupBufferBytes(req, schema, opts.Instance, matrixRecords); len(buf) > 0 {
		i := 0
		for _, grp := range req.Groups {
			if grp == nil {
				continue
			}
			if b, ok := buf[grp]; ok && i < len(result.Groups) {
				v := b
				result.Groups[i].RowBufferBytes = &v
			}
			i++
		}
	}

	// Populate the per-filterer predict surface: one FiltererPredict
	// descriptor per req.Filterers slot in matching order.
	// ComponentSchema is projected from the static filterer
	// capability table; BufferedComponents flags slots whose
	// Mergeability is None. In v1 every built-in filterer is Mergeable
	// (counters fold trivially), so BufferedComponents is always false
	// — the field is still surfaced for future per-filter specifics
	// that may downgrade individual entries. Predict stays no-execute
	// — the schema is looked up via filtererComponentSchemaIndex,
	// never via constructed operators.
	populateFiltererPredicts(result, req, opts)

	// Check description quality (the cohort's own fields only).
	validateDescriptionQuality(env, cohortSchema, opts)

	// Compute streamability — per-type Streamable() methods plus schema-aware
	// gates (decimal fields force buffered). Honours
	// the extensions snapshot when present so custom operator overrides land.
	result.Streamable, result.StreamableReasons = computeStreamable(req, schema, opts)

	// Before-filters attributes (ATTR_PREVIOUS_STATE) — the rule the
	// runtime's buffered pipeline splits the attribute list on.
	result.ComputedBeforeFilters = prevstate.BeforeFiltersLabels(req.Attributes, func(t types.AttributeType) types.AttributeType { return opRoute(opts.Instance, t) })

	// Fused-crosstab dispatch — the engine's own rule on the
	// defaults-resolved request over the cohort's schema (the runtime
	// gate reads cohort.Schema(); a join declines first either way).
	result.CrosstabFusable, result.CrosstabFusionReasons = computeCrosstabFusion(req, cohortSchema, opts)

	// Compute autocomplete-style suggestions. Suggestions may surface even
	// when the request is otherwise valid (streamability hints), so this
	// runs unconditionally after every other validator.
	result.Suggestions = computeSuggestions(req, schema, result.Streamable, opts.Extensions, opts.Instance)

	// Fit-for-purpose advisories (PULSE_ADVISORY_*): coded, non-blocking,
	// never warnings — Strict cannot escalate one — and never a change to
	// what executes. On the defaults-resolved request over the schema it
	// executes over; the instance drops suppressed codes.
	result.Advisories = computeAdvisories(advisoryInput{
		req:             req,
		schema:          schema,
		cohortSchema:    cohortSchema,
		plan:            multPlan,
		pv:              result.PValues,
		suggestedWeight: result.SuggestedWeight,
		measures:        opts.SidecarMeasureLevels,
		inst:            opts.Instance,
		records:         matrixRecords,
		recordsKnown:    matrixRecords >= 0,
	})

	// If any errors were added, mark invalid.
	if len(env.Errors) > 0 {
		result.Valid = false
	}

	return env
}

// validateRequestBody is the Process-predict request body: the rules
// the runtime's Process applies while it builds the request's operators
// — operator Type presence and registration (a profile-hidden name
// routes as never registered, opRoute), per-operator params, features,
// the accepted-types table, model-error / regression / attribute
// shapes, windows, tests, sort keys, the crosstab section (its cell and
// margin aggregations judged like any aggregation), Request.Overlays
// (ValidateOverlays) and label bindings. req is the defaults-resolved
// request and schema the one it executes over (non-nil); weightErr is
// the request's weight-resolution refusal, if any.
//
// It runs AFTER the entry passes (slot gate, item expansion, joins,
// multiplicity, return, zones, field references, weights) and reports
// every finding on env. Predict calls it on the request; Compose predict
// on every slot and fit request (validateComposeRequest) and chain
// predict on every stage (ValidateChainWithOptions), so a rule added
// here reaches all three — TestComposePredict_SlotVerdictMatchesProcessPredict
// and TestChainPredict_StageBodyMatchesRuntime pin that. It reads no record and constructs no operator.
func validateRequestBody(env *descriptor.Envelope, req *types.Request, schema *encoding.Schema, opts *PredictOptions, weightErr error) {
	// A slot still without an operator Type is refused by the runtime's
	// operator construction; report it with the runtime's code and
	// message.
	validateOperatorTypes(env, req, schema)
	validateRegisteredOperators(env, req, opts)
	validateAggregationParams(env, req, opts.instance())
	validateGroupDateParams(env, req, schema, opts.instance())
	validateRangeOperatorParams(env, req, opts.instance())
	validateGroupFitParams(env, req, opts.instance())
	validateDatePartParams(env, req, schema, opts.instance())

	// Validate pre-filter feature operators and compute the post-feature
	// column set so downstream stages can reference derived columns.
	projected := validateFeatures(env, req, schema, opts)

	// Project attribute output labels into the column set too. Attributes
	// inject labels mid-pipeline (after features, before grouping); without
	// this projection, aggregations and sort keys that reference attribute
	// labels would falsely trip the unknown-field check.
	projectAttributeOutputs(req, projected, opts.Instance)

	// Validate request fields exist in schema (or in feature outputs).
	validateRequestFields(env, req, schema, projected, opts)

	// The accepted-types table's per-slot type refusals, the runtime
	// constructors' codes (internal/fieldaccept).
	validateFieldAcceptance(env, req, schema, opts.instance(), weightErr)

	// Validate window operations (structural checks; no execution).
	validateWindows(env, req, schema, opts)

	// Static vector lengths of the pair built-ins (dot / dist / cosine)
	// in the request's expressions — the runtime build's refusal.
	predictVectorLengths(env, req, schema)

	// Validate tier-1 and tier-2 statistical tests against the schema +
	// projected column set. Tier-1 catches missing fields and type
	// mismatches; tier-2 catches alpha and unknown-type errors. Field
	// existence for tier-2 columns is deferred to runtime since the
	// projected post-pipeline column set is not yet a settled contract.
	validateTests(env, req, schema, projected, opts)

	// Validate response-level sort keys against the projected output columns.
	validateSort(env, req, schema)

	// Validate the Crosstab section (structural + axis field references +
	// normalization gates). Streamability reasons land via
	// computeStreamable below.
	validateCrosstab(env, req, schema, opts)

	// Validate overlay specs (Request.Overlays). Surfaces unknown kind,
	// ref-shape compatibility against the host, and the per-kind
	// supported scope set. Streamability is gated on the host operator;
	// overlay streamability surfaces via types.OverlayStreamable.
	ValidateOverlays(env, req, schema, opts)

	// Validate label bindings (display-time categorical translation). Snapshot
	// carries the registered label tables; augment-mode collisions are
	// checked against OutputLabels — the one output-column set the
	// runtime checks too — so a sibling "<field>_label" cannot shadow an
	// aggregation, attribute, group-key, window or feature column. The
	// schema is the dictionary-inheritance view (prevstate.KeySchema), the
	// runtime's too: a binding on an ATTR_PREVIOUS_STATE output column
	// validates as one on its state field.
	if len(req.Labels) > 0 {
		ValidateLabels(env, req.Labels, prevstate.KeySchema(req.Attributes, schema), extensionsFromOpts(opts), opts.instance(), OutputLabels(req, schema, opts.instance()))
	}
}

// computeCrosstabFusion answers PredictResult.CrosstabFusable /
// CrosstabFusionReasons: nil without a crosstab spec, false with
// CrosstabFusionDisabledReason when the instance switched fusion off
// (the engine then never consults the rule), else CrosstabFusion.
func computeCrosstabFusion(req *types.Request, schema *encoding.Schema, opts *PredictOptions) (*bool, []string) {
	if req == nil || req.Crosstab == nil {
		return nil, nil
	}
	if opts.DisableCrosstabFusion {
		fused := false
		return &fused, []string{CrosstabFusionDisabledReason}
	}
	fused, reasons := CrosstabFusion(req, schema, opts.Extensions, opts.instance())
	return &fused, reasons
}

// HoldoutStreamableReason is predict's Streamable reason for a
// regression whose holdout predicate (types.RegressionSpec.Holdout)
// forces the buffered path: its held-out rows are scored after the fit,
// a second pass the single-pass engine has no form of.
func HoldoutStreamableReason(t types.RegressionType) string {
	return "regression " + string(t) + " holdout requires the buffered path (the held-out rows are scored after the fit)"
}

// computeStreamable reports whether the request can execute via the
// streaming Process path. Mirrors processing.canStream's gates but reads
// from the types.* Streamable() methods instead of constructing operators
// (predict cannot import processing).
//
// Returns (true, nil) when streamable; (false, reasons) listing every
// gate that blocks streaming. The reasons slice is intentionally
// human-readable so it can land in the envelope unchanged.
func computeStreamable(req *types.Request, schema *encoding.Schema, opts *PredictOptions) (bool, []string) {
	var reasons []string

	// Regression streamability defers to RegressionSpec.Streamable():
	// it folds the type-level family check, the modifier downgrade
	// (Resample / Selection force buffered), and the Phase-by-Phase
	// implementation status gate (today only unpenalized REG_OLS streams).
	// Phases 2-5 widen the gate as their engines land — predict and the
	// processor share the same source of truth via this method.
	for _, reg := range req.Regressions {
		if reg == nil {
			continue
		}
		if !regressionSpecStreamable(opts, reg) {
			if reg.Covariance.Robust() {
				reasons = append(reasons, "regression "+string(reg.Type)+" covariance "+string(reg.Covariance.Type)+" requires the buffered path (a second pass for residuals and leverage)")
			} else if reg.Holdout != "" {
				reasons = append(reasons, HoldoutStreamableReason(reg.Type))
			} else {
				reasons = append(reasons, "regression "+string(reg.Type)+" requires the buffered path under the current spec")
			}
		}
	}
	// Regression slots do not compose with groupers, features or
	// two-pass attributes in the streaming path; beside tier-1 row tests
	// they stream (U48). Mirror the runtime gate so predict stays
	// parity-true.
	if len(req.Regressions) > 0 {
		if len(req.Groups) > 0 {
			reasons = append(reasons, "regression with groupers runs via the buffered path")
		}
		if len(req.Features) > 0 {
			reasons = append(reasons, "regression with features runs via the buffered path")
		}
		if tp := firstTwoPassAttribute(req, schema, opts); tp != "" {
			reasons = append(reasons, "regression with two-pass attribute "+string(tp)+" runs via the buffered path")
		}
	}

	// Crosstab dispatch overrides the regular streamable gate: when set,
	// crosstabStreamableReasons is the authoritative list (matrix shape,
	// margins, normalization, or nested axes all force buffered).
	if req.Crosstab != nil {
		reasons = append(reasons, crosstabStreamableReasons(req.Crosstab)...)
		// Crosstab does not require the standard "at least one
		// aggregation" gate — the cell aggregation lives on
		// req.Crosstab.Cell, not req.Aggregations.
		return len(reasons) == 0, reasons
	}

	// Matrix slots stream on the ungrouped and grouped paths (mirrors
	// processing.canStream): an unknown or hidden type, a buffered spec
	// (types.MatrixSpec.Streamable — the type folded with its params,
	// e.g. a rank method) or a two-pass attribute route the request
	// buffered.
	if len(req.Matrices) > 0 {
		for _, m := range req.Matrices {
			// An extension operator's declared tier, else the routed
			// built-in spec-level rule (processing.canStream's twin).
			if !matrixSpecStreamable(opts.instance(), extensionsFromOpts(opts), m) {
				reasons = append(reasons, "matrix "+string(m.Type)+" requires the buffered path")
			}
		}
		if tp := firstTwoPassAttribute(req, schema, opts); tp != "" {
			reasons = append(reasons, "matrices with two-pass attribute "+string(tp)+" run via the buffered path")
		}
	}

	// A request whose only per-record work is matrices, tier-1 tests or
	// regressions streams (processing.hasStreamingWork's twin); one
	// with none of them buffers.
	if len(req.Aggregations) == 0 && len(req.Matrices) == 0 && len(req.Tests) == 0 && len(req.Regressions) == 0 {
		reasons = append(reasons, "no aggregations, matrices, tests or regressions: streaming path requires per-record work")
	}
	for _, grp := range req.Groups {
		if !streamableWithOverlay(opts, "grouper", string(grp.Type), opRoute(opts.instance(), grp.Type).Streamable()) {
			reasons = append(reasons, "group "+string(grp.Type)+" requires the buffered path")
		}
	}
	for _, attr := range req.Attributes {
		if !streamableWithOverlay(opts, "attribute", string(attr.Type), opRoute(opts.instance(), attr.Type).Streamable()) {
			if opRoute(opts.instance(), attr.Type) == types.ATTR_PREVIOUS_STATE {
				reasons = append(reasons, "attribute "+string(attr.Type)+" orders every record of each subject, so it runs via the buffered path")
				continue
			}
			reasons = append(reasons, "attribute "+string(attr.Type)+" requires a full pass for population stats")
		}
	}
	// Two-pass attributes do not yet compose with grouped or feature
	// streaming (mirrors processing.canStream's combination gate).
	if tp := firstTwoPassAttribute(req, schema, opts); tp != "" && (len(req.Groups) > 0 || len(req.Features) > 0) {
		reasons = append(reasons, "two-pass attribute "+string(tp)+" with groupers or features runs via the buffered path")
	}
	if len(req.Windows) > 0 {
		reasons = append(reasons, "windows run over the post-aggregate row set")
	}

	for _, agg := range req.Aggregations {
		if !streamableWithOverlay(opts, "aggregator", string(agg.Type), opRoute(opts.instance(), agg.Type).Streamable()) {
			reasons = append(reasons, "aggregation "+string(agg.Type)+" is not streamable")
			continue
		}
		// Built-in decimal field aggregation routes through
		// AggregateDecimalField to preserve precision; the streaming
		// numeric fold loses it. An extension aggregator reads the
		// decimal itself (DecimalValue), so its declared Streamable flag
		// (checked above) decides — mirrors processing.canStream.
		if schema != nil && !extensionsFromOpts(opts).HasAggregator(string(agg.Type)) {
			if f := schema.Field(agg.Field); f != nil && f.Type.IsDecimal() {
				reasons = append(reasons, "aggregation on decimal field "+agg.Field+" forces buffered path")
			}
		}
	}

	for _, feat := range req.Features {
		if !streamableWithOverlay(opts, "feature", string(feat.Type), opRoute(opts.instance(), feat.Type).Streamable()) {
			reasons = append(reasons, "feature "+string(feat.Type)+" is not streamable")
			continue
		}
		// A global-pass pre-pass reading an earlier feature's column
		// routes buffered (internal/featstream, the runtime's verdict).
		if ok, reason := featstream.Streamable(&types.Feature{Type: opRoute(opts.instance(), feat.Type), Field: feat.Field, Params: feat.Params}, schema); !ok {
			reasons = append(reasons, reason)
		}
	}

	reasons = append(reasons, streamableTestReasons(req, schema, opts)...)

	return len(reasons) == 0, reasons
}

// predictFromBytes is the in-package byte-slice convenience over
// Predict. The public byte-level entry point is the instance method
// (*pulse.Pulse).PredictBytes, which fills PredictOptions from the
// instance.
func predictFromBytes(data []byte, req *types.Request, opts *PredictOptions) *descriptor.Envelope {
	return Predict(bytes.NewReader(data), req, opts)
}

// predictArchive runs the predict pipeline against a Pulse shard
// archive. The canonical _schema.pulse entry supplies the schema the
// request validates against; per-shard headers contribute the
// cumulative RecordCount and Shards listing. Streamability is computed
// from the same gates the single-file path uses — a shard archive
// inherits the streamability of its request against the canonical
// schema, not of its shape (see PredictResult.Streamable docs).
func predictArchive(src archiveSource, req *types.Request, opts *PredictOptions) *descriptor.Envelope {
	arch, err := encx.OpenArchive(src.r, src.size)
	if err != nil {
		result := &descriptor.PredictResult{
			Valid:                    false,
			Request:                  req,
			Shards:                   []descriptor.ShardInfo{},
			DefaultsApplied:          []descriptor.DefaultApplied{},
			Aggregations:             []descriptor.AggregationPredict{},
			Groups:                   []descriptor.GroupPredict{},
			Filterers:                []descriptor.FiltererPredict{},
			Suggestions:              []descriptor.Suggestion{},
			OverlaysApplied:          []descriptor.OverlayAppliedDescriptor{},
			OverlaysSchemaDivergence: []descriptor.SlotPair{},
			OverlayCost:              map[string]float64{},
			TimeZones:                []descriptor.ResolvedZone{},
		}
		env := descriptor.NewEnvelope(result)
		env.AddError(string(errors.PULSE_ARCHIVE_CORRUPT), "invalid pulse shard archive: "+err.Error(), nil)
		return env
	}

	rc, oerr := arch.Open(encx.ReservedSchemaName)
	if oerr != nil {
		result := &descriptor.PredictResult{
			Valid:                    false,
			Request:                  req,
			Shards:                   []descriptor.ShardInfo{},
			DefaultsApplied:          []descriptor.DefaultApplied{},
			Aggregations:             []descriptor.AggregationPredict{},
			Groups:                   []descriptor.GroupPredict{},
			Filterers:                []descriptor.FiltererPredict{},
			Suggestions:              []descriptor.Suggestion{},
			OverlaysApplied:          []descriptor.OverlayAppliedDescriptor{},
			OverlaysSchemaDivergence: []descriptor.SlotPair{},
			OverlayCost:              map[string]float64{},
			TimeZones:                []descriptor.ResolvedZone{},
		}
		env := descriptor.NewEnvelope(result)
		env.AddError(string(errors.PULSE_SHARD_MISSING),
			"archive missing reserved schema entry "+encx.ReservedSchemaName+": "+oerr.Error(), nil)
		return env
	}

	// Materialize the canonical schema-doc payload so we can feed
	// Predict (which expects a ReadSeeker over header + schema). We
	// only need the leading header + schema block; the trailing SHRD
	// extension on _schema.pulse is harmless to Predict because
	// Predict only reads ReadHeader + ReadSchema, then validates the
	// request against schema-derived state.
	canonical, rerr := io.ReadAll(rc)
	_ = rc.Close()
	if rerr != nil {
		result := &descriptor.PredictResult{
			Valid:                    false,
			Request:                  req,
			Shards:                   []descriptor.ShardInfo{},
			DefaultsApplied:          []descriptor.DefaultApplied{},
			Aggregations:             []descriptor.AggregationPredict{},
			Groups:                   []descriptor.GroupPredict{},
			Filterers:                []descriptor.FiltererPredict{},
			Suggestions:              []descriptor.Suggestion{},
			OverlaysApplied:          []descriptor.OverlayAppliedDescriptor{},
			OverlaysSchemaDivergence: []descriptor.SlotPair{},
			OverlayCost:              map[string]float64{},
			TimeZones:                []descriptor.ResolvedZone{},
		}
		env := descriptor.NewEnvelope(result)
		env.AddError(string(errors.ENCODING_INVALID),
			"reading canonical schema entry "+encx.ReservedSchemaName+": "+rerr.Error(), nil)
		return env
	}

	// The cumulative record count bounds the return size estimates;
	// unknown when a shard header cannot be peeked (reported below).
	var records *int64
	var shardRecords []int64
	var total int64
	known := true
	for _, entry := range arch.Entries() {
		if entry.Name == encx.ReservedSchemaName {
			continue
		}
		count, perr := arch.PeekShardRecordCount(entry.Name)
		if perr != nil {
			known = false
			break
		}
		total += count
		shardRecords = append(shardRecords, count)
	}
	if known {
		records = &total
	} else {
		shardRecords = nil
	}

	env := predictSingle(bytes.NewReader(canonical), req, opts, records, shardRecords, arch.DirectoryBytes())
	result, _ := env.Data.(*descriptor.PredictResult)
	if result == nil {
		return env
	}

	// Enumerate non-reserved entries in central-directory order and
	// sum per-shard record counts via PeekShardRecordCount.
	var cumulative int64
	shards := make([]descriptor.ShardInfo, 0)
	for _, entry := range arch.Entries() {
		if entry.Name == encx.ReservedSchemaName {
			continue
		}
		count, perr := arch.PeekShardRecordCount(entry.Name)
		if perr != nil {
			env.AddError(string(errors.PULSE_SHARD_HEADER_INVALID),
				"peeking record count for shard "+entry.Name+": "+perr.Error(),
				map[string]any{"entry": entry.Name})
			result.Valid = false
			continue
		}
		shards = append(shards, descriptor.ShardInfo{
			Filename:    entry.Name,
			RecordCount: count,
		})
		cumulative += count
	}
	result.Shards = shards
	result.RecordCount = cumulative
	return env
}

// validateRequestFields reports the schema-typed checks on the request's
// slots (model-error aggregators, regression and attribute shape).
// Whether a referenced name exists at all is FieldRefRefusals'
// judgement, not this one's.
func validateRequestFields(env *descriptor.Envelope, req *types.Request, schema *encoding.Schema, projected map[string]bool, opts *PredictOptions) {
	// A numeric aggregation on a categorical field (its dictionary
	// index) is the accepted-types table's PULSE_FIELD_TYPE_UNSUPPORTED,
	// reported by validateFieldAcceptance at every aggregation slot.

	// Unknown names are the field-reference rule's refusals
	// (FieldRefRefusals, reported right after zone resolution); the
	// checks below judge only fields the schema carries.

	// A decimal128 field on a built-in aggregator with no decimal arm is
	// the accepted-types table's decimal rule, reported once (the
	// runtime's PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL refusal) by
	// validateFieldAcceptance at every aggregation slot.

	// Model-error aggregators (AGG_MAPE / AGG_RMSE / AGG_MAE): the
	// runtime constructor's refusals, re-derived from the schema.
	validateModelErrorAggs(env, req, schema, opts)

	// AGG_AUC: the shared configuration rules (internal/aucspec).
	validateAUCAggs(env, req, schema, opts)

	// Check regression slots. Phase 0 validates structural shape only
	// (known type, target + predictors named, fields exist); deeper
	// runtime checks (n ≥ p + 1, family/link compatibility) land with
	// the engines in Phases 1–4.
	validateRegressions(env, req, schema, projected, opts.Instance)

	// ATTR_CODE_IN: the runtime constructor's refusals, re-derived from
	// the schema, and the absent-categorical-code warning.
	validateCodeIn(env, req, schema, projected, opts)

	// ATTR_PREVIOUS_STATE: the shared slot rule (internal/prevstate).
	validatePreviousState(env, req, schema, opts)

	// ATTR_FORMULA: the runtime constructor's empty-expression refusal
	// (an expression in params.expression is not read).
	validateFormulaExpressions(env, req, opts)

	// Check attribute fields.
	for _, attr := range req.Attributes {
		// Removed-type sentinel: ATTR_RANK was retired in favor of WIN_RANK.
		// Surface a migration hint instead of the generic registry-miss error
		// — unless the instance hides WIN_RANK: then the hint would advertise
		// an operator it does not offer, so ATTR_RANK takes the generic miss.
		if attr.Type == "ATTR_RANK" && !opts.instance().Hidden(string(types.WIN_RANK)) {
			env.AddError(
				string(errors.SERVICE_VALIDATION),
				"ATTR_RANK was removed in this release; use WIN_RANK with empty partition_by and a single ASC order_by on the same field",
				map[string]any{"attribute": "ATTR_RANK", "replacement": "WIN_RANK"},
			)
			continue
		}
		// Regression attributes (ATTR_REG_FITTED / RESIDUAL / LEVERAGE) carry
		// their own Target + Predictors instead of a single Field. Validate
		// those references here; the factory rechecks numeric-type
		// compatibility at construction time.
		if rt := opRoute(opts.Instance, attr.Type); rt == types.ATTR_REG_FITTED || rt == types.ATTR_REG_RESIDUAL || rt == types.ATTR_REG_LEVERAGE {
			if model.FitReferenced(rt, attr) {
				// params.fit: the bound fit supplies target and
				// predictors (the field-reference pass judges it).
				continue
			}
			if attr.Target == "" {
				env.AddError(
					string(errors.SERVICE_VALIDATION),
					string(attr.Type)+" requires Target",
					map[string]any{"attribute": string(attr.Type)},
				)
			}
			if len(attr.Predictors) == 0 {
				env.AddError(
					string(errors.SERVICE_VALIDATION),
					string(attr.Type)+" requires at least one predictor",
					map[string]any{"attribute": string(attr.Type)},
				)
			}
			continue
		}
	}
}

// projectAttributeOutputs adds each attribute's output label to the
// projected column set. The label rule mirrors processor.go's
// applyAttributes / defaultAttributeLabel: an explicit label wins;
// otherwise the default is "<TYPE>_<field>", with the regression
// attributes (ATTR_REG_FITTED / RESIDUAL / LEVERAGE) substituting
// Target for Field since they do not carry a single source field.
//
// Duplicated locally rather than imported from internal/processing/ because
// descriptor must not import the processing package (predict is a
// no-execute path).
func projectAttributeOutputs(req *types.Request, projected map[string]bool, inst *InstanceSnapshot) {
	for _, attr := range req.Attributes {
		label := attr.Label
		if label == "" {
			label = attributeDefaultLabel(attr, inst)
		}
		projected[label] = true
	}
}

// attributeDefaultLabel mirrors processing.defaultAttributeLabel so
// predict produces the same projected column names process would emit.
// The duplication is intentional — descriptor/predict.go must not
// import internal/processing/. A type inst hides takes the
// never-registered label rule.
func attributeDefaultLabel(attr *types.Attribute, inst *InstanceSnapshot) string {
	if label, ok := vectors.VectorAttributeLabel(opRoute(inst, attr.Type), attr); ok {
		return label
	}
	switch route := opRoute(inst, attr.Type); route {
	case types.ATTR_REG_FITTED, types.ATTR_REG_RESIDUAL, types.ATTR_REG_LEVERAGE:
		if model.FitReferenced(route, attr) {
			return model.DefaultFitLabel(attr)
		}
		return string(attr.Type) + "_" + attr.Target
	case types.ATTR_REG_PREDICT:
		return model.DefaultScoreLabel(attr)
	}
	return string(attr.Type) + "_" + attr.Field
}

// validateDescriptionQuality emits warnings for fields with low-quality descriptions.
func validateDescriptionQuality(env *descriptor.Envelope, schema *encoding.Schema, opts *PredictOptions) {
	for _, f := range schema.Fields {
		if isLowQualityDescription(f.Description) {
			entry := &descriptor.EnvelopeEntry{
				Code:    string(errors.PULSE_FIELD_DESCRIPTION_LOW_QUALITY),
				Message: "field " + f.Name + " has a low-quality description",
				Details: map[string]any{"field": f.Name, "description": f.Description},
			}
			if opts.Strict {
				env.Errors = append(env.Errors, entry)
			} else {
				env.Warnings = append(env.Warnings, entry)
			}
		}
	}
}

// isLowQualityDescription checks if a description is empty or too
// short/generic — the shared encx.IsLowQualityDescription rule.
func isLowQualityDescription(desc string) bool {
	return encx.IsLowQualityDescription(desc)
}

// overlayCostStreamable is the OverlayCost score assigned to overlay
// kinds whose streamability flag is true — the kind folds inside the
// existing streaming Process pass via a kind-specific accumulator
// carried alongside the per-group reducers, so the marginal cost is
// effectively a few f64 adds per record. The value is a rough record-
// count multiplier per PRD §I-FR-I3; 0.05 ≈ "5% extra work" relative
// to a fresh pass over the source records. The streamable SERIES-host
// trio (INDEX_VS_TOTAL, ZSCORE_VS_TOTAL, the SERIES dispatch of
// SHARE_OF_TOTAL) lands at this value today.
const overlayCostStreamable = 0.05

// overlayCostBuffered is the OverlayCost score assigned to overlay
// kinds whose streamability flag is false — the kind requires the
// fully materialised host payload (the buffered crosstab matrix, the
// finalized per-group SeriesPayload for the sibling family, etc.) and
// folds at the post-host exit, traversing the payload a second time.
// The value is a rough record-count multiplier per PRD §I-FR-I3; 1.0
// ≈ "one extra pass" over the materialised host structure. The
// MATRIX-host catalog plus the SIBLING family (DELTA_VS_SIBLING,
// INDEX_VS_SIBLING) lands at this value today.
const overlayCostBuffered = 1.0

// overlayCostForKind returns the OverlayCost multiplier for a given
// overlay kind. The dispatch reads types.OverlayStreamable(kind) —
// streamable kinds get overlayCostStreamable, buffered kinds get
// overlayCostBuffered. The static streamability table in
// types/overlay_streamability.go is the single source of truth, so a
// kind that flips streamable automatically flips its cost here.
// Unknown kinds (absent from the table) fall through to the buffered
// score — the validator already surfaces PULSE_OVERLAY_KIND_UNKNOWN
// for the same condition; the cost score stays maximally conservative.
func overlayCostForKind(kind types.OverlayKind) float64 {
	streamable, known := types.OverlayStreamable(kind)
	if !known || !streamable {
		return overlayCostBuffered
	}
	return overlayCostStreamable
}

// populateOverlayDescriptors fills PredictResult.OverlaysApplied and
// PredictResult.OverlayCost from req.Overlays. Per kind-catalog-v1 PRD
// §I-FR-I3 the predict surface is `OverlaysApplied +
// OverlaysSchemaDivergence + OverlayCost`. It emits one
// OverlayAppliedDescriptor per spec (Name + Kind + Scope + Streamable)
// and a per-kind OverlayCost multiplier keyed by the spec name. The
// divergence slot stays empty on the Request-only Predict surface.
//
// The streamability echo and the cost score both consult
// types.OverlayStreamable(kind) — the static table in
// types/overlay_streamability.go is the single source of truth, so a
// kind that flips streamable automatically flips here.
func populateOverlayDescriptors(result *descriptor.PredictResult, req *types.Request, opts *PredictOptions) {
	if result == nil || req == nil || len(req.Overlays) == 0 {
		return
	}
	result.OverlaysApplied, result.OverlayCost = appendOverlayDescriptors(
		result.OverlaysApplied, result.OverlayCost, req.Overlays, opts,
	)
}

// aggregatorComponentSchemaIndex returns a {operator name → ComponentSchema}
// lookup over the built-in aggregator capability table. The map is the
// authoritative no-execute source predict reads at slot-population time
// — the universal-floor keys are already prepended inside aggSchema, so
// every entry mirrors the same shape the manifest projects under
// Manifest.ComponentsSchemas.Aggregators.
//
// When opts carries an ExtensionsSnapshot, extension aggregator entries
// from snap.ComponentSchemas are merged in afterwards so a slot that
// references an extension operator sees the embedder-declared schema
// instead of the empty default. Built-ins are not overwritten — the
// built-in capability table wins on collision (which should never
// happen because extension names cannot collide with built-ins, per
// validateExtensions).
//
// The built-in map is built once per weighting variant
// (builtinAggregatorSchemaIndex, #390) — as the instance manifest, no
// weighted floor keys when capability:weighting is hidden. With no
// extension aggregator schema to merge the SHARED map is returned:
// READ-ONLY, as is every Keys slice in it. Otherwise a copy carries the
// extension entries.
func aggregatorComponentSchemaIndex(opts *PredictOptions) map[string]descriptor.ComponentSchema {
	base := builtinAggregatorSchemaIndex(opts.instance().Enabled(featWeighting))
	snap := extensionsFromOpts(opts)
	if snap == nil || len(snap.ComponentSchemas) == 0 {
		return base
	}
	var out map[string]descriptor.ComponentSchema
	for _, m := range snap.Aggregators {
		if _, dup := base[m.Name]; dup {
			continue
		}
		if schema, ok := snap.ComponentSchemas[m.Name]; ok {
			if out == nil {
				out = maps.Clone(base)
			}
			out[m.Name] = schema
		}
	}
	if out == nil {
		return base
	}
	return out
}

// grouperComponentSchemaIndex returns a {operator name → ComponentSchema}
// lookup over the built-in grouper capability table. The map is the
// authoritative no-execute source predict reads at slot-population time
// — the universal-floor keys ({total_n, n_null}) are already prepended
// inside groupSchema, so every entry mirrors the same shape the manifest
// projects under Manifest.ComponentsSchemas.Groupers.
//
// Extension groupers from opts.Extensions.ComponentSchemas are merged
// in afterwards on the same contract as aggregatorComponentSchemaIndex.
func grouperComponentSchemaIndex(opts *PredictOptions) map[string]descriptor.ComponentSchema {
	caps := grouperCapabilities()
	out := make(map[string]descriptor.ComponentSchema, len(caps))
	for _, op := range caps {
		out[op.Name] = op.ComponentSchema
	}
	snap := extensionsFromOpts(opts)
	if snap != nil && len(snap.ComponentSchemas) > 0 {
		for _, m := range snap.Groupers {
			if _, dup := out[m.Name]; dup {
				continue
			}
			if schema, ok := snap.ComponentSchemas[m.Name]; ok {
				out[m.Name] = schema
			}
		}
	}
	return out
}

// populateGroupPredicts fills PredictResult.Groups with one GroupPredict
// descriptor per req.Groups slot, in matching order. The per-slot
// ComponentSchema is looked up from the built-in grouper capability
// table — the manifest snapshot's single source of truth (same
// projection ComponentsSchemas.Groupers uses). Extension groupers that
// have not yet declared a ComponentSchema produce an empty
// ComponentSchema and BufferedComponents=false; the universal-floor
// populator still attaches {"total_n", "n_null"} at runtime.
//
// BufferedComponents is true iff the resolved ComponentSchema's
// Mergeability is descriptor.None. Of the seven registered groupers
// today, GROUP_QUANTILE is the canonical None — quantile cutoffs need
// the sorted full input. The flag advertises streaming behaviour but
// does NOT flip PredictResult.Streamable.
//
// Predict stays no-execute: this helper reads only the static
// capabilities table — no `internal/service/` or `internal/processing/` imports.
func populateGroupPredicts(result *descriptor.PredictResult, req *types.Request, opts *PredictOptions) {
	if result == nil || req == nil {
		return
	}
	if len(req.Groups) == 0 {
		// Empty-but-not-nil — keep JSON shape stable.
		result.Groups = []descriptor.GroupPredict{}
		return
	}
	idx := grouperComponentSchemaIndex(opts)
	out := make([]descriptor.GroupPredict, 0, len(req.Groups))
	for _, grp := range req.Groups {
		if grp == nil {
			continue
		}
		schema := idx[string(opRoute(opts.instance(), grp.Type))]
		out = append(out, descriptor.GroupPredict{
			Type:               grp.Type,
			Field:              grp.Field,
			ComponentSchema:    schema,
			BufferedComponents: schema.Mergeability == descriptor.None,
		})
	}
	result.Groups = out
}

// populateAggregationPredicts fills PredictResult.Aggregations with one
// AggregationPredict descriptor per req.Aggregations slot, in matching
// order. The per-slot ComponentSchema is looked up from the built-in
// aggregator capability table (the manifest snapshot's single source of
// truth — same projection ComponentsSchemas.Aggregators uses). When the
// slot references an extension operator that the snapshot has not yet
// declared a schema for, the descriptor surfaces an empty
// ComponentSchema and BufferedComponents=false; the universal-floor
// populator still attaches {"n", "n_null"} at runtime.
//
// BufferedComponents is true iff the resolved ComponentSchema's
// Mergeability is descriptor.None — meaning the components map cannot
// be reconstructed from per-chunk partials and the orchestrator emits
// the Components block only on the terminal buffered flush. The flag
// advertises streaming behaviour but does NOT flip PredictResult.Streamable
// (the data slice still streams).
//
// Predict stays no-execute: this helper reads only the static
// capabilities table — no `internal/service/` or `internal/processing/` imports.
func populateAggregationPredicts(result *descriptor.PredictResult, req *types.Request, schema *encoding.Schema, opts *PredictOptions) {
	if result == nil || req == nil {
		return
	}
	if len(req.Aggregations) == 0 {
		// Empty-but-not-nil — keep JSON shape stable.
		result.Aggregations = []descriptor.AggregationPredict{}
		return
	}
	idx := aggregatorComponentSchemaIndex(opts)
	out := make([]descriptor.AggregationPredict, 0, len(req.Aggregations))
	for _, agg := range req.Aggregations {
		if agg == nil {
			continue
		}
		cs := idx[string(opRoute(opts.instance(), agg.Type))]
		// The index is shared (builtinAggregatorSchemaIndex): the
		// result's consumer may mutate its schema, so it gets its own
		// Keys.
		cs.Keys = slices.Clone(cs.Keys)
		ap := descriptor.AggregationPredict{
			Type:               agg.Type,
			Field:              agg.Field,
			Label:              agg.Label,
			ComponentSchema:    cs,
			BufferedComponents: cs.Mergeability == descriptor.None,
		}
		// An expanding vector aggregation (params.expand) names the
		// per-element columns it writes in place of its label.
		if kinds := aggregationKinds(opts.instance(), extensionsFromOpts(opts)); vectors.AggregationExpands(agg, kinds) {
			if cols, ok := vectors.AggregationOutputColumns(req, agg, schema, kinds); ok {
				ap.Columns = cols
			}
		}
		out = append(out, ap)
	}
	result.Aggregations = out
}

// filtererComponentSchemaIndex returns a {operator name → ComponentSchema}
// lookup over the built-in filterer capability table. The map is the
// authoritative no-execute source predict reads at slot-population
// time — the universal-floor keys ({n_in, n_out, n_null_input}) are
// already prepended inside filterSchema, so every entry mirrors the
// same shape the manifest projects under
// Manifest.ComponentsSchemas.Filterers.
//
// Extension filterers from opts.Extensions.ComponentSchemas are merged
// in afterwards on the same contract as aggregatorComponentSchemaIndex.
func filtererComponentSchemaIndex(opts *PredictOptions) map[string]descriptor.ComponentSchema {
	caps := filtererCapabilities()
	out := make(map[string]descriptor.ComponentSchema, len(caps))
	for _, op := range caps {
		out[op.Name] = op.ComponentSchema
	}
	snap := extensionsFromOpts(opts)
	if snap != nil && len(snap.ComponentSchemas) > 0 {
		for _, m := range snap.Filterers {
			if _, dup := out[m.Name]; dup {
				continue
			}
			if schema, ok := snap.ComponentSchemas[m.Name]; ok {
				out[m.Name] = schema
			}
		}
	}
	return out
}

// populateFiltererPredicts fills PredictResult.Filterers with one
// FiltererPredict descriptor per req.Filterers slot, in matching order.
// The per-slot ComponentSchema is looked up from the built-in filterer
// capability table — the manifest snapshot's single source of truth
// (same projection ComponentsSchemas.Filterers uses). Extension
// filterers that have not yet declared a ComponentSchema produce an
// empty ComponentSchema and BufferedComponents=false; the universal-
// floor populator still attaches {n_in, n_out, n_null_input} at
// runtime.
//
// BufferedComponents is true iff the resolved ComponentSchema's
// Mergeability is descriptor.None. In v1 every built-in filterer is
// Mergeable (counter triples fold trivially via integer addition), so
// the flag is always false for built-ins; the surface exists for
// future per-filter specifics that may downgrade individual entries.
// The flag advertises streaming behaviour but does NOT flip
// PredictResult.Streamable.
//
// Predict stays no-execute: this helper reads only the static
// capabilities table — no `internal/service/` or `internal/processing/` imports.
func populateFiltererPredicts(result *descriptor.PredictResult, req *types.Request, opts *PredictOptions) {
	if result == nil || req == nil {
		return
	}
	if len(req.Filterers) == 0 {
		// Empty-but-not-nil — keep JSON shape stable.
		result.Filterers = []descriptor.FiltererPredict{}
		return
	}
	idx := filtererComponentSchemaIndex(opts)
	out := make([]descriptor.FiltererPredict, 0, len(req.Filterers))
	for _, fil := range req.Filterers {
		if fil == nil {
			continue
		}
		schema := idx[string(opRoute(opts.instance(), fil.Type))]
		out = append(out, descriptor.FiltererPredict{
			Type:               fil.Type,
			Field:              fil.Field,
			ComponentSchema:    schema,
			BufferedComponents: schema.Mergeability == descriptor.None,
		})
	}
	result.Filterers = out
}

// appendOverlayDescriptors is the shared per-spec emitter used by both
// the Request-host predict surface (PredictResult) and the FACET-host
// predict surface (FacetValidationResult). Each spec produces one
// OverlayAppliedDescriptor entry plus one entry in the cost map keyed by
// the spec's renderer-facing name (or a synthesised default when empty).
// Streamability + cost both consult types.OverlayStreamable(kind) so the
// static streamability table stays the single source of truth.
//
// Returns the (possibly grown) descriptor slice and cost map. The cost
// map is mutated in place when non-nil; callers must seed an empty map
// on the destination struct before invoking this helper so the JSON
// output stays empty-but-not-nil (matching the PredictResult contract).
//
// Streamability, shape and cost key on the kind's route
// (PredictOptions.overlayRoute), so a kind the instance hides is
// described exactly as a kind not in the catalog; Name and Kind keep
// the authored kind. opts may be nil.
func appendOverlayDescriptors(
	descriptors []descriptor.OverlayAppliedDescriptor, costs map[string]float64,
	specs []types.OverlaySpec, opts *PredictOptions,
) ([]descriptor.OverlayAppliedDescriptor, map[string]float64) {
	for i := range specs {
		spec := &specs[i]
		name := overlayDescriptorName(spec)
		route := opts.overlayRoute(spec.Kind)
		streamable, _ := types.OverlayStreamable(route)
		descriptors = append(descriptors, descriptor.OverlayAppliedDescriptor{
			Name:       name,
			Kind:       spec.Kind,
			Scope:      spec.Scope,
			Shape:      resolveOverlayShape(route, spec.Scope),
			Ref:        resolveOverlayRef(spec),
			Streamable: streamable,
		})
		// Per-kind cost multiplier — streamable kinds carry
		// overlayCostStreamable (~5% extra work) because they fold
		// inside the streaming Process pass; buffered kinds carry
		// overlayCostBuffered (~one extra payload traversal) because
		// they re-walk the materialised host structure at the post-
		// host exit.
		if costs != nil {
			costs[name] = overlayCostForKind(route)
		}
	}
	return descriptors, costs
}

// resolveOverlayShape returns the OverlayShape the layer will carry
// for the given (Kind, Scope) pair. Reads the per-kind capability
// table (overlayCapabilityFor) as the single source of truth.
//
// Resolution rules:
//
//   - Single-shape kinds (the bulk of the catalog) return the kind's
//     only declared shape directly.
//   - Dual-shape kinds (OVERLAY_INDEX_VS_REF / OVERLAY_DELTA_VS_REF /
//     OVERLAY_PANEL_INDEX_VS_REF on Compose hosts) disambiguate via
//     Scope: OverlayScopeCell ⇒ matrix, OverlayScopeGroup ⇒ series,
//     OverlayScopeTotal ⇒ scalar.
//   - OVERLAY_FORMULA is a tri-shape kind whose shape is selected by
//     Scope (cell ⇒ matrix, group ⇒ series, total ⇒ scalar) per the
//     per-shape FORMULA binder.
//   - Whole-chain kinds (OVERLAY_INDEX_VS_STAGE / OVERLAY_DELTA_VS_STAGE)
//     inherit the target stage's host shape at runtime — Predict cannot
//     resolve a single shape without inspecting the chain, so the
//     resolver returns "" (the descriptor surfaces an empty Shape
//     field via the omitempty rule).
//   - Unknown kinds (absent from the catalog) return "" — the
//     validator surfaces PULSE_OVERLAY_KIND_UNKNOWN for the same
//     condition.
func resolveOverlayShape(kind types.OverlayKind, scope types.OverlayScope) types.OverlayShape {
	cap := overlayCapabilityFor(kind)
	if len(cap.Shapes) == 0 {
		return ""
	}
	if len(cap.Shapes) == 1 {
		return cap.Shapes[0]
	}
	// Multi-shape kind — disambiguate via Scope. The capability
	// table declares every shape the kind may emit; the scope-to-
	// shape mapping is uniform across the catalog:
	//   cell  ⇒ matrix
	//   group ⇒ series
	//   total ⇒ scalar
	//   row   ⇒ series
	//   column⇒ series
	//   matrix⇒ matrix
	scopeShape := scopeToShape(scope)
	if scopeShape == "" {
		return ""
	}
	for _, s := range cap.Shapes {
		if s == scopeShape {
			return s
		}
	}
	// Scope incompatible with any declared shape — the per-kind
	// validator (PULSE_OVERLAY_SCOPE_UNSUPPORTED) catches this; the
	// descriptor surfaces "" so renderers can branch on the gap.
	return ""
}

// scopeToShape maps an OverlayScope to its canonical OverlayShape per
// the catalog convention. Returns "" for unknown scopes.
func scopeToShape(scope types.OverlayScope) types.OverlayShape {
	switch scope {
	case types.OverlayScopeCell:
		return types.OverlayShapeMatrix
	case types.OverlayScopeMatrix:
		return types.OverlayShapeMatrix
	case types.OverlayScopeRow, types.OverlayScopeColumn, types.OverlayScopeGroup:
		return types.OverlayShapeSeries
	case types.OverlayScopeTotal:
		return types.OverlayShapeScalar
	}
	return ""
}

// resolveOverlayRef returns a renderer-friendly string describing the
// resolved OverlayRef discriminated union variant. Format follows the
// per-family conventions documented on OverlayAppliedDescriptor.Ref.
//
// Returns "" for:
//   - Implicit-margin kinds (CHISQ_*, FISHER_EXACT_CELL, INDEX_VS_TOTAL,
//     ZSCORE_VS_TOTAL, SHARE_OF_TOTAL, FORMULA) that leave Ref empty.
//   - COMPOSE-only kinds (OVERLAY_INDEX_VS_REF / OVERLAY_DELTA_VS_REF /
//     OVERLAY_PROP_Z_CELL / OVERLAY_PROP_Z_PANEL / OVERLAY_PANEL_INDEX_VS_REF
//     / OVERLAY_T_CELL / OVERLAY_T_VS_REF / OVERLAY_CHISQ_VS_REF /
//     OVERLAY_RANK) whose reference + target slots ride on the
//     ComposeOverlaySpec slot-label pair, not on Ref.
func resolveOverlayRef(spec *types.OverlaySpec) string {
	if spec == nil {
		return ""
	}
	ref := spec.Ref
	switch {
	case ref.Margin != nil:
		return "margin:" + string(ref.Margin.Axis)
	case ref.Sibling != nil:
		return "sibling:" + ref.Sibling.Field + "=" + ref.Sibling.Value
	case ref.BaselineIndex != nil:
		return "baseline_index:" + itoa(ref.BaselineIndex.Position)
	case ref.Prior != nil:
		return "prior:" + itoa(ref.Prior.Lag)
	case ref.RollingMean != nil:
		return "rolling_mean"
	case ref.YoY != nil:
		return "yoy"
	case ref.Population != nil:
		return "population:" + ref.Population.Cohort
	case ref.Stage != nil:
		if ref.Stage.Index != nil {
			return "stage:index:" + itoa(*ref.Stage.Index)
		}
		return "stage:name:" + ref.Stage.Name
	case ref.Slot != nil:
		return "slot:" + ref.Slot.Name
	}
	return ""
}

// itoa renders an integer for the Ref string surface. Uses
// strconv.Itoa via the small wrapper to keep the call site readable
// (the surrounding function reads cleanly as a switch on the union).
func itoa(n int) string {
	return strconv.Itoa(n)
}

// populateFacetOverlayDescriptors fills FacetValidationResult.OverlaysApplied
// and FacetValidationResult.OverlayCost from req.Overlays. Sibling to
// populateOverlayDescriptors — the FACET-host predict surface mirrors the
// Request-host predict surface (kind-catalog-v1 PRD §I-FR-I3) so LLM
// callers see the same per-spec descriptor shape (Name + Kind + Scope +
// Streamable) and the same per-kind cost map regardless of host shape.
//
// Per-kind cost dispatch: all four FACET-host kinds route through the
// same overlayCostForKind helper. The streamability table at
// types/overlay_streamability.go declares OVERLAY_INDEX_VS_POP and
// OVERLAY_ZSCORE_VS_POP as streamable (cost ~0.05) and OVERLAY_CHISQ_VS_POP
// and OVERLAY_KS_VS_POP as buffered (cost ~1.0) per PRD §2 Non-Goals
// ("Streaming overlay path for inferential kinds"). Cost flips
// automatically when a kind's streamability flag flips.
func populateFacetOverlayDescriptors(result *FacetValidationResult, req *types.FacetRequest, opts *PredictOptions) {
	if result == nil || req == nil || len(req.Overlays) == 0 {
		return
	}
	result.OverlaysApplied, result.OverlayCost = appendOverlayDescriptors(
		result.OverlaysApplied, result.OverlayCost, req.Overlays, opts,
	)
}

// overlayDescriptorName returns the renderer-facing label for an
// overlay spec. When the caller set OverlaySpec.Name explicitly that
// string wins; otherwise we synthesise a deterministic default from
// Kind + Scope + the populated Ref family pointer so the cost map and
// descriptor stay keyed consistently.
//
// The processing layer synthesises its own default at execution time
// (see types/overlay.go's OverlaySpec doc). Predict's synthesis must
// stay aligned with that runtime default for the cost-map key to
// match the response-side OverlayLayer.Name. Until the processing
// helper is exported the synthesis here mirrors the documented
// "Kind|Scope|Ref" recipe.
func overlayDescriptorName(spec *types.OverlaySpec) string {
	if spec == nil {
		return ""
	}
	if spec.Name != "" {
		return spec.Name
	}
	name := string(spec.Kind) + "|" + string(spec.Scope)
	if spec.Ref.Margin != nil {
		name += "|margin:" + string(spec.Ref.Margin.Axis)
	}
	return name
}

// predictJoinedSchema returns the schema a Request executes over: for a
// single JoinSpec whose right cohort opts.SchemaLoader can read, the
// joined schema (internal/encoding.JoinedSchema — the runtime's own
// rule); otherwise schema unchanged. A right cohort that cannot be read,
// a join-key refusal (internal/encoding.JoinKeysRefusals) or a
// joined-field collision is a predict error under its own code, as the
// runtime refuses each.
func predictJoinedSchema(env *descriptor.Envelope, req *types.Request, schema *encoding.Schema, opts *PredictOptions) *encoding.Schema {
	if req == nil || len(req.Joins) != 1 || req.Joins[0] == nil || opts == nil || opts.SchemaLoader == nil {
		return schema
	}
	spec := req.Joins[0]
	right, err := opts.SchemaLoader(spec.Right)
	if err != nil {
		addCodedError(env, err)
		return schema
	}
	// Kind and OnPairs: the one join-key rule the runtime refuses with
	// before it decodes the right side. The joined schema below does
	// not depend on the keys, so validation continues against it.
	for _, ce := range encx.JoinKeysRefusals(schema, right, spec) {
		env.AddError(string(ce.Code), ce.Message, ce.Details)
	}
	joined, err := encx.JoinedSchema(schema, right, spec.As)
	if err != nil {
		addCodedError(env, err)
		return schema
	}
	return joined
}

// validateOperatorTypes reports every top-level slot whose operator
// Type is still empty once defaults have (or, under DisableDefaults,
// have not) run. The runtime refuses each one at operator construction
// with PROCESSING_CONFIG "unknown <family> type: "; predict mirrors
// code and message, adding the slot path. An aggregation or group slot
// naming a vector field is the field-reference walk's refusal instead
// (PULSE_VECTOR_FIELD_NOT_SCALAR, suggesting the vector operator), which
// the runtime meets first.
func validateOperatorTypes(env *descriptor.Envelope, req *types.Request, schema *encoding.Schema) {
	if req == nil {
		return
	}
	report := func(family, slot string) {
		env.AddError(string(errors.PROCESSING_CONFIG), "unknown "+family+" type: ", map[string]any{"slot": slot})
	}
	isVec := func(field string) bool {
		_, _, ok := vectors.VectorField(schema, field)
		return ok
	}
	for i, a := range req.Aggregations {
		if a != nil && a.Type == "" && !isVec(a.Field) {
			report("aggregation", "aggregations["+strconv.Itoa(i)+"]")
		}
	}
	for i, f := range req.Filterers {
		if f != nil && f.Type == "" {
			report("filter", "filterers["+strconv.Itoa(i)+"]")
		}
	}
	for i, f := range req.Features {
		if f != nil && f.Type == "" {
			report("feature", "features["+strconv.Itoa(i)+"]")
		}
	}
	for i, a := range req.Attributes {
		if a != nil && a.Type == "" {
			report("attribute", "attributes["+strconv.Itoa(i)+"]")
		}
	}
	for i, g := range req.Groups {
		if g != nil && g.Type == "" && !isVec(g.Field) {
			report("group", "groups["+strconv.Itoa(i)+"]")
		}
	}
}

// validateRegisteredOperators reports every aggregation, group,
// filterer and attribute slot (top level, crosstab axes, cell and margin
// aggregations) whose non-empty operator Type the instance does not
// register: neither a built-in it offers (a profile-hidden built-in is
// never registered) nor an embedder extension of the slot's category.
// The runtime refuses each at operator lookup with PROCESSING_CONFIG
// "unknown <family> type: <name>" ("unknown crosstab margin aggregation
// type: <name>" for a margin aggregation); predict mirrors code and
// message, adding the slot path. ATTR_RANK keeps its removed-type
// migration hint instead while WIN_RANK is offered.
func validateRegisteredOperators(env *descriptor.Envelope, req *types.Request, opts *PredictOptions) {
	if req == nil {
		return
	}
	snap := opts.Extensions
	registered := func(name string, builtin bool, ext []descriptor.OperatorMeta) bool {
		if name == "" || opts.instance().Hidden(name) {
			return name == ""
		}
		if builtin {
			return true
		}
		if snap != nil {
			for _, m := range ext {
				if m.Name == name {
					return true
				}
			}
		}
		return false
	}
	var snapAggs, snapGroups, snapFilters, snapAttrs []descriptor.OperatorMeta
	if snap != nil {
		snapAggs, snapGroups, snapFilters, snapAttrs = snap.Aggregators, snap.Groupers, snap.Filterers, snap.Attributes
	}
	// The slot path is built only for a refusal: the walk allocates
	// nothing on a valid request (TestObservabilityOffNoAllocs).
	report := func(message, slot string, i int, name string) {
		if i >= 0 {
			slot += "[" + strconv.Itoa(i) + "]"
		}
		env.AddError(string(errors.PROCESSING_CONFIG), message+name, map[string]any{"slot": slot, "type": name})
	}
	agg := func(a *types.Aggregation, slot string, i int, message string) {
		if a != nil && !registered(string(a.Type), builtinAggregations[a.Type], snapAggs) {
			report(message, slot, i, string(a.Type))
		}
	}
	group := func(g *types.Group, slot string, i int) {
		if g != nil && !registered(string(g.Type), builtinGroups[g.Type], snapGroups) {
			report("unknown group type: ", slot, i, string(g.Type))
		}
	}
	for i, a := range req.Aggregations {
		agg(a, "aggregations", i, "unknown aggregation type: ")
	}
	for i, g := range req.Groups {
		group(g, "groups", i)
	}
	for i, f := range req.Filterers {
		if f != nil && !registered(string(f.Type), builtinFilterers[f.Type], snapFilters) {
			report("unknown filter type: ", "filterers", i, string(f.Type))
		}
	}
	for i, a := range req.Attributes {
		if a == nil || (a.Type == "ATTR_RANK" && !opts.instance().Hidden(string(types.WIN_RANK))) {
			continue
		}
		if !registered(string(a.Type), builtinAttributes[a.Type], snapAttrs) {
			report("unknown attribute type: ", "attributes", i, string(a.Type))
		}
	}
	if ct := req.Crosstab; ct != nil {
		for i, g := range ct.Rows {
			group(g, "crosstab.rows", i)
		}
		for i, g := range ct.Columns {
			group(g, "crosstab.columns", i)
		}
		if ct.HasMeasures() {
			for i, a := range ct.Cells {
				// A missing type is the Cells fault validateCrosstab reports.
				if a != nil && a.Type != "" {
					agg(a, "crosstab.cells", i, "unknown aggregation type: ")
				}
			}
		} else {
			agg(ct.Cell, "crosstab.cell", -1, "unknown aggregation type: ")
		}
		for i, a := range ct.MarginAggregations {
			agg(a, "crosstab.margin_aggregations", i, "unknown crosstab margin aggregation type: ")
		}
	}
}

// builtinAggregations / builtinGroups / builtinFilterers /
// builtinAttributes are the built-in operator names per category
// (types.All*Types).
var (
	builtinAggregations = setOf(types.AllAggregationTypes())
	builtinGroups       = setOf(types.AllGroupTypes())
	builtinFilterers    = setOf(types.AllFiltererTypes())
	builtinAttributes   = setOf(types.AllAttributeTypes())
)

func setOf[T comparable](ts []T) map[T]bool {
	out := make(map[T]bool, len(ts))
	for _, t := range ts {
		out[t] = true
	}
	return out
}

// newPredictResult is the empty, valid PredictResult every predict run
// starts from (every slice non-nil so the JSON shape is stable).
func newPredictResult(req *types.Request) *descriptor.PredictResult {
	return &descriptor.PredictResult{
		Valid:                    true,
		Request:                  req,
		Shards:                   []descriptor.ShardInfo{},
		DefaultsApplied:          []descriptor.DefaultApplied{},
		Aggregations:             []descriptor.AggregationPredict{},
		Groups:                   []descriptor.GroupPredict{},
		Filterers:                []descriptor.FiltererPredict{},
		OverlaysApplied:          []descriptor.OverlayAppliedDescriptor{},
		OverlaysSchemaDivergence: []descriptor.SlotPair{},
		OverlayCost:              map[string]float64{},
		TimeZones:                []descriptor.ResolvedZone{},
	}
}
