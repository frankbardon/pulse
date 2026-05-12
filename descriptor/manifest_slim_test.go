package descriptor

import (
	"encoding/json"
	"testing"
)

// TestSlimManifest_RemovesDescriptions verifies that SlimManifest blanks
// every prose Description field while preserving structural metadata.
func TestSlimManifest_RemovesDescriptions(t *testing.T) {
	full := BuildManifest()
	slim := SlimManifest(full)

	if slim == nil {
		t.Fatal("SlimManifest returned nil")
	}

	for _, op := range slim.Components.Aggregators {
		if op.Description != "" {
			t.Errorf("aggregator %s retained Description in slim mode", op.Name)
		}
		if op.StreamableHint != "" {
			t.Errorf("aggregator %s retained StreamableHint in slim mode", op.Name)
		}
		for _, p := range op.Params {
			if p.Description != "" {
				t.Errorf("aggregator %s param %s retained Description in slim mode", op.Name, p.Name)
			}
		}
	}
	for _, tm := range slim.Tests {
		if tm.Description != "" {
			t.Errorf("test %s retained Description in slim mode", tm.Name)
		}
	}
	for _, tm := range slim.PostTests {
		if tm.Description != "" {
			t.Errorf("post-test %s retained Description in slim mode", tm.Name)
		}
	}
	for _, d := range slim.SynthDistributions {
		if d.Description != "" {
			t.Errorf("distribution %s retained Description in slim mode", d.Name)
		}
	}
	for _, e := range slim.ErrorCodes {
		if e.Description != "" {
			t.Errorf("error code %s retained Description in slim mode", e.Code)
		}
	}
	for _, mt := range slim.MCPTools {
		if mt.Description != "" {
			t.Errorf("mcp tool %s retained Description in slim mode", mt.Name)
		}
	}
}

// TestSlimManifest_PreservesStructure verifies that names, types, and
// counts are unchanged in slim mode.
func TestSlimManifest_PreservesStructure(t *testing.T) {
	full := BuildManifest()
	slim := SlimManifest(full)

	if len(slim.Components.Aggregators) != len(full.Components.Aggregators) {
		t.Errorf("slim aggregator count %d != full %d", len(slim.Components.Aggregators), len(full.Components.Aggregators))
	}
	if len(slim.Tests) != len(full.Tests) {
		t.Errorf("slim Tests count %d != full %d", len(slim.Tests), len(full.Tests))
	}
	if len(slim.PostTests) != len(full.PostTests) {
		t.Errorf("slim PostTests count %d != full %d", len(slim.PostTests), len(full.PostTests))
	}
	if len(slim.ErrorCodes) != len(full.ErrorCodes) {
		t.Errorf("slim ErrorCodes count %d != full %d", len(slim.ErrorCodes), len(full.ErrorCodes))
	}
	// Sample one operator to confirm structural fields intact.
	if len(slim.Components.Aggregators) > 0 {
		op := slim.Components.Aggregators[0]
		if op.Name == "" || op.Category == "" || len(op.AcceptsTypes) == 0 {
			t.Errorf("slim aggregator lost structural fields: %+v", op)
		}
	}
}

// TestSlimManifest_DoesNotMutateInput verifies that the input manifest is
// not modified.
func TestSlimManifest_DoesNotMutateInput(t *testing.T) {
	m := BuildManifest()
	before, _ := json.Marshal(m)
	_ = SlimManifest(m)
	after, _ := json.Marshal(m)
	if string(before) != string(after) {
		t.Fatalf("SlimManifest mutated input manifest")
	}
}

// TestSlimManifest_SmallerThanFull sanity-checks that the slim payload is
// noticeably smaller than the full one.
func TestSlimManifest_SmallerThanFull(t *testing.T) {
	full, _ := json.Marshal(BuildManifest())
	slim, _ := json.Marshal(SlimManifest(BuildManifest()))
	if len(slim) >= len(full) {
		t.Errorf("slim payload (%d bytes) not smaller than full (%d bytes)", len(slim), len(full))
	}
	if float64(len(slim))/float64(len(full)) > 0.75 {
		// Sanity threshold: if slim is more than 75% of full, descriptions
		// either weren't significant or the function isn't stripping them.
		t.Errorf("slim payload only %.1f%% smaller than full; expected more", 100*(1-float64(len(slim))/float64(len(full))))
	}
}
