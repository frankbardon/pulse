package descriptor

import (
	"strconv"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/seriesfield"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// Non-scalar overlay hosts — the Compose-host and whole-chain halves
// of the rule the field-reference walk applies to Request.Overlays
// (processOverlayArrayValuedRefusals, the Process-host half). An
// overlay handler reads ONE number per host row, so scoring an
// aggregation whose row value is not one number folds nothing —
// silently. Two classes are refused (vectors.AggregationKinds, so the
// instance's routing and extension declarations apply):
//
//   - array-valued (IsArrayValued — AGG_VEC_MEAN / AGG_VEC_SUM, or an
//     extension declared ArrayValued) scored by its bare label: one
//     figure per member. details.reason "array_valued". Under
//     params.expand a series kind's params.field may name one of its
//     per-element columns "<label>[<element label>]" instead
//     (seriesfield.Scored.Element): an ordinary f64 column, scored like
//     any scalar aggregation (an element no row answered is NaN, an
//     undefined figure like a scalar aggregate's).
//   - non-scalar (IsNonScalar — the RichAggregator lifts AGG_WELFORD's
//     moment struct, AGG_SET_UNION / AGG_SET_INTERSECTION's label
//     list, AGG_SET_FREQUENCY's count map, or an extension declared
//     NonScalar): details.reason "non_scalar". A kind that declares it
//     reads that aggregator's payload (overlayReadsPayload — the
//     OVERLAY_T_VS_REF / OVERLAY_Z_VS_REF Welford triple) is exempt.
//   - categorical code (IsCode — an aggregation whose value is one of
//     its input field's own encoded values, the built-in AGG_MODE or an
//     extension declared EmitsInputValue, over a categorical_* field
//     (the dictionary index) or a packed_bool field (the 0/1 truth
//     value)): one number, but a code, not a magnitude — an index /
//     delta / share of codes is meaningless, and a 0 code warns
//     PULSE_OVERLAY_REF_ZERO. details.reason "categorical_code", plus
//     the input `field` and its `field_type`. It depends on the INPUT
//     field's schema type, so it is judged against the schema the slot
//     executes over (the cohort's, the joined one for a joined slot, a
//     chain stage >= 1's synthesised one — where a group key column is
//     categorical and an aggregation output column f64, except that a
//     column an upstream code-emitting aggregation wrote stays the code
//     it is, vectors.AggregationKinds.ChainCodeColumns): no schema, no
//     judgement.
//
// All are PROCESSING_CONFIG. Predict (ValidateCompose /
// ValidateChain) and the runtime (Service.applyComposeOverlays /
// Service.processChain) call the same functions; the Process-host half
// rides the field-reference walk, which predict and the runtime
// (Service.checkFieldRefs) share.
//
// Only the SCORED aggregation is judged (scoredNonScalarAggregation):
// a series overlay on a grouped Process request reads only the one
// aggregation params.field names (seriesfield.Resolve), and a
// non-crosstab slot or stage is narrowed to its group columns plus
// that one value column before any gate or handler reads a row
// (processing.narrowSeriesHost), so an unscored one never reaches the
// fold. A crosstab slot or stage is not narrowed, and keeps the
// whole-request array-valued rule (its cells carry rich payloads the
// cell kinds read). On a Process crosstab host the categorical-code
// rule judges the CELL aggregation instead (crosstabCellCodeRefusal),
// for the MATRIX kinds that read cell values
// (overlayhost.ReadsCellValue).

// scoredValueReasons are the details.reason values, and the message
// phrase each one reads as.
const (
	reasonArrayValued     = "array_valued"
	reasonNonScalar       = "non_scalar"
	reasonCategoricalCode = "categorical_code"
)

// scoredValueLong is the reason's message clause ("... is <clause>").
func scoredValueLong(reason string) string {
	switch reason {
	case reasonNonScalar:
		return "non-scalar (its value is a label list, a count map or a struct, not one number)"
	case reasonCategoricalCode:
		return codeClause
	}
	return "array-valued (one figure per vector member; with params.expand, params.field may name one element column <label>[<element label>])"
}

// codeClause is the categorical-code reason's message clause.
const codeClause = "a categorical code (its value is a value of its categorical or packed_bool input field — a dictionary index or a 0/1 truth value — not a magnitude)"

// codeFix is the categorical-code refusal's remedy.
const codeFix = "score a count or share instead (AGG_FREQUENCY of one label, AGG_AVERAGE of a packed_bool)"

// scoredValueRemedy is the Compose / chain refusal message's tail.
func scoredValueRemedy(reason string) string {
	if reason == reasonCategoricalCode {
		return "; an overlay compares magnitudes across host rows — " + codeFix
	}
	return "; an overlay folds one number per host row — compare a scalar aggregation instead"
}

// lazyFieldTypes is a fieldTypeLookup over the schema load returns,
// called at most once and only when a lookup is made (an input-valued
// scored aggregation); nil when load is nil.
func lazyFieldTypes(load func() *encoding.Schema) fieldTypeLookup {
	if load == nil {
		return nil
	}
	var (
		done   bool
		lookup fieldTypeLookup
	)
	return func(field string) (encoding.FieldType, bool) {
		if !done {
			done, lookup = true, schemaFieldTypes(load())
		}
		if lookup == nil {
			return 0, false
		}
		return lookup(field)
	}
}

// fieldTypeLookup returns the type of a field of the schema a slot
// executes over (ok false: not a schema field); nil judges no input
// type.
type fieldTypeLookup func(field string) (encoding.FieldType, bool)

// schemaFieldTypes is schema's fieldTypeLookup; nil for a nil schema.
func schemaFieldTypes(schema *encoding.Schema) fieldTypeLookup {
	if schema == nil {
		return nil
	}
	return func(field string) (encoding.FieldType, bool) {
		if f := schema.Field(field); f != nil {
			return f.Type, true
		}
		return 0, false
	}
}

// codeInput returns the input field type that makes a a categorical
// code under kinds (IsCode), ok false when it is not one. inputType nil
// judges nothing.
func codeInput(a *types.Aggregation, kinds vectors.AggregationKinds, inputType fieldTypeLookup) (encoding.FieldType, bool) {
	if a == nil || inputType == nil || a.Field == "" || !kinds.EmitsInputValue(a.Type) {
		return 0, false
	}
	ft, ok := inputType(a.Field)
	if !ok || !kinds.IsCode(a.Type, ft) {
		return 0, false
	}
	return ft, true
}

// withCodeDetails adds the categorical-code refusal's `field` and
// `field_type` to details when reason is categorical_code.
func withCodeDetails(details map[string]any, reason string, a *types.Aggregation, kinds vectors.AggregationKinds, inputType fieldTypeLookup) map[string]any {
	if reason != reasonCategoricalCode {
		return details
	}
	if ft, ok := codeInput(a, kinds, inputType); ok {
		details["field"] = a.Field
		details["field_type"] = ft.String()
	}
	return details
}

// overlayReadsPayload reports whether kind declares it reads the
// payload of the (routed) aggregator t — the kind's hard feature
// dependency names it (hardEdges: OVERLAY_T_VS_REF / OVERLAY_Z_VS_REF
// read AGG_WELFORD's triple). Such a kind scores the struct itself.
func overlayReadsPayload(kind types.OverlayKind, t types.AggregationType) bool {
	for _, dep := range hardEdges[string(kind)] {
		if dep == string(t) {
			return true
		}
	}
	return false
}

// scoredValueReason is the refusal reason for kind scoring a, or "".
// gateOwns (nil: none) names a non-scalar type another, earlier rule
// refuses on both sides, which keeps it. inputType (nil: none) types
// a's input field for the categorical-code rule.
func scoredValueReason(kind types.OverlayKind, a *types.Aggregation, kinds vectors.AggregationKinds, gateOwns func(types.AggregationType) bool, inputType fieldTypeLookup) string {
	t := a.Type
	switch {
	case kinds.IsArrayValued(t):
		return reasonArrayValued
	case kinds.IsNonScalar(t) && !overlayReadsPayload(kind, kinds.Routed(t)) && (gateOwns == nil || !gateOwns(t)):
		return reasonNonScalar
	}
	if _, ok := codeInput(a, kinds, inputType); ok {
		return reasonCategoricalCode
	}
	return ""
}

// firstArrayValuedAggregation returns the index and slot of r's first
// array-valued aggregation under the instance's routing, or -1, nil.
func firstArrayValuedAggregation(r *types.Request, kinds vectors.AggregationKinds) (int, *types.Aggregation) {
	if r == nil {
		return -1, nil
	}
	for i, a := range r.Aggregations {
		if a != nil && kinds.IsArrayValued(a.Type) {
			return i, a
		}
	}
	return -1, nil
}

// scoredNonScalarAggregation returns the index, slot and refusal
// reason of the aggregation kind scores on r when it is array-valued,
// non-scalar or a categorical code (scoredValueReason; inputType nil
// skips the code rule), else -1, nil, "". scored resolves
// what it scores (params.field, else aggregations[0] — the narrowing's
// own reading); a resolution error is left to the field rule
// (PULSE_OVERLAY_FIELD_UNKNOWN), which owns it, and an expanded
// element column of an array-valued aggregation is one f64 figure per
// row, never refused here. A crosstab request is
// never narrowed, so every aggregation counts there, under the
// array-valued rule only. kinds is the instance's aggregation routing
// (aggregationKinds). The Process host (fieldRefWalk's overlay step),
// the Compose host and the chain host all judge through it.
func scoredNonScalarAggregation(r *types.Request, kind types.OverlayKind, kinds vectors.AggregationKinds, gateOwns func(types.AggregationType) bool, inputType fieldTypeLookup, scored func([]*types.Aggregation) (seriesfield.Scored, error)) (int, *types.Aggregation, string) {
	if r == nil {
		return -1, nil, ""
	}
	if r.Crosstab != nil {
		if i, a := firstArrayValuedAggregation(r, kinds); a != nil {
			return i, a, reasonArrayValued
		}
		return -1, nil, ""
	}
	sc, err := scored(r.Aggregations)
	ai := sc.Index
	if err != nil || ai < 0 || ai >= len(r.Aggregations) || sc.Element(r.Aggregations) {
		return -1, nil, ""
	}
	a := r.Aggregations[ai]
	if a == nil {
		return -1, nil, ""
	}
	if reason := scoredValueReason(kind, a, kinds, gateOwns, inputType); reason != "" {
		return ai, a, reason
	}
	return -1, nil, ""
}

// ComposeOverlayArrayValuedRefusal refuses a Compose overlay that
// scores (on its reference, a target, or — no targets — every slot) an
// array-valued aggregation: the one params.field names on a kind
// seriesfield.AppliesCompose accepts, else the slot's Aggregations[0]
// (any aggregation on a crosstab slot). labels are the slots' labels
// (index-aligned with requests, defaults applied). slotSchema (nil:
// none) returns the schema slot j executes over (its cohort's, or the
// joined one; nil when unreadable) for the categorical-code rule; it
// is called only for a slot whose scored aggregation is input-valued.
// Nil when no overlay reads one; a kind the instance hides is left to
// the kind rule.
func ComposeOverlayArrayValuedRefusal(overlays []types.ComposeOverlaySpec, requests []*types.Request, labels []string, inst *InstanceSnapshot, slotSchema func(j int) *encoding.Schema) error {
	for i := range overlays {
		if err := composeOverlayArrayValuedRefusal(i, &overlays[i], requests, labels, inst, slotSchema); err != nil {
			return err
		}
	}
	return nil
}

func composeOverlayArrayValuedRefusal(i int, spec *types.ComposeOverlaySpec, requests []*types.Request, labels []string, inst *InstanceSnapshot, slotSchema func(j int) *encoding.Schema) *errors.CodedError {
	kind := string(spec.Kind)
	if inst.Hidden(kind) {
		return nil
	}
	reads := func(label string) bool {
		if len(spec.Targets) == 0 || label == spec.Reference {
			return true
		}
		for _, t := range spec.Targets {
			if t == label {
				return true
			}
		}
		return false
	}
	for j, r := range requests {
		if r == nil || j >= len(labels) || !reads(labels[j]) {
			continue
		}
		slot := labels[j]
		var inputType fieldTypeLookup
		if slotSchema != nil {
			j := j
			inputType = lazyFieldTypes(func() *encoding.Schema { return slotSchema(j) })
		}
		kinds := aggregationKinds(inst, nil)
		cols := vectors.AggregationColumns(r, composeSlotSchemaFunc(slotSchema, j), kinds)
		ai, agg, reason := scoredNonScalarAggregation(r, spec.Kind, kinds, nil, inputType, func(aggs []*types.Aggregation) (seriesfield.Scored, error) {
			if !seriesfield.AppliesCompose(spec.Kind) {
				return seriesfield.Scored{}, nil
			}
			return seriesfield.ResolveComposeSlot(aggs, spec, i, slot, cols)
		})
		if agg == nil {
			continue
		}
		label := types.AggregationLabelOf(agg)
		host := "requests[" + strconv.Itoa(j) + "].aggregations[" + strconv.Itoa(ai) + "]"
		return errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
			"overlays["+strconv.Itoa(i)+"] ("+kind+") reads slot "+strconv.Quote(labels[j])+", whose "+host+" "+label+" ("+string(agg.Type)+
				") is "+scoredValueLong(reason)+scoredValueRemedy(reason),
			withCodeDetails(map[string]any{"overlay_index": i, "kind": kind, "slot": labels[j], "host": host,
				"aggregation": string(agg.Type), "label": label, "reason": reason}, reason, agg, kinds, inputType))
	}
	return nil
}

// ChainOverlayArrayValuedRefusal refuses a whole-chain overlay that
// scores an array-valued aggregation on its target or reference stage
// (only the last stage can carry one: an array-valued stage may not
// feed another) — the one params.field names on a kind
// seriesfield.AppliesChain accepts, else the stage's Aggregations[0]
// (any aggregation on a crosstab stage). A
// spec whose stage references do not resolve is left to the
// stage-reference rules. stage0 (nil: no judgement) is the schema
// stage 0 executes over (the cohort's, or the joined one) for the
// categorical-code rule; a later stage is judged against the schema
// synthesised from the stage before it (synthChainSchema: group key
// columns categorical, aggregation outputs f64) plus the code columns
// it inherits (vectors.AggregationKinds.ChainCodeColumns — an output
// column an upstream code-emitting aggregation wrote is still a code),
// so the stages must carry their smart defaults by then. Nil when no
// overlay reads one.
func ChainOverlayArrayValuedRefusal(req *types.ChainRequest, inst *InstanceSnapshot, stage0 *encoding.Schema) error {
	if req == nil {
		return nil
	}
	for i, spec := range req.Overlays {
		if err := chainOverlayArrayValuedRefusal(req, i, spec, inst, chainStagedRequests(req.Stages), stage0); err != nil {
			return err
		}
	}
	return nil
}

// chainStageInputSchema is the schema chain stage idx executes over:
// stage0 for stage 0, else the one synthesised from staged[idx-1] (the
// stage as executed — smart defaults resolved) over ITS input schema,
// so an expanding vector aggregation's element columns are typed (its
// members resolve there); nil when stage0 is nil or an earlier stage is
// missing.
func chainStageInputSchema(staged []*types.Request, idx int, stage0 *encoding.Schema, kinds vectors.AggregationKinds) *encoding.Schema {
	if stage0 == nil || idx < 0 {
		return nil
	}
	schema := stage0
	for s := 0; s < idx; s++ {
		if s >= len(staged) || staged[s] == nil {
			return nil
		}
		schema = synthChainSchema(staged[s], schema, kinds)
	}
	return schema
}

// lazyChainStageFieldTypes is the code judgement's lookup over chain
// stage idx's input: chainStageInputSchema plus the code columns it
// inherits (vectors.ChainCodeColumns over synthChainSchema — a column a
// code-emitting aggregation wrote upstream answers the type it is a
// code of), built at most once and only when a lookup is made.
func lazyChainStageFieldTypes(staged []*types.Request, idx int, stage0 *encoding.Schema, kinds vectors.AggregationKinds) fieldTypeLookup {
	var (
		done   bool
		lookup fieldTypeLookup
	)
	return func(field string) (encoding.FieldType, bool) {
		if !done {
			done = true
			lookup = vectors.CodeFieldTypes(chainStageInputSchema(staged, idx, stage0, kinds),
				kinds.ChainCodeColumns(staged, idx, stage0, chainSynth(kinds)))
		}
		if lookup == nil {
			return 0, false
		}
		return lookup(field)
	}
}

// chainStagedRequests is req's stages' requests, index-aligned.
func chainStagedRequests(stages []*types.ChainStage) []*types.Request {
	out := make([]*types.Request, len(stages))
	for i, st := range stages {
		if st != nil {
			out[i] = st.Request
		}
	}
	return out
}

func chainOverlayArrayValuedRefusal(req *types.ChainRequest, i int, spec *types.ChainOverlaySpec, inst *InstanceSnapshot, staged []*types.Request, stage0 *encoding.Schema) *errors.CodedError {
	if spec == nil || inst.Hidden(string(spec.Kind)) {
		return nil
	}
	for _, side := range []struct {
		role string
		ref  types.StageRef
		dflt bool
	}{{"target", spec.Target, true}, {"ref", spec.Ref, false}} {
		idx, ok := chainStageIndex(req, side.ref, side.dflt)
		if !ok || req.Stages[idx] == nil {
			continue
		}
		role := side.role
		kinds := aggregationKinds(inst, nil)
		var inputType fieldTypeLookup
		if stage0 != nil {
			idx := idx
			inputType = lazyChainStageFieldTypes(staged, idx, stage0, kinds)
		}
		cols := vectors.AggregationColumns(req.Stages[idx].Request, chainStageSchemaFunc(staged, idx, stage0, kinds), kinds)
		ai, agg, reason := scoredNonScalarAggregation(req.Stages[idx].Request, spec.Kind, kinds, chainGateOwns(inst), inputType, func(aggs []*types.Aggregation) (seriesfield.Scored, error) {
			if !seriesfield.AppliesChain(spec.Kind) {
				return seriesfield.Scored{}, nil
			}
			return seriesfield.ResolveChainStage(aggs, spec, i, role, idx, cols)
		})
		if agg == nil {
			continue
		}
		label := types.AggregationLabelOf(agg)
		return errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
			"chain overlays["+strconv.Itoa(i)+"] ("+string(spec.Kind)+") "+side.role+" stage "+strconv.Itoa(idx)+" aggregation "+label+" ("+string(agg.Type)+
				") is "+scoredValueLong(reason)+scoredValueRemedy(reason),
			withCodeDetails(map[string]any{"overlay_index": i, "kind": string(spec.Kind), "role": side.role, "stage_index": idx,
				"stage_name": req.Stages[idx].Name, "aggregation_index": ai, "aggregation": string(agg.Type),
				"label": label, "reason": reason}, reason, agg, kinds, inputType))
	}
	return nil
}

// composeSlotSchemaFunc is slot j's schema getter for
// vectors.AggregationColumns (nil when slotSchema is nil).
func composeSlotSchemaFunc(slotSchema func(j int) *encoding.Schema, j int) func() *encoding.Schema {
	if slotSchema == nil {
		return nil
	}
	return func() *encoding.Schema { return slotSchema(j) }
}

// chainStageSchemaFunc is chain stage idx's input-schema getter for
// vectors.AggregationColumns: chainStageInputSchema, nil when stage0 is
// nil (no schema — an element column then resolves syntactically and
// the schema-holding call judges it).
func chainStageSchemaFunc(staged []*types.Request, idx int, stage0 *encoding.Schema, kinds vectors.AggregationKinds) func() *encoding.Schema {
	if stage0 == nil {
		return nil
	}
	return func() *encoding.Schema { return chainStageInputSchema(staged, idx, stage0, kinds) }
}

// ChainStageInputSchema is the exported chainStageInputSchema: the
// schema chain stage idx of stages (as executed) runs over — stage0
// for stage 0, else the one synthesised from the stage before it; nil
// when stage0 is nil. kinds is the instance's aggregation view. The
// runtime's whole-chain overlay barrier hands it to the series value
// narrowing so an expanded element column resolves against the same
// members predict-chain reads.
func ChainStageInputSchema(stages []*types.Request, idx int, stage0 *encoding.Schema, kinds vectors.AggregationKinds) *encoding.Schema {
	return chainStageInputSchema(stages, idx, stage0, kinds)
}

// chainGateOwns names the non-scalar aggregators the ProcessChain stage
// gate refuses first on both sides: a non-mergeable one
// (mergegate.MergeRefusal — AGG_WELFORD, an extension not declared
// Mergeable) is PULSE_CHAIN_NOT_MERGEABLE on every stage, so the
// overlay rule (which the runtime checks before any stage, predict
// after them) leaves it to that gate and the first refusal matches.
func chainGateOwns(inst *InstanceSnapshot) func(types.AggregationType) bool {
	facts := inst.Extensions().mergeFacts(inst)
	return func(t types.AggregationType) bool {
		mergeable, ext := facts.Aggregator(string(t))
		if !ext {
			mergeable = t.Mergeable()
		}
		return !mergeable
	}
}

// chainStageIndex resolves a StageRef the way the chain overlay
// resolvers do (Index, else Name, else — dflt — the last stage); ok is
// false when it does not resolve to exactly one stage.
func chainStageIndex(req *types.ChainRequest, ref types.StageRef, dflt bool) (int, bool) {
	n := len(req.Stages)
	switch {
	case ref.Index != nil && ref.Name != "":
		return 0, false
	case ref.Index != nil:
		return *ref.Index, *ref.Index >= 0 && *ref.Index < n
	case ref.Name != "":
		for i, st := range req.Stages {
			if st != nil && st.Name == ref.Name {
				return i, true
			}
		}
		return 0, false
	case dflt && n > 0:
		return n - 1, true
	}
	return 0, false
}

// processOverlayArrayValuedRefusals is the Process-host half: one
// refusal per distinct array-valued or non-scalar aggregation an entry
// of req.Overlays scores — the one params.field names on a kind
// seriesfield.Applies accepts, else Aggregations[0] (every other kind
// reads the first aggregation as its host value). cols lists each
// aggregation's columns (vectors.AggregationColumns over the schema
// the request executes over), so a params.field naming an expanded
// element column resolves — and is scored, never refused. A crosstab request's
// overlays read cells, so only its cell's categorical-code rule applies
// (below). An unresolvable field is
// left to PULSE_OVERLAY_FIELD_UNKNOWN. Details {aggregation, label,
// slot, reason: "array_valued" | "non_scalar" | "categorical_code"}
// (the last adds the input field and field_type); slot is
// "aggregations[<i>]". inputType types the fields the request executes
// over (nil: no categorical-code judgement) — the schema's, plus on a
// chain stage >= 1 the code columns it inherits
// (vectors.CodeFieldTypes). A crosstab request is judged by
// crosstabCellCodeRefusal instead; readsCell reports whether a kind
// reads its cell values (the instance's overlayhost.ReadsCellValue).
func processOverlayArrayValuedRefusals(req *types.Request, kinds vectors.AggregationKinds, inputType fieldTypeLookup, cols seriesfield.Columns, readsCell func(types.OverlayKind) bool) []*errors.CodedError {
	if req == nil || len(req.Overlays) == 0 {
		return nil
	}
	if req.Crosstab != nil {
		if ce := crosstabCellCodeRefusal(req, kinds, inputType, readsCell); ce != nil {
			return []*errors.CodedError{ce}
		}
		return nil
	}
	var out []*errors.CodedError
	seen := map[int]bool{}
	for i := range req.Overlays {
		spec := &req.Overlays[i]
		ai, host, reason := scoredNonScalarAggregation(req, spec.Kind, kinds, nil, inputType, func(aggs []*types.Aggregation) (seriesfield.Scored, error) {
			if !seriesfield.Applies(spec.Kind) {
				return seriesfield.Scored{}, nil
			}
			return seriesfield.Resolve(aggs, spec, i, cols)
		})
		if host == nil || seen[ai] {
			continue
		}
		seen[ai] = true
		label := types.AggregationLabelOf(host)
		short := "array-valued"
		long := "array-valued (one figure per vector member)"
		first, named := "put a scalar aggregation first", "an overlay folds one number per host row — name a scalar aggregation instead"
		switch reason {
		case reasonNonScalar:
			short = "non-scalar (a label list, a count map or a struct, not one number)"
			long = short
		case reasonCategoricalCode:
			short, long = codeClause, codeClause
			first, named = codeFix, "an overlay compares magnitudes across host rows — "+codeFix
		}
		msg := "overlays read the first aggregation as their host value, and " + label + " (" + string(host.Type) + ") is " + short + "; " + first
		if ai != 0 {
			msg = "overlays[" + strconv.Itoa(i) + "] (" + string(spec.Kind) + ") params.field scores " + label + " (" + string(host.Type) +
				"), which is " + long + "; " + named
		}
		out = append(out, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG, msg,
			withCodeDetails(map[string]any{"aggregation": string(host.Type), "label": label, "slot": "aggregations[" + strconv.Itoa(ai) + "]", "reason": reason},
				reason, host, kinds, inputType)))
	}
	return out
}

// crosstabCellCodeRefusal is the MATRIX half of the categorical-code
// rule: a crosstab request whose cell aggregation is a categorical code
// (IsCode over its input field — AGG_MODE over a categorical_* /
// packed_bool field writes the modal dictionary index / truth value
// into every cell and margin) refused for the first overlay of a kind
// that reads the cell VALUES (readsCell — overlayhost.ReadsCellValue:
// the share / index / delta / z-score vs-margin kinds, χ², Fisher, the
// proportion pairwise kinds, OVERLAY_FORMULA). A kind reading only the
// cell's component moments, or no overlay that reads values, leaves it
// alone. One refusal per request (one cell aggregation): the E4-S14
// Details shape {aggregation, label, slot: "crosstab.cell", reason:
// "categorical_code", field, field_type}, PROCESSING_CONFIG. The array-
// valued cell keeps its own whole-request crosstab refusal.
func crosstabCellCodeRefusal(req *types.Request, kinds vectors.AggregationKinds, inputType fieldTypeLookup, readsCell func(types.OverlayKind) bool) *errors.CodedError {
	cell := req.Crosstab.Cell
	if readsCell == nil {
		return nil
	}
	if _, ok := codeInput(cell, kinds, inputType); !ok {
		return nil
	}
	for i := range req.Overlays {
		kind := req.Overlays[i].Kind
		if !readsCell(kind) {
			continue
		}
		label := types.AggregationLabelOf(cell)
		return errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
			"overlays["+strconv.Itoa(i)+"] ("+string(kind)+") reads the crosstab cell values, and the cell "+label+" ("+string(cell.Type)+
				") is "+codeClause+"; an overlay compares magnitudes across cells — score a count instead (an AGG_COUNT cell, AGG_FREQUENCY of one label, AGG_AVERAGE of a packed_bool)",
			withCodeDetails(map[string]any{"aggregation": string(cell.Type), "label": label, "slot": "crosstab.cell", "reason": reasonCategoricalCode},
				reasonCategoricalCode, cell, kinds, inputType))
	}
	return nil
}
