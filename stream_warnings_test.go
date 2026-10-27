package pulse_test

import (
	"context"
	"io"
	"log/slog"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/types"
)

// TestProcessStream_SurfacesRuntimeWarnings: a run-time warning raised
// by a streamed run reaches the consumer — the ProcessStream iterator's
// Warnings accessor (unobserved, observed and `return`-shaped
// iterators alike) and ProcessStreamResult's completed terminator —
// equal to what a buffered Process returns on Response.Warnings.
func TestProcessStream_SurfacesRuntimeWarnings(t *testing.T) {
	t.Parallel()

	plain, fs, cohort := acceptanceCohort(t)
	observed, err := pulse.New(pulse.Options{FS: fs, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	buffered, err := plain.Process(ctx, categoricalRequest(t, leakageBody, cohort))
	if err != nil {
		t.Fatal(err)
	}
	want := buffered.Warnings
	if len(leakageWarnings(want)) != 1 {
		t.Fatalf("buffered run warnings %v: want the leakage entry", want)
	}
	shaped := func() *pulse.Request {
		r := categoricalRequest(t, leakageBody, cohort)
		r.Return = &types.Return{Exclude: []string{"metadata"}}
		return r
	}
	for _, tc := range []struct {
		name string
		p    *pulse.Pulse
		req  func() *pulse.Request
	}{
		{"plain", plain, func() *pulse.Request { return categoricalRequest(t, leakageBody, cohort) }},
		{"observed", observed, func() *pulse.Request { return categoricalRequest(t, leakageBody, cohort) }},
		{"shaped", plain, shaped},
		{"observed_shaped", observed, shaped},
	} {
		t.Run(tc.name, func(t *testing.T) {
			iter, err := tc.p.ProcessStream(ctx, tc.req())
			if err != nil {
				t.Fatal(err)
			}
			defer iter.Close()
			for {
				_, ok, err := iter.Next(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if !ok {
					break
				}
			}
			w, ok := iter.(interface {
				Warnings() []*types.ResponseWarning
			})
			if !ok {
				t.Fatalf("ProcessStream iterator %T has no Warnings accessor", iter)
			}
			if got := w.Warnings(); !reflect.DeepEqual(got, want) {
				t.Errorf("stream warnings %v, want the buffered %v", got, want)
			}

			res, err := tc.p.ProcessStreamResult(ctx, tc.req())
			if err != nil {
				t.Fatal(err)
			}
			for range res.Chunks {
			}
			term := <-res.Done
			if term.Status != pulse.StreamCompleted {
				t.Fatalf("terminator %+v", term)
			}
			if !reflect.DeepEqual(term.Warnings, want) {
				t.Errorf("terminator warnings %v, want the buffered %v", term.Warnings, want)
			}
		})
	}
}
