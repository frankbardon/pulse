package guide

import (
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
)

// Checks is the no-execute validation the caller (the facade) runs for
// Explain's request mode: each check predicts its root over the
// root's cohort — header, schema and sidecar, never a record — and
// returns predict's envelope. An error ends Explain (a cohort that
// cannot be read). A nil check is skipped, and Explain says what it
// could not check. Request is called only for a request (or Compose
// slot, or a chain's first stage) that names a cohort, Chain and Facet
// only when their root names one; Compose always.
type Checks struct {
	Request func(*types.Request) (*descriptor.Envelope, error)
	Compose func(*types.ComposedRequest) (*descriptor.Envelope, error)
	Chain   func(*types.ChainRequest) (*descriptor.Envelope, error)
	Facet   func(*types.FacetRequest) (*descriptor.Envelope, error)
}

// ValidateExplainRequest checks the parts of req that need no cohort:
// exactly one request root (request mode) or a response with at most
// its request companion (response mode), and a known detail level. It
// returns the root and the effective detail.
func ValidateExplainRequest(req descriptor.ExplainRequest) (descriptor.ExplainRoot, descriptor.ExplainDetail, error) {
	detail, err := explainDetail(req.Detail)
	if err != nil {
		return "", "", err
	}
	if req.Response != nil {
		var extra []string
		for _, r := range []struct {
			set  bool
			name descriptor.ExplainRoot
		}{
			{req.Composed != nil, descriptor.ExplainRootComposed},
			{req.Chain != nil, descriptor.ExplainRootChain},
			{req.Facet != nil, descriptor.ExplainRootFacet},
			{req.Sample != nil, descriptor.ExplainRootSample},
		} {
			if r.set {
				extra = append(extra, string(r.name))
			}
		}
		if len(extra) > 0 {
			return "", "", errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				"explain: a response takes only the request that produced it as its companion",
				map[string]any{"field": "request", "roots": extra, "valid": []string{string(descriptor.ExplainRootRequest)}})
		}
		return descriptor.ExplainRootResponse, detail, nil
	}
	var roots []string
	root := descriptor.ExplainRoot("")
	for _, r := range []struct {
		set  bool
		name descriptor.ExplainRoot
	}{
		{req.Request != nil, descriptor.ExplainRootRequest},
		{req.Composed != nil, descriptor.ExplainRootComposed},
		{req.Chain != nil, descriptor.ExplainRootChain},
		{req.Facet != nil, descriptor.ExplainRootFacet},
		{req.Sample != nil, descriptor.ExplainRootSample},
	} {
		if r.set {
			roots = append(roots, string(r.name))
			root = r.name
		}
	}
	valid := []string{
		string(descriptor.ExplainRootRequest), string(descriptor.ExplainRootComposed), string(descriptor.ExplainRootChain),
		string(descriptor.ExplainRootFacet), string(descriptor.ExplainRootSample),
	}
	if len(roots) != 1 {
		return "", "", errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			"explain: set exactly one request root (request, composed, chain, facet or sample), or a response",
			map[string]any{"field": "request", "roots": roots, "valid": valid})
	}
	return root, detail, nil
}

// explainDetail resolves the detail level: empty is terse.
func explainDetail(d descriptor.ExplainDetail) (descriptor.ExplainDetail, error) {
	switch d {
	case "":
		return descriptor.ExplainTerse, nil
	case descriptor.ExplainTerse, descriptor.ExplainFull:
		return d, nil
	}
	return "", errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
		"explain: detail must be terse or full",
		map[string]any{"field": "detail", "detail": string(d),
			"valid": []string{string(descriptor.ExplainTerse), string(descriptor.ExplainFull)}})
}

// Explain describes what the request root in req will do (request
// mode) for the instance inst. Steps come from each slot's structure
// plus its operator's plain purpose; checks supply the inferred
// defaults, the advisories and predict's verdict. Every sentence passes
// the instance's prose scrub, and no hidden operator is named.
func Explain(inst *descx.InstanceSnapshot, req descriptor.ExplainRequest, checks Checks) (*descriptor.ExplainResult, error) {
	root, detail, err := ValidateExplainRequest(req)
	if err != nil {
		return nil, err
	}
	e := &explainer{
		inst:   inst,
		scrub:  descx.NewProseScrub(inst),
		g:      inst.Ontology(),
		full:   detail == descriptor.ExplainFull,
		checks: checks,
		counts: map[*role]int{},
		seenOp: map[string]bool{},
		seenCv: map[string]bool{},
		res: &descriptor.ExplainResult{
			Mode:     descriptor.ExplainModeRequest,
			Root:     root,
			Detail:   detail,
			Findings: []descriptor.ExplainFinding{},
		},
	}
	switch root {
	case descriptor.ExplainRootResponse:
		e.res.Mode = descriptor.ExplainModeResponse
		e.explainResponse(req.Response, req.Request)
	case descriptor.ExplainRootRequest:
		err = e.explainRequest(req.Request)
	case descriptor.ExplainRootComposed:
		err = e.explainComposed(req.Composed)
	case descriptor.ExplainRootChain:
		err = e.explainChain(req.Chain)
	case descriptor.ExplainRootFacet:
		err = e.explainFacet(req.Facet)
	case descriptor.ExplainRootSample:
		e.explainSample(req.Sample)
	}
	if err != nil {
		return nil, err
	}
	e.finish()
	return e.res, nil
}

// role is one kind of step: Lead opens its text ("Aggregation 2"), One
// and Many count it in the summary.
type role struct {
	lead, one, many string
}

var (
	roleJoin       = &role{"Join", "join", "joins"}
	roleFilter     = &role{"Filter", "filter", "filters"}
	roleFeature    = &role{"Feature", "feature column", "feature columns"}
	roleAttribute  = &role{"Attribute", "attribute column", "attribute columns"}
	roleWindow     = &role{"Window", "window column", "window columns"}
	roleGroup      = &role{"Grouping", "grouping", "groupings"}
	roleAgg        = &role{"Aggregation", "aggregation", "aggregations"}
	roleCrosstab   = &role{"The crosstab", "crosstab", "crosstabs"}
	roleTest       = &role{"Test", "test", "tests"}
	rolePostTest   = &role{"Post-test", "post-test", "post-tests"}
	roleRegression = &role{"Regression", "regression", "regressions"}
	roleMatrix     = &role{"Matrix", "matrix result", "matrix results"}
	roleOverlay    = &role{"Overlay", "overlay", "overlays"}
)

// summaryOrder is the order the summary counts steps in.
var summaryOrder = []*role{
	roleJoin, roleFilter, roleFeature, roleAttribute, roleWindow, roleGroup, roleAgg,
	roleCrosstab, roleTest, rolePostTest, roleRegression, roleMatrix, roleOverlay,
}

// inferential roles carry assumptions worth stating in full detail.
var inferentialRoles = map[*role]bool{roleTest: true, rolePostTest: true, roleRegression: true}

type explainer struct {
	inst   *descx.InstanceSnapshot
	scrub  descx.ProseScrub
	g      *descx.OntologyGraph
	full   bool
	checks Checks
	res    *descriptor.ExplainResult

	counts     map[*role]int
	ops        []string // operators named, first seen first
	seenOp     map[string]bool
	inferOps   []string // operators named in an inferential role
	notes      []string // response mode: full-detail sentences beyond the summary
	caveats    []string
	seenCv     map[string]bool
	uncheckedN int // request roots that name no cohort
}

// stepIn is one operator slot to describe.
type stepIn struct {
	slot      string
	role      *role
	n         int // 1-based position; 0 = unnumbered
	op        string
	defaulted bool
	fields    []string
	detail    string // structural phrase after the operator
}

func (e *explainer) lead(in stepIn) string {
	if in.n == 0 {
		return in.role.lead
	}
	return in.role.lead + " " + strconv.Itoa(in.n)
}

// opStep describes one operator slot and records its operator for the
// glossary, follow-up and assumption passes.
func (e *explainer) opStep(in stepIn) {
	e.counts[in.role]++
	lead := e.lead(in)
	st := descriptor.ExplainStep{Slot: in.slot, Fields: in.fields}
	switch {
	case in.op == "":
		st.Text = lead + in.detail + " names no operator; smart defaults infer one from the field's type when the cohort is read."
	case e.inst.Hidden(in.op):
		st.Text = lead + in.detail + " names an operator this instance does not offer."
		e.caveat(lead + " names an operator this instance does not offer, so the request cannot run here.")
	default:
		st.Operator = in.op
		st.Defaulted = in.defaulted
		text := lead + " runs " + in.op + in.detail + "."
		if in.defaulted && len(in.fields) > 0 {
			text += " " + in.op + " is inferred for " + tick(in.fields[0]) + " from its type."
		}
		if p, ok := purposeOf(e.inst, in.op); ok {
			if plain := strings.TrimSpace(e.scrub.Text(p.Plain)); plain != "" {
				text += " " + plain
			}
		}
		st.Text = text
		e.noteOp(in.op, inferentialRoles[in.role])
	}
	e.res.Steps = append(e.res.Steps, st)
}

// plainStep is a step that runs no operator (a join, the weight, a
// facet's field list).
func (e *explainer) plainStep(slot string, fields []string, text string) {
	e.res.Steps = append(e.res.Steps, descriptor.ExplainStep{Slot: slot, Fields: fields, Text: text})
}

func (e *explainer) noteOp(op string, inferential bool) {
	if e.seenOp[op] {
		return
	}
	e.seenOp[op] = true
	e.ops = append(e.ops, op)
	if inferential {
		e.inferOps = append(e.inferOps, op)
	}
}

func (e *explainer) caveat(s string) {
	if s == "" || e.seenCv[s] {
		return
	}
	e.seenCv[s] = true
	e.caveats = append(e.caveats, s)
}

// absorb folds a check's envelope into the result: its errors become
// caveats (prefix names the slot), its verdict joins Valid. Advisories
// are taken separately: a Compose root's slot advisories already ride
// the Compose check, so a slot predict's own are not repeated.
func (e *explainer) absorb(env *descriptor.Envelope, prefix string) {
	if env == nil {
		return
	}
	ok := len(env.Errors) == 0
	if e.res.Valid == nil {
		e.res.Valid = &ok
	} else if !ok {
		*e.res.Valid = false
	}
	for _, er := range env.Errors {
		if er == nil {
			continue
		}
		ref := descriptor.EnvelopeEntry{Code: er.Code, Message: er.Message}
		if prefix != "" || len(er.Details) > 0 {
			ref.Details = map[string]any{}
			for k, v := range er.Details {
				ref.Details[k] = v
			}
			if prefix != "" {
				ref.Details["slot"] = strings.TrimSuffix(prefix, ".")
			}
		}
		e.res.Refusals = append(e.res.Refusals, ref)
		// The caveat names the code only: the message is predict's
		// prose, relayed whole in Refusals, not an Explain template.
		e.caveat("Predict refuses " + prefixWords(prefix) + "with " + er.Code + "; pulse errors lookup " + er.Code + " says how to fix it.")
	}
}

func prefixWords(prefix string) string {
	if prefix == "" {
		return "this request "
	}
	return strings.TrimSuffix(prefix, ".") + " "
}

func (e *explainer) advisories(env *descriptor.Envelope) {
	if env == nil {
		return
	}
	var advs []descriptor.Advisory
	switch d := env.Data.(type) {
	case *descriptor.PredictResult:
		advs = d.Advisories
	case *descx.ComposeValidationResult:
		advs = d.Advisories
	case *descx.FacetValidationResult:
		advs = d.Advisories
	}
	e.res.Advisories = append(e.res.Advisories, advs...)
}

// defaults returns the operators predict inferred, keyed by the slot's
// wire path ("aggregations[0]"), and records them on the result with
// goPrefix before predict's own path.
func (e *explainer) defaults(env *descriptor.Envelope, goPrefix []string) map[string]string {
	if env == nil {
		return nil
	}
	pr, ok := env.Data.(*descriptor.PredictResult)
	if !ok {
		return nil
	}
	out := map[string]string{}
	for _, d := range pr.DefaultsApplied {
		if len(d.Path) >= 2 {
			if key, ok := defaultSlotKey[d.Path[0]]; ok {
				out[key+"["+d.Path[1]+"]"] = d.Type
			}
		}
		d.Path = append(append([]string(nil), goPrefix...), d.Path...)
		e.res.DefaultsApplied = append(e.res.DefaultsApplied, d)
	}
	return out
}

// defaultSlotKey maps the Go field names DefaultApplied paths start
// with to their wire keys.
var defaultSlotKey = map[string]string{"Aggregations": "aggregations", "Groups": "groups"}

// explainRequest is the Request root.
func (e *explainer) explainRequest(r *types.Request) error {
	env, err := e.checkRequest(r)
	if err != nil {
		return err
	}
	e.absorb(env, "")
	e.advisories(env)
	e.requestSteps("", r, e.defaults(env, nil))
	e.res.Summary = "This request " + readsCohort(r.Cohort) + e.runsPhrase() + "."
	return nil
}

// checkRequest predicts r when it names a cohort and a check is wired.
func (e *explainer) checkRequest(r *types.Request) (*descriptor.Envelope, error) {
	if r.Cohort == nil || e.checks.Request == nil {
		e.uncheckedN++
		return nil, nil
	}
	return e.checks.Request(r)
}

func readsCohort(c *types.Cohort) string {
	if c == nil || c.Filename == "" {
		return ""
	}
	return "reads " + tick(c.Filename) + " and "
}

// runsPhrase counts the steps by role: "runs 1 filter and 2
// aggregations", or "names no analysis step".
func (e *explainer) runsPhrase() string {
	var parts []string
	for _, r := range summaryOrder {
		n := e.counts[r]
		switch {
		case n == 1:
			parts = append(parts, "1 "+r.one)
		case n > 1:
			parts = append(parts, strconv.Itoa(n)+" "+r.many)
		}
	}
	if len(parts) == 0 {
		return "names no analysis step"
	}
	return "runs " + andList(parts)
}

// requestSteps describes one Request's slots in the order the engine
// applies them, prefix naming the request's place in its root.
func (e *explainer) requestSteps(prefix string, r *types.Request, inferred map[string]string) {
	for i, j := range r.Joins {
		if j == nil {
			continue
		}
		var fields, pairs []string
		for _, on := range j.On {
			fields = append(fields, on.LeftField)
			pairs = append(pairs, tick(on.LeftField)+" matches "+tick(on.RightField))
		}
		e.counts[roleJoin]++
		text := "Join " + strconv.Itoa(i+1) + " adds the fields of " + tick(j.Right)
		if len(pairs) > 0 {
			text += " to each row where " + andList(pairs)
		}
		e.plainStep(prefix+"joins["+strconv.Itoa(i)+"]", fields, text+".")
	}
	for i, f := range r.Filterers {
		if f == nil {
			continue
		}
		detail := onFields(nonEmpty(f.Field))
		switch {
		case len(f.Values) == 1:
			detail += " with 1 listed value"
		case len(f.Values) > 1:
			detail += " with " + strconv.Itoa(len(f.Values)) + " listed values"
		}
		if f.Expression != "" {
			detail += " with an expression"
		}
		e.opStep(stepIn{slot: prefix + "filterers[" + strconv.Itoa(i) + "]", role: roleFilter, n: i + 1,
			op: string(f.Type), fields: nonEmpty(f.Field), detail: detail})
	}
	for i, f := range r.Features {
		if f == nil {
			continue
		}
		e.opStep(stepIn{slot: prefix + "features[" + strconv.Itoa(i) + "]", role: roleFeature, n: i + 1,
			op: string(f.Type), fields: nonEmpty(f.Field), detail: onFields(nonEmpty(f.Field))})
	}
	for i, a := range r.Attributes {
		if a == nil {
			continue
		}
		fields, detail := nonEmpty(a.Field), onFields(nonEmpty(a.Field))
		if a.Target != "" {
			fields = append(nonEmpty(a.Target), a.Predictors...)
			detail = predicting(a.Target, a.Predictors)
		}
		e.opStep(stepIn{slot: prefix + "attributes[" + strconv.Itoa(i) + "]", role: roleAttribute, n: i + 1,
			op: string(a.Type), fields: fields, detail: detail})
	}
	for i, w := range r.Windows {
		if w == nil {
			continue
		}
		fields, detail := nonEmpty(w.Field), onFields(nonEmpty(w.Field))
		if len(w.PartitionBy) > 0 {
			detail += " within each " + andList(ticks(w.PartitionBy))
			fields = append(fields, w.PartitionBy...)
		}
		var order []string
		for _, o := range w.OrderBy {
			order = append(order, o.Field)
		}
		if len(order) > 0 {
			detail += ", ordered by " + andList(ticks(order))
			fields = append(fields, order...)
		}
		e.opStep(stepIn{slot: prefix + "windows[" + strconv.Itoa(i) + "]", role: roleWindow, n: i + 1,
			op: string(w.Type), fields: fields, detail: detail})
	}
	for i, g := range r.Groups {
		if g == nil {
			continue
		}
		e.opStep(e.inferredStep(stepIn{slot: prefix + "groups[" + strconv.Itoa(i) + "]", role: roleGroup, n: i + 1,
			op: string(g.Type), fields: nonEmpty(g.Field), detail: onFields(nonEmpty(g.Field))}, "groups", i, inferred))
	}
	for i, a := range r.Aggregations {
		if a == nil {
			continue
		}
		e.opStep(e.inferredStep(stepIn{slot: prefix + "aggregations[" + strconv.Itoa(i) + "]", role: roleAgg, n: i + 1,
			op: string(a.Type), fields: nonEmpty(a.Field), detail: onFields(nonEmpty(a.Field))}, "aggregations", i, inferred))
	}
	if ct := r.Crosstab; ct != nil {
		var rows, cols, fields []string
		for _, g := range ct.Rows {
			if g != nil && g.Field != "" {
				rows = append(rows, g.Field)
			}
		}
		for _, g := range ct.Columns {
			if g != nil && g.Field != "" {
				cols = append(cols, g.Field)
			}
		}
		fields = append(append(fields, rows...), cols...)
		op, detail := "", " in each cell"
		if ct.Cell != nil {
			op = string(ct.Cell.Type)
			if ct.Cell.Field != "" {
				detail = " on " + tick(ct.Cell.Field) + " in each cell"
				fields = append(fields, ct.Cell.Field)
			}
		}
		if len(rows) > 0 {
			detail += ", with rows by " + andList(ticks(rows))
		}
		if len(cols) > 0 {
			detail += " and columns by " + andList(ticks(cols))
		}
		e.opStep(stepIn{slot: prefix + "crosstab", role: roleCrosstab, op: op, fields: fields, detail: detail})
	}
	for i, t := range r.Tests {
		if t != nil {
			e.opStep(testStep(prefix+"tests["+strconv.Itoa(i)+"]", roleTest, i+1, t))
		}
	}
	for i, t := range r.PostTests {
		if t != nil {
			e.opStep(testStep(prefix+"post_tests["+strconv.Itoa(i)+"]", rolePostTest, i+1, t))
		}
	}
	for i, g := range r.Regressions {
		if g == nil {
			continue
		}
		e.opStep(stepIn{slot: prefix + "regressions[" + strconv.Itoa(i) + "]", role: roleRegression, n: i + 1,
			op: string(g.Type), fields: append(nonEmpty(g.Target), g.Predictors...), detail: predicting(g.Target, g.Predictors)})
	}
	for i, m := range r.Matrices {
		detail := ""
		switch {
		case len(m.Fields) > 0:
			detail = " over " + andList(ticks(m.Fields))
		case m.Vector != "":
			detail = " over the vector " + tick(m.Vector)
		}
		e.opStep(stepIn{slot: prefix + "matrices[" + strconv.Itoa(i) + "]", role: roleMatrix, n: i + 1,
			op: string(m.Type), fields: m.Fields, detail: detail})
	}
	for i, o := range r.Overlays {
		e.opStep(stepIn{slot: prefix + "overlays[" + strconv.Itoa(i) + "]", role: roleOverlay, n: i + 1,
			op: string(o.Kind), detail: scopePhrase(o.Scope)})
	}
	if w := r.Weight; w != nil && w.Field != "" {
		text := "Rows are weighted by " + tick(w.Field)
		if w.Kind != "" {
			text += " as " + string(w.Kind) + " weights"
		}
		e.plainStep(prefix+"weight", []string{w.Field}, text+".")
	}
	if m := r.Multiplicity; m != nil {
		e.plainStep(prefix+"multiplicity", nil, adjustSentence(m))
	}
}

// inferredStep fills an untyped slot's operator from predict's
// defaults.
func (e *explainer) inferredStep(in stepIn, key string, i int, inferred map[string]string) stepIn {
	if in.op == "" {
		if op, ok := inferred[key+"["+strconv.Itoa(i)+"]"]; ok {
			in.op, in.defaulted = op, true
		}
	}
	return in
}

func testStep(slot string, r *role, n int, t *types.Test) stepIn {
	fields := nonEmpty(t.Field, t.Field2)
	detail := onFields(fields)
	if t.SplitBy != "" {
		detail += " split by " + tick(t.SplitBy)
		fields = append(fields, t.SplitBy)
	}
	if t.Rows != "" || t.Cols != "" {
		detail += " across " + andList(ticks(nonEmpty(t.Rows, t.Cols)))
		fields = append(fields, nonEmpty(t.Rows, t.Cols)...)
	}
	if t.SubjectField != "" {
		detail += " per subject " + tick(t.SubjectField)
		fields = append(fields, t.SubjectField)
	}
	if t.Alpha > 0 {
		detail += " at alpha " + strconv.FormatFloat(t.Alpha, 'g', -1, 64)
	}
	return stepIn{slot: slot, role: r, n: n, op: string(t.Type), fields: fields, detail: detail}
}

func adjustSentence(m *types.Multiplicity) string {
	s := "P-values are adjusted for multiple comparisons"
	if m.Method != "" {
		s += " by the " + string(m.Method) + " method"
	}
	if m.Family != "" {
		s += " over the " + string(m.Family) + " family"
	}
	if m.Alpha > 0 {
		s += " at alpha " + strconv.FormatFloat(m.Alpha, 'g', -1, 64)
	}
	return s + "."
}

func scopePhrase(s types.OverlayScope) string {
	if s == "" {
		return ""
	}
	return " at " + string(s) + " scope"
}

func predicting(target string, predictors []string) string {
	detail := ""
	if target != "" {
		detail = " predicting " + tick(target)
	}
	if len(predictors) > 0 {
		detail += " from " + andList(ticks(predictors))
	}
	return detail
}

// explainComposed is the Compose root: each slot as a Request, then
// the Compose-level overlays and correction.
func (e *explainer) explainComposed(c *types.ComposedRequest) error {
	if e.checks.Compose != nil {
		env, err := e.checks.Compose(c)
		if err != nil {
			return err
		}
		e.absorb(env, "")
		e.advisories(env)
	}
	for i, r := range c.Requests {
		if r == nil {
			continue
		}
		prefix := "requests[" + strconv.Itoa(i) + "]."
		env, err := e.checkRequest(r)
		if err != nil {
			return err
		}
		e.absorb(env, prefix)
		e.requestSteps(prefix, r, e.defaults(env, []string{"Requests", strconv.Itoa(i)}))
	}
	for i, o := range c.Overlays {
		detail := ""
		if len(o.Targets) > 0 {
			detail = " comparing " + andList(ticks(o.Targets))
		}
		if o.Reference != "" {
			detail += " with the reference " + tick(o.Reference)
		}
		e.opStep(stepIn{slot: "overlays[" + strconv.Itoa(i) + "]", role: roleOverlay, n: i + 1, op: string(o.Kind), detail: detail})
	}
	if c.Multiplicity != nil {
		e.plainStep("multiplicity", nil, adjustSentence(c.Multiplicity))
	}
	e.res.Summary = "This request batch runs " + count(len(c.Requests), "request side by side", "requests side by side") +
		" and in all " + e.runsPhrase() + "."
	return nil
}

// explainChain is the chain root: stage 0 reads the cohort, each later
// stage the rows the stage before it returns.
func (e *explainer) explainChain(c *types.ChainRequest) error {
	if c.Cohort != nil && e.checks.Chain != nil {
		env, err := e.checks.Chain(c)
		if err != nil {
			return err
		}
		e.absorb(env, "")
	} else {
		e.uncheckedN++
	}
	laterUntyped := false
	for i, st := range c.Stages {
		if st == nil || st.Request == nil {
			continue
		}
		prefix := "stages[" + strconv.Itoa(i) + "].request."
		var inferred map[string]string
		if i == 0 && c.Cohort != nil && e.checks.Request != nil {
			first := *st.Request
			first.Cohort = c.Cohort
			env, err := e.checks.Request(&first)
			if err != nil {
				return err
			}
			inferred = e.defaults(env, []string{"Stages", "0", "Request"})
		}
		if i > 0 && hasUntyped(st.Request) {
			laterUntyped = true
		}
		e.requestSteps(prefix, st.Request, inferred)
	}
	for i, o := range c.Overlays {
		if o == nil {
			continue
		}
		detail := " comparing " + stageName(o.Target) + " with " + stageName(o.Ref)
		e.opStep(stepIn{slot: "overlays[" + strconv.Itoa(i) + "]", role: roleOverlay, n: i + 1, op: string(o.Kind), detail: detail})
	}
	if laterUntyped {
		e.caveat("A stage after the first leaves an operator to smart defaults; it is inferred from the previous stage's output when the chain runs.")
	}
	e.res.Summary = "This chain " + readsCohort(c.Cohort) + "runs " + count(len(c.Stages), "stage", "stages") +
		", each on the rows the stage before it returns, and in all " + e.runsPhrase() + "."
	return nil
}

func hasUntyped(r *types.Request) bool {
	for _, a := range r.Aggregations {
		if a != nil && a.Type == "" {
			return true
		}
	}
	for _, g := range r.Groups {
		if g != nil && g.Type == "" {
			return true
		}
	}
	return false
}

func stageName(s types.StageRef) string {
	if s.Name != "" {
		return "stage " + tick(s.Name)
	}
	if s.Index != nil {
		return "stage " + strconv.Itoa(*s.Index)
	}
	return "a stage"
}

// explainFacet is the facet root: per-field summaries, narrowed by
// filters, decorated by overlays.
func (e *explainer) explainFacet(f *types.FacetRequest) error {
	if f.Cohort != nil && e.checks.Facet != nil {
		env, err := e.checks.Facet(f)
		if err != nil {
			return err
		}
		e.absorb(env, "")
		e.advisories(env)
	} else {
		e.uncheckedN++
	}
	if len(f.Fields) > 0 {
		e.plainStep("fields", f.Fields, "Summarises "+andList(ticks(f.Fields))+
			": a count per value for a category field, and summary figures for a numeric one.")
	}
	for i, fl := range f.Filterers {
		if fl == nil {
			continue
		}
		e.opStep(stepIn{slot: "filterers[" + strconv.Itoa(i) + "]", role: roleFilter, n: i + 1,
			op: string(fl.Type), fields: nonEmpty(fl.Field), detail: onFields(nonEmpty(fl.Field))})
	}
	if len(f.AdditiveFields) > 0 {
		e.plainStep("additive_fields", f.AdditiveFields, "Counts every value of "+andList(ticks(f.AdditiveFields))+
			" as if that field's own filters were lifted.")
	}
	if f.DiscreteTopK > 0 {
		e.plainStep("discrete_top_k", nil, "Keeps the "+strconv.Itoa(f.DiscreteTopK)+" most common values of each category field.")
	}
	if len(f.NumericPercentiles) > 0 {
		var ps []string
		for _, p := range f.NumericPercentiles {
			ps = append(ps, strconv.FormatFloat(p, 'g', -1, 64))
		}
		e.plainStep("numeric_percentiles", nil, "Adds the "+andList(ps)+" quantiles of each numeric field.")
	}
	if f.IncludeHistogram {
		bins := f.HistogramBins
		if bins == 0 {
			bins = 20
		}
		e.plainStep("include_histogram", nil, "Adds a "+strconv.Itoa(bins)+"-bin histogram of each numeric field.")
	}
	for i, o := range f.Overlays {
		e.opStep(stepIn{slot: "overlays[" + strconv.Itoa(i) + "]", role: roleOverlay, n: i + 1, op: string(o.Kind), detail: scopePhrase(o.Scope)})
	}
	s := "This facet request summarises " + count(len(f.Fields), "field", "fields")
	if f.Cohort != nil && f.Cohort.Filename != "" {
		s += " of " + tick(f.Cohort.Filename)
	}
	if rest := e.runsPhrase(); len(e.counts) > 0 {
		s += " and " + rest
	}
	e.res.Summary = s + "."
	return nil
}

// explainSample is the sample root. Nothing is predicted: a sample
// names no operator.
func (e *explainer) explainSample(s *types.SampleRequest) {
	e.plainStep("n", nil, "Returns up to "+count(s.N, "row", "rows")+" of the cohort as they are stored.")
	for i, l := range s.Labels {
		if l == nil {
			continue
		}
		e.plainStep("labels["+strconv.Itoa(i)+"]", nonEmpty(l.Field), "Shows "+tick(l.Field)+" through the label table "+tick(l.Table)+".")
	}
	sum := "This sample request returns up to " + count(s.N, "row", "rows")
	if s.Cohort != nil && s.Cohort.Filename != "" {
		sum += " of " + tick(s.Cohort.Filename)
	}
	e.res.Summary = sum + "."
}

// finish adds the unchecked caveat, the full-detail parts and scrubs
// every sentence.
func (e *explainer) finish() {
	if e.uncheckedN > 0 {
		e.caveats = append([]string{"No cohort is named, so the field types, the inferred operators and the fit checks are not checked."}, e.caveats...)
	}
	if e.full {
		for _, op := range e.inferOps {
			if p, ok := purposeOf(e.inst, op); ok {
				for _, a := range p.Assumptions {
					e.caveat(op + ": " + strings.TrimSpace(a))
				}
			}
		}
		e.res.GlossaryRefs = e.glossaryRefs()
		e.res.FollowUps = e.followUps()
	}
	sc := e.scrub.Text
	e.res.Summary = sc(e.res.Summary)
	for i := range e.res.Steps {
		e.res.Steps[i].Text = sc(e.res.Steps[i].Text)
	}
	for i := range e.res.Advisories {
		e.res.Advisories[i].Message = sc(e.res.Advisories[i].Message)
	}
	for _, c := range e.caveats {
		if c = sc(c); c != "" {
			e.res.Caveats = append(e.res.Caveats, c)
		}
	}
	if e.full {
		e.res.Sentences = append(e.res.Sentences, e.res.Summary)
		for _, st := range e.res.Steps {
			if st.Text != "" {
				e.res.Sentences = append(e.res.Sentences, st.Text)
			}
		}
		for _, a := range e.res.Advisories {
			if a.Message != "" {
				e.res.Sentences = append(e.res.Sentences, a.Message)
			}
		}
		for _, n := range e.notes {
			if n = sc(n); n != "" {
				e.res.Sentences = append(e.res.Sentences, n)
			}
		}
	}
}

// glossaryRefs lists the glossary terms the named operators link, in
// first-seen order, keeping only terms the instance's pruned ontology
// still carries.
func (e *explainer) glossaryRefs() []string {
	var out []string
	seen := map[string]bool{}
	for _, op := range e.ops {
		p, ok := purposeOf(e.inst, op)
		if !ok {
			continue
		}
		for _, id := range p.Glossary {
			if seen[id] || !e.g.Has(descx.OntologyID(descriptor.OntologyNodeGlossaryTerm, id)) {
				continue
			}
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// followUps lists the named operators' declared follow-ups whose
// target survives the prune, first one per target.
func (e *explainer) followUps() []descriptor.Alternative {
	var out []descriptor.Alternative
	seen := map[string]bool{}
	for _, op := range e.ops {
		p, ok := purposeOf(e.inst, op)
		if !ok {
			continue
		}
		opID := descx.OntologyID(descriptor.OntologyNodeOperator, op)
		for _, a := range keepAlternatives(e.g, opID, descriptor.OntologyEdgeFollowUp, p.FollowUps, e.scrub) {
			if !seen[a.Use] {
				seen[a.Use] = true
				out = append(out, a)
			}
		}
	}
	return out
}

func tick(s string) string { return "`" + s + "`" }

func ticks(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = tick(s)
	}
	return out
}

func onFields(fields []string) string {
	if len(fields) == 0 {
		return ""
	}
	return " on " + andList(ticks(fields))
}

func nonEmpty(ss ...string) []string {
	var out []string
	for _, s := range ss {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func count(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// andList joins items as "a", "a and b", "a, b and c".
func andList(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}
