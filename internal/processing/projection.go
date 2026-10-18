package processing

import (
	"encoding/json"

	exprast "github.com/expr-lang/expr/ast"
	exprparser "github.com/expr-lang/expr/parser"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/fieldaccept"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// FieldSet is an order-insignificant set of schema field names that
// must be decoded into each Record during the buffered Process path.
// The sentinel "*" entry means "no projection — keep every field" and
// short-circuits Has() to true; callers that produced a wide set should
// fall back to the full-decode path rather than try to project.
type FieldSet map[string]struct{}

// NewFieldSet returns an empty FieldSet sized for hint members.
func NewFieldSet(hint int) FieldSet {
	if hint < 4 {
		hint = 4
	}
	return make(FieldSet, hint)
}

// Add inserts name into the set. Empty names are ignored.
func (s FieldSet) Add(name string) {
	if name == "" {
		return
	}
	s[name] = struct{}{}
}

// Widen marks the set as covering every field. After this call Has()
// returns true for any name. Used when extraction can't be proven
// complete — for example a malformed expression or an extension
// operator without an introspection hook.
func (s FieldSet) Widen() {
	s["*"] = struct{}{}
}

// IsWide reports whether the set has been widened to cover every field.
func (s FieldSet) IsWide() bool {
	_, ok := s["*"]
	return ok
}

// Has reports whether name is in the set. Returns true for every name
// when the set has been widened.
func (s FieldSet) Has(name string) bool {
	if _, ok := s["*"]; ok {
		return true
	}
	_, ok := s[name]
	return ok
}

// Len returns the number of explicitly listed fields. Returns the
// schema field count when the set is wide (caller-supplied schema
// resolves the count; pass len(schema.Fields) at the call site).
func (s FieldSet) Len() int {
	if _, ok := s["*"]; ok {
		// Caller knows the schema size; we report explicit entries only.
		return len(s) - 1
	}
	return len(s)
}

// NeededFields walks req against schema and returns the set of source
// field names that the buffered or streaming path must decode into
// each Record so every requested operator output computes correctly.
//
// When extraction can't prove completeness (malformed expression,
// extension operator without a FieldInputs hook) the returned set is
// widened and callers should fall back to the full-decode path.
//
// The ext argument may be nil — in that case any custom operator in
// req that resolves only via overlay would still produce a wide set
// because we'd have no introspection. Built-in operators are fully
// introspectable here.
func NeededFields(req *types.Request, schema *encoding.Schema, ext *ExtensionRegistry) FieldSet {
	return NeededFieldsWithModels(req, schema, ext, nil)
}

// NeededFieldsWithModels is NeededFields for a request whose
// ATTR_REG_PREDICT slots score models bound in models: each such slot
// reads its model's predictor columns, which the request does not
// spell. A slot whose model is not bound adds nothing — the
// field-reference pass has refused it before any decode.
func NeededFieldsWithModels(req *types.Request, schema *encoding.Schema, ext *ExtensionRegistry, models *model.Set) FieldSet {
	out := NewFieldSet(8)
	if req == nil || schema == nil {
		out.Widen()
		return out
	}
	// Windowed record rows carry the FULL record: a windows-only
	// request emits one row per filtered record (recordsToRows), and
	// projection is output-transparent, so every field decodes — the
	// unprojected row is canonical (#201d).
	if windowsEmitRecordRows(req) {
		out.Widen()
		return out
	}

	known := schemaFieldSet(schema)
	// An expression reads a named vector as one identifier standing for
	// its members (exprProgram.bindVectors): appendExprIdents expands it,
	// or the members would decode as null and the expression would
	// silently fold NaN.
	vecMembers := exprVectorMembers(req, schema)

	addKnown := func(name string) {
		if _, ok := known[name]; ok {
			out.Add(name)
		}
	}
	addAllKnown := func(names []string) {
		for _, n := range names {
			addKnown(n)
		}
	}

	// Row weights: the request's and every slot's own. A weight column
	// left out of the projection decodes as null on every row, so
	// every row would read as an invalid weight. (The instance default,
	// pulse.Options.DefaultWeight, is added by the service, which owns
	// it.)
	for _, name := range requestWeightFields(req) {
		addKnown(name)
	}

	for _, f := range req.Filterers {
		if f == nil {
			continue
		}
		addKnown(f.Field)
		if f.Type == types.FILTER_EXPRESSION && f.Expression != "" {
			if !appendExprIdents(out, f.Expression, known, vecMembers) {
				out.Widen()
				return out
			}
		}
	}

	for _, a := range req.Aggregations {
		if a == nil {
			continue
		}
		addKnown(a.Field)
		addAggParamFields(out, a, known)
	}

	if req.Crosstab != nil {
		for _, g := range req.Crosstab.Rows {
			if g == nil {
				continue
			}
			addKnown(g.Field)
		}
		for _, g := range req.Crosstab.Columns {
			if g == nil {
				continue
			}
			addKnown(g.Field)
		}
		// Every measure of a multi-measure crosstab (Cells) reads its
		// field exactly as the single cell does.
		for _, c := range req.Crosstab.CellAggregations() {
			if c == nil {
				continue
			}
			addKnown(c.Field)
			addAggParamFields(out, c, known)
		}
		// Auxiliary margin-only aggregations read source records
		// exactly as the cell does, so they contribute to the
		// projection exactly as the cell does. Omitting them leaves
		// the operator reading fields no Record carries.
		for _, a := range req.Crosstab.MarginAggregations {
			if a == nil {
				continue
			}
			addKnown(a.Field)
			addAggParamFields(out, a, known)
		}
	}

	for _, b := range req.Labels {
		if b == nil {
			continue
		}
		addKnown(b.Field)
	}

	for _, a := range req.Attributes {
		if a == nil {
			continue
		}
		addKnown(a.Field)
		addKnown(a.Target)
		addAllKnown(a.Predictors)
		if a.Type == types.ATTR_REG_PREDICT {
			if p, err := model.DecodeScoreParams(a.Params); err == nil {
				if b, lerr := models.Lookup(p.Fit); lerr == nil {
					addAllKnown(b.Predictors)
				}
			}
		}
		if a.Type == types.ATTR_FORMULA && a.Expression != "" {
			if !appendExprIdents(out, a.Expression, known, vecMembers) {
				out.Widen()
				return out
			}
		}
		// A fitted vector attribute's params.fit_on filterers read
		// their columns like Request filterers (its members ride the
		// vector / inline-fields unions below).
		for _, f := range vectors.AttributeFitOn(a.Type, a) {
			addKnown(f.Field)
			if f.Type == types.FILTER_EXPRESSION && f.Expression != "" {
				if !appendExprIdents(out, f.Expression, known, vecMembers) {
					out.Widen()
					return out
				}
			}
		}
	}

	for _, g := range req.Groups {
		if g == nil {
			continue
		}
		addKnown(g.Field)
	}

	for _, w := range req.Windows {
		if w == nil {
			continue
		}
		addKnown(w.Field)
		if w.Type == types.WIN_LIFT {
			if name, ok := jsonStringField(w.Params, "base"); ok {
				addKnown(name)
			}
		}
		addAllKnown(w.PartitionBy)
		for _, k := range w.OrderBy {
			addKnown(k.Field)
		}
	}

	for _, f := range req.Features {
		if f == nil {
			continue
		}
		addKnown(f.Field)
		// Built-in features whose Params reference additional fields.
		switch f.Type {
		case types.FEAT_TRAIN_TEST_SPLIT:
			if name, ok := jsonStringField(f.Params, "stratify"); ok {
				addKnown(name)
			}
		case types.FEAT_TARGET_ENCODE:
			if name, ok := jsonStringField(f.Params, "target"); ok {
				addKnown(name)
			}
		}
		// Extension features: defer to the registered FieldInputs hook
		// if any; otherwise widen.
		if ext != nil {
			if _, custom := ext.Features[f.Type]; custom {
				inputs, ok := ext.FieldInputsFor("feature", string(f.Type), f.Params)
				if !ok {
					out.Widen()
					return out
				}
				addAllKnown(inputs)
			}
		}
	}

	for _, t := range req.Tests {
		if t == nil {
			continue
		}
		addKnown(t.Field)
		addKnown(t.Field2)
		addKnown(t.SplitBy)
		addKnown(t.Rows)
		addKnown(t.Cols)
		addKnown(t.SubjectField)
		// A mean test's params.covariate (CUPED) is a decoded column too.
		if fieldaccept.TakesCovariate(t.Type) {
			addKnown(fieldaccept.CovariateParam(t.Params))
		}
		for _, k := range t.OrderBy {
			addKnown(k.Field)
		}
		if ext != nil {
			if _, custom := ext.RowTests[t.Type]; custom {
				inputs, ok := ext.FieldInputsFor("test", string(t.Type), t.Params)
				if !ok {
					out.Widen()
					return out
				}
				addAllKnown(inputs)
			}
		}
	}
	// PostTests run on materialized result rows, not source records.

	for _, r := range req.Regressions {
		if r == nil {
			continue
		}
		addKnown(r.Target)
		addAllKnown(r.Predictors)
		if c := r.Covariance; c != nil {
			addKnown(c.Cluster)
			addKnown(c.OrderBy)
		}
		addKnown(r.JackknifeBy)
	}

	// Virtual vectors decode every member (internal/vectors, the one
	// resolver predict and the runtime refuse with). A vector that does
	// not resolve is refused before any record is read; decode wide
	// rather than guess.
	members, ok := vectors.Members(req, schema)
	if !ok {
		out.Widen()
		return out
	}
	addAllKnown(members)
	// A native vector field a slot names directly (no Request.Vectors
	// entry) decodes whole.
	addAllKnown(vectors.ReferencedNatives(req, schema))
	// A matrix spec's inline fields resolve by the same rules.
	matMembers, ok := vectors.MatrixMembers(req, schema)
	if !ok {
		out.Widen()
		return out
	}
	addAllKnown(matMembers)
	// A vector-input test's inline fields / fields2 resolve by the same
	// rules (its named vectors ride vectors.Members above).
	testMembers, ok := vectors.TestInputMembers(req, schema)
	if !ok {
		out.Widen()
		return out
	}
	addAllKnown(testMembers)
	// A vector-input attribute's inline fields resolve by the same rules.
	attrMembers, ok := vectors.AttributeInputMembers(req, schema)
	if !ok {
		out.Widen()
		return out
	}
	addAllKnown(attrMembers)
	// A vector-input grouper's inline fields (groups and both crosstab
	// axes) resolve by the same rules.
	groupMembers, ok := vectors.GroupInputMembers(req, schema)
	if !ok {
		out.Widen()
		return out
	}
	addAllKnown(groupMembers)
	// A vector aggregator's inline fields (aggregations, the crosstab
	// cell and its margin aggregations) resolve by the same rules; a
	// member left out decodes null on every row and the element would
	// silently read as unanswered.
	aggMembers, ok := vectors.AggregationInputMembers(req, schema)
	if !ok {
		out.Widen()
		return out
	}
	addAllKnown(aggMembers)

	for _, k := range req.Sort {
		// Sort keys may name output labels rather than schema fields;
		// addKnown ignores non-schema names.
		addKnown(k.Field)
	}

	// Extension operators in non-feature/test categories: widen when a
	// custom operator is registered but offers no introspection hook.
	if ext != nil {
		for _, a := range req.Aggregations {
			if a == nil {
				continue
			}
			if _, custom := ext.Aggregators[a.Type]; custom {
				inputs, ok := ext.FieldInputsFor("aggregator", string(a.Type), a.Params)
				if !ok {
					out.Widen()
					return out
				}
				addAllKnown(inputs)
			}
		}
		if req.Crosstab != nil {
			for _, a := range req.Crosstab.CellAggregations() {
				if a == nil {
					continue
				}
				if _, custom := ext.Aggregators[a.Type]; custom {
					inputs, ok := ext.FieldInputsFor("aggregator", string(a.Type), a.Params)
					if !ok {
						out.Widen()
						return out
					}
					addAllKnown(inputs)
				}
			}
		}
		if req.Crosstab != nil {
			for _, a := range req.Crosstab.MarginAggregations {
				if a == nil {
					continue
				}
				if _, custom := ext.Aggregators[a.Type]; !custom {
					continue
				}
				inputs, ok := ext.FieldInputsFor("aggregator", string(a.Type), a.Params)
				if !ok {
					out.Widen()
					return out
				}
				addAllKnown(inputs)
			}
		}
		for _, a := range req.Attributes {
			if a == nil {
				continue
			}
			if _, custom := ext.Attributes[a.Type]; custom {
				inputs, ok := ext.FieldInputsFor("attribute", string(a.Type), a.Params)
				if !ok {
					out.Widen()
					return out
				}
				addAllKnown(inputs)
			}
		}
		for _, f := range req.Filterers {
			if f == nil {
				continue
			}
			if _, custom := ext.Filterers[f.Type]; custom {
				inputs, ok := ext.FieldInputsFor("filterer", string(f.Type), nil)
				if !ok {
					out.Widen()
					return out
				}
				addAllKnown(inputs)
			}
		}
		for _, g := range req.Groups {
			if g == nil {
				continue
			}
			if _, custom := ext.Groupers[g.Type]; custom {
				inputs, ok := ext.FieldInputsFor("grouper", string(g.Type), g.Params)
				if !ok {
					out.Widen()
					return out
				}
				addAllKnown(inputs)
			}
		}
		// An extension MAT_* operator reads only its members (it
		// finalizes over the engine's co-moment); a FieldInputs hook
		// adds what it declares beyond them.
		for _, m := range req.Matrices {
			if _, custom := ext.MatrixOps[m.Type]; custom {
				if inputs, ok := ext.FieldInputsFor("matrix", string(m.Type), m.Params); ok {
					addAllKnown(inputs)
				}
			}
		}
		// A MAT_DISTANCE params.between extension grouper reads what
		// its FieldInputs hook declares, exactly as in Request.Groups.
		for _, m := range req.Matrices {
			g, has := vectors.SpecBetween(m)
			if !has {
				continue
			}
			if _, custom := ext.Groupers[g.Type]; custom {
				inputs, ok := ext.FieldInputsFor("grouper", string(g.Type), g.Params)
				if !ok {
					out.Widen()
					return out
				}
				addAllKnown(inputs)
			}
		}
		for _, w := range req.Windows {
			if w == nil {
				continue
			}
			if _, custom := ext.Windows[w.Type]; custom {
				inputs, ok := ext.FieldInputsFor("window", string(w.Type), w.Params)
				if !ok {
					out.Widen()
					return out
				}
				addAllKnown(inputs)
			}
		}
	}

	return out
}

// windowsEmitRecordRows reports whether req is the windows-only shape
// whose result rows are the filtered records themselves: windows with
// no group, no aggregation and no crosstab (Processor.Process's
// record-row arm). Each such row carries every schema field, so the
// projection retains the whole record for it.
func windowsEmitRecordRows(req *types.Request) bool {
	return req != nil && len(req.Windows) > 0 && len(req.Groups) == 0 &&
		len(req.Aggregations) == 0 && req.Crosstab == nil
}

// requestWeightFields lists the weight fields req names explicitly:
// the request-level weight and each slot's own (aggregations, crosstab
// cell and margin aggregations, tests, post-tests, regressions,
// attributes, overlays, groups and crosstab axes). A `null` slot names
// none.
func requestWeightFields(req *types.Request) []string {
	var out []string
	add := func(w types.SlotWeight) {
		if spec := w.Spec(); spec != nil {
			out = append(out, spec.Field)
		}
	}
	if req.Weight != nil {
		out = append(out, req.Weight.Field)
	}
	for _, a := range req.Aggregations {
		if a != nil {
			add(a.Weight)
		}
	}
	if ct := req.Crosstab; ct != nil {
		for _, a := range ct.CellAggregations() {
			if a != nil {
				add(a.Weight)
			}
		}
		for _, a := range ct.MarginAggregations {
			if a != nil {
				add(a.Weight)
			}
		}
	}
	for _, t := range req.Tests {
		if t != nil {
			add(t.Weight)
		}
	}
	for _, t := range req.PostTests {
		if t != nil {
			add(t.Weight)
		}
	}
	for _, r := range req.Regressions {
		if r != nil {
			add(r.Weight)
		}
	}
	for _, m := range req.Matrices {
		add(m.Weight)
	}
	for _, a := range req.Attributes {
		if a != nil {
			add(a.Weight)
		}
	}
	for _, o := range req.Overlays {
		add(o.Weight)
	}
	groups := req.Groups
	if ct := req.Crosstab; ct != nil {
		groups = append(append(append([]*types.Group(nil), groups...), ct.Rows...), ct.Columns...)
	}
	for _, g := range groups {
		if g != nil {
			add(g.Weight)
		}
	}
	return out
}

// schemaFieldSet returns the field-name set for the schema.
func schemaFieldSet(schema *encoding.Schema) map[string]struct{} {
	m := make(map[string]struct{}, len(schema.Fields))
	for _, f := range schema.Fields {
		m[f.Name] = struct{}{}
	}
	return m
}

// exprVectorMembers maps each of req's named vectors to its resolved
// members (nil when the request defines none or one fails to resolve —
// that request is refused before any decode).
func exprVectorMembers(req *types.Request, schema *encoding.Schema) map[string][]string {
	resolved := ExprVectors(req, schema)
	if len(resolved) == 0 {
		return nil
	}
	m := make(map[string][]string, len(resolved))
	for _, r := range resolved {
		m[r.Name] = r.SourceFields()
	}
	return m
}

// appendExprIdents parses src as an expr-lang expression and adds every
// identifier that matches a schema field name to out, and every member
// of an identifier that names a request vector (vecs: name -> members;
// nil: none). Returns false when the expression fails to parse — caller
// must widen the set.
func appendExprIdents(out FieldSet, src string, known map[string]struct{}, vecs map[string][]string) bool {
	tree, err := exprparser.Parse(src)
	if err != nil {
		return false
	}
	exprast.Walk(&tree.Node, &exprIdentVisitor{out: out, known: known, vecs: vecs})
	return true
}

// exprIdentVisitor collects IdentifierNode values that match schema
// field names, expanding a named vector to its members. Member access
// (e.g., date.year) is rooted at the base identifier so the underlying
// source field is captured.
type exprIdentVisitor struct {
	out   FieldSet
	known map[string]struct{}
	vecs  map[string][]string
}

func (v *exprIdentVisitor) Visit(node *exprast.Node) {
	id, ok := (*node).(*exprast.IdentifierNode)
	if !ok {
		return
	}
	if _, ok := v.known[id.Value]; ok {
		v.out.Add(id.Value)
		return
	}
	for _, m := range v.vecs[id.Value] {
		if _, ok := v.known[m]; ok {
			v.out.Add(m)
		}
	}
}

// addAggParamFields walks an aggregation's Params for built-in field
// references that don't live on the Aggregation struct itself. Today
// that is the cohort-analytics family:
//
//   - AGG_DISTINCT_SUM:  Params.distinct_by
//   - AGG_WEIGHTED_MEAN: Params.weight_field
//   - AGG_RATIO:         Params.numerator_field, Params.denominator_field
//   - AGG_MAPE / AGG_RMSE / AGG_MAE: Params.prediction_field
//   - AGG_AUC:           Params.outcome
//
// A field named ONLY here and not projected is decoded onto no Record,
// so Record.NumericValue answers ok=false for it on every row and the
// operator reads that as a missing input rather than as a broken
// request — a confident, silent zero with n_null equal to the full
// admitted count. Any future built-in whose Params name a schema field
// belongs in this switch on the same day it is added.
//
// Extension aggregators take a separate code path that defers to the
// registered FieldInputs hook; this helper handles only the built-ins.
func addAggParamFields(out FieldSet, a *types.Aggregation, known map[string]struct{}) {
	if a == nil {
		return
	}
	switch a.Type {
	case types.AGG_DISTINCT_SUM:
		// The distinct KEY. The summed VALUE rides a.Field and is
		// projected by the caller.
		if name, ok := jsonStringField(a.Params, "distinct_by"); ok {
			if _, in := known[name]; in {
				out.Add(name)
			}
		}
	case types.AGG_WEIGHTED_MEAN:
		if name, ok := jsonStringField(a.Params, "weight_field"); ok {
			if _, in := known[name]; in {
				out.Add(name)
			}
		}
	case types.AGG_MAPE, types.AGG_RMSE, types.AGG_MAE:
		if name, ok := jsonStringField(a.Params, "prediction_field"); ok {
			if _, in := known[name]; in {
				out.Add(name)
			}
		}
	case types.AGG_AUC:
		if name, ok := jsonStringField(a.Params, "outcome"); ok {
			if _, in := known[name]; in {
				out.Add(name)
			}
		}
	case types.AGG_RATIO:
		if name, ok := jsonStringField(a.Params, "numerator_field"); ok {
			if _, in := known[name]; in {
				out.Add(name)
			}
		}
		if name, ok := jsonStringField(a.Params, "denominator_field"); ok {
			if _, in := known[name]; in {
				out.Add(name)
			}
		}
	}
}

// jsonStringField unmarshals a single string-valued key from a raw
// JSON params block. Returns (value, true) on a clean match, ("",
// false) on any decode failure or absence.
func jsonStringField(raw json.RawMessage, key string) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return "", false
	}
	v, ok := m[key]
	if !ok {
		return "", false
	}
	var s string
	if err := json.Unmarshal(v, &s); err != nil {
		return "", false
	}
	if s == "" {
		return "", false
	}
	return s, true
}
