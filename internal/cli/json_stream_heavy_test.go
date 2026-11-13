//go:build heavy

package cli

import (
	"fmt"
	"io"
	"runtime"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/types"
)

// liveHeapSampler is a discarding writer that, on every Write, collects
// garbage and records the peak live heap above base — the memory the
// writer holds while the bytes go out.
type liveHeapSampler struct {
	base, peak uint64
	written    int
}

func (s *liveHeapSampler) Write(p []byte) (int, error) {
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	if ms.HeapAlloc > s.base {
		s.peak = max(s.peak, ms.HeapAlloc-s.base)
	}
	s.written += len(p)
	return len(p), nil
}

func sampleWrite(t *testing.T, write func(io.Writer) error) *liveHeapSampler {
	t.Helper()
	// Twice: the second collection empties the sync.Pool victim caches
	// an earlier encode left behind, so base is the resident result.
	runtime.GC()
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	s := &liveHeapSampler{base: ms.HeapAlloc}
	if err := write(s); err != nil {
		t.Fatal(err)
	}
	return s
}

// TestWriteEnvelopeStreamed_PeakHeap: writing a large result's --json
// envelope row by row holds no whole-document []byte. writeJSON's peak
// live heap above the resident result is at least the output (the
// compact body and its indented copy); the streamed writer's stays a
// small fraction of it.
func TestWriteEnvelopeStreamed_PeakHeap(t *testing.T) {
	// serial: measures the process heap; a parallel test would perturb it

	const rows = 50_000
	resp := &types.Response{Data: make([]map[string]any, rows)}
	for i := range rows {
		resp.Data[i] = map[string]any{"id": fmt.Sprintf("id%07d", i), "n": int64(i), "mean": float64(i) / 7}
	}
	env := withRuntimeWarnings(descriptor.NewEnvelope(resp), resp)

	whole := sampleWrite(t, func(w io.Writer) error { return writeJSON(w, env) })
	streamed := sampleWrite(t, func(w io.Writer) error { return writeEnvelopeStreamed(w, env) })
	t.Logf("output %d bytes; peak live heap above the result: writeJSON %d, streamed %d",
		whole.written, whole.peak, streamed.peak)
	if streamed.written != whole.written {
		t.Fatalf("streamed wrote %d bytes, writeJSON %d", streamed.written, whole.written)
	}
	if whole.peak < uint64(whole.written) {
		t.Fatalf("writeJSON peak %d below its output %d: the baseline does not hold the document", whole.peak, whole.written)
	}
	if streamed.peak > uint64(whole.written)/10 {
		t.Errorf("streamed peak %d is over a tenth of the %d-byte output: a whole-document buffer is back", streamed.peak, whole.written)
	}
}
