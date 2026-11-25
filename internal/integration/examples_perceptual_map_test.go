package integration_test

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// TestExamples_PerceptualMapRecoversPlantedAxes pins what the
// perceptual_map story (matrices/21_perceptual_map.json) teaches over
// brand_ratings.pulse: the first component is the planted premium axis
// (quality / premium / value), the second the modern axis (modern /
// innovative / heritage), the two keep most of the variation, and the
// MAT_DISTANCE centroids put the four outer brands in the four quadrants
// with brand_e near the origin, so opposite corners are the furthest
// pairs. The story golden pins the figures; this test pins the shape a
// fixture change must keep.
func TestExamples_PerceptualMapRecoversPlantedAxes(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	osfs := afero.NewOsFs()
	p, err := pulse.New(pulse.Options{FS: osfs})
	if err != nil {
		t.Fatal(err)
	}
	buildExampleFixtures(t, p, osfs, tmp)

	resp := processExample(t, p, "matrices/21_perceptual_map.json", tmp)
	if len(resp.Matrices) != 3 {
		t.Fatalf("got %d matrix results, want 3 (axes, brand_distance, rating_distance)", len(resp.Matrices))
	}
	axes, dist, ratings := resp.Matrices[0], resp.Matrices[1], resp.Matrices[2]
	for _, m := range resp.Matrices {
		if m.Error != nil {
			t.Fatalf("%s slot error %+v", m.Name, m.Error)
		}
	}

	// Loadings: each item loads strongly on its planted component and
	// weakly on the other.
	strong := map[string]int{"quality": 0, "premium": 0, "value": 0, "modern": 1, "innovative": 1, "heritage": 1}
	for i, item := range axes.Primary.RowKeys {
		want, ok := strong[item]
		if !ok {
			t.Fatalf("unexpected loading row %q", item)
		}
		row := axes.Primary.Values[i]
		if math.Abs(row[want]) < 0.7 || math.Abs(row[1-want]) > 0.3 {
			t.Errorf("%s loadings %v: want |PC%d| >= 0.7 and the other <= 0.3", item, row, want+1)
		}
	}
	if cum := floatVector(t, &axes, "cumulative"); cum[1] < 0.65 {
		t.Errorf("two components keep %v of the variation, want >= 0.65", cum[1])
	}

	// Centroids: one brand per quadrant, brand_e near the origin.
	cent := dist.Auxiliary["centroids"]
	if cent == nil {
		t.Fatal("brand_distance has no auxiliary.centroids")
	}
	pos := map[string][]float64{}
	for i, b := range cent.RowKeys {
		pos[b] = cent.Values[i]
	}
	quadrants := map[string][2]float64{"brand_a": {1, 1}, "brand_b": {1, -1}, "brand_c": {-1, 1}, "brand_d": {-1, -1}}
	for b, q := range quadrants {
		xy := pos[b]
		if len(xy) != 2 || xy[0]*q[0] < 0.4 || xy[1]*q[1] < 0.4 {
			t.Errorf("%s centroid %v: want quadrant (%+v, %+v), each coordinate at least 0.4 from zero", b, xy, q[0], q[1])
		}
	}
	if e := pos["brand_e"]; len(e) != 2 || math.Hypot(e[0], e[1]) > 0.2 {
		t.Errorf("brand_e centroid %v: want within 0.2 of the origin", e)
	}

	// Distances, on the map and over the ratings themselves: opposite
	// corners are the two furthest pairs, and the two pairs sharing a
	// side of the premium axis are the closest corner pairs.
	for _, m := range []*types.MatrixValues{dist.Primary, ratings.Primary} {
		idx := map[string]int{}
		for i, b := range m.RowKeys {
			idx[b] = i
		}
		d := func(a, b string) float64 { return m.Values[idx[a]][idx[b]] }
		diag := math.Min(d("brand_a", "brand_d"), d("brand_b", "brand_c"))
		price := math.Max(d("brand_a", "brand_b"), d("brand_c", "brand_d"))
		style := math.Min(d("brand_a", "brand_c"), d("brand_b", "brand_d"))
		if !(price < style && style < diag) {
			t.Errorf("corner pairs: same-price max %v, same-style min %v, diagonal min %v; want price < style < diagonal", price, style, diag)
		}
	}
}
