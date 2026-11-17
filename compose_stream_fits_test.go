package pulse

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// compose_stream_fits_test.go pins WriteComposeStream's opt-in terminal
// fits line (ComposeStreamOptions.TerminalFits, CLI --stream-fits): it
// is the last line, after every row and after the sweep ranking line,
// and carries exactly the buffered `fits` object — sweep-bound and
// explicit fits alike, serial and parallel. Off, and on for a fit-free
// batch, the stream is byte-identical to the default one.

// streamLines splits an NDJSON stream into its decoded records.
func streamLines(t *testing.T, raw []byte) []map[string]json.RawMessage {
	t.Helper()
	var out []map[string]json.RawMessage
	for _, line := range bytes.Split(bytes.TrimSpace(raw), []byte("\n")) {
		var rec map[string]json.RawMessage
		if err := json.Unmarshal(line, &rec); err != nil {
			t.Fatalf("line %s: %v", line, err)
		}
		out = append(out, rec)
	}
	return out
}

func writeStream(t *testing.T, req *ComposedRequest, resp *ComposedResponse, opts ComposeStreamOptions) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := WriteComposeStream(&buf, req, resp, opts); err != nil {
		t.Fatalf("WriteComposeStream: %v", err)
	}
	return buf.Bytes()
}

// TestComposeStream_TerminalFitsMatchesBuffered: a ranked sweep with a
// sweep-bound fit ("best") and an explicit one ("w60") streams its rows,
// then the ranking line, then ONE final {"fits":{...}} line whose value
// is byte-equal to the buffered response's `fits`. Dropping that line
// gives the option-off stream byte for byte.
func TestComposeStream_TerminalFitsMatchesBuffered(t *testing.T) {
	t.Parallel()

	p, err := New(Options{FS: fitsFacadeFS(t)})
	if err != nil {
		t.Fatal(err)
	}
	c := sweepBindBatch(t, []types.FitSpec{sweepBound("best", "m"), {Name: "w60", Request: facadeFitRequest("0", "60")}},
		sweepBindBody(t, `["x"]`, ""), nil)
	ctx := context.Background()
	for name, run := range map[string]func() (*ComposedResponse, error){
		"Compose":         func() (*ComposedResponse, error) { return p.Compose(ctx, c) },
		"ComposeParallel": func() (*ComposedResponse, error) { return p.ComposeParallel(ctx, c, ComposeOptions{MaxWorkers: 2}) },
	} {
		out, err := run()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if out.Fits["best"] == nil || out.Fits["w60"] == nil {
			t.Fatalf("%s: fits %+v", name, out.Fits)
		}
		buffered, err := json.Marshal(out)
		if err != nil {
			t.Fatal(err)
		}
		var top map[string]json.RawMessage
		if err := json.Unmarshal(buffered, &top); err != nil {
			t.Fatal(err)
		}

		on := writeStream(t, c, out, ComposeStreamOptions{TerminalFits: true})
		off := writeStream(t, c, out, ComposeStreamOptions{})
		recs := streamLines(t, on)
		last := recs[len(recs)-1]
		if len(last) != 1 || !bytes.Equal(last["fits"], top["fits"]) {
			t.Fatalf("%s: last line %s, want {\"fits\":%s}", name, on[bytes.LastIndexByte(bytes.TrimSpace(on), '\n')+1:], top["fits"])
		}
		if _, ok := recs[len(recs)-2]["ranking"]; !ok {
			t.Errorf("%s: the fits line does not follow the ranking line:\n%s", name, on)
		}
		rows := 0
		for _, rec := range recs {
			if _, ok := rec["index"]; ok {
				rows++
			}
		}
		if rows == 0 {
			t.Errorf("%s: stream wrote no rows:\n%s", name, on)
		}
		trimmed := on[:bytes.LastIndexByte(bytes.TrimSuffix(on, []byte("\n")), '\n')+1]
		if !bytes.Equal(trimmed, off) {
			t.Errorf("%s: on minus the fits line differs from off:\non:  %s\noff: %s", name, trimmed, off)
		}
		for _, rec := range streamLines(t, off) {
			if _, ok := rec["fits"]; ok {
				t.Errorf("%s: option off wrote a fits line:\n%s", name, off)
			}
		}
	}
}

// TestComposeStream_TerminalFitsNeedsDeclaredFits: on, a fit-free batch
// streams exactly what off does (no empty fits line).
func TestComposeStream_TerminalFitsNeedsDeclaredFits(t *testing.T) {
	t.Parallel()

	p, err := New(Options{FS: fitsFacadeFS(t)})
	if err != nil {
		t.Fatal(err)
	}
	c := &ComposedRequest{Requests: []*Request{facadeFitSlot("s")}}
	out, err := p.Compose(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	on := writeStream(t, c, out, ComposeStreamOptions{TerminalFits: true})
	off := writeStream(t, c, out, ComposeStreamOptions{})
	if !bytes.Equal(on, off) {
		t.Errorf("fit-free batch: on %s, off %s", on, off)
	}
}

// TestComposeStream_OffByteIdentical pins the default stream's bytes for
// a batch with an explicit fit and grouped count rows — the stream the
// CLI wrote before the writer moved into the library: rows only, no
// fits record. Counts only, so the golden holds on every arch.
func TestComposeStream_OffByteIdentical(t *testing.T) {
	t.Parallel()

	p, err := New(Options{FS: fitsFacadeFS(t)})
	if err != nil {
		t.Fatal(err)
	}
	slot := &Request{
		Label:        "by_region",
		Cohort:       &types.Cohort{Filename: fitsFacadeCohort},
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "week", Label: "n"}},
	}
	c := &ComposedRequest{Fits: []types.FitSpec{{Name: "w60", Request: facadeFitRequest("0", "60")}}, Requests: []*Request{slot}}
	out, err := p.Compose(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	off := writeStream(t, c, out, ComposeStreamOptions{})
	const want = "{\"index\":0,\"row\":{\"n\":52,\"region\":\"north\"}}\n{\"index\":0,\"row\":{\"n\":52,\"region\":\"south\"}}\n"
	if string(off) != want {
		t.Fatalf("off stream:\n%s\nwant:\n%s", off, want)
	}
}
