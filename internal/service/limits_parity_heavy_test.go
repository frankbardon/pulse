//go:build heavy

package service

import (
	"context"
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse/internal/limits"
)

// TestLimitsPredictRuntimeParity is the FR-31 gate on a limitsRows-row
// buildWideCohort cohort: on every runtime enforcement site — serial,
// buffered, the parallel decode and shard reducers, the shard merge,
// the buffered and fused crosstab, join, matrix, Compose (serial and
// parallel), ProcessChain, facet and RequestTimeout — a tight limit
// trips exactly the limit the case names, and
//
//   - every certain finding trips at runtime, with the same {code,
//     limit, configured} (and, for a certain figure, the same
//     observed);
//   - every runtime trip was certain, possible or unknowable at
//     predict (RequestTimeout only — Compose, chain and facet are each
//     judged by their own predict root, a located finding at the
//     location the runtime error carries);
//   - a request predict clears never trips a predictable limit: each
//     finding re-run with the limit raised to its estimate — the upper
//     bound — no longer trips that limit, and every case under the
//     built-in defaults has no finding and runs clean.
func TestLimitsPredictRuntimeParity(t *testing.T) {
	t.Parallel()

	f := buildLimitsFixture(t, limitsRows, limitsFields)
	sites := map[string]bool{}
	for _, c := range parityCases(f) {
		t.Run(c.name, func(t *testing.T) {
			l := limits.Defaults()
			c.limits(&l)
			findings, trip := checkParity(t, f, c, l)
			switch {
			case c.wantTrip == "" && trip != nil:
				t.Fatalf("site %s: tripped %+v, want a clean run", c.site, *trip)
			case c.wantTrip != "" && trip == nil:
				t.Fatalf("site %s: no trip, want %s", c.site, c.wantTrip)
			case trip != nil && trip.limit != c.wantTrip:
				t.Fatalf("site %s: tripped %s, want %s", c.site, trip.limit, c.wantTrip)
			}
			if trip != nil {
				if got := limits.Value(l, trip.limit); trip.configured != got {
					t.Fatalf("site %s: configured %d, instance limit %d", c.site, trip.configured, got)
				}
				sites[c.site] = true
			}

			// Raised to its estimate, each finding's limit clears — to the
			// largest estimate of that limit (a Compose slot or chain
			// stage each carry their own).
			for _, fd := range findings {
				for _, g := range findings {
					if g.Limit == fd.Limit && g.Estimated > fd.Estimated {
						fd = g
					}
				}
				raised := l
				limits.Set(&raised, limits.Name(fd.Limit), fd.Estimated)
				again, trip := checkParity(t, f, c, raised)
				for _, g := range again {
					if g.Limit == fd.Limit {
						t.Fatalf("%s at its estimate %d still reported %+v", fd.Limit, fd.Estimated, g)
					}
				}
				if trip != nil && string(trip.limit) == fd.Limit {
					t.Fatalf("%s at its estimate %d still tripped: %+v", fd.Limit, fd.Estimated, *trip)
				}
			}

			// The built-in defaults: no finding, no trip, on limitsRows rows.
			if c.site != "timeout" {
				if fs, trip := checkParity(t, f, c, limits.Defaults()); len(fs) != 0 || trip != nil {
					t.Fatalf("defaults: findings %+v, trip %v", fs, trip)
				}
			}
		})
	}
	for _, s := range []string{"serial", "buffered", "parallel decode reducer", "parallel shard reducer", "merge",
		"buffered crosstab", "fused crosstab", "join", "matrix", "compose", "compose (parallel)", "chain", "facet", "timeout",
		"derived columns", "materialize (memory pre-flight)", "compose (slot memory pre-flight)",
		"compose (archive slot memory pre-flight)", "chain (stage 0 memory pre-flight)",
		"join (build-side memory backstop)", "compose (slot join backstop)", "chain (stage 0 join backstop)"} {
		if !sites[s] {
			t.Errorf("runtime site %q never tripped", s)
		}
	}
}

// TestLimitsReleaseMemory is the FR-30 gate: a run a limit trips
// AFTER it built buffered state — the materialized records of the
// buffered grouped and crosstab arms, the fused grid, the shard and
// segment partials, a rich facet's accumulators — retains none of it.
// Each pass holds the returned error (and response) the way a caller
// would; retainedPerRecord (post-GC heap growth over the held results,
// per scanned record — never B/op) must stay under maxRetained, while
// the control that holds one record-sized byte per row reads well
// above it.
func TestLimitsReleaseMemory(t *testing.T) {
	// serial: measures process-wide heap (runtime.ReadMemStats / GC), which concurrent tests inflate

	const (
		passes      = 2
		maxRetained = 2.0 // B/record; a leaked materialization is ~100+
	)
	f := buildLimitsFixture(t, limitsRows, limitsFields)
	stride := f.schema.RecordByteSize()
	byName := map[string]parityCase{}
	for _, c := range parityCases(f) {
		byName[c.name] = c
	}
	for _, name := range []string{
		"buffered grouped", "buffered crosstab", "fused crosstab",
		"parallel decode", "parallel shards merge", "facet groups",
	} {
		c, ok := byName[name]
		if !ok {
			t.Fatalf("no parity case %q", name)
		}
		t.Run(name, func(t *testing.T) {
			svc := New(f.cfg)
			c.setup(t, svc)
			l := limits.Defaults()
			c.limits(&l)
			svc.SetLimits(l)
			trip := func() (any, int, error) {
				err := c.run(context.Background(), svc)
				if tr, ok := tripOf(t, err); !ok || tr.limit != c.wantTrip {
					return nil, 0, stderrors.Join(stderrors.New("want a "+string(c.wantTrip)+" trip"), err)
				}
				return err, limitsRows, nil
			}
			// Warm the service's cohort handles outside the measurement.
			if _, _, err := trip(); err != nil {
				t.Fatal(err)
			}
			per := retainedPerRecord(t, passes, trip)
			t.Logf("retained %.3f B/record after a %s trip", per, c.wantTrip)
			if per > maxRetained {
				t.Fatalf("a tripped run retains %.1f B/record (> %.1f): the buffered state outlived the trip", per, maxRetained)
			}
		})
	}
	t.Run("control", func(t *testing.T) {
		per := retainedPerRecord(t, passes, func() (any, int, error) {
			return make([]byte, stride*limitsRows), limitsRows, nil
		})
		if per < float64(stride)/2 {
			t.Fatalf("control retained %.1f B/record, want ~%d: the measurement cannot see a leak", per, stride)
		}
	})
}
