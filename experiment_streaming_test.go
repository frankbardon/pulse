package pulse_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// TestExperimentStreaming_TestsOnlyAndRegressionOnly (U48 #336): on the
// 40,000-row A/B showcase cohort a tests-only request and a
// regression-only request need no throwaway AGG_COUNT — predict says
// streamable with no reasons and sizes no `data` section, the run takes
// the streaming arm, its results equal the workaround request's, and a
// regression's own multiplicity block is still folded at the terminal
// flush.
func TestExperimentStreaming_TestsOnlyAndRegressionOnly(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	var mu sync.Mutex
	var arms []observe.Arm
	p, err := pulse.New(pulse.Options{FS: fs, Hooks: &observe.Hooks{
		OnOperationEnd: func(_ context.Context, _ observe.OperationInfo, res observe.OperationResult) {
			mu.Lock()
			arms = append(arms, res.Arm)
			mu.Unlock()
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	showcaseABCohort(t, p)
	lastArm := func() observe.Arm {
		mu.Lock()
		defer mu.Unlock()
		return arms[len(arms)-1]
	}
	const workaround = `"aggregations":[{"type":"AGG_COUNT","field":"user_id"}],`
	cases := []struct {
		name string
		body string
	}{
		{"tests only", `"tests":[
			{"type":"TEST_WELCH","field":"pages_viewed","split_by":"arm","params":{"covariate":"prior_sessions","reference":"old_page"}},
			{"type":"TEST_CHISQ","rows":"arm","cols":"device"}]`},
		{"regression only", `"regressions":[{"type":"REG_OLS","target":"pages_viewed","predictors":["is_variant","prior_sessions"],
			"multiplicity":{"method":"holm"}}]`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := func(extra string) *types.Request {
				return decodeRequest(t, `{"cohort":{"filename":"ab.pulse"},`+extra+c.body+`}`)
			}
			env := predictEnvelope(t, p, fs, "ab.pulse", req(""))
			res := env.Data.(*descriptor.PredictResult)
			if len(env.Errors) != 0 || !res.Streamable || len(res.StreamableReasons) != 0 {
				t.Fatalf("predict errors %+v streamable %v (%v)", env.Errors, res.Streamable, res.StreamableReasons)
			}
			var total int64
			for _, s := range res.Sizes {
				if s.Section == "data" {
					t.Fatalf("data sized for a request that emits no rows: %+v", s)
				}
				total += s.FullBytes
			}
			if total > 64<<10 {
				t.Fatalf("predicted %d bytes: %+v", total, res.Sizes)
			}

			got, err := p.Process(ctx, req(""))
			if err != nil {
				t.Fatal(err)
			}
			if arm := lastArm(); arm != observe.ArmStreaming {
				t.Fatalf("ran on the %s arm", arm)
			}
			if got.Data != nil || got.Metadata.TotalRows != 40000 {
				t.Fatalf("data %v total rows %d", got.Data, got.Metadata.TotalRows)
			}
			want, err := p.Process(ctx, req(workaround))
			if err != nil {
				t.Fatal(err)
			}
			for _, pair := range [][2]any{{got.Tests, want.Tests}, {got.Regressions, want.Regressions}} {
				a, _ := json.Marshal(pair[0])
				b, _ := json.Marshal(pair[1])
				if string(a) != string(b) {
					t.Fatalf("differs from the AGG_COUNT workaround:\n%s\n%s", a, b)
				}
			}
			if len(got.Regressions) > 0 && (len(got.Regressions[0].PAdjusted) != 2 || got.Regressions[0].Multiplicity == nil) {
				t.Fatalf("regression multiplicity not folded: %+v", got.Regressions[0])
			}
		})
	}
}

// TestExperimentStreaming_NoAggregationChunkShape pins what a streaming
// consumer of a tests-only request sees: ProcessStream yields no row
// (Response.Data is empty on both arms) but carries the run's metadata
// and components once drained; ProcessStreamResult sends no chunk and
// one StreamCompleted terminator with TotalRows 0.
func TestExperimentStreaming_NoAggregationChunkShape(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	showcaseABCohort(t, p)
	req := func() *types.Request {
		return decodeRequest(t, `{"cohort":{"filename":"ab.pulse"},
			"tests":[{"type":"TEST_WELCH","field":"pages_viewed","split_by":"arm","params":{"reference":"old_page"}}]}`)
	}
	iter, err := p.ProcessStream(ctx, req())
	if err != nil {
		t.Fatal(err)
	}
	if row, ok, err := iter.Next(ctx); err != nil || ok {
		t.Fatalf("Next = %v %v %v, want exhausted", row, ok, err)
	}
	if m := iter.Metadata(); m == nil || m.TotalRows != 40000 || m.FilteredRows != 40000 {
		t.Fatalf("metadata %+v", m)
	}
	if c := iter.Components(); c == nil || c.Run == nil || c.Run.TotalRecords != 40000 || len(c.Aggregations) != 0 {
		t.Fatalf("components %+v", c)
	}
	_ = iter.Close()

	res, err := p.ProcessStreamResult(ctx, req())
	if err != nil {
		t.Fatal(err)
	}
	var chunks int
	for range res.Chunks {
		chunks++
	}
	term := <-res.Done
	if chunks != 0 || term.Status != pulse.StreamCompleted || term.TotalRows != 0 || term.Error != nil {
		t.Fatalf("chunks %d terminator %+v", chunks, term)
	}
}
