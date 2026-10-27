package weighting

import (
	"math"
	"testing"
)

// TestCoWelford_YHalfIsWelford: the y half of the bivariate bucket is
// the Welford bucket of y alone, bit for bit, weighted or not.
func TestCoWelford_YHalfIsWelford(t *testing.T) {
	t.Parallel()

	var c CoWelford
	var w Welford
	for i := range 500 {
		x := float64((i*37)%101) * 0.37
		y := float64((i*13)%53)*1.1 + x*0.5
		wt := 1.0
		if i%3 == 0 {
			wt = 2.5
		}
		c.Add(x, y, wt)
		w.Add(y, wt)
	}
	if got := c.YWelford(); got != w {
		t.Fatalf("y half %+v, Welford %+v", got, w)
	}
}

// TestCoWelford_MergeEqualsSerial: merging partition buckets (Chan)
// gives the serial bucket's moments, and the moments equal the direct
// centred sums.
func TestCoWelford_MergeEqualsSerial(t *testing.T) {
	t.Parallel()

	type row struct{ x, y, w float64 }
	var rows []row
	for i := range 1000 {
		x := float64((i*37)%101) * 0.37
		y := float64((i*13)%53)*1.1 + x*0.5
		rows = append(rows, row{x, y, float64(1 + i%4)})
	}
	var serial CoWelford
	for _, r := range rows {
		serial.Add(r.x, r.y, r.w)
	}
	for _, cut := range []int{0, 1, 333, 999, 1000} {
		var a, b CoWelford
		for i, r := range rows {
			if i < cut {
				a.Add(r.x, r.y, r.w)
			} else {
				b.Add(r.x, r.y, r.w)
			}
		}
		a.Merge(b)
		if a.N != serial.N || a.SumW != serial.SumW || a.SumWSq != serial.SumWSq {
			t.Fatalf("cut %d: counts %+v, serial %+v", cut, a, serial)
		}
		for _, c := range [][2]float64{{a.MeanX, serial.MeanX}, {a.MeanY, serial.MeanY}, {a.M2X, serial.M2X}, {a.M2Y, serial.M2Y}, {a.CXY, serial.CXY}} {
			if math.Abs(c[0]-c[1]) > 1e-9*math.Max(1, math.Abs(c[1])) {
				t.Fatalf("cut %d: merged %+v, serial %+v", cut, a, serial)
			}
		}
	}
	var sw, sx, sy float64
	for _, r := range rows {
		sw += r.w
		sx += r.w * r.x
		sy += r.w * r.y
	}
	mx, my := sx/sw, sy/sw
	var cxy float64
	for _, r := range rows {
		cxy += r.w * (r.x - mx) * (r.y - my)
	}
	if math.Abs(serial.CXY-cxy) > 1e-9*math.Abs(cxy) || math.Abs(serial.MeanX-mx) > 1e-12*mx {
		t.Fatalf("co-moment %v (direct %v), mean %v (direct %v)", serial.CXY, cxy, serial.MeanX, mx)
	}
}
