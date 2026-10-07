package pulse_test

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/types"
)

// streamReturnReq is the streaming fixture: a grouped run with a
// measure column (m, rounded under precision), a count column (n,
// exact) and a decimal128 sum (d, exact), Components on.
func streamReturnReq(cohort string, ret *types.Return) *types.Request {
	return &types.Request{
		Cohort: &types.Cohort{Filename: cohort},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_AVERAGE, Field: "x", Label: "m"},
			{Type: types.AGG_COUNT, Field: "x", Label: "n"},
			{Type: types.AGG_SUM, Field: "t_decimal128", Label: "d"},
		},
		Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
		Return: ret,
	}
}

// streamReturnCases are the non-identity blocks the streaming parity
// gates run, the two non-full presets included.
func streamReturnCases() map[string]*types.Return {
	return map[string]*types.Return{
		"minimal":             {Preset: types.ReturnPresetMinimal},
		"standard":            {Preset: types.ReturnPresetStandard},
		"standard + prec":     {Preset: types.ReturnPresetStandard, Precision: 1},
		"column include+prec": {Include: []string{"data[*].m", "data[*].n", "components"}, Precision: 2},
		"column exclude":      {Exclude: []string{"data[*].d", "components.groupers"}},
		"precision only":      {Precision: 1},
		"data excluded":       {Exclude: []string{"data"}},
	}
}

type rowMarshaler interface {
	MarshalRow(pulse.Row) ([]byte, error)
}

type returnedReporter interface {
	Returned() *types.ReturnedMarker
}

// numberDoc decodes b keeping every number literal verbatim, so two
// documents compare equal only when their wire numbers match.
func numberDoc(t *testing.T, b []byte) any {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("decode %s: %v", b, err)
	}
	return v
}

// TestReturn_StreamMatchesBuffered: under every block, each row
// ProcessStream yields equals the buffered shaped response's `data`
// element in Go and — through the iterator's MarshalRow — on the wire
// (columns + precision); the terminal Components / Metadata equal the
// buffered shaped response's on the wire; the `returned` marker is
// reported only once the stream is exhausted and equals the buffered
// one.
func TestReturn_StreamMatchesBuffered(t *testing.T) {
	p, _, cohort := acceptanceCohort(t)
	ctx := context.Background()
	for name, ret := range streamReturnCases() {
		t.Run(name, func(t *testing.T) {
			resp, b := processJSON(t, p, streamReturnReq(cohort, ret))
			top := decodeTop(t, b)
			var wantRows []json.RawMessage
			if raw, ok := top["data"]; ok {
				if err := json.Unmarshal(raw, &wantRows); err != nil {
					t.Fatal(err)
				}
			}

			iter, err := p.ProcessStream(ctx, streamReturnReq(cohort, ret))
			if err != nil {
				t.Fatalf("ProcessStream: %v", err)
			}
			defer iter.Close()
			rm, ok := iter.(rowMarshaler)
			if !ok {
				t.Fatalf("shaped iterator has no MarshalRow")
			}
			rr, ok := iter.(returnedReporter)
			if !ok {
				t.Fatalf("shaped iterator has no Returned")
			}
			var rows []pulse.Row
			for {
				if rr.Returned() != nil {
					t.Fatal("returned marker reported before the stream was exhausted")
				}
				row, ok, err := iter.Next(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if !ok {
					break
				}
				rows = append(rows, row)
			}
			if len(rows) != len(wantRows) || len(rows) != len(resp.Data) {
				t.Fatalf("streamed %d rows, buffered wire %d / Go %d", len(rows), len(wantRows), len(resp.Data))
			}
			for i, row := range rows {
				if !reflect.DeepEqual(row, resp.Data[i]) {
					t.Errorf("row %d Go = %v, want %v", i, row, resp.Data[i])
				}
				got, err := rm.MarshalRow(row)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got, wantRows[i]) {
					t.Errorf("row %d wire = %s, want %s", i, got, wantRows[i])
				}
			}

			comp := iter.Components()
			if raw, ok := top["components"]; ok {
				got, err := json.Marshal(comp)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(numberDoc(t, got), numberDoc(t, raw)) {
					t.Errorf("terminal components = %s, want %s", got, raw)
				}
			} else if comp != nil {
				t.Errorf("components excluded but streamed: %+v", comp)
			}
			if raw, ok := top["metadata"]; ok {
				got, _ := json.Marshal(iter.Metadata())
				if !bytes.Equal(got, raw) {
					t.Errorf("metadata = %s, want %s", got, raw)
				}
			} else if iter.Metadata() != nil {
				t.Errorf("metadata excluded but streamed: %+v", iter.Metadata())
			}
			if !reflect.DeepEqual(rr.Returned(), resp.Returned) || resp.Returned == nil {
				t.Errorf("returned = %+v, want %+v", rr.Returned(), resp.Returned)
			}
		})
	}
}

// streamChunks drains ProcessStreamResult and returns every chunk.
func streamChunks(t *testing.T, p *pulse.Pulse, req *types.Request) []pulse.StreamChunk[pulse.Row] {
	t.Helper()
	sr, err := p.ProcessStreamResult(context.Background(), req)
	if err != nil {
		t.Fatalf("ProcessStreamResult: %v", err)
	}
	var out []pulse.StreamChunk[pulse.Row]
	for c := range sr.Chunks {
		out = append(out, c)
	}
	if term := <-sr.Done; term.Status != pulse.StreamCompleted {
		t.Fatalf("stream ended %v: %v", term.Status, term.Error)
	}
	return out
}

// streamJSON is a stream's wire form: every chunk marshalled, one per
// line.
func streamJSON(t *testing.T, p *pulse.Pulse, req *types.Request) []byte {
	t.Helper()
	var buf bytes.Buffer
	for _, c := range streamChunks(t, p, req) {
		b, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	return buf.Bytes()
}

// TestReturn_StreamResultChunks: ProcessStreamResult shapes every chunk
// — rows equal the buffered shaped rows, an excluded `components` is
// absent from EVERY chunk, a kept one keeps its precision mid-stream
// too (the non-terminal projection carries the plan), the terminal
// chunk's components equal the buffered shaped response's, and only the
// terminal chunk carries the `returned` marker.
func TestReturn_StreamResultChunks(t *testing.T) {
	p, _, cohort := acceptanceCohort(t)
	for name, ret := range streamReturnCases() {
		if name == "data excluded" {
			continue // no rows, so no chunks at all
		}
		t.Run(name, func(t *testing.T) {
			checkStreamChunks(t, p, func() *types.Request { return streamReturnReq(cohort, ret) })
		})
	}
	// A range grouper's components carry non-integer floats on EVERY
	// chunk (range_max 16.5), so precision must reach the mid-stream
	// projection too, not only the terminal chunk.
	t.Run("range grouper + prec", func(t *testing.T) {
		checkStreamChunks(t, p, func() *types.Request {
			r := streamReturnReq(cohort, &types.Return{Precision: 1})
			r.Groups = []*types.Group{{Type: types.GROUP_RANGE, Field: "y"}}
			return r
		})
	})
}

func checkStreamChunks(t *testing.T, p *pulse.Pulse, mk func() *types.Request) {
	t.Helper()
	resp, b := processJSON(t, p, mk())
	top := decodeTop(t, b)
	chunks := streamChunks(t, p, mk())
	if len(chunks) < 2 || len(chunks) != len(resp.Data) {
		t.Fatalf("chunks = %d, want %d (>= 2)", len(chunks), len(resp.Data))
	}
	rawComp, compKept := top["components"]
	for i, c := range chunks {
		terminal := i == len(chunks)-1
		if !reflect.DeepEqual(c.Data, resp.Data[i]) {
			t.Errorf("chunk %d data = %v, want %v", i, c.Data, resp.Data[i])
		}
		if (c.Returned != nil) != terminal {
			t.Errorf("chunk %d (terminal=%v) returned = %+v", i, terminal, c.Returned)
		}
		if !compKept {
			if c.Components != nil {
				t.Errorf("chunk %d carries excluded components", i)
			}
			continue
		}
		got, err := json.Marshal(c.Components)
		if err != nil {
			t.Fatal(err)
		}
		want := numberDoc(t, rawComp)
		if !terminal {
			// Mid-stream: the buffered figures minus the
			// terminal-only per-group entries.
			if m, ok := want.(map[string]any); ok {
				if aggs, ok := m["aggregations"].([]any); ok {
					for _, a := range aggs {
						delete(a.(map[string]any), "groups")
					}
				}
			}
		}
		if !reflect.DeepEqual(numberDoc(t, got), want) {
			t.Errorf("chunk %d (terminal=%v) components = %s, want %v", i, terminal, got, want)
		}
	}
	if last := chunks[len(chunks)-1].Returned; !reflect.DeepEqual(last, resp.Returned) {
		t.Errorf("terminal returned = %+v, want %+v", last, resp.Returned)
	}
}

// TestReturnFullIsIdentity_Stream: with no `return`, an empty block or
// preset `full`, ProcessStreamResult's wire form is byte-identical and
// carries no marker, and ProcessStream hands back the unshaped iterator
// (no MarshalRow); a non-identity block does change the stream.
func TestReturnFullIsIdentity_Stream(t *testing.T) {
	p, _, cohort := acceptanceCohort(t)
	baseline := streamJSON(t, p, streamReturnReq(cohort, nil))
	if bytes.Contains(baseline, []byte(`"returned"`)) || !bytes.Contains(baseline, []byte(`"components"`)) {
		t.Fatalf("baseline stream: %s", baseline)
	}
	for name, ret := range map[string]*types.Return{
		"preset full": {Preset: types.ReturnPresetFull},
		"empty block": {},
	} {
		if got := streamJSON(t, p, streamReturnReq(cohort, ret)); !bytes.Equal(got, baseline) {
			t.Errorf("%s stream differs:\n got %s\nwant %s", name, got, baseline)
		}
		iter, err := p.ProcessStream(context.Background(), streamReturnReq(cohort, ret))
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := iter.(rowMarshaler); ok {
			t.Errorf("%s: identity plan wrapped the iterator", name)
		}
		_ = iter.Close()
	}
	shaped := streamJSON(t, p, streamReturnReq(cohort, &types.Return{Exclude: []string{"components.run"}}))
	if bytes.Equal(shaped, baseline) || !bytes.Contains(shaped, []byte(`"returned"`)) {
		t.Errorf("an exclude left the stream unshaped: %s", shaped)
	}
}

// TestReturn_RequestHashCoversReturn: `return` changes the output, so
// it participates in Request.Hash (and so in a stream's RequestHash).
func TestReturn_RequestHashCoversReturn(t *testing.T) {
	a := streamReturnReq("c.pulse", nil)
	b := streamReturnReq("c.pulse", &types.Return{Preset: types.ReturnPresetMinimal})
	if a.Hash() == b.Hash() {
		t.Error("return block does not change Request.Hash")
	}
}
