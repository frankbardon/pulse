// Package runwarn is the single reading of every run-time warning a
// result carries, as the {code, message, details} entries the --json
// envelope's top-level `warnings` and a stream's trailing warnings
// record both carry. The CLI envelope writers and the Compose NDJSON
// writer (internal/composestream) share it, so the two never drift.
package runwarn

import (
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/types"
)

// Entries reads every run-time warning result carries —
// Response.Warnings (one Response, every Compose slot, every chain
// stage), ComposedResponse.Warnings and FacetResult.Warnings. A Compose
// slot's entry gains details.request (the slot index) and a chain
// stage's details.stage, unless the warning already locates itself;
// the result's own entries are never mutated (data keeps them
// unchanged). Overlay-layer warnings stay on their layer. Nil for a
// warning-free result, so a clean run's envelope and stream are
// byte-identical.
func Entries(result any) []*descriptor.EnvelopeEntry {
	var out []*descriptor.EnvelopeEntry
	add := func(ws []*types.ResponseWarning, key string, at int) {
		for _, w := range ws {
			if w == nil {
				continue
			}
			details := w.Details
			if key != "" {
				if _, set := details[key]; !set {
					details = make(map[string]any, len(w.Details)+1)
					for k, v := range w.Details {
						details[k] = v
					}
					details[key] = at
				}
			}
			out = append(out, &descriptor.EnvelopeEntry{Code: w.Code, Message: w.Message, Details: details})
		}
	}
	switch r := result.(type) {
	case *types.Response:
		if r != nil {
			add(r.Warnings, "", 0)
		}
	case *types.ComposedResponse:
		if r != nil {
			for i, sub := range r.Responses {
				if sub != nil {
					add(sub.Warnings, "request", i)
				}
			}
			add(r.Warnings, "", 0)
		}
	case *types.ChainResponse:
		if r != nil {
			// Final aliases the last stage: walk Stages only.
			for i, st := range r.Stages {
				if st != nil {
					add(st.Warnings, "stage", i)
				}
			}
		}
	case *types.FacetResult:
		if r != nil {
			add(r.Warnings, "", 0)
		}
	}
	return out
}
