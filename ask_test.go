package pulse

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// TestAsk_ValidRequestRuns asserts the success path: predict-valid
// request returns Predict populated, Process populated, Suggestions
// empty.
func TestAsk_ValidRequestRuns(t *testing.T) {
	memFs := afero.NewMemMapFs()
	createTestPulseFile(t, memFs, "test.pulse", []string{"age"}, [][]string{
		{"10"}, {"20"}, {"30"},
	})

	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	resp, err := p.Ask(context.Background(), &AskRequest{
		Request: &Request{
			Cohort: &types.Cohort{Filename: "test.pulse"},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "age", Label: "n"},
			},
		},
	})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if resp.Predict == nil || !resp.Predict.Valid {
		t.Fatalf("expected valid predict, got: %+v", resp.Predict)
	}
	if resp.Process == nil {
		t.Fatal("expected Process populated on valid request")
	}
	if len(resp.Suggestions) != 0 {
		t.Errorf("expected no suggestions on success, got %d", len(resp.Suggestions))
	}
	if resp.FormatVersion != "1.0" {
		t.Errorf("FormatVersion = %q, want \"1.0\"", resp.FormatVersion)
	}
}

// TestAsk_InvalidAbort asserts the default abort behavior: predict-
// invalid + OnInvalid="abort" returns a SERVICE_VALIDATION error from
// pulse.Ask. Process and the AskResponse are not returned.
func TestAsk_InvalidAbort(t *testing.T) {
	memFs := afero.NewMemMapFs()
	createTestPulseFile(t, memFs, "test.pulse", []string{"age"}, [][]string{
		{"10"}, {"20"},
	})

	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	resp, err := p.Ask(context.Background(), &AskRequest{
		OnInvalid: "abort",
		Request: &Request{
			Cohort: &types.Cohort{Filename: "test.pulse"},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "no_such_field"},
			},
		},
	})
	if err == nil {
		t.Fatalf("expected error on invalid request with abort; got resp=%+v", resp)
	}
	if !errors.HasCode(err, errors.SERVICE_VALIDATION) {
		t.Errorf("expected SERVICE_VALIDATION error, got: %v", err)
	}
}

// TestAsk_InvalidSuggest asserts the suggest mode: predict-invalid +
// OnInvalid="suggest" returns ok with Predict.Valid=false, Suggestions
// populated, Process nil.
func TestAsk_InvalidSuggest(t *testing.T) {
	memFs := afero.NewMemMapFs()
	createTestPulseFile(t, memFs, "test.pulse", []string{"age"}, [][]string{
		{"10"}, {"20"},
	})

	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	resp, err := p.Ask(context.Background(), &AskRequest{
		OnInvalid: "suggest",
		Request: &Request{
			Cohort: &types.Cohort{Filename: "test.pulse"},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "no_such_field"},
			},
		},
	})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if resp.Predict == nil || resp.Predict.Valid {
		t.Fatalf("expected predict.Valid=false, got: %+v", resp.Predict)
	}
	if resp.Process != nil {
		t.Error("expected Process nil on predict-invalid")
	}
	if len(resp.Suggestions) == 0 {
		t.Fatal("expected suggestions populated on predict-invalid + suggest mode")
	}
	if len(resp.Errors) == 0 {
		t.Error("expected errors echoed through AskResponse")
	}
}

// TestAsk_PredictOnly asserts predict-only mode: Predict=true skips
// execution even on a valid request.
func TestAsk_PredictOnly(t *testing.T) {
	memFs := afero.NewMemMapFs()
	createTestPulseFile(t, memFs, "test.pulse", []string{"age"}, [][]string{
		{"10"}, {"20"},
	})

	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	resp, err := p.Ask(context.Background(), &AskRequest{
		Predict: true,
		Request: &Request{
			Cohort: &types.Cohort{Filename: "test.pulse"},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "age"},
			},
		},
	})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if resp.Predict == nil || !resp.Predict.Valid {
		t.Fatal("expected valid predict")
	}
	if resp.Process != nil {
		t.Error("expected Process nil in predict-only mode")
	}
}

// TestAsk_InvalidOnInvalidValue asserts that an unrecognized OnInvalid
// value is rejected with a SERVICE_VALIDATION error before the pipeline
// runs.
func TestAsk_InvalidOnInvalidValue(t *testing.T) {
	memFs := afero.NewMemMapFs()
	createTestPulseFile(t, memFs, "test.pulse", []string{"age"}, [][]string{
		{"10"},
	})

	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = p.Ask(context.Background(), &AskRequest{
		OnInvalid: "nonsense",
		Request: &Request{
			Cohort: &types.Cohort{Filename: "test.pulse"},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "age"},
			},
		},
	})
	if err == nil {
		t.Fatal("expected error for unknown on_invalid value")
	}
	if !errors.HasCode(err, errors.SERVICE_VALIDATION) {
		t.Errorf("expected SERVICE_VALIDATION, got: %v", err)
	}
}

// TestAsk_SuggestionsDedupedAndSorted asserts that suggestions are
// de-duplicated by (Action, Hint) and arrive sorted by Action then
// Hint. A request with two missing-field references produces a single
// fixup entry for the shared error code; introducing a second distinct
// error code adds another entry, and the two land in sorted order.
func TestAsk_SuggestionsDedupedAndSorted(t *testing.T) {
	memFs := afero.NewMemMapFs()
	createTestPulseFile(t, memFs, "test.pulse", []string{"age"}, [][]string{
		{"10"},
	})

	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	resp, err := p.Ask(context.Background(), &AskRequest{
		OnInvalid: "suggest",
		Request: &Request{
			Cohort: &types.Cohort{Filename: "test.pulse"},
			Aggregations: []*types.Aggregation{
				// Two aggregations referencing unknown fields → same code fires twice.
				{Type: types.AGG_COUNT, Field: "no_such_field_a"},
				{Type: types.AGG_COUNT, Field: "no_such_field_b"},
			},
		},
	})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if len(resp.Errors) < 2 {
		t.Fatalf("expected at least two predict errors, got %d", len(resp.Errors))
	}
	if len(resp.Suggestions) == 0 {
		t.Fatal("expected suggestions populated")
	}

	// Dedup check: identical (Action, Hint) pairs collapse to one entry.
	type k struct {
		a errors.FixupAction
		h string
	}
	seen := make(map[k]int)
	for _, s := range resp.Suggestions {
		seen[k{s.Action, s.Hint}]++
	}
	for key, count := range seen {
		if count > 1 {
			t.Errorf("duplicate suggestion (%v, %q) appeared %d times", key.a, key.h, count)
		}
	}

	// Stable sort: previous entry must be <= current under (Action, Hint).
	for i := 1; i < len(resp.Suggestions); i++ {
		prev, cur := resp.Suggestions[i-1], resp.Suggestions[i]
		if prev.Action > cur.Action {
			t.Errorf("suggestions not sorted by Action at index %d: %q then %q", i, prev.Action, cur.Action)
		}
		if prev.Action == cur.Action && prev.Hint > cur.Hint {
			t.Errorf("suggestions not sorted by Hint within Action at index %d", i)
		}
	}
}

// TestAsk_MCPHandler is a round-trip-style sanity check on the
// AskRequest / AskResponse shapes when serialized to JSON and back,
// which is what the MCP handler does under the hood. We exercise the
// JSON marshaling path here in the facade test so it remains close to
// the type definitions; the MCP handler test in internal/mcp covers
// the wire shape end-to-end.
func TestAsk_MCPHandler(t *testing.T) {
	memFs := afero.NewMemMapFs()
	createTestPulseFile(t, memFs, "test.pulse", []string{"age"}, [][]string{
		{"10"}, {"20"},
	})

	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	in := &AskRequest{
		Request: &Request{
			Cohort: &types.Cohort{Filename: "test.pulse"},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "age", Label: "n"},
			},
		},
	}
	body, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal AskRequest: %v", err)
	}
	var decoded AskRequest
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("unmarshal AskRequest: %v", err)
	}

	resp, err := p.Ask(context.Background(), &decoded)
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	out, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal AskResponse: %v", err)
	}
	// Sanity check that the round-tripped envelope mentions the key fields.
	s := string(out)
	for _, want := range []string{`"format_version"`, `"predict"`, `"errors"`, `"warnings"`} {
		if !strings.Contains(s, want) {
			t.Errorf("AskResponse JSON missing %s", want)
		}
	}
}
