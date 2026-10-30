package integration_test

import (
	"context"
	"strconv"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// BenchmarkComposeBanner measures the N-scan cost of a Compose banner
// (U47 E3-S3): the banner is Compose sugar, so every section × block is
// its own crosstab slot and its own scan of the cohort. The arms, over
// the same in-memory cohort under ComposeParallel:
//
//   - one_slot: a single Total-only crosstab — one scan, the unit cost;
//   - banner_1x4: one section × (Total + Gender + Age + Region) — 4 scans;
//   - banner_3x4: three sections × the same four blocks — 12 scans.
//
// No threshold is asserted (a benchmark is not a gate); the recorded
// ratio banner / one_slot is the deliverable — near the slot count when
// the scans run serially, below it as ComposeParallel overlaps them.
//
// Run with:
//
//	go test . -bench BenchmarkComposeBanner -benchmem -run '^$'
func BenchmarkComposeBanner(b *testing.B) {
	for _, rows := range []int{100_000, 1_000_000} {
		fs := afero.NewMemMapFs()
		writeBannerCohort(b, fs, rows)
		p, err := pulse.New(pulse.Options{FS: fs})
		if err != nil {
			b.Fatal(err)
		}
		oneSlot := bannerBenchComposed(1, false)
		oneSlot.Banner.Groups = nil
		arms := []struct {
			name string
			c    *types.ComposedRequest
		}{
			{"one_slot", oneSlot},
			{"banner_1x4", bannerBenchComposed(1, true)},
			{"banner_3x4", bannerBenchComposed(3, true)},
		}
		for _, arm := range arms {
			b.Run("rows="+strconv.Itoa(rows)+"/"+arm.name, func(b *testing.B) {
				ctx := context.Background()
				for b.Loop() {
					if _, err := p.ComposeParallel(ctx, arm.c, pulse.ComposeOptions{}); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

// bannerBenchComposed is a banner of n sections over the bench cohort:
// the Total block, plus Gender / Age / Region when groups is set, each
// slot with a lettered pairwise overlay.
func bannerBenchComposed(n int, groups bool) *types.ComposedRequest {
	c := bannerComposed(nil)
	c.Banner.Sections = nil
	for i := 0; i < n; i++ {
		c.Banner.Sections = append(c.Banner.Sections, types.BannerSection{Label: "s" + strconv.Itoa(i)})
	}
	if !groups {
		c.Banner.Groups = nil
	}
	return c
}
