package guide

import (
	"encoding/json"
	"slices"
	"sort"
	"strconv"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/types"
)

// explain_results.go reads the results larger than one Process
// response: a ComposedResponse slot by slot, a ChainResponse stage by
// stage, a FacetResult field by field. Each reuses the Process
// response reader under a scope ("Request 2", "Stage 1", "Field
// `region`") and a wire-path prefix, sharing one responseReader so the
// many-tests caveat counts the whole result.

// readSlot reads one slot or stage response under scope and path
// prefix at, its sentence ahead of its findings in full detail.
func (e *explainer) readSlot(rr *responseReader, scope, at string, resp *types.Response, req *types.Request) []string {
	e.scope, e.at = scope, at
	defer func() { e.scope, e.at = "", "" }()
	mark := len(rr.notes)
	parts := e.readResponse(rr, resp, req)
	rr.notes = slices.Insert(rr.notes, mark, scope+" reports "+partsPhrase(parts)+recordsPhrase(resp)+".")
	e.slotCaveats(rr, resp, req)
	return parts
}

// readHostOverlays reads a batch's or chain's own overlay layers under
// scope (top-level paths, "overlays[k]").
func (e *explainer) readHostOverlays(rr *responseReader, scope string, layers []*types.OverlayLayer) int {
	e.scope, e.at = scope, ""
	defer func() { e.scope = "" }()
	n := 0
	for i, l := range layers {
		if l == nil {
			continue
		}
		n++
		e.overlayFinding(rr, i+1, l)
	}
	return n
}

// explainComposedResponse reads a ComposedResponse: each slot response
// with its request (req's Requests[i], when the batch request rides
// beside it), then the batch's own overlay layers, each naming the
// correction that ran over it.
func (e *explainer) explainComposedResponse(resp *types.ComposedResponse, req *types.ComposedRequest) {
	rr := &responseReader{}
	slots := 0
	for i, r := range resp.Responses {
		if r == nil {
			continue
		}
		slots++
		var spec *types.Request
		if req != nil && i < len(req.Requests) {
			spec = req.Requests[i]
		}
		e.readSlot(rr, "Request "+strconv.Itoa(i+1), "responses["+strconv.Itoa(i)+"].", r, spec)
	}
	layers := make([]*types.OverlayLayer, len(resp.Overlays))
	for i := range resp.Overlays {
		layers[i] = &resp.Overlays[i]
	}
	overlays := e.readHostOverlays(rr, "The batch", layers)
	if resp.Returned != nil {
		rr.later = append(rr.later, "The batch's overlays were shaped by a return block, so a part it left out is not read here.")
	}
	e.finalCaveats(rr, "composed")
	s := "This batch reports " + count(slots, "response", "responses") + " side by side"
	if overlays > 0 {
		s += " and " + count(overlays, "batch overlay", "batch overlays") + " across them"
	}
	e.res.Summary = s + rr.evidencePhrase() + "."
	e.notes = rr.notes
}

// explainChainResponse reads a ChainResponse stage by stage, then its
// whole-chain overlay layers. Each stage is read with the request the
// chain echoed (normalized_request: what the engine ran, smart defaults
// resolved), else the chain request riding beside it.
func (e *explainer) explainChainResponse(resp *types.ChainResponse, req *types.ChainRequest) {
	rr := &responseReader{}
	src := resp.NormalizedRequest
	if src == nil {
		src = req
	}
	stages := resp.Stages
	if len(stages) == 0 && resp.Final != nil {
		stages = []*types.Response{resp.Final}
	}
	if resp.NormalizedRequest != nil {
		rr.notes = append(rr.notes, "Each stage's operators are named from the request the chain echoes in its normalized_request.")
	}
	n := 0
	var last []string
	var lastResp *types.Response
	for i, st := range stages {
		if st == nil {
			continue
		}
		n++
		var spec *types.Request
		if src != nil && i < len(src.Stages) && src.Stages[i] != nil {
			spec = src.Stages[i].Request
		}
		last, lastResp = e.readSlot(rr, "Stage "+strconv.Itoa(i+1), "stages["+strconv.Itoa(i)+"].", st, spec), st
	}
	overlays := e.readHostOverlays(rr, "The chain", resp.Overlays)
	e.finalCaveats(rr, "chain")
	s := "This chain reports " + count(n, "stage", "stages")
	if n > 1 {
		s += ", each on the rows the stage before it returns"
	}
	if lastResp != nil {
		s += "; its last stage reports " + partsPhrase(last) + recordsPhrase(lastResp)
	}
	if overlays > 0 {
		s += ", with " + count(overlays, "whole-chain overlay", "whole-chain overlays")
	}
	e.res.Summary = s + rr.evidencePhrase() + "."
	e.notes = rr.notes
}

// explainFacetResult reads a FacetResult: one block per facet field —
// the field's summary, then the overlay layers that decorate it — then
// the additive counts and any layer no field could be tied to.
func (e *explainer) explainFacetResult(res *types.FacetResult, req *types.FacetRequest) {
	rr := &responseReader{}
	names := facetFieldOrder(res.Fields, req)
	hosts := facetOverlayHosts(res, req)
	attached := map[int]bool{}
	for _, name := range names {
		e.facetFieldFinding(rr, "fields."+name, name, "", res.Fields[name])
		e.scope = "Field " + tick(name)
		for i := range res.Overlays {
			if hosts[i] == name {
				attached[i] = true
				e.overlayFinding(rr, i+1, &res.Overlays[i])
			}
		}
		e.scope = ""
	}
	for _, name := range facetFieldOrder(res.Additive, nil) {
		e.facetFieldFinding(rr, "additive."+name, name, " with its own filters lifted", res.Additive[name])
	}
	unattached := false
	for i := range res.Overlays {
		if !attached[i] {
			unattached = true
			e.overlayFinding(rr, i+1, &res.Overlays[i])
		}
	}
	if unattached && req == nil {
		rr.later = append(rr.later, "The facet request is absent, so an overlay is not tied to the field it decorates; "+
			"pass it beside the result to read each overlay with its field.")
	}
	if len(res.Warnings) > 0 {
		rr.later = append(rr.later, "The facet result carries "+count(len(res.Warnings), "warning", "warnings")+
			" (for example a top-K truncation); its warnings slot holds them.")
	}
	rr.notes = append(rr.notes, "The facet read "+strconv.FormatInt(res.TotalRecords, 10)+" records; "+
		strconv.FormatInt(res.FilteredRecords, 10)+" passed the filters.")
	e.finalCaveats(rr, "facet")
	s := "This facet result summarises " + count(len(names), "field", "fields") + " from " +
		strconv.FormatInt(res.FilteredRecords, 10) + " of " + strconv.FormatInt(res.TotalRecords, 10) + " records"
	if len(res.Overlays) > 0 {
		s += ", with " + count(len(res.Overlays), "overlay", "overlays")
	}
	e.res.Summary = s + rr.evidencePhrase() + "."
	e.notes = rr.notes
}

// facetFieldOrder lists fields' names: the request's order first (when
// it rides beside the result), then any other name, sorted.
func facetFieldOrder(fields map[string]*types.FacetField, req *types.FacetRequest) []string {
	var out []string
	if req != nil {
		for _, f := range req.Fields {
			if fields[f] != nil && !slices.Contains(out, f) {
				out = append(out, f)
			}
		}
	}
	var rest []string
	for f, v := range fields {
		if v != nil && !slices.Contains(out, f) {
			rest = append(rest, f)
		}
	}
	sort.Strings(rest)
	return append(out, rest...)
}

// facetOverlayHosts maps each overlay layer to the facet field it
// decorates: the companion spec's params.field, else the request's only
// field, else (no companion) the result's only field. A layer left out
// of the map is read after the fields.
func facetOverlayHosts(res *types.FacetResult, req *types.FacetRequest) map[int]string {
	out := map[int]string{}
	only := ""
	switch {
	case req != nil && len(req.Fields) == 1:
		only = req.Fields[0]
	case req == nil && len(res.Fields) == 1:
		for f := range res.Fields {
			only = f
		}
	}
	for i := range res.Overlays {
		host := only
		if req != nil && i < len(req.Overlays) && len(req.Overlays[i].Params) > 0 {
			var p struct {
				Field string `json:"field"`
			}
			if json.Unmarshal(req.Overlays[i].Params, &p) == nil && p.Field != "" {
				host = p.Field
			}
		}
		if host != "" && res.Fields[host] != nil {
			out[i] = host
		}
	}
	return out
}

// facetFieldFinding reads one facet field's summary: descriptive, or
// not_computable when a numeric field holds no value.
func (e *explainer) facetFieldFinding(rr *responseReader, slot, name, qualifier string, f *types.FacetField) {
	fd := descriptor.ExplainFinding{Slot: slot, Subject: name + qualifier, Verdict: descriptor.VerdictDescriptive, Numbers: map[string]*float64{}}
	setNum(fd.Numbers, "null_count", float64(f.NullCount))
	text := "Field " + tick(name)
	if f.TypeName != "" {
		text += " (" + f.TypeName + ")"
	}
	text += qualifier
	switch {
	case f.Discrete != nil:
		d := f.Discrete
		setNum(fd.Numbers, "distinct_count", float64(d.DistinctCount))
		text += " has " + count64(d.DistinctCount, "distinct value", "distinct values")
		if len(d.Values) > 0 {
			top := d.Values[0]
			setNum(fd.Numbers, "top_count", float64(top.Count))
			text += "; the most common is " + tick(top.Value) + " with " + count64(top.Count, "record", "records")
		}
		text += ", and " + count64(f.NullCount, "record has", "records have") + " no value."
		if d.TruncatedAt > 0 {
			setNum(fd.Numbers, "truncated_at", float64(d.TruncatedAt))
			text += " The result lists the " + strconv.Itoa(len(d.Values)) + " most common values and leaves out " + strconv.Itoa(d.TruncatedAt) + "."
		}
	case f.Numeric != nil:
		m := f.Numeric
		setNum(fd.Numbers, "count", float64(m.Count))
		if m.Count == 0 {
			fd.Verdict = descriptor.VerdictNotComputable
			fd.Numbers["mean"] = nil
			text += " holds no value in the filtered records, so it has no summary figures."
			break
		}
		for k, v := range map[string]float64{"min": m.Min, "max": m.Max, "mean": m.Mean, "stddev": m.StdDev, "sum": m.Sum} {
			setNum(fd.Numbers, k, v)
		}
		for k, v := range m.Percentiles {
			setNum(fd.Numbers, "percentiles."+k, v)
		}
		text += " has " + count64(m.Count, "value", "values") + " from " + fmtNum(m.Min) + " to " + fmtNum(m.Max) +
			", with mean " + fmtNum(m.Mean) + " and standard deviation " + fmtNum(m.StdDev) +
			", and " + count64(f.NullCount, "record has", "records have") + " no value."
	default:
		text += " carries no summary."
	}
	e.addFinding(rr, fd, text)
}

func count64(n int64, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.FormatInt(n, 10) + " " + many
}
