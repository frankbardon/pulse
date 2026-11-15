package guide

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/postfit"
	"github.com/frankbardon/pulse/types"
)

// explain_postfit.go describes a Compose batch's `post_fit` entries:
// request mode one step per entry at `post_fit[i]` (the index every
// post-fit refusal names — details.post_fit, or a params refusal's
// details.path, located by locatedSlot — and the one PostFit[i]
// answers), response mode one finding per ComposedResponse.PostFit
// entry. A post-fit operator is extension-only, so its step names the
// instance's registration (or says the instance registers none of that
// name) and its Purpose when the registration declares one.

var rolePostFit = &role{"Post-fit entry", "post-fit entry", "post-fit entries"}

// postFitSteps describes c's post-fit entries in the order they run:
// after the fits and optimize blocks, before the comparisons.
func (e *explainer) postFitSteps(c *types.ComposedRequest) {
	for i := range c.PostFit {
		e.postFitStep(i, &c.PostFit[i])
	}
}

// postFitStep is one entry: its operator, label, the fits it reads, its
// params and the phase it runs in.
func (e *explainer) postFitStep(i int, spec *types.PostFitSpec) {
	slot := postfit.Slot + "[" + strconv.Itoa(i) + "]"
	op := string(spec.Type)
	detail := ""
	if spec.Label != "" {
		detail += ", labelled " + tick(spec.Label) + ","
	}
	if len(spec.Fits) > 0 {
		detail += " over " + plural(len(spec.Fits), "the fit ", "the fits ") + andList(ticks(spec.Fits))
	} else {
		detail += " that reads no fit"
	}
	if ps := postFitParams(spec.Params); ps != "" {
		detail += ", with " + ps
	}
	phase := " It runs after the fits and optimize blocks and before any comparison, reads no record, and answers at `" + slot + "` of the response."
	lead := rolePostFit.lead + " " + strconv.Itoa(i+1)
	switch {
	case op == "":
		e.counts[rolePostFit]++
		e.plainStep(slot, nil, lead+" names no post-fit operator."+phase)
		return
	case !e.inst.Hidden(op) && !e.postFitRegistered(spec.Type):
		e.counts[rolePostFit]++
		e.plainStep(slot, nil, lead+" names "+tick(op)+", a post-fit operator this instance does not register."+phase)
		e.caveat(lead + " names a post-fit operator this instance does not register, so the batch cannot run here.")
		return
	}
	mark := len(e.res.Steps)
	e.opStep(stepIn{slot: slot, role: rolePostFit, n: i + 1, op: op, detail: detail})
	if mark < len(e.res.Steps) {
		e.res.Steps[mark].Text += phase
	}
}

// postFitRegistered reports whether the instance registers a post-fit
// operator of the name.
func (e *explainer) postFitRegistered(name types.PostFitType) bool {
	ext := e.inst.Extensions()
	if ext == nil {
		return false
	}
	_, ok := ext.PostFitDecls[name]
	return ok
}

// postFitParams names an entry's params, sorted by key: a scalar with
// its value, a list with its length, an object as one. "" when the entry
// carries none.
func postFitParams(raw json.RawMessage) string {
	var m map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &m) != nil || len(m) == 0 {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		v := strings.TrimSpace(string(m[k]))
		switch {
		case strings.HasPrefix(v, "["):
			var list []json.RawMessage
			_ = json.Unmarshal(m[k], &list)
			parts = append(parts, tick(k)+" ("+count(len(list), "value", "values")+")")
		case strings.HasPrefix(v, "{"):
			parts = append(parts, tick(k)+" (an object)")
		default:
			parts = append(parts, tick(k)+" = "+v)
		}
	}
	return plural(len(keys), "the param ", "the params ") + andList(parts)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// readPostFit reads a ComposedResponse's PostFit, one finding per entry
// at `post_fit[i]`. It returns the entries read.
func (e *explainer) readPostFit(rr *responseReader, results []types.PostFitResult) int {
	for i := range results {
		e.postFitFinding(rr, i, &results[i])
	}
	return len(results)
}

// postFitFinding is one entry's result: descriptive (an embedder's
// computation over finished fits carries no inference Pulse can judge),
// not_computable when the entry failed.
func (e *explainer) postFitFinding(rr *responseReader, i int, r *types.PostFitResult) {
	slot := postfit.Slot + "[" + strconv.Itoa(i) + "]"
	subject := r.Label
	if subject == "" {
		subject = slot
	}
	lead := rolePostFit.lead + " " + tick(subject)
	if r.Type != "" {
		lead += " (" + string(r.Type) + ")"
	}
	f := descriptor.ExplainFinding{Slot: slot, Subject: subject, Verdict: descriptor.VerdictDescriptive, Numbers: map[string]*float64{}}
	if r.Error != nil {
		f.Verdict = descriptor.VerdictNotComputable
		e.addFinding(rr, f, lead+" reports nothing: it failed with "+r.Error.Code+".")
		rr.later = append(rr.later, rolePostFit.lead+" "+tick(subject)+" failed with "+r.Error.Code+"; pulse errors lookup "+r.Error.Code+" says how to fix it.")
		return
	}
	names := make([]string, 0, len(r.Scalars))
	for k := range r.Scalars {
		names = append(names, k)
	}
	sort.Strings(names)
	var scalars []string
	for _, k := range names {
		v := r.Scalars[k]
		setNum(f.Numbers, "scalars."+k, v)
		if finite(v) {
			scalars = append(scalars, tick(k)+" = "+fmtNum(v))
		} else {
			scalars = append(scalars, tick(k)+" undefined")
		}
	}
	tnames := make([]string, 0, len(r.Tables))
	for k := range r.Tables {
		tnames = append(tnames, k)
	}
	sort.Strings(tnames)
	var tables []string
	for _, k := range tnames {
		t := r.Tables[k]
		if t == nil {
			continue
		}
		setNum(f.Numbers, "tables."+k+".rows", float64(len(t.Rows)))
		tables = append(tables, "the table "+tick(k)+" of "+count(len(t.Rows), "row", "rows")+" by "+
			count(len(t.Columns), "column", "columns")+" ("+andList(ticks(t.Columns))+")")
	}
	var parts []string
	if len(scalars) > 0 {
		parts = append(parts, plural(len(scalars), "the scalar ", "the scalars ")+andList(scalars))
	}
	parts = append(parts, tables...)
	text := lead + " reports nothing"
	if len(parts) > 0 {
		text = lead + " reports " + andList(parts)
	}
	e.addFinding(rr, f, text+".")
}
