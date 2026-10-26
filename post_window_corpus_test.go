package pulse

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/internal/examples"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// post_window_corpus_test.go — the byte-identity gate of the post-window
// attribute phase (transformed-predictors E2-S1, FR-12). The phase moves
// an attribute only when it reads a window label (directly or through a
// moved attribute); a request whose attributes read none has the zero
// plan, and on the zero plan every site takes its historical path —
// processRecords / DeriveRecords call the unchanged filterThenAttributes,
// the walk judges every attribute at step 3, the window copy adds
// nothing. So the gate is: every request of the shipped example corpus
// (each request-shaped object of each example body: a Process body, a
// Compose slot, a fit, a sweep template) classifies to the zero plan.
// The examples' own run (TestExamples_RunEndToEnd), the story goldens
// and the repository's existing test suites then pin the output bytes
// of those requests and of every test request.

// requestObjects returns every JSON object in v that carries an
// "attributes" or "windows" key — the request-shaped objects of an
// example body, wherever they nest.
func requestObjects(v any, out *[]map[string]any) {
	switch x := v.(type) {
	case map[string]any:
		if _, a := x["attributes"]; a {
			*out = append(*out, x)
		} else if _, w := x["windows"]; w {
			*out = append(*out, x)
		}
		for _, c := range x {
			requestObjects(c, out)
		}
	case []any:
		for _, c := range x {
			requestObjects(c, out)
		}
	}
}

// TestWindowPredictor_PostWindowPlanInertOverCorpus: no shipped example
// moves an attribute after the windows — as written, and with a probe
// window no attribute reads added to every request that carries
// attributes (the corpus ships no request combining the two, so the
// probe is what runs the classification over every attribute shape the
// corpus holds: formulas, regression and scoring attributes, vector
// attributes, previous-state slots).
//
// The same gate holds the windows-first classification
// (vectors.WindowsFirst): no shipped request turns it on, so no example
// changes classification.
//
// The exceptions are the examples that teach the phase: the one-step
// MMM decay sweeps (grid and lhs) read their adstock labels in formula
// attributes, so they MUST classify post-window (checked, not skipped).
func TestWindowPredictor_PostWindowPlanInertOverCorpus(t *testing.T) {
	teaches := map[string]bool{"compose-mmm-decay-sweep": true, "compose-mmm-decay-lhs-sweep": true}
	var taught int
	probe := &types.Window{Type: types.WIN_ROW_NUMBER, Label: "post_window_probe", OrderBy: []types.OrderKey{{Field: "post_window_probe_key"}}}
	var requests, probed int
	for _, ex := range examples.All() {
		var body any
		if err := json.Unmarshal(ex.Body, &body); err != nil {
			t.Fatalf("%s: %v", ex.Name, err)
		}
		var objs []map[string]any
		requestObjects(body, &objs)
		for _, obj := range objs {
			raw, err := json.Marshal(obj)
			if err != nil {
				t.Fatal(err)
			}
			var req types.Request
			if err := json.Unmarshal(raw, &req); err != nil {
				t.Fatalf("%s: a request-shaped object does not decode: %v", ex.Name, err)
			}
			requests++
			if teaches[ex.Name] {
				if pw := vectors.PostWindowAttributes(&req, nil, nil, nil); len(req.Attributes) > 0 {
					if !pw.Any() || pw.Refusal(&req) != nil {
						t.Errorf("%s: the example that teaches the post-window phase classifies %+v", ex.Name, pw)
					}
					taught++
				}
				continue
			}
			inert := func(r *types.Request, how string) {
				pw := vectors.PostWindowAttributes(r, nil, nil, nil)
				if pw.Any() || pw.Dep != nil || pw.WindowReads != nil || pw.Refusal(r) != nil {
					t.Errorf("%s (%s): attributes classify post-window (%+v): the example now runs a reordered pipeline", ex.Name, how, pw)
				}
				// Windows-first (mmm-followups E4-S1): no shipped
				// request runs its windows ahead of its aggregations.
				if pw.WindowsFirst || vectors.WindowsFirst(r, nil, nil, nil) {
					t.Errorf("%s (%s): the request classifies windows-first: the example now runs a reordered pipeline", ex.Name, how)
				}
			}
			inert(&req, "as written")
			if len(req.Attributes) > 0 {
				probed++
				withProbe := req
				withProbe.Windows = append(append([]*types.Window(nil), req.Windows...), probe)
				inert(&withProbe, "probe window")
			}
		}
	}
	if taught == 0 {
		t.Fatal("the post-window teaching example was not found in the corpus")
	}
	if requests == 0 || probed == 0 {
		t.Fatalf("corpus scanned %d requests, %d with attributes; the gate judged nothing", requests, probed)
	}
}
